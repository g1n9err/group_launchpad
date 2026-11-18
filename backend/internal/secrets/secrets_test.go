package secrets

import (
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"
)

func testKey(t *testing.T) string {
	t.Helper()
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return base64.StdEncoding.EncodeToString(raw)
}

func TestRoundTrip(t *testing.T) {
	c, err := New(testKey(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, plain := range []string{"", "x", "a-refresh-token-1//abc", strings.Repeat("long-", 100)} {
		enc, err := c.Encrypt(plain)
		if err != nil {
			t.Fatalf("Encrypt: %v", err)
		}
		got, err := c.Decrypt(enc)
		if err != nil {
			t.Fatalf("Decrypt: %v", err)
		}
		if got != plain {
			t.Errorf("round trip mismatch: got %q want %q", got, plain)
		}
	}
}

func TestEncryptIsNonDeterministic(t *testing.T) {
	c, _ := New(testKey(t))
	a, _ := c.Encrypt("same-input")
	b, _ := c.Encrypt("same-input")
	if a == b {
		t.Error("expected fresh nonce per encryption — ciphertexts must differ")
	}
}

func TestWrongKeyFails(t *testing.T) {
	encCipher, _ := New(testKey(t))
	decCipher, _ := New(testKey(t)) // different key
	enc, err := encCipher.Encrypt("secret")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if _, err := decCipher.Decrypt(enc); err == nil {
		t.Fatal("decrypt with wrong key must fail")
	}
}

func TestTamperedCiphertextFails(t *testing.T) {
	c, _ := New(testKey(t))
	enc, _ := c.Encrypt("secret")
	raw, _ := base64.StdEncoding.DecodeString(enc)
	raw[len(raw)-1] ^= 0xFF // flip last ciphertext byte
	tampered := base64.StdEncoding.EncodeToString(raw)
	if _, err := c.Decrypt(tampered); err == nil {
		t.Fatal("tampered ciphertext must fail authentication")
	}
}

func TestMalformedInputs(t *testing.T) {
	c, _ := New(testKey(t))
	if _, err := c.Decrypt("not-base64!!!"); err == nil {
		t.Error("invalid base64 must fail")
	}
	if _, err := c.Decrypt(base64.StdEncoding.EncodeToString([]byte("short"))); err == nil {
		t.Error("too-short ciphertext must fail")
	}
}

func TestKeyValidationAndStableKeyID(t *testing.T) {
	if _, err := New("not-base64"); err == nil {
		t.Error("non-base64 key must be rejected")
	}
	short := base64.StdEncoding.EncodeToString(make([]byte, 16))
	if _, err := New(short); err == nil {
		t.Error("16-byte key must be rejected (need 32)")
	}
	c1, _ := New(testKey(t))
	c2, _ := New(testKey(t))
	if c1.KeyID() == c2.KeyID() {
		t.Error("different keys must have different key IDs")
	}
	if len(c1.KeyID()) != 16 { // 8 bytes hex
		t.Errorf("keyID length = %d, want 16 hex chars", len(c1.KeyID()))
	}
}
