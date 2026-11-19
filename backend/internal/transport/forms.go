package transport

import (
	"net/http"
	"net/url"

	"github.com/labstack/echo/v4"

	"launchpad/internal/core/integrations"
	gen "launchpad/internal/transport/gen"
)

func (h *Handlers) ProvisionGroupForm(c echo.Context, groupID gen.GroupId) error {
	if err := h.coordinator.authorize(c); err != nil {
		return err
	}
	if h.forms == nil {
		return errNotReady("Google Forms is unavailable")
	}

	var body gen.IntakeFormRequest
	if c.Request().ContentLength != 0 {
		if err := c.Bind(&body); err != nil {
			return errBadRequest("invalid intake form request")
		}
	}
	var questions []integrations.IntakeQuestion
	if body.Questions != nil {
		questions = make([]integrations.IntakeQuestion, 0, len(*body.Questions))
		for _, q := range *body.Questions {
			question := integrations.IntakeQuestion{Title: q.Title, Type: string(q.Type), Required: q.Required}
			if q.Options != nil {
				question.Options = *q.Options
			}
			questions = append(questions, question)
		}
	}
	ctx := c.Request().Context()
	group, err := h.groups.Get(ctx, groupID.String())
	if err != nil {
		return err
	}
	id, err := h.forms.Provision(ctx, group.ID, group.Name, questions)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, gen.IntakeFormDraft{FormId: id, EditUrl: "https://docs.google.com/forms/d/" + url.PathEscape(id) + "/edit"})
}
