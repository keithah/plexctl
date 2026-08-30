package plexauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A failing legacy endpoint must be reported to the caller rather than being
// silently swallowed, otherwise a broken legacy path is indistinguishable from
// an account that genuinely has no servers.
func TestResourcesReportsDiscardedLegacyError(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/resources" {
			http.Error(w, "legacy exploded", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`[{"name":"Living","clientIdentifier":"m1","provides":"server","connections":[{"uri":"http://living:32400","local":true}]}]`)); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer s.Close()

	var warnings []string
	c := New(s.URL, "test", &http.Client{})
	c.OnWarning = func(msg string) { warnings = append(warnings, msg) }

	r, err := c.Resources(context.Background(), "token")
	if err != nil || len(r) != 1 {
		t.Fatalf("resources=%+v err=%v, want the JSON fallback to succeed", r, err)
	}
	if len(warnings) == 0 {
		t.Fatal("legacy discovery failure was discarded without any warning")
	}
	if !strings.Contains(strings.ToLower(strings.Join(warnings, " ")), "legacy") {
		t.Fatalf("warnings=%q, want one naming the legacy discovery failure", warnings)
	}
}

// An empty-but-valid legacy container is not a failure and must not be
// reported as one, but it must still fall through to the JSON endpoint.
func TestResourcesFallsThroughOnEmptyLegacyWithoutWarning(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/resources" {
			w.Header().Set("Content-Type", "application/xml")
			if _, err := w.Write([]byte(`<MediaContainer></MediaContainer>`)); err != nil {
				t.Errorf("write response: %v", err)
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`[{"name":"Living","clientIdentifier":"m1","provides":"server","connections":[{"uri":"http://living:32400","local":true}]}]`)); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer s.Close()

	var warnings []string
	c := New(s.URL, "test", &http.Client{})
	c.OnWarning = func(msg string) { warnings = append(warnings, msg) }

	r, err := c.Resources(context.Background(), "token")
	if err != nil || len(r) != 1 {
		t.Fatalf("resources=%+v err=%v, want the JSON fallback to succeed", r, err)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings=%q, want none for an empty legacy container", warnings)
	}
}
