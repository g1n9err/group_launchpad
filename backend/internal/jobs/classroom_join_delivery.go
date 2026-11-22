package jobs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
)

type ClassroomJoinDeliveryArgs struct {
	DeliveryID uuid.UUID `json:"delivery_id"`
	CourseID   string    `json:"course_id"`
}

func (ClassroomJoinDeliveryArgs) Kind() string { return "classroom_join_delivery" }

type ClassroomJoinDeliveryStore interface {
	QueueClassroomJoinDeliveries(context.Context, uuid.UUID, string, []string, func(pgx.Tx, uuid.UUID) error) error
	StartClassroomJoinDelivery(context.Context, uuid.UUID) (string, string, bool, error)
	FinishClassroomJoinDelivery(context.Context, uuid.UUID, string) error
}

func QueueClassroomJoinEmails(store ClassroomJoinDeliveryStore, client *river.Client[pgx.Tx]) func(context.Context, uuid.UUID, string, ...string) error {
	return func(ctx context.Context, groupID uuid.UUID, courseID string, recipients ...string) error {
		return store.QueueClassroomJoinDeliveries(ctx, groupID, courseID, recipients, func(tx pgx.Tx, id uuid.UUID) error {
			_, err := client.InsertTx(ctx, tx, ClassroomJoinDeliveryArgs{DeliveryID: id, CourseID: courseID},
				&river.InsertOpts{Queue: IntakeEmailQueue})
			return err
		})
	}
}

type ClassroomJoinDeliveryWorker struct {
	river.WorkerDefaults[ClassroomJoinDeliveryArgs]
	Store    ClassroomJoinDeliveryStore
	JoinCode func(context.Context, string) (string, error)
	Mailer   AuditMailer
}

func (w *ClassroomJoinDeliveryWorker) Work(ctx context.Context, job *river.Job[ClassroomJoinDeliveryArgs]) error {
	id := job.Args.DeliveryID
	recipient, courseName, started, err := w.Store.StartClassroomJoinDelivery(ctx, id)
	if err != nil || !started {
		return err
	}

	code, err := w.JoinCode(ctx, job.Args.CourseID)
	if err != nil {
		status := "failed"
		if job.Attempt < job.MaxAttempts {
			status = "pending"
		}
		finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		return errors.Join(err, w.Store.FinishClassroomJoinDelivery(finishCtx, id, status))
	}

	body := fmt.Sprintf("Hello,\n\nOpen https://classroom.google.com/ to join %s. If you are joining as a teacher, accept your teaching invitation in Classroom. If you are joining as a student, select Join class and enter this code:\n\n%s\n", courseName, code)
	unknown, sendErr := w.Mailer.SendMessage(ctx, recipient, "Google Classroom join code", body)
	status := "failed"
	switch {
	case unknown:
		status = "unknown"
	case sendErr == nil:
		status = "sent"
	}
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return w.Store.FinishClassroomJoinDelivery(finishCtx, id, status)
}
