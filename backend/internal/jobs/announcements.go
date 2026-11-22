package jobs

import (
	"context"
	"encoding/json"
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

type AnnouncementPublisherResolver interface {
	ForGroup(context.Context, uuid.UUID) (AnnouncementPublisher, error)
}

type AnnouncementWorker struct {
	river.WorkerDefaults[AnnouncementArgs]
	Store interface {
		StartAnnouncement(context.Context, uuid.UUID) (announcements.Announcement, bool, error)
		FinishAnnouncement(context.Context, uuid.UUID, string, string, string) error
		EnsureAnnouncementNotification(context.Context, uuid.UUID) error
	}
	Slack         AnnouncementPublisher
	SlackResolver AnnouncementPublisherResolver
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
	publisher := w.Slack
	if w.SlackResolver != nil {
		publisher, err = w.SlackResolver.ForGroup(ctx, item.GroupID)
		if err != nil {
			return w.Store.FinishAnnouncement(ctx, item.ID, "failed", "", "Slack workspace is not available")
		}
	}
	if publisher == nil {
		return w.Store.FinishAnnouncement(ctx, item.ID, "failed", "", "Slack is not configured")
	}
	channelIDs := item.ChannelIDs
	if len(channelIDs) == 0 {
		channelIDs = []string{item.ChannelID}
	}
	deliveries := make([]map[string]string, 0, len(channelIDs))
	failed, unknownCount := 0, 0
	var firstError string
	for _, channelID := range channelIDs {
		id, unknown, postErr := publisher.PostAnnouncement(ctx, channelID, item.Message)
		if postErr != nil {
			failed++
			if firstError == "" {
				firstError = postErr.Error()
			}
			if unknown {
				unknownCount++
			}
			continue
		}
		deliveries = append(deliveries, map[string]string{"channelId": channelID, "messageId": id})
	}
	status, message, externalID := "published", "", ""
	if len(channelIDs) > 1 {
		encoded, _ := json.Marshal(deliveries)
		externalID = string(encoded)
	} else if len(deliveries) == 1 {
		externalID = deliveries[0]["messageId"]
	}
	if failed > 0 {
		status = "failed"
		message = firstError
		if unknownCount > 0 {
			status = "unknown"
		}
		if len(deliveries) > 0 && unknownCount == 0 {
			message = "announcement was delivered to only some group channels"
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
		ClearAnnouncementHistory(context.Context, uuid.UUID) error
	}
}

func (s *AnnouncementService) Schedule(ctx context.Context, groupID uuid.UUID, request announcements.Schedule) (announcements.Announcement, error) {
	request, err := request.Validate()
	if err != nil {
		return announcements.Announcement{}, err
	}
	if request.Type == "general" {
		request.ChannelName = "all"
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

func (s *AnnouncementService) ClearHistory(ctx context.Context, groupID uuid.UUID) error {
	return s.Store.ClearAnnouncementHistory(ctx, groupID)
}
