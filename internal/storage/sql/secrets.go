package sql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/site/credential"
)

// ConfigureCredentialCipher enables transparent encryption for channel API
// keys and OAuth credentials. Existing plaintext rows are migrated atomically;
// existing ciphertext is authenticated before the transaction commits.
func (s *SQLStore) ConfigureCredentialCipher(ctx context.Context, cipher *credential.Cipher) error {
	if cipher == nil {
		return errors.New("credential cipher is required")
	}
	s.credentialCipher = cipher
	if err := s.migrateChannelSecrets(ctx); err != nil {
		s.credentialCipher = nil
		return err
	}
	return nil
}

func (s *SQLStore) sealSecret(value string) (string, error) {
	if value == "" || s.credentialCipher == nil {
		return value, nil
	}
	sealed, err := s.credentialCipher.Seal(value)
	if err != nil {
		return "", fmt.Errorf("encrypt stored credential: %w", err)
	}
	return sealed, nil
}

func (s *SQLStore) openSecret(value string) (string, error) {
	if value == "" || s.credentialCipher == nil || !credential.IsSealed(value) {
		return value, nil
	}
	var plaintext string
	if err := s.credentialCipher.Open(value, &plaintext); err != nil {
		return "", fmt.Errorf("decrypt stored credential: %w", err)
	}
	return plaintext, nil
}

func (s *SQLStore) decryptConfigs(configs []*model.Config) error {
	for _, config := range configs {
		if config == nil || strings.TrimSpace(config.OAuthCredential) == "" {
			continue
		}
		plaintext, err := s.openSecret(config.OAuthCredential)
		if err != nil {
			return fmt.Errorf("decrypt OAuth credential for channel %d: %w", config.ID, err)
		}
		config.OAuthCredential = plaintext
	}
	return nil
}

type storedSecret struct {
	id    int64
	value string
}

func (s *SQLStore) migrateChannelSecrets(ctx context.Context) error {
	return s.WithTransaction(ctx, func(tx *sql.Tx) error {
		oauthSecrets, err := s.collectStoredSecrets(ctx, tx, "SELECT id, COALESCE(oauth_credential, '') FROM channels WHERE oauth_credential IS NOT NULL AND oauth_credential <> ''")
		if err != nil {
			return fmt.Errorf("read OAuth credentials for encryption: %w", err)
		}
		apiKeys, err := s.collectStoredSecrets(ctx, tx, "SELECT id, api_key FROM api_keys")
		if err != nil {
			return fmt.Errorf("read API keys for encryption: %w", err)
		}

		for _, secret := range oauthSecrets {
			if credential.IsSealed(secret.value) {
				if _, err := s.openSecret(secret.value); err != nil {
					return fmt.Errorf("validate OAuth credential for channel %d: %w", secret.id, err)
				}
				continue
			}
			sealed, err := s.sealSecret(secret.value)
			if err != nil {
				return fmt.Errorf("validate OAuth credential for channel %d: %w", secret.id, err)
			}
			if sealed == secret.value {
				continue
			}
			if _, err := s.execTx(ctx, tx, "UPDATE channels SET oauth_credential=? WHERE id=?", sealed, secret.id); err != nil {
				return fmt.Errorf("encrypt OAuth credential for channel %d: %w", secret.id, err)
			}
		}
		for _, secret := range apiKeys {
			if credential.IsSealed(secret.value) {
				if _, err := s.openSecret(secret.value); err != nil {
					return fmt.Errorf("validate API key row %d: %w", secret.id, err)
				}
				continue
			}
			sealed, err := s.sealSecret(secret.value)
			if err != nil {
				return fmt.Errorf("validate API key row %d: %w", secret.id, err)
			}
			if sealed == secret.value {
				continue
			}
			if _, err := s.execTx(ctx, tx, "UPDATE api_keys SET api_key=? WHERE id=?", sealed, secret.id); err != nil {
				return fmt.Errorf("encrypt API key row %d: %w", secret.id, err)
			}
		}
		return nil
	})
}

func (s *SQLStore) collectStoredSecrets(ctx context.Context, tx *sql.Tx, query string) ([]storedSecret, error) {
	rows, err := tx.QueryContext(ctx, s.q(query))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	secrets := make([]storedSecret, 0)
	for rows.Next() {
		var secret storedSecret
		if err := rows.Scan(&secret.id, &secret.value); err != nil {
			return nil, err
		}
		secrets = append(secrets, secret)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return secrets, nil
}
