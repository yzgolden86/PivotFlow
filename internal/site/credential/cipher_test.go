package credential

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
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

func TestIsSealedValidatesEnvelopeShape(t *testing.T) {
	c, err := New([]byte("01234567890123456789012345678901"), "v2")
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := c.Seal(map[string]string{"token": "secret"})
	if err != nil {
		t.Fatal(err)
	}

	if !IsSealed(sealed) {
		t.Fatalf("IsSealed(%q) = false, want true", sealed)
	}

	for _, value := range []string{
		"fc1.actual-upstream-key",
		"fc1.v1.not-base64.not-base64",
		"fc1.v1.c2hvcnQ.AAAAAAAAAAAAAAAAAAAAAA",
		"fc1.v1.MDEyMzQ1Njc4OTAx.c2hvcnQ",
		"fc1..MDEyMzQ1Njc4OTAx.AAAAAAAAAAAAAAAAAAAAAA",
		"fc1.invalid!version.MDEyMzQ1Njc4OTAx.AAAAAAAAAAAAAAAAAAAAAA",
	} {
		if IsSealed(value) {
			t.Errorf("IsSealed(%q) = true, want false", value)
		}
	}
}

func TestNewRejectsInvalidKeyVersion(t *testing.T) {
	for _, version := range []string{"release.1", "version with spaces", strings.Repeat("v", 65)} {
		if _, err := New([]byte("01234567890123456789012345678901"), version); !errors.Is(err, ErrInvalidMasterKey) {
			t.Errorf("New(version=%q) error=%v, want ErrInvalidMasterKey", version, err)
		}
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
	dbPath := filepath.Join(t.TempDir(), "pivotflow.db")
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
