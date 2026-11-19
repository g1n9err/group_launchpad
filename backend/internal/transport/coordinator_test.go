package transport_test

import (
	"net/http"
	"testing"

	"launchpad/internal/transport"
)

func TestCoordinatorRoutesRejectUnauthorizedRequests(t *testing.T) {
	cfg := testConfig()
	srv := httptestNew(transport.New(cfg, discardLogger(), nil, nil, nil, nil, nil, nil), t)
	group := "/api/groups/00000000-0000-0000-0000-000000000001"
	routes := []struct {
		method string
		path   string
	}{
		{http.MethodPost, group + "/launch"},
		{http.MethodGet, group + "/status"},
		{http.MethodPost, group + "/slack/provision"},
		{http.MethodPost, group + "/classroom/provision"},
		{http.MethodPost, group + "/drive/provision"},
		{http.MethodPost, group + "/forms/provision"},
		{http.MethodPost, group + "/forms/publish"},
		{http.MethodPost, group + "/audits"},
		{http.MethodGet, group + "/audits"},
		{http.MethodPost, group + "/forms/responses/import"},
		{http.MethodPost, group + "/forms/distribute"},
		{http.MethodGet, group + "/forms/deliveries"},
		{http.MethodGet, "/api/integrations/google/connect"},
	}
	for _, route := range routes {
		t.Run(route.path, func(t *testing.T) {
			for _, credentials := range []struct{ user, password string }{
				{}, {"coordinator", "wrong-password"}, {"student", testCoordinatorPassword},
			} {
				req, err := http.NewRequest(route.method, srv.URL+route.path, nil)
				if err != nil {
					t.Fatal(err)
				}
				if credentials.user != "" {
					req.SetBasicAuth(credentials.user, credentials.password)
				}
				res, err := srv.Client().Do(req)
				if err != nil {
					t.Fatal(err)
				}
				res.Body.Close()
				if res.StatusCode != http.StatusUnauthorized || res.Header.Get("WWW-Authenticate") == "" {
					t.Fatalf("status=%d, want 401 with a Basic authentication challenge", res.StatusCode)
				}
			}
		})
	}
}

func TestCoordinatorRoutesRejectCrossOriginWrites(t *testing.T) {
	srv := httptestNew(transport.New(testConfig(), discardLogger(), nil, nil, nil, nil, nil, nil), t)
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/groups/00000000-0000-0000-0000-000000000001/launch", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.SetBasicAuth("coordinator", testCoordinatorPassword)
	req.Header.Set("Origin", "https://untrusted.example")
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("status=%d, want 403", res.StatusCode)
	}
}

func TestCoordinatorAccessFailsClosed(t *testing.T) {
	cfg := testConfig()
	cfg.CoordinatorPassword = ""
	srv := httptestNew(transport.New(cfg, discardLogger(), nil, nil, nil, nil, nil, nil), t)
	code, body := doJSON(t, srv, http.MethodPost, "/api/groups/00000000-0000-0000-0000-000000000001/launch", "")
	if code != http.StatusServiceUnavailable || errCode(t, body) != "NOT_READY" {
		t.Fatalf("status=%d body=%v; want coordinator access unavailable", code, body)
	}
}
