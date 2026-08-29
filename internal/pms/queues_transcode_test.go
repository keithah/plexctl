package pms

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/keithah/plexctl/internal/api"
)

func TestDownloadQueueAndTranscodeEndpoints(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/downloadQueue/1" {
			if _, err := w.Write([]byte(`{"MediaContainer":{"DownloadQueue":[{"id":1,"itemCount":2,"status":"processing"}]}}`)); err != nil {
				t.Errorf("write response: %v", err)
			}
			return
		}
		if r.URL.Path == "/downloadQueue/1/items" {
			if _, err := w.Write([]byte(`{"MediaContainer":{"DownloadQueueItem":[{"id":9,"title":"Movie","status":"processing"}]}}`)); err != nil {
				t.Errorf("write response: %v", err)
			}
			return
		}
		if r.URL.Path == "/video/:/transcode/universal/decision" {
			if r.URL.Query().Get("transcodeSessionId") != "abc" {
				t.Errorf("query: %s", r.URL.RawQuery)
			}
			if _, err := w.Write([]byte(`{"MediaContainer":{"canDirectPlay":true}}`)); err != nil {
				t.Errorf("write response: %v", err)
			}
			return
		}
		if r.URL.Path == "/video/:/transcode/universal/subtitles" {
			return
		}
		http.NotFound(w, r)
	}))
	defer s.Close()
	a, err := api.New(s.URL, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	c := New(a)
	q, err := c.DownloadQueue(context.Background(), "1")
	if err != nil || q.MediaContainer.DownloadQueue[0].Status != "processing" {
		t.Fatalf("queue: %+v %v", q, err)
	}
	items, err := c.DownloadQueueItems(context.Background(), "1")
	if err != nil || items.MediaContainer.Items[0].Title != "Movie" {
		t.Fatalf("items: %+v %v", items, err)
	}
	_, err = c.TranscodeDecision(context.Background(), "video", "abc", url.Values{"videoBitrate": {"4000"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.TranscodeSubtitles(context.Background(), "video", "abc", nil); err != nil {
		t.Fatal(err)
	}
}

// The real universal/subtitles endpoint serves WebVTT, not JSON, so the body
// must be returned verbatim instead of being JSON-decoded.
func TestTranscodeSubtitlesReturnsNonJSONBody(t *testing.T) {
	const vtt = "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nHello\n"
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/vtt")
		if _, err := w.Write([]byte(vtt)); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer s.Close()
	a, err := api.New(s.URL, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := New(a).TranscodeSubtitles(context.Background(), "video", "abc", nil)
	if err != nil {
		t.Fatalf("subtitles: %v", err)
	}
	if got != vtt {
		t.Fatalf("subtitles body = %q, want %q", got, vtt)
	}
}
