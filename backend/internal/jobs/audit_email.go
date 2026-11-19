package jobs

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"launchpad/internal/core/audit"
)

const AuditEmailQueue = "audit_email"

type AuditEmailArgs struct {
	NotificationID uuid.UUID `json:"notification_id"`
}

func (AuditEmailArgs) Kind() string { return "audit_notification_email" }

type AuditMailer interface {
	SendMessage(ctx context.Context, recipient, subject, body string) (acceptanceUnknown bool, err error)
}

type AuditEmailWorker struct {
	river.WorkerDefaults[AuditEmailArgs]
	Store interface {
		StartAuditEmail(context.Context, uuid.UUID) (audit.Notification, bool, error)
		FinishAuditEmail(context.Context, uuid.UUID, string, string) error
	}
	Mailer    AuditMailer
	Recipient string
}

func (w *AuditEmailWorker) Work(ctx context.Context, job *river.Job[AuditEmailArgs]) error {
	id := job.Args.NotificationID
	notification, started, err := w.Store.StartAuditEmail(ctx, id)
	if err != nil || !started {
		return err
	}
	var body strings.Builder
	fmt.Fprintf(&body, "Group: %s\nChecked: %s\n\n", notification.GroupName, notification.CheckedAt.UTC().Format(time.RFC3339))
	for _, change := range notification.Changes {
		if change.Before == "" || change.Before == "ok" {
			fmt.Fprintf(&body, "- %s / %s: %s\n", change.Provider, change.Key, change.After)
		} else if change.After == "ok" {
			fmt.Fprintf(&body, "- %s / %s: resolved (was %s)\n", change.Provider, change.Key, change.Before)
		} else {
			fmt.Fprintf(&body, "- %s / %s: %s to %s\n", change.Provider, change.Key, change.Before, change.After)
		}
	}
	unknown, sendErr := w.Mailer.SendMessage(ctx, w.Recipient, "Group Launchpad resource audit", body.String())
	status, message := "sent", ""
	if sendErr != nil {
		status, message = "failed", sendErr.Error()
		if unknown {
			status = "unknown"
		}
	}
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return w.Store.FinishAuditEmail(writeCtx, id, status, message)
}
