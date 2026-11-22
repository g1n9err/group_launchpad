package transport

import (
	"context"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"launchpad/internal/core/integrations"
	gen "launchpad/internal/transport/gen"
)

type IntakeDeliveryService interface {
	QueueDeliveries(context.Context, uuid.UUID, *string) ([]integrations.IntakeDelivery, error)
	Status(context.Context, uuid.UUID) ([]integrations.IntakeDelivery, error)
}

// DistributeGroupForm POST — coordinator/admin (writes + PII recipients).
func (h *Handlers) DistributeGroupForm(c echo.Context, groupID gen.GroupId) error {
	if _, err := h.requireGroupWrite(c, uuid.UUID(groupID)); err != nil {
		return err
	}
	if h.delivery == nil || !h.deliveryEnabled {
		return errNotReady("intake email delivery is unavailable")
	}
	var body gen.IntakeDistributionRequest
	if c.Request().ContentLength != 0 {
		if err := c.Bind(&body); err != nil {
			return errBadRequest("invalid intake email request")
		}
	}
	if body.Message != nil {
		message := strings.TrimSpace(*body.Message)
		if utf8.RuneCountInString(message) > 4000 || strings.ContainsRune(message, 0) {
			return errBadRequest("intake email message must contain at most 4000 characters and no null bytes")
		}
		body.Message = &message
	}
	deliveries, err := h.delivery.QueueDeliveries(c.Request().Context(), groupID, body.Message)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusAccepted, toIntakeDeliveries(deliveries))
}

// GetGroupFormDeliveries GET — recipient emails are student PII:
// coordinator/admin only.
func (h *Handlers) GetGroupFormDeliveries(c echo.Context, groupID gen.GroupId) error {
	if _, err := h.requireGroupWrite(c, uuid.UUID(groupID)); err != nil {
		return err
	}
	if h.delivery == nil {
		return errNotReady("intake delivery status is unavailable")
	}
	deliveries, err := h.delivery.Status(c.Request().Context(), groupID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, toIntakeDeliveries(deliveries))
}

func toIntakeDeliveries(deliveries []integrations.IntakeDelivery) gen.IntakeDeliveries {
	result := gen.IntakeDeliveries{Deliveries: make([]gen.IntakeDelivery, 0, len(deliveries))}
	for _, delivery := range deliveries {
		item := gen.IntakeDelivery{Id: delivery.ID, Recipient: delivery.Recipient,
			Status: gen.IntakeDeliveryStatus(delivery.Status), Attempts: delivery.Attempts}
		if delivery.Error != "" {
			item.Error = &delivery.Error
		}
		result.Deliveries = append(result.Deliveries, item)
	}
	return result
}
