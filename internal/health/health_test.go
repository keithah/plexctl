package health

import (
	"context"
	"github.com/keithah/plexctl/internal/api"
	"github.com/keithah/plexctl/internal/pms"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPing(t *testing.T) {
	s := httptest.NewServer(httpHandler(t, `{"MediaContainer":{"size":0}}`))
	defer s.Close()
	a, _ := api.New(s.URL, "secret", nil)
	r := Ping(context.Background(), pms.New(a))
	if !r.OK || r.Stage != "identity" {
		t.Fatalf("%+v", r)
	}
}
func httpHandler(t *testing.T, body string) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/json" {
			http.Error(w, "bad accept", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(body)); err != nil {
			t.Errorf("write response: %v", err)
		}
	})
}

func healthClient(t *testing.T, url string) *pms.Client {
	t.Helper()
	a, err := api.New(url, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	return pms.New(a)
}

// A rejected token is actionable differently from an unreachable server, so it
// must be classified as an auth failure rather than a generic identity failure.
func TestAuthFailureIsClassified(t *testing.T) {
	for _, code := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "denied", code)
		}))
		got := Check(context.Background(), healthClient(t, s.URL))
		s.Close()
		if got.Classification != AuthFailure {
			t.Errorf("HTTP %d classified as %q, want %q", code, got.Classification, AuthFailure)
		}
	}
}

func TestCheckRequiresTwoMediaByteProbes(t *testing.T) {
	requests := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/identity":
			_, _ = w.Write([]byte(`{"MediaContainer":{"machineIdentifier":"m1"}}`))
		case r.URL.Path == "/library/sections/all":
			_, _ = w.Write([]byte(`{"MediaContainer":{"Directory":[{"key":"1","type":"movie"}]}}`))
		case r.URL.Path == "/library/sections/1/all":
			_, _ = w.Write([]byte(`{"MediaContainer":{"Metadata":[{"key":"11"},{"key":"12"}]}}`))
		case r.URL.Path == "/library/metadata/11" || r.URL.Path == "/library/metadata/12":
			_, _ = w.Write([]byte(`{"MediaContainer":{"Metadata":[{"Media":[{"Part":[{"key":"/library/parts/1/file.mkv"}]}]}]}}`))
		case r.URL.Path == "/library/parts/1/file.mkv":
			requests++
			if r.Header.Get("Range") != "bytes=0-1024" {
				t.Errorf("range=%q", r.Header.Get("Range"))
			}
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(make([]byte, 1024))
		default:
			http.NotFound(w, r)
		}
	}))
	defer s.Close()
	got := Check(context.Background(), healthClient(t, s.URL))
	if !got.OK || got.Stage != "media" || requests != 2 {
		t.Fatalf("result=%+v requests=%d", got, requests)
	}
}

func TestCheckFailsWhenMediaBytesFail(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/identity":
			_, _ = w.Write([]byte(`{"MediaContainer":{"machineIdentifier":"m1"}}`))
		case "/library/sections/all":
			_, _ = w.Write([]byte(`{"MediaContainer":{"Directory":[{"key":"1","type":"movie"}]}}`))
		case "/library/sections/1/all":
			_, _ = w.Write([]byte(`{"MediaContainer":{"Metadata":[{"key":"11"}]}}`))
		case "/library/metadata/11":
			_, _ = w.Write([]byte(`{"MediaContainer":{"Metadata":[{"Media":[{"Part":[{"key":"/library/parts/1/file.mkv"}]}]}]}}`))
		case "/library/parts/1/file.mkv":
			http.Error(w, "bad media", http.StatusServiceUnavailable)
		default:
			http.NotFound(w, r)
		}
	}))
	defer s.Close()
	got := Check(context.Background(), healthClient(t, s.URL))
	if got.OK || got.Stage != "media" {
		t.Fatalf("result=%+v", got)
	}
}

func TestCheckReportsLibraryAndSuccess(t *testing.T) {
	identityOnly := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/identity" {
			if _, err := w.Write([]byte(`{"MediaContainer":{"machineIdentifier":"m1"}}`)); err != nil {
				t.Errorf("write response: %v", err)
			}
			return
		}
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer identityOnly.Close()
	if got := Check(context.Background(), healthClient(t, identityOnly.URL)); got.OK || got.Classification != LibraryFailure {
		t.Fatalf("result=%+v, want a library failure", got)
	}

	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := w.Write([]byte(`{"MediaContainer":{"machineIdentifier":"m1","Directory":[]}}`)); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer healthy.Close()
	if got := Check(context.Background(), healthClient(t, healthy.URL)); got.OK || got.Classification != LibraryFailure {
		t.Fatalf("result=%+v, want a library failure without media", got)
	}
}
