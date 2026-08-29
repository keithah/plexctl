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
