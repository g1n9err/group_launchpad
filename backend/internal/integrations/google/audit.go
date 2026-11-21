package google

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"slices"
	"time"

	"launchpad/internal/core/audit"
	"launchpad/internal/core/integrations"
)

type AuditChecker struct {
	tokens  integrations.GoogleAccessTokenSource
	client  *http.Client
	baseURL string // overrides the hardcoded endpoints (tests)
}

func NewAuditChecker(tokens integrations.GoogleAccessTokenSource) *AuditChecker {
	return &AuditChecker{tokens: tokens, client: &http.Client{Timeout: 5 * time.Second}}
}

func (g *AuditChecker) CheckResource(ctx context.Context, resource audit.Resource) string {
	id := url.PathEscape(resource.ID)
	var endpoint string
	base := g.baseURL
	switch resource.Provider {
	case "google_classroom":
		if base == "" {
			base = "https://classroom.googleapis.com"
		}
		endpoint = base + "/v1/courses/" + id + "?fields=id,courseState"
	case "google_drive":
		if base == "" {
			base = "https://www.googleapis.com"
		}
		endpoint = base + "/drive/v3/files/" + id + "?fields=id,mimeType,trashed,parents"
	case "google_forms":
		if base == "" {
			base = "https://forms.googleapis.com"
		}
		endpoint = base + "/v1/forms/" + id + "?fields=formId,publishSettings"
	default:
		return "unavailable"
	}
	token, err := g.tokens.AccessToken(ctx)
	if err != nil {
		return "unavailable"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "unavailable"
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := g.client.Do(req)
	if err != nil {
		return "unavailable"
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
		return "missing_or_inaccessible"
	}
	if resp.StatusCode != http.StatusOK {
		return "unavailable"
	}

	var metadata struct {
		ID          string   `json:"id"`
		FormID      string   `json:"formId"`
		CourseState string   `json:"courseState"`
		MIMEType    string   `json:"mimeType"`
		Trashed     bool     `json:"trashed"`
		Parents     []string `json:"parents"`
		Publish     struct {
			State struct {
				IsPublished bool `json:"isPublished"`
				IsAccepting bool `json:"isAcceptingResponses"`
			} `json:"publishState"`
		} `json:"publishSettings"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&metadata); err != nil {
		return "unavailable"
	}
	if metadata.ID != resource.ID && metadata.FormID != resource.ID {
		return "unavailable"
	}
	switch resource.Provider {
	case "google_classroom":
		switch metadata.CourseState {
		case "ACTIVE":
			// ok — fall through
		case "PROVISIONED":
			// Consumer (non-Workspace for Education) accounts cannot
			// transition courses to ACTIVE via the API (403
			// @CourseStateDenied); a PROVISIONED course EXISTS, which is
			// what PRD §4.7 audits for. Reported as its own status, not an
			// inconsistency.
			return "provisioned"
		case "ARCHIVED":
			return "archived"
		default:
			return "inactive"
		}
	case "google_drive":
		if metadata.Trashed {
			return "trashed"
		}
		if metadata.MIMEType != driveFolderMIME ||
			(resource.ParentID != "" && !slices.Contains(metadata.Parents, resource.ParentID)) {
			return "misconfigured"
		}
	case "google_forms":
		if !metadata.Publish.State.IsPublished || !metadata.Publish.State.IsAccepting {
			return "inactive"
		}
	}
	return "ok"
}

// WithHTTPClient binds a custom client and base URL (test fixture support).
func (g *AuditChecker) WithHTTPClient(client *http.Client, baseURL string) *AuditChecker {
	g.client = client
	g.baseURL = baseURL
	return g
}
