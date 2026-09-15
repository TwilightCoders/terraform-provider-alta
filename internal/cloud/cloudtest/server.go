// Package cloudtest provides an in-memory Alta API for tests: it serves recorded fixtures
// and applies writes the way the portal's endpoints do.
package cloudtest

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/TwilightCoders/terraform-provider-alta/internal/cloud"
	"github.com/TwilightCoders/terraform-provider-alta/internal/cloud/cognito"
)

// Fixture ids of the recorded Example site and its router.
const (
	SiteID   = "aBcDeFgHiJkLmNoPqRsTu"
	DeviceID = "0a1b2c3d4e5f"
)

// Fixture loads a recorded site and state from internal/testdata/fixtures/<dir>.
func Fixture(t testing.TB, dir, siteFile, stateFile string) (cloud.Object, cloud.State) {
	t.Helper()
	var site cloud.Object
	var state cloud.State
	decodeFile(t, filepath.Join(fixturesDir(), dir, siteFile), &site)
	decodeFile(t, filepath.Join(fixturesDir(), dir, stateFile), &state)
	return site, state
}

// Current is the fixture captured after the 2026-09-14 back-port.
func Current(t testing.TB) (cloud.Object, cloud.State) {
	return Fixture(t, "2026-09-14", "cloud-site.json", "cloud-state.json")
}

func fixturesDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "testdata", "fixtures")
}

func decodeFile(t testing.TB, path string, out any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(out); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

// Server is a fake Alta API.
type Server struct {
	*httptest.Server
	mu     sync.Mutex
	site   cloud.Object
	state  cloud.State
	writes []cloud.Object
}

// NewServer serves site and state until the test ends.
func NewServer(t testing.TB, site cloud.Object, state cloud.State) *Server {
	t.Helper()
	s := &Server{site: site, state: state}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/site", func(w http.ResponseWriter, _ *http.Request) { s.respond(w, s.site) })
	mux.HandleFunc("GET /api/site/state", func(w http.ResponseWriter, _ *http.Request) { s.respond(w, s.state) })
	mux.HandleFunc("POST /api/site", s.write(func(body cloud.Object) {
		for k, v := range body {
			if k != "id" && k != "token" {
				s.site[k] = v
			}
		}
	}))
	mux.HandleFunc("POST /api/device/edit", s.write(func(body cloud.Object) {
		if device, ok := s.state.Device(body["id"].(string)); ok {
			for k, v := range body {
				if k != "id" && k != "token" {
					device[k] = v
				}
			}
		}
	}))
	mux.HandleFunc("POST /api/client/edit", s.write(func(body cloud.Object) {
		record, ok := s.state.Client(body["id"].(string))
		if !ok {
			record = cloud.Object{"id": body["id"]}
			s.state.Clients = append(s.state.Clients, record)
		}
		config := cloud.Object{}
		for k, v := range body {
			switch k {
			case "id", "token", "siteid":
			case "wired", "profile", "schedule", "filter":
				record[k] = v
			default:
				if v != nil {
					config[k] = v
				}
			}
		}
		record["config"] = config
	}))
	s.Server = httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

// Client returns an API client for the server, with authentication stubbed out.
func (s *Server) Client() *cloud.Client {
	return cloud.NewClient(cloud.Config{BaseURL: s.URL, Email: "test", Password: "test", Auth: stubAuth{}, HTTPClient: s.Server.Client()})
}

// Writes returns the bodies of every write received, in order.
func (s *Server) Writes() []cloud.Object {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]cloud.Object(nil), s.writes...)
}

// Site returns the current site document.
func (s *Server) Site() cloud.Object {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.site
}

func (s *Server) respond(w http.ResponseWriter, v any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) write(apply func(cloud.Object)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body cloud.Object
		dec := json.NewDecoder(r.Body)
		dec.UseNumber()
		if err := dec.Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		s.writes = append(s.writes, body)
		apply(body)
		_, _ = w.Write([]byte("{}"))
	}
}

type stubAuth struct{}

func (stubAuth) Login(context.Context, string, string) (cognito.Tokens, error) {
	return cognito.Tokens{IDToken: "test", RefreshToken: "r", ExpiresAt: time.Now().Add(time.Hour)}, nil
}

func (stubAuth) Refresh(ctx context.Context, _ string) (cognito.Tokens, error) {
	return stubAuth{}.Login(ctx, "", "")
}
