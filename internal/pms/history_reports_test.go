package pms

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/keithah/plexctl/internal/api"
)

func TestHistoryReportMetadataDecoding(t *testing.T) {
	c, _, done := recorder(t, `{"MediaContainer":{"size":1,"Metadata":[{"ratingKey":"42","title":"Episode","parentTitle":"Season 2","grandparentTitle":"Series","type":"episode","viewedAt":1700000000,"librarySectionID":"7","librarySectionTitle":"Television","accountID":123456,"accountTitle":"Alex","duration":3600000}]}}`)
	defer done()

	history, err := c.History(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(history.MediaContainer.Metadata) != 1 {
		t.Fatalf("history item count = %d, want 1", len(history.MediaContainer.Metadata))
	}
	item := history.MediaContainer.Metadata[0]
	if item.RatingKey != "42" || item.Title != "Episode" || item.ParentTitle != "Season 2" || item.GrandparentTitle != "Series" || item.Type != "episode" {
		t.Fatalf("title hierarchy = %+v", item)
	}
	if item.ViewedAt == nil || *item.ViewedAt != 1700000000 {
		t.Fatalf("viewedAt = %v, want 1700000000", item.ViewedAt)
	}
	if item.LibrarySectionID != "7" || item.LibrarySectionTitle != "Television" || item.AccountID != 123456 || item.AccountTitle != "Alex" {
		t.Fatalf("report identifiers = %+v", item)
	}
	if item.Duration == nil || *item.Duration != 3600000 {
		t.Fatalf("duration = %v, want 3600000", item.Duration)
	}
}

func TestListSectionItemsPagesWithEscapedKey(t *testing.T) {
	var requests []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		requests = append(requests, r.URL.RequestURI())
		if r.URL.EscapedPath() != "/library/sections/TV%2FDrama%20&%20More/all" {
			t.Errorf("escaped path = %q", r.URL.EscapedPath())
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("X-Plex-Container-Start") {
		case "0":
			_, _ = w.Write([]byte(`{"MediaContainer":{"size":2,"totalSize":3,"Metadata":[{"ratingKey":"2","title":"Second"},{"ratingKey":"1","title":"First"}]}}`))
		case "2":
			_, _ = w.Write([]byte(`{"MediaContainer":{"size":1,"totalSize":3,"Metadata":[{"ratingKey":"3","title":"Third"}]}}`))
		default:
			t.Errorf("unexpected page start: %q", r.URL.Query().Get("X-Plex-Container-Start"))
			http.Error(w, "unexpected start", http.StatusBadRequest)
		}
	}))
	defer s.Close()

	a, err := api.New(s.URL, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	items, err := New(a).ListSectionItems(context.Background(), "TV/Drama & More")
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{items.MediaContainer.Metadata[0].RatingKey, items.MediaContainer.Metadata[1].RatingKey, items.MediaContainer.Metadata[2].RatingKey}; !reflect.DeepEqual(got, []string{"2", "1", "3"}) {
		t.Fatalf("item order = %v, want response order", got)
	}
	if len(requests) != 2 {
		t.Fatalf("requests = %v, want two pages", requests)
	}
	for _, request := range requests {
		u, err := url.Parse(request)
		if err != nil {
			t.Fatal(err)
		}
		if u.Query().Get("X-Plex-Container-Size") != "100" {
			t.Fatalf("page size query = %q, want 100", u.Query().Get("X-Plex-Container-Size"))
		}
	}
}

func TestListSectionItemsRejectsPageWithoutProgress(t *testing.T) {
	c, _, done := recorder(t, `{"MediaContainer":{"size":0,"totalSize":1,"Metadata":[]}}`)
	defer done()

	_, err := c.ListSectionItems(context.Background(), "7")
	if err == nil || !strings.Contains(err.Error(), "section 7") || !strings.Contains(err.Error(), "no progress") {
		t.Fatalf("error = %v, want contextual no-progress error", err)
	}
}

func TestItemsRemainsCallerControlled(t *testing.T) {
	c, paths, done := recorder(t, `{"MediaContainer":{"size":0,"Metadata":[]}}`)
	defer done()

	q := url.Values{"X-Plex-Container-Size": {"9"}, "sort": {"titleSort:asc"}}
	if _, err := c.Items(context.Background(), "TV/Drama", q); err != nil {
		t.Fatal(err)
	}
	if len(*paths) != 1 {
		t.Fatalf("requests = %v", *paths)
	}
	u, err := url.Parse((*paths)[0])
	if err != nil {
		t.Fatal(err)
	}
	if u.EscapedPath() != "/library/sections/TV%2FDrama/all" || u.Query().Get("X-Plex-Container-Size") != "9" || u.Query().Get("sort") != "titleSort:asc" {
		t.Fatalf("Items request = %s", (*paths)[0])
	}
}
