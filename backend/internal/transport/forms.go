package transport

import (
	"net/http"
	"net/url"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"launchpad/internal/core/auth"
	"launchpad/internal/core/integrations"
	gen "launchpad/internal/transport/gen"
)

func (h *Handlers) GetGroupIntakeForm(c echo.Context, groupID gen.GroupId) error {
	id, err := h.requireGroupRead(c, uuid.UUID(groupID))
	if err != nil {
		return err
	}
	if h.forms == nil {
		return errNotReady("Google Forms is unavailable")
	}
	form, err := h.forms.State(c.Request().Context(), groupID)
	if err != nil {
		return err
	}
	result := gen.IntakeFormState{
		Status: form.Status, VerifiedEmail: form.VerifiedSince != nil,
		Questions: make([]gen.IntakeFormQuestion, 0, len(form.Questions)),
	}
	// The edit URL is an operator capability (modify the form) — restricted
	// to coordinator/admin. Members receive the responder URL only.
	staff := id.Role == auth.RoleCoordinator || id.Role == auth.RoleAdmin
	if staff && form.ID != "" {
		editURL := "https://docs.google.com/forms/d/" + url.PathEscape(form.ID) + "/edit"
		result.EditUrl = &editURL
	}
	if form.ResponderURL != "" {
		result.ResponderUrl = &form.ResponderURL
	}
	for _, q := range form.Questions {
		question := gen.IntakeFormQuestion{Title: q.Title, Type: gen.IntakeFormQuestionType(q.Type), Required: q.Required}
		if len(q.Options) > 0 {
			question.Options = &q.Options
		}
		result.Questions = append(result.Questions, question)
	}
	return c.JSON(http.StatusOK, result)
}

func (h *Handlers) ProvisionGroupForm(c echo.Context, groupID gen.GroupId) error {
	if _, err := h.requireGroupWrite(c, uuid.UUID(groupID)); err != nil {
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

func (h *Handlers) PublishGroupForm(c echo.Context, groupID gen.GroupId) error {
	if _, err := h.requireGroupWrite(c, uuid.UUID(groupID)); err != nil {
		return err
	}
	if h.forms == nil {
		return errNotReady("Google Forms is unavailable")
	}
	form, err := h.forms.Publish(c.Request().Context(), groupID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, gen.PublishedIntakeForm{FormId: form.ID, ResponderUrl: form.ResponderURL})
}

func (h *Handlers) ImportGroupFormResponses(c echo.Context, groupID gen.GroupId) error {
	if _, err := h.requireGroupWrite(c, uuid.UUID(groupID)); err != nil {
		return err
	}
	if h.forms == nil {
		return errNotReady("Google Forms is unavailable")
	}
	summary, err := h.forms.ImportResponses(c.Request().Context(), groupID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, gen.IntakeResponseSummary{Saved: summary.Saved, Unchanged: summary.Unchanged, Ignored: summary.Ignored})
}
