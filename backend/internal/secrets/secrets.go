// Package secrets provides AES-256-GCM encryption for values that must be
// stored encrypted at rest (PRD 9.2) — OAuth refresh tokens, PKCE verifiers.
// The key comes from the environment (TOKEN_ENCRYPTION_KEY), never from the
// database and never from the repository.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
)

// Cipher encrypts strings with AES-256-GCM. Ciphertext format: base64(nonce ‖ ct).
type Cipher struct {
	aead  cipher.AEAD
	keyID string
}

// New decodes a base64-encoded 32-byte key. keyID is derived from the key
// material so stored rows record which key encrypted them (rotation support).
func New(base64Key string) (*Cipher, error) {
	raw, err := base64.StdEncoding.DecodeString(base64Key)
	if err != nil {
		return nil, fmt.Errorf("secrets: TOKEN_ENCRYPTION_KEY is not valid base64: %w", err)
	}
	if len(raw) != 32 {
		return nil, fmt.Errorf("secrets: TOKEN_ENCRYPTION_KEY must decode to 32 bytes, got %d", len(raw))
	}
	block, err := aes.NewCipher(raw)
	if err != nil {
		return nil, fmt.Errorf("secrets: init cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secrets: init GCM: %w", err)
	}
	digest := sha256.Sum256(raw)
	return &Cipher{aead: aead, keyID: hex.EncodeToString(digest[:8])}, nil
}

// KeyID identifies the encryption key without revealing it.
func (c *Cipher) KeyID() string { return c.keyID }

// Encrypt returns base64(nonce ‖ ciphertext). Each call uses a fresh nonce,
// so encrypting the same plaintext twice yields different ciphertexts.
func (c *Cipher) Encrypt(plaintext string) (string, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("secrets: nonce: %w", err)
	}
	sealed := c.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt reverses Encrypt. Wrong key or tampered ciphertext is an error that
// never includes key or plaintext material.
func (c *Cipher) Decrypt(encoded string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", errors.New("secrets: ciphertext is not valid base64")
	}
	if len(raw) < c.aead.NonceSize() {
		return "", errors.New("secrets: ciphertext too short")
	}
	nonce, body := raw[:c.aead.NonceSize()], raw[c.aead.NonceSize():]
	plaintext, err := c.aead.Open(nil, nonce, body, nil)
	if err != nil {
		return "", errors.New("secrets: decryption failed (wrong key or tampered ciphertext)")
	}
	return string(plaintext), nil
}
