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
	"strings"
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

// ActivateCourse PATCHes courseState to ACTIVE. PROVISIONED courses are
// drafts invisible to students; PATCHing an already-ACTIVE course with the
// same value is a no-op, so this is idempotent. updateMask is a QUERY
// parameter in the Classroom API, not a body field.
func (g *ClassroomGateway) ActivateCourse(ctx context.Context, token, courseID string) error {
	body, err := json.Marshal(map[string]string{"courseState": "ACTIVE"})
	if err != nil {
		return errors.New("could not encode activation")
	}
	endpoint := fmt.Sprintf("%s/%s?updateMask=courseState", g.coursesURL, url.PathEscape(courseID))
	resp, err := g.rawRequest(ctx, token, http.MethodPatch, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("classroom: activate course: HTTP %d", resp.StatusCode)
	}
	return nil
}

// EnrollStudent invites a student to a course by email via the Classroom
// API. Already-enrolled (409) returns success — idempotent.
func (g *ClassroomGateway) EnrollStudent(ctx context.Context, token, courseID, email string) error {
	body, err := json.Marshal(map[string]string{"userId": email})
	if err != nil {
		return errors.New("could not encode enrollment")
	}
	endpoint := fmt.Sprintf("%s/%s/students?enrollmentCode=", g.coursesURL, url.PathEscape(courseID))
	resp, err := g.rawRequest(ctx, token, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusConflict {
		return nil // already enrolled
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("classroom: enroll %s: HTTP %d", maskEmail(email), resp.StatusCode)
	}
	return nil
}

// AssignTeacher adds a teacher to a course by email via the Classroom API.
// Already-assigned (409) returns success — idempotent.
func (g *ClassroomGateway) AssignTeacher(ctx context.Context, token, courseID, email string) error {
	body, err := json.Marshal(map[string]string{"userId": email})
	if err != nil {
		return errors.New("could not encode teacher assignment")
	}
	endpoint := fmt.Sprintf("%s/%s/teachers", g.coursesURL, url.PathEscape(courseID))
	resp, err := g.rawRequest(ctx, token, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusConflict {
		return nil // already a teacher
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("classroom: assign teacher %s: HTTP %d", maskEmail(email), resp.StatusCode)
	}
	return nil
}

func maskEmail(email string) string {
	if at := strings.Index(email, "@"); at > 2 {
		return email[:3] + "***" + email[at:]
	}
	return "***"
}

func (g *ClassroomGateway) rawRequest(ctx context.Context, token, method, endpoint string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, errors.New("could not build Google Classroom request")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return g.client.Do(req)
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
