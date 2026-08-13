package credential

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCipherRoundTripAndTamper(t *testing.T) {
	c, err := New([]byte("01234567890123456789012345678901"), "v1")
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := c.Seal(map[string]string{"access_token": "secret"})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	if err := c.Open(sealed, &got); err != nil {
		t.Fatal(err)
	}
	if got["access_token"] != "secret" {
		t.Fatalf("unexpected credential: %#v", got)
	}
	if err := c.Open(sealed+"x", &got); !errors.Is(err, ErrInvalidCiphertext) {
		t.Fatalf("tamper error = %v", err)
	}
}

func TestCipherRejectsWrongVersionAndKey(t *testing.T) {
	c1, _ := New([]byte("01234567890123456789012345678901"), "v1")
	c2, _ := New([]byte("11234567890123456789012345678901"), "v1")
	c3, _ := New([]byte("01234567890123456789012345678901"), "v2")
	sealed, _ := c1.Seal(map[string]string{"username": "u"})
	var out map[string]string
	if err := c2.Open(sealed, &out); !errors.Is(err, ErrInvalidCiphertext) {
		t.Fatalf("wrong key error = %v", err)
	}
	if err := c3.Open(sealed, &out); !errors.Is(err, ErrInvalidCiphertext) {
		t.Fatalf("wrong version error = %v", err)
	}
}

func TestNewFromEnvCreatesAndReusesDefaultSQLiteKeyFile(t *testing.T) {
	t.Setenv(masterKeyEnv, "")
	t.Setenv(masterKeyFileEnv, "")
	dbPath := filepath.Join(t.TempDir(), "ccload.db")
	t.Setenv("SQLITE_PATH", dbPath)

	first, err := NewFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(filepath.Dir(dbPath), "fusion-master.key")
	if _, err := os.Stat(keyPath); err != nil {
		t.Fatalf("key file was not created: %v", err)
	}
	sealed, err := first.Seal(map[string]string{"token": "secret"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	if err := second.Open(sealed, &got); err != nil {
		t.Fatalf("reused key could not decrypt: %v", err)
	}
	if got["token"] != "secret" {
		t.Fatalf("unexpected decrypted value: %#v", got)
	}
}
