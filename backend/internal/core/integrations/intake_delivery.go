package integrations

import (
	"context"

	"github.com/google/uuid"
)

type IntakeDelivery struct {
	ID        uuid.UUID
	Recipient string
	Status    string
	Attempts  int
	Error     string
}

type IntakeEmail struct {
	Recipient    string
	GroupName    string
	ResponderURL string
	Message      string
}

type IntakeMailer interface {
	// Send reports whether the relay may have accepted the email despite an error.
	Send(ctx context.Context, email IntakeEmail) (acceptanceUnknown bool, err error)
}
