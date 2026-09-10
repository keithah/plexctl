package monitor

import (
	"encoding/json"
	"errors"
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
			w.Write([]byte(`{"MediaContainer":{"size":1,"Directory":[{"key":"1","type":"movie"}]}}`))
		case "/library/sections/1/all":
			w.Write([]byte(`{"MediaContainer":{"Metadata":[{"key":"11"},{"key":"12"}]}}`))
		case "/library/metadata/11", "/library/metadata/12":
			w.Write([]byte(`{"MediaContainer":{"Metadata":[{"Media":[{"Part":[{"key":"/library/parts/1/file.mkv"}]}]}]}}`))
		case "/library/parts/1/file.mkv":
			w.WriteHeader(http.StatusPartialContent)
			w.Write(make([]byte, 1024))
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

func TestHandlerReportsCorrelationWithoutChangingFailureStatus(t *testing.T) {
	var event *CorrelationEvent
	tracker := NewCorrelationTracker(time.Minute, 2, time.Now)
	h := Handler{
		Correlation: tracker,
		OnCorrelation: func(got CorrelationEvent) {
			event = &got
		},
		Resolve: func(string, string) (*pms.Client, error) {
			return nil, ErrDiscoveryUnavailable
		},
	}
	for _, path := range []string{"/plex/account/one", "/plex/account/two"} {
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, path, nil))
		if r.Code != http.StatusServiceUnavailable {
			t.Fatalf("status=%d, want individual 503", r.Code)
		}
	}
	if event == nil || event.TargetCount != 2 {
		t.Fatalf("event=%+v, want two-target correlation", event)
	}
}

func TestHandlerDoesNotExposeResolverErrorDetail(t *testing.T) {
	secret := "https://private.example/?token=secret"
	h := Handler{Resolve: func(string, string) (*pms.Client, error) {
		return nil, errors.New(secret)
	}}
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/plex/account/server", nil))
	if r.Code != http.StatusNotFound || contains(r.Body.String(), secret) {
		t.Fatalf("status=%d body=%s, resolver detail leaked", r.Code, r.Body)
	}
}

func TestHandlerMapsDiscoveryFailureTo503(t *testing.T) {
	h := Handler{Resolve: func(string, string) (*pms.Client, error) {
		return nil, ErrDiscoveryUnavailable
	}}
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/plex/account/server", nil))
	if r.Code != http.StatusServiceUnavailable || !contains(r.Body.String(), `"classification":"discovery"`) {
		t.Fatalf("status=%d body=%s, want 503 discovery", r.Code, r.Body)
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
