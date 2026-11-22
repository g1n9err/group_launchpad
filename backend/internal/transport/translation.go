package transport

import (
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/labstack/echo/v4"
	"launchpad/internal/core"
)

func (h *Handlers) translateDraft(c echo.Context) error {
	id, err := h.requireStaff(c)
	if err != nil {
		return err
	}
	if h.translateText == nil {
		return errNotReady("translation is unavailable")
	}
	var body struct {
		Text   string `json:"text"`
		Target string `json:"target"`
	}
	if c.Bind(&body) != nil || strings.TrimSpace(body.Text) == "" || utf8.RuneCountInString(body.Text) > 4000 {
		return errBadRequest("enter up to 4000 characters to translate")
	}
	switch body.Target {
	case "hy", "en", "ru":
	default:
		return errBadRequest("unsupported translation language")
	}
	if !h.translationThrottle.take(c.RealIP(), id.UserID.String()) {
		return &core.AppError{Code: core.CodeRateLimited, Message: "too many translation requests"}
	}
	translated, err := h.translateText(c.Request().Context(), body.Text, body.Target)
	if err != nil {
		return errNotReady("translation is unavailable")
	}
	return c.JSON(http.StatusOK, map[string]string{"text": translated})
}
