package jobs

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"launchpad/internal/core/integrations"
)

const IntakeEmailQueue = "intake_email"

type IntakeDeliveryArgs struct {
	DeliveryID uuid.UUID `json:"delivery_id"`
}

func (IntakeDeliveryArgs) Kind() string { return "intake_delivery" }

type IntakeDeliveryStore interface {
	StartIntakeDelivery(context.Context, uuid.UUID) (integrations.IntakeEmail, bool, error)
	FinishIntakeDelivery(context.Context, uuid.UUID, string, string) error
}

type IntakeDeliveryWorker struct {
	river.WorkerDefaults[IntakeDeliveryArgs]
	Store  IntakeDeliveryStore
	Mailer integrations.IntakeMailer
}

func (w *IntakeDeliveryWorker) Work(ctx context.Context, job *river.Job[IntakeDeliveryArgs]) error {
	id := job.Args.DeliveryID
	email, started, err := w.Store.StartIntakeDelivery(ctx, id)
	if err != nil || !started {
		return err
	}
	unknown, sendErr := w.Mailer.Send(ctx, email)
	status, message := "sent", ""
	if sendErr != nil {
		status, message = "failed", sendErr.Error()
		if unknown {
			status = "unknown"
		}
	}
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return w.Store.FinishIntakeDelivery(writeCtx, id, status, message)
}

type IntakeDeliveryQueue interface {
	QueueIntakeDeliveries(context.Context, uuid.UUID, func(pgx.Tx, uuid.UUID) error) ([]integrations.IntakeDelivery, error)
	IntakeDeliveries(context.Context, uuid.UUID) ([]integrations.IntakeDelivery, error)
}

type IntakeDeliveryService struct {
	Queue      IntakeDeliveryQueue
	River      *river.Client[pgx.Tx]
	CheckReady func(context.Context) error
}

func (s *IntakeDeliveryService) QueueDeliveries(ctx context.Context, groupID uuid.UUID) ([]integrations.IntakeDelivery, error) {
	if s.CheckReady != nil {
		if err := s.CheckReady(ctx); err != nil {
			return nil, err
		}
	}
	return s.Queue.QueueIntakeDeliveries(ctx, groupID, func(tx pgx.Tx, id uuid.UUID) error {
		_, err := s.River.InsertTx(ctx, tx, IntakeDeliveryArgs{DeliveryID: id}, &river.InsertOpts{Queue: IntakeEmailQueue})
		return err
	})
}

func (s *IntakeDeliveryService) Status(ctx context.Context, groupID uuid.UUID) ([]integrations.IntakeDelivery, error) {
	return s.Queue.IntakeDeliveries(ctx, groupID)
}
