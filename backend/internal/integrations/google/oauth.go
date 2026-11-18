// Package google implements core/integrations.GoogleAuthGateway against the
// real Google OAuth endpoints. It is credential-gated by construction: the
// composition root only wires it when GOOGLE_CLIENT_ID, GOOGLE_CLIENT_SECRET,
// GOOGLE_REDIRECT_URL, and TOKEN_ENCRYPTION_KEY are configured. No call is
// ever simulated; transport errors are mapped to sanitized categories before
// leaving this package (no token or request material in messages).
package google

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"
	goauth "golang.org/x/oauth2/google"

	coreintegrations "launchpad/internal/core/integrations"
)

const userinfoEndpoint = "https://openidconnect.googleapis.com/v1/userinfo"

// Gateway implements core/integrations.GoogleAuthGateway.
type Gateway struct {
	conf oauth2.Config
	http *http.Client
}

// NewGateway builds the gateway. Scopes come from the domain layer so the
// adapter can never drift from the PRD-justified set.
func NewGateway(clientID, clientSecret, redirectURL string, scopes []string) *Gateway {
	return &Gateway{
		conf: oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			Endpoint:     goauth.Endpoint,
			RedirectURL:  redirectURL,
			Scopes:       scopes,
		},
		http: &http.Client{Timeout: 15 * time.Second},
	}
}

// AuthCodeURL builds the Google consent URL: PKCE S256, offline access, and
// prompt=consent (Testing-mode refresh tokens expire after 7 days; forcing
// consent guarantees a fresh refresh token on every connect). Note: no
// include_granted_scopes parameter — Google defines it only as `true`
// (incremental auth) and rejects `false` during consent assembly with an
// opaque unknownerror page.
func (g *Gateway) AuthCodeURL(state, codeChallenge string) string {
	v := url.Values{
		"response_type": {"code"},
		"client_id":     {g.conf.ClientID},
		"redirect_uri":  {g.conf.RedirectURL},
		"scope":         {strings.Join(g.conf.Scopes, " ")},
		"access_type":   {"offline"},
		// consent (reissue refresh tokens) + select_account (defeat multi-account
		// authuser confusion — the known cause of 500s on Google's
		// /signin/oauth/warning/continue for Testing-mode apps).
		"prompt":                {"consent select_account"},
		"state":                 {state},
		"code_challenge":        {codeChallenge},
		"code_challenge_method": {"S256"},
	}
	return goauth.Endpoint.AuthURL + "?" + v.Encode()
}

// Exchange swaps the authorization code for tokens and fetches the connected
// account's email from the OpenID userinfo endpoint.
func (g *Gateway) Exchange(ctx context.Context, code, codeVerifier string) (coreintegrations.GoogleTokenBundle, error) {
	tok, err := g.conf.Exchange(ctx, code, oauth2.SetAuthURLParam("code_verifier", codeVerifier))
	if err != nil {
		return coreintegrations.GoogleTokenBundle{}, sanitize(err, "exchange")
	}
	if tok.RefreshToken == "" {
		return coreintegrations.GoogleTokenBundle{}, errors.New("no refresh token issued")
	}
	email, err := g.userinfo(ctx, tok.AccessToken)
	if err != nil {
		return coreintegrations.GoogleTokenBundle{}, err
	}
	return coreintegrations.GoogleTokenBundle{
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		Expiry:       tok.Expiry,
		AccountEmail: email,
	}, nil
}

// Refresh mints an access token from a refresh token. A permanent rejection
// (invalid_grant) maps to coreintegrations.ErrRefreshRevoked.
func (g *Gateway) Refresh(ctx context.Context, refreshToken string) (string, time.Time, error) {
	ts := g.conf.TokenSource(ctx, &oauth2.Token{RefreshToken: refreshToken})
	tok, err := ts.Token()
	if err != nil {
		var re *oauth2.RetrieveError
		if errors.As(err, &re) && re.ErrorCode == "invalid_grant" {
			return "", time.Time{}, coreintegrations.ErrRefreshRevoked
		}
		return "", time.Time{}, sanitize(err, "refresh")
	}
	return tok.AccessToken, tok.Expiry, nil
}

// userinfo resolves the connected account's email. Bearer access token only —
// the client secret is never involved.
func (g *Gateway) userinfo(ctx context.Context, accessToken string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, userinfoEndpoint, nil)
	if err != nil {
		return "", errors.New("userinfo request build failed")
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := g.http.Do(req)
	if err != nil {
		return "", sanitize(err, "userinfo")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return "", errors.New("userinfo read failed")
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("userinfo failed with status %d", resp.StatusCode)
	}
	var claims struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
	}
	if err := json.Unmarshal(body, &claims); err != nil {
		return "", errors.New("userinfo payload unparseable")
	}
	if claims.Email == "" || !claims.EmailVerified {
		return "", errors.New("google account email missing or unverified")
	}
	return claims.Email, nil
}

// sanitize wraps a transport error in a category-only message. oauth2 errors
// embed full HTTP responses (URLs, sometimes request fragments) — none of it
// may reach logs or API consumers.
func sanitize(err error, op string) error {
	var re *oauth2.RetrieveError
	if errors.As(err, &re) {
		return fmt.Errorf("google %s: server rejected the request (%s)", op, re.ErrorCode)
	}
	return fmt.Errorf("google %s: request failed", op)
}
