package pms

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/keithah/plexctl/internal/api"
)

func TestLibraryQueriesAndChildren(t *testing.T) {
	var paths []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.RequestURI())
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"MediaContainer":{"size":1,"Metadata":[{"title":"Example"}]}}`))
	}))
	defer s.Close()
	a, err := api.New(s.URL, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	c := New(a)
	ctx := context.Background()
	if _, err = c.Search(ctx, "1", "star wars"); err != nil {
		t.Fatal(err)
	}
	if _, err = c.RecentlyAdded(ctx, "1", 7); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Children(ctx, "123"); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 3 {
		t.Fatalf("got paths %v", paths)
	}
	q, _ := url.Parse(paths[0])
	if q.Query().Get("title") != "star wars" {
		t.Fatalf("search query: %s", paths[0])
	}
	q, _ = url.Parse(paths[1])
	if q.Query().Get("sort") != "addedAt:desc" || q.Query().Get("limit") != "7" {
		t.Fatalf("recent query: %s", paths[1])
	}
	if paths[2] != "/library/metadata/123/children" {
		t.Fatalf("children path: %s", paths[2])
	}
}

func TestSessionModels(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/status/sessions" {
			w.Write([]byte(`{"MediaContainer":{"size":1,"Metadata":[{"title":"Playing","session":{"id":"abc"}}]}}`))
			return
		}
		w.Write([]byte(`{"MediaContainer":{"size":0}}`))
	}))
	defer s.Close()
	a, _ := api.New(s.URL, "", nil)
	c := New(a)
	v, err := c.Sessions(context.Background())
	if err != nil || v.MediaContainer.Size != 1 || v.MediaContainer.Metadata[0].Session.ID != "abc" {
		t.Fatalf("value=%+v err=%v", v, err)
	}
}
