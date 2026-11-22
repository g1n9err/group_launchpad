package transport

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/labstack/echo/v4"

	"launchpad/internal/core/auth"
)

type PasswordRecovery struct {
	Auth    auth.Recovery
	SiteURL string
	Send    func(context.Context, string, string, string) (bool, error)
}

func (h *Handlers) requestPasswordReset(c echo.Context) error {
	recovery := h.passwordRecovery
	if recovery == nil {
		return errNotReady("password recovery is unavailable")
	}
	var body struct {
		Email    string `json:"email"`
		Language string `json:"language"`
	}
	if c.Bind(&body) != nil || !auth.ValidateEmail(body.Email) {
		return errBadRequest("enter a valid email address")
	}
	email := auth.NormalizeEmail(body.Email)
	if !h.recoveryThrottle.take(c.RealIP(), email) {
		return c.NoContent(http.StatusAccepted)
	}
	select {
	case h.recoverySlots <- struct{}{}:
		go func() {
			defer func() { <-h.recoverySlots }()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			token, err := recovery.Auth.Issue(ctx, email)
			if errors.Is(err, auth.ErrInvalidCredentials) || errors.Is(err, auth.ErrNoCredential) {
				return
			}
			if err != nil {
				h.logger.Warn("could not issue password recovery link")
				return
			}
			link, _ := url.Parse(recovery.SiteURL)
			link.Fragment = "/reset-password/" + token
			subject, message := passwordRecoveryMessage(body.Language, link.String())
			if _, err := recovery.Send(ctx, email, subject, message); err != nil {
				h.logger.Warn("could not send password recovery email")
			}
		}()
	default:
		return errNotReady("password recovery is busy")
	}
	return c.NoContent(http.StatusAccepted)
}

func (h *Handlers) resetPassword(c echo.Context) error {
	if h.passwordRecovery == nil {
		return errNotReady("password recovery is unavailable")
	}
	var body struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if c.Bind(&body) != nil || len(body.Token) != 75 {
		return errBadRequest("invalid or expired password recovery link")
	}
	err := h.passwordRecovery.Auth.Reset(c.Request().Context(), body.Token, body.Password)
	if errors.Is(err, auth.ErrInvalidResetLink) {
		return errBadRequest("invalid or expired password recovery link")
	}
	if err != nil {
		return err
	}
	clearSessionCookie(c)
	return c.NoContent(http.StatusNoContent)
}

func passwordRecoveryMessage(language, link string) (subject, body string) {
	switch language {
	case "hy":
		return "Վերականգնեք Group Launchpad-ի գաղտնաբառը",
			"Բացեք հղումը՝ նոր գաղտնաբառ ընտրելու համար։ Հղումը գործում է 30 րոպե։\n\n" + link +
				"\n\nԵթե վերականգնում չեք խնդրել, կարող եք անտեսել այս նամակը։"
	case "ru":
		return "Восстановление пароля Group Launchpad",
			"Откройте ссылку, чтобы выбрать новый пароль. Ссылка действует 30 минут.\n\n" + link +
				"\n\nЕсли вы не запрашивали восстановление, проигнорируйте это письмо."
	default:
		return "Reset your Group Launchpad password",
			"Open this link to choose a new password. It expires in 30 minutes.\n\n" + link +
				"\n\nIf you did not request a reset, you can ignore this email."
	}
}
