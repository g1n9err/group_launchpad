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
	tokens integrations.GoogleAccessTokenSource
	client *http.Client
}

func NewAuditChecker(tokens integrations.GoogleAccessTokenSource) *AuditChecker {
	return &AuditChecker{tokens: tokens, client: &http.Client{Timeout: 5 * time.Second}}
}

func (g *AuditChecker) CheckResource(ctx context.Context, resource audit.Resource) string {
	id := url.PathEscape(resource.ID)
	var endpoint string
	switch resource.Provider {
	case "google_classroom":
		endpoint = "https://classroom.googleapis.com/v1/courses/" + id + "?fields=id,courseState"
	case "google_drive":
		endpoint = "https://www.googleapis.com/drive/v3/files/" + id + "?fields=id,mimeType,trashed,parents"
	case "google_forms":
		endpoint = "https://forms.googleapis.com/v1/forms/" + id + "?fields=formId,publishSettings"
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
		if metadata.CourseState == "ARCHIVED" {
			return "archived"
		}
		if metadata.CourseState != "ACTIVE" {
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
