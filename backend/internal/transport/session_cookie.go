package transport

// Persistent sign-in (HttpOnly session cookie).
//
// The bearer token stays in memory only; to survive page refreshes WITHOUT
// exposing credentials to JavaScript, login/refresh/resume ALSO set an
// HttpOnly cookie scoped to /api/auth:
//
//   - HttpOnly  → JavaScript can never read it (no XSS token theft)
//   - Path=/api/auth → never sent to any data endpoint (no ambient
//     authorization on API routes; CSRF surface limited to /api/auth/*)
//   - SameSite=Strict → never sent cross-site
//   - one-time  → /api/auth/resume ROTATES it atomically (replay of an old
//     cookie fails); logout revokes the session and clears the cookie
//
// This is the standard persistent-login pattern (server-side session
// cookie) and preserves the "token never in localStorage/sessionStorage/
// readable cookies/URLs" rule.

import (
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
)

const sessionCookieName = "launchpad_session"

// setSessionCookie stores the CURRENT bearer token server-side only: the
// browser holds it in an HttpOnly cookie it cannot read.
func setSessionCookie(c echo.Context, token string, ttl time.Duration) {
	http.SetCookie(c.Response().Writer, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/api/auth",
		MaxAge:   int(ttl.Seconds()),
		HttpOnly: true,
		Secure:   c.Scheme() == "https",
		SameSite: http.SameSiteStrictMode,
	})
}

// clearSessionCookie expires the cookie (logout / failed resume).
func clearSessionCookie(c echo.Context) {
	http.SetCookie(c.Response().Writer, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/api/auth",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   c.Scheme() == "https",
		SameSite: http.SameSiteStrictMode,
	})
}

// sessionCookieToken returns the cookie-carried token, if any.
func sessionCookieToken(c echo.Context) (string, bool) {
	cookie, err := c.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return "", false
	}
	return cookie.Value, true
}
