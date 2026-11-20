package jobs

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"launchpad/internal/core/announcements"
)

const SlackAnnouncementsQueue = "slack_announcements"

type AnnouncementArgs struct {
	ID uuid.UUID `json:"id"`
}

func (AnnouncementArgs) Kind() string { return "post_slack_announcement" }

type AnnouncementPublisher interface {
	PostAnnouncement(ctx context.Context, channelID, text string) (messageID string, outcomeUnknown bool, err error)
}

type AnnouncementWorker struct {
	river.WorkerDefaults[AnnouncementArgs]
	Store interface {
		StartAnnouncement(context.Context, uuid.UUID) (announcements.Announcement, bool, error)
		FinishAnnouncement(context.Context, uuid.UUID, string, string, string) error
		EnsureAnnouncementNotification(context.Context, uuid.UUID) error
	}
	Slack AnnouncementPublisher
}

func (w *AnnouncementWorker) Work(ctx context.Context, job *river.Job[AnnouncementArgs]) error {
	item, claimed, err := w.Store.StartAnnouncement(ctx, job.Args.ID)
	if err != nil {
		return err
	}
	if !claimed {
		if item.Status == "scheduled" {
			return river.JobSnooze(max(time.Second, time.Until(item.ScheduledAt)))
		}
		if item.Status == "publishing" {
			return river.JobSnooze(30 * time.Second)
		}
		if item.Status == "published" || item.Status == "failed" || item.Status == "unknown" {
			return w.Store.EnsureAnnouncementNotification(ctx, item.ID)
		}
		return nil
	}
	externalID, unknown, postErr := w.Slack.PostAnnouncement(ctx, item.ChannelID, item.Message)
	status, message := "published", ""
	if postErr != nil {
		status, message = "failed", postErr.Error()
		if unknown {
			status = "unknown"
		}
	}
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return w.Store.FinishAnnouncement(writeCtx, item.ID, status, externalID, message)
}

type AnnouncementService struct {
	River *river.Client[pgx.Tx]
	Store interface {
		ScheduleAnnouncement(context.Context, uuid.UUID, announcements.Schedule, func(pgx.Tx, uuid.UUID) error) (announcements.Announcement, error)
		Announcements(context.Context, uuid.UUID) ([]announcements.Announcement, error)
	}
}

func (s *AnnouncementService) Schedule(ctx context.Context, groupID uuid.UUID, request announcements.Schedule) (announcements.Announcement, error) {
	request, err := request.Validate()
	if err != nil {
		return announcements.Announcement{}, err
	}
	return s.Store.ScheduleAnnouncement(ctx, groupID, request, func(tx pgx.Tx, id uuid.UUID) error {
		_, err := s.River.InsertTx(ctx, tx, AnnouncementArgs{ID: id}, &river.InsertOpts{
			Queue: SlackAnnouncementsQueue, ScheduledAt: request.ScheduledAt,
		})
		return err
	})
}

func (s *AnnouncementService) List(ctx context.Context, groupID uuid.UUID) ([]announcements.Announcement, error) {
	return s.Store.Announcements(ctx, groupID)
}
