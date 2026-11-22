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

func (g *ClassroomGateway) JoinCode(ctx context.Context, token, courseID string) (string, error) {
	endpoint := fmt.Sprintf("%s/%s?fields=courseState,enrollmentCode", g.coursesURL, url.PathEscape(courseID))
	resp, err := g.rawRequest(ctx, token, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("read Classroom join code: HTTP %d", resp.StatusCode)
	}
	var course struct {
		State string `json:"courseState"`
		Code  string `json:"enrollmentCode"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<10)).Decode(&course); err != nil {
		return "", errors.New("invalid Classroom course response")
	}
	if course.State != "ACTIVE" || course.Code == "" {
		return "", errors.New("Classroom course must be active and have a join code")
	}
	return course.Code, nil
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
		// Surface Google's own reason (e.g. course needs a teacher before
		// it can be ACTIVE) — it is the operator's actionable detail.
		var googleErr struct {
			Error struct {
				Message string `json:"message"`
				Status  string `json:"status"`
			} `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 4<<10)).Decode(&googleErr)
		if reason := strings.TrimSpace(googleErr.Error.Message); reason != "" {
			return fmt.Errorf("classroom: activate course: HTTP %d: %s", resp.StatusCode, reason)
		}
		return fmt.Errorf("classroom: activate course: HTTP %d", resp.StatusCode)
	}
	return nil
}

// EnrollStudent adds a student directly when authorized, or creates an invitation.
func (g *ClassroomGateway) EnrollStudent(ctx context.Context, token, courseID, email string) error {
	return g.addCourseMember(ctx, token, courseID, email, "students", "STUDENT")
}

// AssignTeacher adds a teacher directly when authorized, or creates an invitation.
func (g *ClassroomGateway) AssignTeacher(ctx context.Context, token, courseID, email string) error {
	return g.addCourseMember(ctx, token, courseID, email, "teachers", "TEACHER")
}

func (g *ClassroomGateway) RemoveStudent(ctx context.Context, token, courseID, email string) error {
	return g.removeCourseMember(ctx, token, courseID, email, "students")
}

func (g *ClassroomGateway) RemoveTeacher(ctx context.Context, token, courseID, email string) error {
	return g.removeCourseMember(ctx, token, courseID, email, "teachers")
}

func (g *ClassroomGateway) removeCourseMember(ctx context.Context, token, courseID, email, roster string) error {
	role := "STUDENT"
	if roster == "teachers" {
		role = "TEACHER"
	}
	invitesURL := strings.TrimSuffix(g.coursesURL, "/courses") + "/invitations?courseId=" + url.QueryEscape(courseID) + "&userId=" + url.QueryEscape(email)
	inviteResp, err := g.rawRequest(ctx, token, http.MethodGet, invitesURL, nil)
	if err != nil {
		return err
	}
	if inviteResp.StatusCode >= 200 && inviteResp.StatusCode < 300 {
		var payload struct {
			Invitations []struct {
				ID       string `json:"id"`
				UserID   string `json:"userId"`
				CourseID string `json:"courseId"`
				Role     string `json:"role"`
			} `json:"invitations"`
		}
		decodeErr := json.NewDecoder(io.LimitReader(inviteResp.Body, 64<<10)).Decode(&payload)
		_ = inviteResp.Body.Close()
		if decodeErr != nil {
			return errors.New("classroom: invalid invitations response")
		}
		for _, invitation := range payload.Invitations {
			if invitation.Role != role || invitation.UserID != email || invitation.CourseID != courseID || invitation.ID == "" {
				continue
			}
			deleteResp, e := g.rawRequest(ctx, token, http.MethodDelete, strings.TrimSuffix(g.coursesURL, "/courses")+"/invitations/"+url.PathEscape(invitation.ID), nil)
			if e != nil {
				return e
			}
			_, _ = io.Copy(io.Discard, io.LimitReader(deleteResp.Body, 4<<10))
			code := deleteResp.StatusCode
			_ = deleteResp.Body.Close()
			if code != http.StatusNotFound && (code < 200 || code >= 300) {
				return fmt.Errorf("classroom: revoke invitation: HTTP %d", code)
			}
		}
	} else {
		code := inviteResp.StatusCode
		_, _ = io.Copy(io.Discard, io.LimitReader(inviteResp.Body, 4<<10))
		_ = inviteResp.Body.Close()
		if code != http.StatusNotFound {
			return fmt.Errorf("classroom: list invitations: HTTP %d", code)
		}
	}
	endpoint := fmt.Sprintf("%s/%s/%s/%s", g.coursesURL, url.PathEscape(courseID), roster, url.PathEscape(email))
	resp, err := g.rawRequest(ctx, token, http.MethodDelete, endpoint, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("classroom: remove member: HTTP %d", resp.StatusCode)
	}
	return nil
}

func (g *ClassroomGateway) addCourseMember(ctx context.Context, token, courseID, email, roster, role string) error {
	endpoint := fmt.Sprintf("%s/%s/%s", g.coursesURL, url.PathEscape(courseID), roster)
	status, err := g.postCourseMember(ctx, token, endpoint, map[string]string{"userId": email})
	if err != nil {
		return err
	}
	if status == http.StatusForbidden {
		// Classroom allows domain admins to add members directly. Teachers
		// with roster permissions use an invitation that the recipient accepts.
		invitationsURL := strings.TrimSuffix(g.coursesURL, "/courses") + "/invitations"
		status, err = g.postCourseMember(ctx, token, invitationsURL, map[string]string{
			"courseId": courseID,
			"userId":   email,
			"role":     role,
		})
		if err != nil {
			return err
		}
		if status == http.StatusConflict || (status >= 200 && status < 300) {
			return nil // invitation already exists or was created
		}
		// An accepted invitation can produce FAILED_PRECONDITION on retry.
		if status == http.StatusBadRequest && g.hasCourseMember(ctx, token, endpoint+"/"+url.PathEscape(email)) {
			return nil
		}
		return fmt.Errorf("classroom: invite %s as %s: HTTP %d", maskEmail(email), strings.ToLower(role), status)
	}
	if status == http.StatusConflict || (status >= 200 && status < 300) {
		return nil // already on the roster or added
	}
	return fmt.Errorf("classroom: add %s as %s: HTTP %d", maskEmail(email), strings.ToLower(role), status)
}

func (g *ClassroomGateway) hasCourseMember(ctx context.Context, token, endpoint string) bool {
	resp, err := g.rawRequest(ctx, token, http.MethodGet, endpoint, nil)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	return resp.StatusCode == http.StatusOK
}

func (g *ClassroomGateway) postCourseMember(ctx context.Context, token, endpoint string, fields map[string]string) (int, error) {
	body, err := json.Marshal(fields)
	if err != nil {
		return 0, fmt.Errorf("encode Classroom roster request: %w", err)
	}
	resp, err := g.rawRequest(ctx, token, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	return resp.StatusCode, nil
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
