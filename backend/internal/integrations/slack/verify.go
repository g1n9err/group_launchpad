package slack

// Slack request-signature verification (Events API / commands): the official
// v0 scheme — hex(HMAC-SHA256(signingSecret, "v0:" + timestamp + ":" + body))
// compared in constant time. The secret never leaves this type.

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// RequestVerifier implements core/integrations.SlackEventVerifier.
type RequestVerifier struct {
	secret []byte
}

// NewRequestVerifier builds the verifier from SLACK_SIGNING_SECRET.
func NewRequestVerifier(signingSecret string) *RequestVerifier {
	return &RequestVerifier{secret: []byte(signingSecret)}
}

// Verify reports whether the request carries a valid v0 signature over the
// given timestamp and raw body. Missing/badly-prefixed signatures fail
// closed; comparison is constant-time (hmac.Equal).
func (v *RequestVerifier) Verify(timestamp, rawBody, receivedSignature string) bool {
	if !strings.HasPrefix(receivedSignature, "v0=") {
		return false
	}
	mac := hmac.New(sha256.New, v.secret)
	mac.Write([]byte("v0:" + timestamp + ":" + rawBody))
	expected := "v0=" + hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(receivedSignature))
}
