package google

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"time"
)

const driveFolderMIME = "application/vnd.google-apps.folder"

type DriveGateway struct {
	filesURL string
	client   *http.Client
}

func NewDriveGateway() *DriveGateway {
	return &DriveGateway{
		filesURL: "https://www.googleapis.com/drive/v3/files",
		client:   &http.Client{Timeout: 15 * time.Second},
	}
}

func (g *DriveGateway) GenerateFolderID(ctx context.Context, token string) (string, error) {
	var result struct {
		IDs []string `json:"ids"`
	}
	_, err := g.request(ctx, token, http.MethodGet, g.filesURL+"/generateIds?count=1&space=drive", nil, &result)
	if err != nil {
		return "", err
	}
	if len(result.IDs) != 1 || result.IDs[0] == "" {
		return "", errors.New("Google Drive returned no folder ID")
	}
	return result.IDs[0], nil
}

type driveFile struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	MIMEType string   `json:"mimeType"`
	Parents  []string `json:"parents,omitempty"`
	Trashed  bool     `json:"trashed,omitempty"`
}

func (g *DriveGateway) EnsureFolder(ctx context.Context, token, id, name, parentID string) error {
	folder := driveFile{ID: id, Name: name, MIMEType: driveFolderMIME}
	if parentID != "" {
		folder.Parents = []string{parentID}
	}
	body, err := json.Marshal(folder)
	if err != nil {
		return errors.New("could not encode Google Drive folder")
	}
	const fields = "id,mimeType,parents,trashed"
	var result driveFile
	status, err := g.request(ctx, token, http.MethodPost, g.filesURL+"?ignoreDefaultVisibility=true&fields="+fields, bytes.NewReader(body), &result)
	if status == http.StatusConflict {
		_, err = g.request(ctx, token, http.MethodGet, g.filesURL+"/"+url.PathEscape(id)+"?fields="+fields, nil, &result)
	}
	if err != nil {
		return err
	}
	if result.ID != id || result.MIMEType != driveFolderMIME || result.Trashed ||
		(parentID != "" && !slices.Contains(result.Parents, parentID)) {
		return errors.New("Google Drive returned a folder with unexpected metadata")
	}
	return nil
}

// GrantAccess gives a user the specified role on a folder via the Drive
// Permissions API. Idempotent: granting to an already-permitted user
// returns success. role: "writer" (editor) or "reader" (viewer).
func (g *DriveGateway) GrantAccess(ctx context.Context, token, folderID, email, role string) error {
	payload := map[string]any{
		"role":         role,
		"type":         "user",
		"emailAddress": email,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return errors.New("could not encode Google Drive permission")
	}
	endpoint := fmt.Sprintf("%s/%s/permissions?sendNotificationEmail=false", g.filesURL, url.PathEscape(folderID))
	_, err = g.request(ctx, token, http.MethodPost, endpoint, bytes.NewReader(body), &struct{}{})
	if err != nil {
		return fmt.Errorf("drive: grant %s to %s: %w", role, email[:min(len(email), 3)]+"***", err) // email partially masked
	}
	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (g *DriveGateway) request(ctx context.Context, token, method, endpoint string, body io.Reader, result any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return 0, errors.New("could not build Google Drive request")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return 0, errors.New("Google Drive request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("Google Drive returned HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(result); err != nil {
		return resp.StatusCode, errors.New("Google Drive returned invalid metadata")
	}
	return resp.StatusCode, nil
}
