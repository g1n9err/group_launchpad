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
	"reflect"
	"strings"
	"time"

	"launchpad/internal/core/integrations"
)

type FormsGateway struct {
	formsURL string
	filesURL string
	client   *http.Client
}

func NewFormsGateway() *FormsGateway {
	return &FormsGateway{
		formsURL: "https://forms.googleapis.com/v1/forms",
		filesURL: "https://www.googleapis.com/drive/v3/files",
		client:   &http.Client{Timeout: 15 * time.Second},
	}
}

func (g *FormsGateway) CreateDraft(ctx context.Context, token, title, documentTitle string) (string, bool, error) {
	body := map[string]any{"info": map[string]string{"title": title, "documentTitle": documentTitle}}
	var form struct {
		ID string `json:"formId"`
	}
	status, err := g.request(ctx, token, http.MethodPost, g.formsURL+"?unpublished=true", body, &form)
	if err != nil {
		switch status {
		case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusTooManyRequests:
			return "", true, err
		default:
			return "", false, err
		}
	}
	if form.ID == "" {
		return "", false, errors.New("Google Forms returned no form ID")
	}
	return form.ID, false, nil
}

func (g *FormsGateway) FindDraft(ctx context.Context, token, documentTitle string) (string, error) {
	escaped := strings.NewReplacer("\\", "\\\\", "'", "\\'").Replace(documentTitle)
	query := url.Values{
		"q":        {"trashed = false and mimeType = 'application/vnd.google-apps.form' and name = '" + escaped + "'"},
		"fields":   {"nextPageToken,files(id)"},
		"spaces":   {"drive"},
		"pageSize": {"10"},
	}
	var id string
	for {
		var page struct {
			NextPageToken string `json:"nextPageToken"`
			Files         []struct {
				ID string `json:"id"`
			} `json:"files"`
		}
		if _, err := g.request(ctx, token, http.MethodGet, g.filesURL+"?"+query.Encode(), nil, &page); err != nil {
			return "", err
		}
		for _, file := range page.Files {
			if id != "" || file.ID == "" {
				return "", errors.New("Google Drive returned ambiguous intake form matches")
			}
			id = file.ID
		}
		if page.NextPageToken == "" {
			return id, nil
		}
		query.Set("pageToken", page.NextPageToken)
	}
}

type formTextQuestion struct {
	Paragraph bool `json:"paragraph,omitempty"`
}

type formChoiceOption struct {
	Value   string `json:"value"`
	IsOther bool   `json:"isOther,omitempty"`
}

type formChoiceQuestion struct {
	Type    string             `json:"type"`
	Options []formChoiceOption `json:"options"`
}

type formQuestion struct {
	Required       bool                `json:"required,omitempty"`
	TextQuestion   *formTextQuestion   `json:"textQuestion,omitempty"`
	ChoiceQuestion *formChoiceQuestion `json:"choiceQuestion,omitempty"`
}

type formQuestionItem struct {
	Title        string `json:"title"`
	QuestionItem struct {
		Question formQuestion `json:"question"`
	} `json:"questionItem"`
}

type googleForm struct {
	Revision string             `json:"revisionId"`
	Items    []formQuestionItem `json:"items"`
}

func (g *FormsGateway) EnsureQuestions(ctx context.Context, token, id string, questions []integrations.IntakeQuestion) error {
	endpoint := g.formsURL + "/" + url.PathEscape(id)
	var form googleForm
	if _, err := g.request(ctx, token, http.MethodGet, endpoint, nil, &form); err != nil {
		return err
	}
	expected := make([]formQuestionItem, 0, len(questions))
	requests := make([]map[string]any, 0, len(questions))
	for i, q := range questions {
		var item formQuestionItem
		item.Title = q.Title
		question := &item.QuestionItem.Question
		question.Required = q.Required
		if q.Type == "single_choice" {
			options := make([]formChoiceOption, 0, len(q.Options))
			for _, value := range q.Options {
				options = append(options, formChoiceOption{Value: value})
			}
			question.ChoiceQuestion = &formChoiceQuestion{Type: "RADIO", Options: options}
		} else {
			question.TextQuestion = &formTextQuestion{Paragraph: q.Type == "paragraph"}
		}
		expected = append(expected, item)
		requests = append(requests, map[string]any{"createItem": map[string]any{"item": item, "location": map[string]int{"index": i}}})
	}
	if reflect.DeepEqual(form.Items, expected) {
		return nil
	}
	if len(form.Items) != 0 || form.Revision == "" {
		return errors.New("Google Forms draft has unexpected questions or no revision")
	}
	body := map[string]any{"requests": requests, "writeControl": map[string]string{"requiredRevisionId": form.Revision}}
	var updated map[string]any
	status, err := g.request(ctx, token, http.MethodPost, endpoint+":batchUpdate", body, &updated)
	if status == http.StatusBadRequest {
		var latest googleForm
		if _, readErr := g.request(ctx, token, http.MethodGet, endpoint, nil, &latest); readErr == nil && reflect.DeepEqual(latest.Items, expected) {
			return nil
		}
	}
	return err
}

