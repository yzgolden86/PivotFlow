// Package credential contains the only code allowed to handle site credentials.
package credential

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	formatPrefix      = "fc1"
	defaultKeyVersion = "v1"
	masterKeyEnv      = "FUSION_MASTER_KEY"
	masterKeyFileEnv  = "FUSION_MASTER_KEY_FILE"
	keyVersionEnv     = "FUSION_MASTER_KEY_VERSION"
)

var (
	ErrCredentialLocked  = errors.New("credential_locked")
	ErrInvalidMasterKey  = errors.New("invalid fusion master key")
	ErrInvalidCiphertext = errors.New("invalid credential ciphertext")
)

// Cipher encrypts small JSON credential payloads using AES-256-GCM.
type Cipher struct {
	key     [32]byte
	version string
}

// New constructs a cipher from exactly 32 raw key bytes.
func New(key []byte, version string) (*Cipher, error) {
	if len(key) != 32 {
		return nil, ErrInvalidMasterKey
	}
	if strings.TrimSpace(version) == "" {
		version = defaultKeyVersion
	}
	var raw [32]byte
	copy(raw[:], key)
	return &Cipher{key: raw, version: strings.TrimSpace(version)}, nil
}

// NewFromEnv reads the base64url encoded master key. Personal SQLite
// deployments may omit the environment variable: in that case a random key is
// created beside SQLITE_PATH and reused on later starts. Credentials are never
// stored as plaintext.
func NewFromEnv() (*Cipher, error) {
	raw := strings.TrimSpace(os.Getenv(masterKeyEnv))
	if raw != "" {
		key, err := decodeKey(raw)
		if err != nil {
			return nil, err
		}
		return New(key, os.Getenv(keyVersionEnv))
	}

	keyPath := strings.TrimSpace(os.Getenv(masterKeyFileEnv))
	if keyPath == "" {
		if sqlitePath := strings.TrimSpace(os.Getenv("SQLITE_PATH")); sqlitePath != "" {
			keyPath = filepath.Join(filepath.Dir(sqlitePath), "fusion-master.key")
		} else {
			// Keep the zero-configuration SQLite deployment usable. NewStore uses
			// data/ccload.db when SQLITE_PATH is omitted, so the credential key
			// must resolve beside that same default database.
			keyPath = filepath.Join("data", "fusion-master.key")
		}
	}
	key, err := loadOrCreateKeyFile(keyPath)
	if err != nil {
		return nil, err
	}
	return New(key, os.Getenv(keyVersionEnv))
}

func loadOrCreateKeyFile(path string) ([]byte, error) {
	path = filepath.Clean(path)
	if raw, err := os.ReadFile(path); err == nil {
		return decodeKey(strings.TrimSpace(string(raw)))
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read fusion master key file: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create fusion master key directory: %w", err)
	}
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("generate fusion master key: %w", err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(key)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil, fmt.Errorf("read fusion master key file: %w", readErr)
		}
		return decodeKey(strings.TrimSpace(string(raw)))
	}
	if err != nil {
		return nil, fmt.Errorf("create fusion master key file: %w", err)
	}
	if _, err := file.WriteString(encoded + "\n"); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("write fusion master key file: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("close fusion master key file: %w", err)
	}
	return key, nil
}

func decodeKey(raw string) ([]byte, error) {
	decoders := []*base64.Encoding{base64.RawURLEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.StdEncoding}
	for _, decoder := range decoders {
		if key, err := decoder.DecodeString(raw); err == nil && len(key) == 32 {
			return key, nil
		}
	}
	return nil, ErrInvalidMasterKey
}

// Version returns the key version embedded in newly sealed values.
func (c *Cipher) Version() string {
	if c == nil {
		return ""
	}
	return c.version
}

// Seal marshals value and encrypts it. The returned value is safe to persist.
func (c *Cipher) Seal(value any) (string, error) {
	if c == nil {
		return "", ErrCredentialLocked
	}
	plaintext, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("marshal credential: %w", err)
	}
	block, err := aes.NewCipher(c.key[:])
	if err != nil {
		return "", fmt.Errorf("create credential cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create credential gcm: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate credential nonce: %w", err)
	}
	ciphertext := gcm.Seal(nil, nonce, plaintext, []byte(c.version))
	return strings.Join([]string{formatPrefix, c.version, base64.RawURLEncoding.EncodeToString(nonce), base64.RawURLEncoding.EncodeToString(ciphertext)}, "."), nil
}

// Open decrypts a value into out. It does not expose credential text in errors.
func (c *Cipher) Open(encoded string, out any) error {
	if c == nil {
		return ErrCredentialLocked
	}
	parts := strings.Split(encoded, ".")
	if len(parts) != 4 || parts[0] != formatPrefix || parts[1] == "" || subtle.ConstantTimeCompare([]byte(parts[1]), []byte(c.version)) != 1 {
		return ErrInvalidCiphertext
	}
	nonce, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return ErrInvalidCiphertext
	}
	ciphertext, err := base64.RawURLEncoding.DecodeString(parts[3])
	if err != nil {
		return ErrInvalidCiphertext
	}
	block, err := aes.NewCipher(c.key[:])
	if err != nil {
		return ErrInvalidCiphertext
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(nonce) != gcm.NonceSize() {
		return ErrInvalidCiphertext
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, []byte(c.version))
	if err != nil {
		return ErrInvalidCiphertext
	}
	if err := json.Unmarshal(plaintext, out); err != nil {
		return ErrInvalidCiphertext
	}
	return nil
}
