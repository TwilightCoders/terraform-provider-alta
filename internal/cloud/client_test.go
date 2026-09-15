package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/TwilightCoders/terraform-provider-alta/internal/cloud/cognito"
)

var now = time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)

type fakeAuth struct {
	logins, refreshes int
	loginErr          error
}

func (a *fakeAuth) Login(context.Context, string, string) (cognito.Tokens, error) {
	a.logins++
	if a.loginErr != nil {
		return cognito.Tokens{}, a.loginErr
	}
	return cognito.Tokens{IDToken: "login-" + strconv.Itoa(a.logins), RefreshToken: "r", ExpiresAt: now.Add(time.Hour)}, nil
}

func (a *fakeAuth) Refresh(context.Context, string) (cognito.Tokens, error) {
	a.refreshes++
	return cognito.Tokens{IDToken: "refreshed", RefreshToken: "r", ExpiresAt: now.Add(2 * time.Hour)}, nil
}

type recorded struct {
	method, path, query, headerToken string
	body                             map[string]any
}

type fakeAPI struct {
	calls   []recorded
	handler func(r recorded) (int, string)
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rec := recorded{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery, headerToken: r.Header.Get("token")}
	if data, _ := io.ReadAll(r.Body); len(data) > 0 {
		_ = json.Unmarshal(data, &rec.body)
	}
	f.calls = append(f.calls, rec)
	status, body := http.StatusOK, "{}"
	if f.handler != nil {
		status, body = f.handler(rec)
	}
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

func newTestClient(t *testing.T, api *fakeAPI, auth *fakeAuth) *Client {
	t.Helper()
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	return NewClient(Config{BaseURL: srv.URL, Email: "e", Password: "p", Auth: auth, HTTPClient: srv.Client(), Now: func() time.Time { return now }})
}

func TestReadsSendTokenHeaderAndPreserveNumbers(t *testing.T) {
	api := &fakeAPI{handler: func(recorded) (int, string) {
		return http.StatusOK, `{"id":"s1","firewall":{"nat":{"rules":[{"destination":{"port":9000}}]}}}`
	}}
	auth := &fakeAuth{}
	c := newTestClient(t, api, auth)

	site, err := c.Site(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	call := api.calls[0]
	if call.path != "/api/site" || call.query != "id=s1" || call.headerToken != "login-1" {
		t.Errorf("request = %+v", call)
	}
	port := site["firewall"].(Object)["nat"].(Object)["rules"].([]any)[0].(Object)["destination"].(Object)["port"]
	if port != json.Number("9000") {
		t.Errorf("port = %#v, want json.Number", port)
	}

	if _, err := c.Site(context.Background(), "s1"); err != nil {
		t.Fatal(err)
	}
	if auth.logins != 1 {
		t.Errorf("logins = %d, want token reuse", auth.logins)
	}
}

func TestReauthenticatesOnceWhenRejected(t *testing.T) {
	api := &fakeAPI{handler: func(r recorded) (int, string) {
		if r.headerToken == "login-1" {
			return http.StatusUnauthorized, `{"error":"expired"}`
		}
		return http.StatusOK, `{"devices":[{"id":"d1"}],"clients":[{"id":"c1"}]}`
	}}
	auth := &fakeAuth{}
	c := newTestClient(t, api, auth)

	state, err := c.State(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	if auth.logins != 2 {
		t.Errorf("logins = %d, want 2", auth.logins)
	}
	if _, ok := state.Device("d1"); !ok {
		t.Error("device d1 not found")
	}
	if _, ok := state.Client("c1"); !ok {
		t.Error("client c1 not found")
	}
}

func TestRefreshesExpiringToken(t *testing.T) {
	api := &fakeAPI{}
	auth := &fakeAuth{}
	c := newTestClient(t, api, auth)
	c.tokens = cognito.Tokens{IDToken: "old", RefreshToken: "r", ExpiresAt: now.Add(30 * time.Second)}

	if _, err := c.Site(context.Background(), "s1"); err != nil {
		t.Fatal(err)
	}
	if auth.refreshes != 1 || auth.logins != 0 || api.calls[0].headerToken != "refreshed" {
		t.Errorf("refreshes=%d logins=%d token=%q", auth.refreshes, auth.logins, api.calls[0].headerToken)
	}
}

func TestErrorsCarryStatus(t *testing.T) {
	api := &fakeAPI{handler: func(recorded) (int, string) { return http.StatusBadRequest, "no site" }}
	c := newTestClient(t, api, &fakeAuth{})

	_, err := c.Site(context.Background(), "nope")
	var se *StatusError
	if !errors.As(err, &se) || se.Status != http.StatusBadRequest || se.Body != "no site" {
		t.Fatalf("err = %v", err)
	}
}

func TestAuthenticationFailureIsReported(t *testing.T) {
	c := newTestClient(t, &fakeAPI{}, &fakeAuth{loginErr: cognito.ErrMFARequired})
	if _, err := c.Site(context.Background(), "s1"); !errors.Is(err, cognito.ErrMFARequired) {
		t.Fatalf("err = %v", err)
	}
}

func TestApplyUsesPortalEndpoints(t *testing.T) {
	api := &fakeAPI{}
	c := newTestClient(t, api, &fakeAuth{})
	ctx := context.Background()

	writes := []Write{
		{Kind: KindSite, SiteID: "s1", Key: "vlans", Value: []any{Object{"id": 3}}},
		{Kind: KindDevice, SiteID: "s1", ID: "d1", Key: "portsCfg", Value: Object{"ports": Object{}}},
		{Kind: KindClient, SiteID: "s1", ID: "aabbccddeeff", Value: Object{"ip": "192.0.2.50", "vlan": 2}},
	}
	for _, w := range writes {
		if err := c.Apply(ctx, w); err != nil {
			t.Fatalf("%s: %v", w.Kind, err)
		}
	}

	want := []struct {
		path string
		keys []string
	}{
		{"/api/site", []string{"id", "vlans", "token"}},
		{"/api/device/edit", []string{"id", "portsCfg", "token"}},
		{"/api/client/edit", []string{"siteid", "id", "ip", "vlan", "token"}},
	}
	for i, w := range want {
		call := api.calls[i]
		if call.method != http.MethodPost || call.path != w.path {
			t.Errorf("call %d = %s %s, want POST %s", i, call.method, call.path, w.path)
		}
		for _, k := range w.keys {
			if _, ok := call.body[k]; !ok {
				t.Errorf("call %d body missing %q: %v", i, k, call.body)
			}
		}
	}
	if api.calls[0].body["id"] != "s1" || api.calls[1].body["id"] != "d1" || api.calls[2].body["siteid"] != "s1" {
		t.Errorf("ids routed wrongly: %v", api.calls)
	}
}

func TestApplyRejectsMalformedWrites(t *testing.T) {
	c := newTestClient(t, &fakeAPI{}, &fakeAuth{})
	for _, w := range []Write{
		{Kind: KindClient, ID: "x", Value: "not an object"},
		{Kind: "bogus"},
	} {
		if err := c.Apply(context.Background(), w); err == nil {
			t.Errorf("%+v: expected error", w)
		}
	}
}

func TestAuditAuthor(t *testing.T) {
	api := &fakeAPI{handler: func(recorded) (int, string) {
		return http.StatusOK, `{"trail":[{"id":"1","ts":"2026-09-14T22:01:34Z","action":"add","type":"nat","data":{"username":"alice"}},{"id":"2","action":"edit","type":"dev","data":{}}]}`
	}}
	c := newTestClient(t, api, &fakeAuth{})
	trail, err := c.Audit(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(trail) != 2 || trail[0].Author() != "alice" || trail[1].Author() != "" {
		t.Fatalf("trail = %+v", trail)
	}
}
