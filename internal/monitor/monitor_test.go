package monitor

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/keithah/plexctl/internal/api"
	"github.com/keithah/plexctl/internal/pms"
)

func TestHandlerHealthyAndUnhealthy(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/identity":
			w.Write([]byte(`{"MediaContainer":{"size":0}}`))
		case "/library/sections/all":
			w.Write([]byte(`{"MediaContainer":{"size":1}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	a, err := api.New(upstream.URL, "tok", nil)
	if err != nil {
		t.Fatal(err)
	}
	client := pms.New(a)
	h := Handler{Timeout: time.Second, Resolve: func(account, server string) (*pms.Client, error) {
		if account != "keith" || server != "SF2" {
			t.Fatalf("unexpected target %s/%s", account, server)
		}
		return client, nil
	}}
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/plex/keith/SF2", nil))
	if r.Code != http.StatusOK {
		t.Fatalf("healthy status = %d, body=%s", r.Code, r.Body)
	}
	var got map[string]any
	if err := json.Unmarshal(r.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["ok"] != true || got["classification"] != "ok" || got["duration_ms"] == nil {
		t.Fatalf("unexpected response: %v", got)
	}

	r = httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest(http.MethodPost, "/plex/keith/SF2", nil))
	if r.Code != http.StatusMethodNotAllowed {
		t.Fatalf("method status = %d", r.Code)
	}
}

func TestHandlerMapsHealthFailureTo503AndDoesNotLeakToken(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("X-Plex-Token=tok"))
	}))
	defer upstream.Close()
	a, err := api.New(upstream.URL, "tok", nil)
	if err != nil {
		t.Fatal(err)
	}
	h := Handler{Timeout: time.Second, Resolve: func(string, string) (*pms.Client, error) { return pms.New(a), nil }}
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/plex/a/b", nil))
	if r.Code != http.StatusServiceUnavailable {
		t.Fatalf("failure status = %d", r.Code)
	}
	if string(r.Body.Bytes()) == "" || contains(r.Body.String(), "tok") {
		t.Fatalf("token leaked: %s", r.Body)
	}
}

func TestHandlerRejectsMalformedTarget(t *testing.T) {
	h := Handler{}
	for _, path := range []string{"/", "/plex/a", "/other/a/b", "/plex/a/b/c"} {
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, path, nil))
		if r.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d", path, r.Code)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
