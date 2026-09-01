package pms

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/keithah/plexctl/internal/api"
)

func recorder(t *testing.T, body string) (*Client, *[]string, func()) {
	t.Helper()
	var paths []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.RequestURI())
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(body)); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	a, err := api.New(s.URL, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	return New(a), &paths, s.Close
}

func TestLibraryQueriesAndChildren(t *testing.T) {
	c, paths, done := recorder(t, `{"MediaContainer":{"size":1,"Metadata":[{"title":"Example"}]}}`)
	defer done()
	ctx := context.Background()
	if _, err := c.RecentlyAdded(ctx, "1", 7); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Children(ctx, "123"); err != nil {
		t.Fatal(err)
	}
	if len(*paths) != 2 {
		t.Fatalf("got paths %v", *paths)
	}
	q, _ := url.Parse((*paths)[0])
	if q.Path != "/library/sections/1/all" || q.Query().Get("sort") != "addedAt:desc" || q.Query().Get("limit") != "7" {
		t.Fatalf("recent request: %s", (*paths)[0])
	}
	if (*paths)[1] != "/library/metadata/123/children" {
		t.Fatalf("children path: %s", (*paths)[1])
	}
}

func TestSectionsUsesDocumentedAllPath(t *testing.T) {
	c, paths, done := recorder(t, `{"MediaContainer":{"size":0,"Directory":[]}}`)
	defer done()
	if _, err := c.Sections(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(*paths) != 1 || (*paths)[0] != "/library/sections/all" {
		t.Fatalf("sections path: %v", *paths)
	}
}

// Search must use the documented /hubs/search operation rather than an
// undocumented title= filter on the section listing endpoint.
func TestSearchUsesDocumentedHubsSearch(t *testing.T) {
	c, paths, done := recorder(t, `{"MediaContainer":{"size":1,"Hub":[{"title":"Movies","type":"movie","Metadata":[{"title":"Star Wars"}]}]}}`)
	defer done()
	v, err := c.Search(context.Background(), "3", "star wars", 5)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := url.Parse((*paths)[0])
	if got.Path != "/hubs/search" {
		t.Fatalf("search path: %s", got.Path)
	}
	q := got.Query()
	if q.Get("query") != "star wars" || q.Get("sectionId") != "3" || q.Get("limit") != "5" {
		t.Fatalf("search query: %s", (*paths)[0])
	}
	if len(v.MediaContainer.Hub) != 1 || v.MediaContainer.Hub[0].Metadata[0].Title != "Star Wars" {
		t.Fatalf("search response: %+v", v)
	}
}

// History must target the documented /status/sessions/history/all operation.
func TestHistoryUsesDocumentedPath(t *testing.T) {
	c, paths, done := recorder(t, `{"MediaContainer":{"size":0}}`)
	defer done()
	if _, err := c.History(context.Background(), url.Values{}); err != nil {
		t.Fatal(err)
	}
	got, _ := url.Parse((*paths)[0])
	if got.Path != "/status/sessions/history/all" {
		t.Fatalf("history path: %s", got.Path)
	}
}

func TestHistoryFiltersAreEncoded(t *testing.T) {
	c, paths, done := recorder(t, `{"MediaContainer":{"size":0}}`)
	defer done()
	q := url.Values{"accountID": []string{"42"}, "sort": []string{"viewedAt:desc"}}
	if _, err := c.History(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	got, _ := url.Parse((*paths)[0])
	if got.Query().Get("accountID") != "42" || got.Query().Get("sort") != "viewedAt:desc" {
		t.Fatalf("history query: %s", (*paths)[0])
	}
}

func TestSessionModels(t *testing.T) {
	c, _, done := recorder(t, `{"MediaContainer":{"size":1,"Metadata":[{"title":"Playing","session":{"id":"abc"}}]}}`)
	defer done()
	v, err := c.Sessions(context.Background())
	if err != nil || v.MediaContainer.Size != 1 || v.MediaContainer.Metadata[0].Session.ID != "abc" {
		t.Fatalf("value=%+v err=%v", v, err)
	}
}

func TestProbeMediaSupportsMusicAndIgnoresRange(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/library/metadata/artist":
			_, _ = w.Write([]byte(`{"MediaContainer":{"Metadata":[{"type":"artist"}]}}`))
		case "/library/metadata/album":
			_, _ = w.Write([]byte(`{"MediaContainer":{"Metadata":[{"type":"album"}]}}`))
		case "/library/metadata/artist/children":
			_, _ = w.Write([]byte(`{"MediaContainer":{"Metadata":[{"key":"/library/metadata/album","type":"album"}]}}`))
		case "/library/metadata/album/children":
			_, _ = w.Write([]byte(`{"MediaContainer":{"Metadata":[{"key":"/library/metadata/track","type":"track"}]}}`))
		case "/library/metadata/track":
			_, _ = w.Write([]byte(`{"MediaContainer":{"Metadata":[{"type":"track","Media":[{"Part":[{"key":"/library/parts/song"}]}]}]}}`))
		case "/library/parts/song":
			_, _ = w.Write(make([]byte, 3<<20))
		default:
			http.NotFound(w, r)
		}
	}))
	defer s.Close()
	a, err := api.New(s.URL, "token", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := New(a).ProbeMedia(context.Background(), "/library/metadata/artist"); err != nil {
		t.Fatalf("music probe: %v", err)
	}
}

func TestProbeMediaCycleIsBounded(t *testing.T) {
	var calls int64
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"MediaContainer":{"Metadata":[{"key":"/library/metadata/cycle","type":"season"}]}}`))
	}))
	defer s.Close()
	a, err := api.New(s.URL, "token", nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := New(a).ProbeMedia(ctx, "/library/metadata/cycle"); err == nil {
		t.Fatal("cycle should not report playable media")
	}
	if got := atomic.LoadInt64(&calls); got > 20 {
		t.Fatalf("cycle issued %d requests, want bounded traversal", got)
	}
}
