package pms

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/keithah/plexctl/internal/api"
)

func recorder(t *testing.T, body string) (*Client, *[]string, func()) {
	t.Helper()
	var paths []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.RequestURI())
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
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

// Search must use the documented /hubs/search operation rather than an
// undocumented title= filter on the section listing endpoint.
func TestSearchUsesDocumentedHubsSearch(t *testing.T) {
	c, paths, done := recorder(t, `{"MediaContainer":{"size":0}}`)
	defer done()
	if _, err := c.Search(context.Background(), "3", "star wars", 5); err != nil {
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

func TestSessionModels(t *testing.T) {
	c, _, done := recorder(t, `{"MediaContainer":{"size":1,"Metadata":[{"title":"Playing","session":{"id":"abc"}}]}}`)
	defer done()
	v, err := c.Sessions(context.Background())
	if err != nil || v.MediaContainer.Size != 1 || v.MediaContainer.Metadata[0].Session.ID != "abc" {
		t.Fatalf("value=%+v err=%v", v, err)
	}
}
