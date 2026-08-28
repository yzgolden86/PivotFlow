package sql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
)

const systemAccessTokenSelectColumns = `id, token, token_hint, description, scopes, created_at, last_used_at, expires_at, is_active`

func scanSystemAccessToken(scanner interface{ Scan(...any) error }) (*model.SystemAccessToken, error) {
	var token model.SystemAccessToken
	var scopesJSON string
	var lastUsedAt int64
	var active int
	if err := scanner.Scan(&token.ID, &token.Token, &token.TokenHint, &token.Description, &scopesJSON, &token.CreatedAt, &lastUsedAt, &token.ExpiresAt, &active); err != nil {
		return nil, err
	}
	if scopesJSON == "" {
		token.Scopes = []string{}
	} else if err := json.Unmarshal([]byte(scopesJSON), &token.Scopes); err != nil {
		return nil, fmt.Errorf("decode system access token scopes: %w", err)
	}
	if lastUsedAt > 0 {
		token.LastUsedAt = &lastUsedAt
	}
	token.IsActive = active != 0
	return &token, nil
}

func (s *SQLStore) CreateSystemAccessToken(ctx context.Context, token *model.SystemAccessToken) error {
	if token == nil || token.Token == "" {
		return errors.New("system access token hash cannot be empty")
	}
	scopes, err := model.NormalizeSystemAccessScopes(token.Scopes)
	if err != nil {
		return err
	}
	token.Scopes = scopes
	if token.CreatedAt <= 0 {
		token.CreatedAt = time.Now().UnixMilli()
	}
	scopesJSON, err := json.Marshal(token.Scopes)
	if err != nil {
		return fmt.Errorf("encode system access token scopes: %w", err)
	}
	args := []any{token.Token, token.TokenHint, token.Description, string(scopesJSON), token.CreatedAt, token.ExpiresAt, token.IsActive}
	if token.ID > 0 {
		query := `INSERT INTO system_access_tokens (id, token, token_hint, description, scopes, created_at, expires_at, is_active) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
		explicitArgs := append([]any{token.ID}, args...)
		if s.IsPostgres() {
			if err := s.withPostgresExplicitIDTx(ctx, "system_access_tokens", func(tx *sql.Tx) error {
				_, execErr := s.execTx(ctx, tx, query, explicitArgs...)
				return execErr
			}); err != nil {
				return fmt.Errorf("create system access token with explicit id: %w", err)
			}
			return nil
		}
		if _, err := s.ExecContext(ctx, query, explicitArgs...); err != nil {
			return fmt.Errorf("create system access token with explicit id: %w", err)
		}
		return nil
	}
	if s.IsPostgres() {
		if err := s.QueryRowContext(ctx, `INSERT INTO system_access_tokens (token, token_hint, description, scopes, created_at, expires_at, is_active) VALUES (?, ?, ?, ?, ?, ?, ?) RETURNING id`, args...).Scan(&token.ID); err != nil {
			return fmt.Errorf("create system access token: %w", err)
		}
		return nil
	}
	result, err := s.ExecContext(ctx, `INSERT INTO system_access_tokens (token, token_hint, description, scopes, created_at, expires_at, is_active) VALUES (?, ?, ?, ?, ?, ?, ?)`, args...)
	if err != nil {
		return fmt.Errorf("create system access token: %w", err)
	}
	token.ID, err = result.LastInsertId()
	if err != nil {
		return fmt.Errorf("read system access token id: %w", err)
	}
	return nil
}

func (s *SQLStore) GetSystemAccessTokenByHash(ctx context.Context, tokenHash string) (*model.SystemAccessToken, error) {
	token, err := scanSystemAccessToken(s.QueryRowContext(ctx, `SELECT `+systemAccessTokenSelectColumns+` FROM system_access_tokens WHERE token = ?`, tokenHash))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, model.ErrSystemAccessTokenNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get system access token: %w", err)
	}
	return token, nil
}

func (s *SQLStore) ListSystemAccessTokens(ctx context.Context) ([]*model.SystemAccessToken, error) {
	rows, err := s.QueryContext(ctx, `SELECT `+systemAccessTokenSelectColumns+` FROM system_access_tokens ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("list system access tokens: %w", err)
	}
	defer func() { _ = rows.Close() }()
	tokens := make([]*model.SystemAccessToken, 0)
	for rows.Next() {
		token, scanErr := scanSystemAccessToken(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan system access token: %w", scanErr)
		}
		tokens = append(tokens, token)
	}
	return tokens, rows.Err()
}

func (s *SQLStore) UpdateSystemAccessToken(ctx context.Context, token *model.SystemAccessToken) error {
	if token == nil || token.ID <= 0 {
		return errors.New("invalid system access token")
	}
	scopes, err := model.NormalizeSystemAccessScopes(token.Scopes)
	if err != nil {
		return err
	}
	scopesJSON, err := json.Marshal(scopes)
	if err != nil {
		return fmt.Errorf("encode system access token scopes: %w", err)
	}
	result, err := s.ExecContext(ctx, `UPDATE system_access_tokens SET description = ?, scopes = ?, expires_at = ?, is_active = ? WHERE id = ?`, token.Description, string(scopesJSON), token.ExpiresAt, token.IsActive, token.ID)
	if err != nil {
		return fmt.Errorf("update system access token: %w", err)
	}
	if count, rowsErr := result.RowsAffected(); rowsErr != nil {
		return fmt.Errorf("read updated system access token rows: %w", rowsErr)
	} else if count == 0 {
		return model.ErrSystemAccessTokenNotFound
	}
	token.Scopes = scopes
	return nil
}

func (s *SQLStore) DeleteSystemAccessToken(ctx context.Context, id int64) error {
	result, err := s.ExecContext(ctx, `DELETE FROM system_access_tokens WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete system access token: %w", err)
	}
	if count, rowsErr := result.RowsAffected(); rowsErr != nil {
		return fmt.Errorf("read deleted system access token rows: %w", rowsErr)
	} else if count == 0 {
		return model.ErrSystemAccessTokenNotFound
	}
	return nil
}

func (s *SQLStore) UpdateSystemAccessTokenLastUsed(ctx context.Context, tokenHash string, now time.Time) error {
	_, err := s.ExecContext(ctx, `UPDATE system_access_tokens SET last_used_at = ? WHERE token = ?`, now.UnixMilli(), tokenHash)
	if err != nil {
		return fmt.Errorf("update system access token last used: %w", err)
	}
	return nil
}
