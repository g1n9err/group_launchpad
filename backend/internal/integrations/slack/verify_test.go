package slack_test

// HMAC verification tests against independently computed vectors (computed
// here with the crypto packages directly, not via the verifier under test).

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"launchpad/internal/integrations/slack"
)

const vecSecret = "8f742231b10e8888abcd99yyyzzz85a5"

func sign(secret, ts, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("v0:" + ts + ":" + body))
	return "v0=" + hex.EncodeToString(mac.Sum(nil))
}

func TestVerifyAcceptsValidSignature(t *testing.T) {
	v := slack.NewRequestVerifier(vecSecret)
	body := `{"type":"event_callback","event_id":"Ev1"}`
	ts := "1700000000"
	if !v.Verify(ts, body, sign(vecSecret, ts, body)) {
		t.Fatal("valid signature rejected")
	}
}

func TestVerifyRejectsTamperedBody(t *testing.T) {
	v := slack.NewRequestVerifier(vecSecret)
	ts := "1700000000"
	sig := sign(vecSecret, ts, `{"type":"a"}`)
	if v.Verify(ts, `{"type":"b"}`, sig) {
		t.Fatal("tampered body accepted")
	}
}

func TestVerifyRejectsWrongSecret(t *testing.T) {
	v := slack.NewRequestVerifier(vecSecret)
	ts := "1700000000"
	body := `{}`
	if v.Verify(ts, body, sign("other-secret", ts, body)) {
		t.Fatal("wrong-secret signature accepted")
	}
}

func TestVerifyRejectsTimestampMismatch(t *testing.T) {
	v := slack.NewRequestVerifier(vecSecret)
	body := `{}`
	if v.Verify("1700000001", body, sign(vecSecret, "1700000000", body)) {
		t.Fatal("signature computed over a different timestamp accepted")
	}
}

func TestVerifyRejectsMalformedSignatures(t *testing.T) {
	v := slack.NewRequestVerifier(vecSecret)
	ts, body := "1700000000", `{}`
	valid := sign(vecSecret, ts, body)

	for name, bad := range map[string]string{
		"missing v0 prefix": valid[3:],
		"empty":             "",
		"not hex":           "v0=zzzz",
		"valid but hexcase": "V0=" + valid[3:],
	} {
		if v.Verify(ts, body, bad) {
			t.Errorf("%s: accepted", name)
		}
	}
}
