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
	"time"

	"launchpad/internal/core/integrations"
)

type ClassroomGateway struct {
	coursesURL string
	client     *http.Client
}

func NewClassroomGateway() *ClassroomGateway {
	return &ClassroomGateway{
		coursesURL: "https://classroom.googleapis.com/v1/courses",
		client:     &http.Client{Timeout: 15 * time.Second},
	}
}

// EnsureCourse assigns the alias atomically on creation. A concurrent or
// previously interrupted request can then recover the same course by alias.
func (g *ClassroomGateway) EnsureCourse(ctx context.Context, token string, in integrations.ClassroomCourseInput) (string, error) {
	body, err := json.Marshal(struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Section     string `json:"section"`
		Description string `json:"description"`
		OwnerID     string `json:"ownerId"`
		CourseState string `json:"courseState"`
	}{in.Alias, in.Name, in.Section, in.Description, "me", "PROVISIONED"})
	if err != nil {
		return "", errors.New("could not encode Google Classroom course")
	}
	id, status, err := g.courseRequest(ctx, token, http.MethodPost, g.coursesURL, bytes.NewReader(body))
	if status == http.StatusConflict {
		id, _, err = g.courseRequest(ctx, token, http.MethodGet, g.coursesURL+"/"+url.PathEscape(in.Alias), nil)
	}
	return id, err
}

func (g *ClassroomGateway) courseRequest(ctx context.Context, token, method, endpoint string, body io.Reader) (string, int, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return "", 0, errors.New("could not build Google Classroom request")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return "", 0, errors.New("Google Classroom request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", resp.StatusCode, fmt.Errorf("Google Classroom returned HTTP %d", resp.StatusCode)
	}
	var course struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&course); err != nil || course.ID == "" {
		return "", resp.StatusCode, errors.New("Google Classroom returned no course ID")
	}
	return course.ID, resp.StatusCode, nil
}