// Publish sets an idempotent Google publish state and reads the responder URL.
func (g *FormsGateway) Publish(ctx context.Context, token, id string) (string, error) {
	endpoint := g.formsURL + "/" + url.PathEscape(id)
	body := map[string]any{
		"publishSettings": map[string]any{"publishState": map[string]bool{"isPublished": true, "isAcceptingResponses": true}},
		"updateMask":      "publishState",
	}
	var updated map[string]any
	if _, err := g.request(ctx, token, http.MethodPost, endpoint+":setPublishSettings", body, &updated); err != nil {
		return "", err
	}
	var form struct {
		ID              string `json:"formId"`
		ResponderURI    string `json:"responderUri"`
		PublishSettings struct {
			PublishState struct {
				Published bool `json:"isPublished"`
				Accepting bool `json:"isAcceptingResponses"`
			} `json:"publishState"`
		} `json:"publishSettings"`
	}
	if _, err := g.request(ctx, token, http.MethodGet, endpoint+"?fields=formId,responderUri,publishSettings", nil, &form); err != nil {
		return "", err
	}
	state := form.PublishSettings.PublishState
	link, err := url.Parse(form.ResponderURI)
	if form.ID != id || !state.Published || !state.Accepting || err != nil ||
		link.Scheme != "https" || link.Host != "docs.google.com" || link.User != nil ||
		!strings.HasPrefix(link.Path, "/forms/") || !strings.HasSuffix(link.Path, "/viewform") {
		return "", errors.New("Google Forms did not return a published responder link")
	}
	return form.ResponderURI, nil
}

func (g *FormsGateway) request(ctx context.Context, token, method, endpoint string, body, output any) (int, error) {
	var payload io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return 0, errors.New("could not encode Google Forms request")
		}
		payload = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, payload)
	if err != nil {
		return 0, errors.New("could not build Google Forms request")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return 0, errors.New("Google Forms request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("Google Forms request returned HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(output); err != nil {
		return resp.StatusCode, errors.New("Google Forms returned invalid metadata")
	}
	return resp.StatusCode, nil
}

func (g *FormsGateway) VerifiedEmail(ctx context.Context, token, id string) (bool, error) {
	var form struct {
		Settings struct {
			EmailCollectionType string `json:"emailCollectionType"`
		} `json:"settings"`
	}
	endpoint := g.formsURL + "/" + url.PathEscape(id) + "?fields=settings"
	_, err := g.request(ctx, token, http.MethodGet, endpoint, nil, &form)
	return form.Settings.EmailCollectionType == "VERIFIED", err
}

func (g *FormsGateway) EnableVerifiedEmail(ctx context.Context, token, id string) error {
	endpoint := g.formsURL + "/" + url.PathEscape(id) + ":batchUpdate"
	body := map[string]any{"requests": []any{map[string]any{"updateSettings": map[string]any{
		"settings":   map[string]string{"emailCollectionType": "VERIFIED"},
		"updateMask": "emailCollectionType",
	}}}}
	var result map[string]any
	if _, err := g.request(ctx, token, http.MethodPost, endpoint, body, &result); err != nil {
		return err
	}
	verified, err := g.VerifiedEmail(ctx, token, id)
	if err != nil {
		return err
	}
	if !verified {
		return errors.New("Google Forms did not enable verified email collection")
	}
	return nil
}

func (g *FormsGateway) ListResponses(ctx context.Context, token, id string, since time.Time, pageToken string) ([]integrations.IntakeResponse, string, error) {
	endpoint := g.formsURL + "/" + url.PathEscape(id) + "/responses"
	query := url.Values{
		"pageSize": {"50"},
		"filter":   {"timestamp >= " + since.UTC().Format(time.RFC3339Nano)},
	}
	if pageToken != "" {
		query.Set("pageToken", pageToken)
	}
	var page struct {
		Responses []integrations.IntakeResponse `json:"responses"`
		NextToken string                        `json:"nextPageToken"`
	}
	_, err := g.request(ctx, token, http.MethodGet, endpoint+"?"+query.Encode(), nil, &page)
	return page.Responses, page.NextToken, err
}
