package slack

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	coreintegrations "launchpad/internal/core/integrations"
)

const oauthAccessURL = "https://slack.com/api/oauth.v2.access"

type OAuthGateway struct {
	clientID    string
	secret      string
	redirectURL string
	http        *http.Client
}

func NewOAuthGateway(clientID, secret, redirectURL string) *OAuthGateway {
	return &OAuthGateway{clientID: clientID, secret: secret, redirectURL: redirectURL, http: &http.Client{Timeout: 15 * time.Second}}
}

func (g *OAuthGateway) AuthCodeURL(state string) string {
	values := url.Values{
		"client_id":    {g.clientID},
		"scope":        {strings.Join(coreintegrations.SlackBotScopes, ",")},
		"redirect_uri": {g.redirectURL},
		"state":        {state},
	}
	return "https://slack.com/oauth/v2/authorize?" + values.Encode()
}

func (g *OAuthGateway) ExchangeOAuth(ctx context.Context, code string) (coreintegrations.SlackInstallBundle, error) {
	form := url.Values{"client_id": {g.clientID}, "client_secret": {g.secret}, "code": {code}, "redirect_uri": {g.redirectURL}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, oauthAccessURL, strings.NewReader(form.Encode()))
	if err != nil {
		return coreintegrations.SlackInstallBundle{}, errors.New("Slack OAuth request could not be created")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := g.http.Do(req)
	if err != nil {
		return coreintegrations.SlackInstallBundle{}, errors.New("Slack OAuth service is unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return coreintegrations.SlackInstallBundle{}, errors.New("Slack OAuth exchange was rejected")
	}
	var payload struct {
		OK          bool   `json:"ok"`
		Error       string `json:"error"`
		AccessToken string `json:"access_token"`
		Scope       string `json:"scope"`
		BotUserID   string `json:"bot_user_id"`
		Team        struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"team"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 1<<20)).Decode(&payload); err != nil || !payload.OK {
		return coreintegrations.SlackInstallBundle{}, errors.New("Slack OAuth response was invalid")
	}
	return coreintegrations.SlackInstallBundle{
		Workspace: coreintegrations.SlackWorkspace{TeamID: payload.Team.ID, TeamName: payload.Team.Name, BotUserID: payload.BotUserID},
		BotToken:  payload.AccessToken,
		Scopes:    strings.Split(payload.Scope, ","),
	}, nil
}
