package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"time"

	"github.com/google/uuid"
)

var ErrInvalidResetLink = errors.New("invalid or expired password recovery link")

type RecoveryRepository interface {
	UserByEmail(context.Context, string) (User, error)
	CredentialHash(context.Context, uuid.UUID) (string, error)
	ReplaceMatchingCredential(context.Context, uuid.UUID, string, string) error
}

type Recovery struct {
	Repo RecoveryRepository
	Key  string
}

func (r Recovery) Issue(ctx context.Context, email string) (string, error) {
	user, err := r.Repo.UserByEmail(ctx, normalize(email))
	if err != nil {
		return "", err
	}
	hash, err := r.Repo.CredentialHash(ctx, user.ID)
	if err != nil {
		return "", err
	}
	payload := make([]byte, 24)
	copy(payload, user.ID[:])
	binary.BigEndian.PutUint64(payload[16:], uint64(time.Now().Add(30*time.Minute).Unix()))
	return base64.RawURLEncoding.EncodeToString(append(payload, r.signature(payload, hash)...)), nil
}

func (r Recovery) Reset(ctx context.Context, token, password string) error {
	payload, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(payload) != 24+sha256.Size ||
		time.Now().Unix() >= int64(binary.BigEndian.Uint64(payload[16:24])) {
		return ErrInvalidResetLink
	}
	id := uuid.UUID(payload[:16])
	oldHash, err := r.Repo.CredentialHash(ctx, id)
	if errors.Is(err, ErrNoCredential) {
		return ErrInvalidResetLink
	}
	if err != nil {
		return err
	}
	if !hmac.Equal(payload[24:], r.signature(payload[:24], oldHash)) {
		return ErrInvalidResetLink
	}
	newHash, err := HashPassword(password)
	if err != nil {
		return err
	}
	return r.Repo.ReplaceMatchingCredential(ctx, id, oldHash, newHash)
}

func (r Recovery) signature(payload []byte, hash string) []byte {
	mac := hmac.New(sha256.New, []byte(r.Key))
	mac.Write([]byte("launchpad password recovery v1\x00"))
	mac.Write(payload)
	mac.Write([]byte(hash))
	return mac.Sum(nil)
}
