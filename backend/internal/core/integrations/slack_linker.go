package integrations

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
)

// NormalizeEmail is the single canonical form for email matching: trimmed and
// lowercased. Applied to every comparison so case-variant duplicates in the
// users table are detected as ambiguity instead of silently matching one.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// Link outcomes — durable reason codes recorded on the event ledger. Symbolic
// only: never the email itself (PII stays out of reasons and logs).
const (
	LinkReasonNoMatch          = "user_no_email_match"      // zero normalized matches
	LinkReasonAmbiguous        = "user_ambiguous_email"     // 2+ case-variant matches
	LinkReasonEmailUnavailable = "user_email_unavailable"   // Slack user gone or email hidden
	LinkReasonLookupFailed     = "user_email_lookup_failed" // transient — retried on next event
	LinkReasonUserMismatch     = "user_slack_conflict"      // matched user bound to another Slack ID
	LinkReasonIDTaken          = "slack_id_taken"           // this Slack ID bound to another user
)

// LinkState is the guarded outcome of persisting a slack_user_id.
type LinkState string

const (
	LinkStateLinked   LinkState = "linked"         // we set the mapping
	LinkStateAlready  LinkState = "already_linked" // concurrent run set the same mapping
	LinkStateTaken    LinkState = "slack_id_taken" // unique violation: another user owns the ID
	LinkStateMismatch LinkState = "user_mismatch"  // user now bound to a different Slack ID
)

// SlackIdentityRepository is the persistence port for identity linking
// (impl: internal/store).
type SlackIdentityRepository interface {
	// FindUserIDsByEmail matches on lower(trim(email)); multiple rows are
	// possible when stored emails differ only by case.
	FindUserIDsByEmail(ctx context.Context, normalizedEmail string) ([]uuid.UUID, error)
	// LinkSlackUser guards the write: only a user with no Slack ID gets one;
	// unique-violation and lost-race outcomes are reported, never guessed.
	LinkSlackUser(ctx context.Context, userID uuid.UUID, slackUserID string) (LinkState, error)
}

// SlackIdentityLinker resolves a Slack user ID to an internal user via the
// user's verified Slack email. Only unambiguous matches are linked.
type SlackIdentityLinker struct {
	gateway SlackGateway
	repo    SlackIdentityRepository
}

// NewSlackIdentityLinker builds the linker.
func NewSlackIdentityLinker(gateway SlackGateway, repo SlackIdentityRepository) *SlackIdentityLinker {
	return &SlackIdentityLinker{gateway: gateway, repo: repo}
}

// Link attempts to bind slackUserID to an internal user. Returns the user ID
// on success; otherwise a durable reason code (never the email address).
func (l *SlackIdentityLinker) Link(ctx context.Context, slackUserID string) (uuid.UUID, string, error) {
	email, err := l.gateway.UserEmail(ctx, slackUserID)
	if err != nil {
		if errors.Is(err, ErrSlackUserNotFound) { //nolint:errorlint — sentinel
			return uuid.Nil, LinkReasonEmailUnavailable, nil
		}
		return uuid.Nil, LinkReasonLookupFailed, nil // transient: next event retries
	}
	if email == "" {
		return uuid.Nil, LinkReasonEmailUnavailable, nil
	}

	ids, err := s_findUserIDs(ctx, l, email)
	if err != nil {
		return uuid.Nil, "", err // infra error: caller surfaces as 500 path
	}
	switch {
	case len(ids) == 0:
		return uuid.Nil, LinkReasonNoMatch, nil
	case len(ids) > 1:
		return uuid.Nil, LinkReasonAmbiguous, nil // never guess between duplicates
	}

	state, err := l.repo.LinkSlackUser(ctx, ids[0], slackUserID)
	if err != nil {
		return uuid.Nil, "", err
	}
	switch state {
	case LinkStateLinked, LinkStateAlready:
		return ids[0], "", nil
	default: // taken or mismatch — do not overwrite silently
		if state == LinkStateTaken {
			return uuid.Nil, LinkReasonIDTaken, nil
		}
		return uuid.Nil, LinkReasonUserMismatch, nil
	}
}

func s_findUserIDs(ctx context.Context, l *SlackIdentityLinker, email string) ([]uuid.UUID, error) {
	return l.repo.FindUserIDsByEmail(ctx, NormalizeEmail(email))
}
