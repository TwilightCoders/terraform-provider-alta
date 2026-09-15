package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDoReturnsStatusAndBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("short and stout"))
	}))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL, http.NoBody)
	status, body, err := Do(srv.Client(), req)
	if err != nil {
		t.Fatal(err)
	}
	if status != http.StatusTeapot || string(body) != "short and stout" {
		t.Fatalf("got %d %q", status, body)
	}
}

func TestDoPropagatesTransportErrors(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:1", http.NoBody)
	if _, _, err := Do(http.DefaultClient, req); err == nil {
		t.Fatal("expected a connection error")
	}
}
