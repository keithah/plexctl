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

func TestHistoryAllFetchesEveryPage(t *testing.T) {
	var starts []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/status/sessions/history/all" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		starts = append(starts, r.URL.Query().Get("X-Plex-Container-Start"))
		switch r.URL.Query().Get("X-Plex-Container-Start") {
		case "0":
			_, _ = w.Write([]byte(`{"MediaContainer":{"size":2,"offset":0,"totalSize":3,"Metadata":[{"ratingKey":"2"},{"ratingKey":"1"}]}}`))
		case "2":
			_, _ = w.Write([]byte(`{"MediaContainer":{"size":1,"offset":2,"totalSize":3,"Metadata":[{"ratingKey":"3"}]}}`))
		default:
			http.Error(w, "unexpected page", http.StatusBadRequest)
		}
	}))
	defer s.Close()

	a, err := api.New(s.URL, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	history, err := New(a).HistoryAll(context.Background(), url.Values{"accountID": {"7"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{history.MediaContainer.Metadata[0].RatingKey, history.MediaContainer.Metadata[1].RatingKey, history.MediaContainer.Metadata[2].RatingKey}; !reflect.DeepEqual(got, []string{"2", "1", "3"}) {
		t.Fatalf("history order = %v", got)
	}
	if !reflect.DeepEqual(starts, []string{"0", "2"}) {
		t.Fatalf("page starts = %v", starts)
	}
}

func TestHistoryAllRejectsMissingTotalSizeOnEmptyPage(t *testing.T) {
	c, _, done := recorder(t, `{"MediaContainer":{"size":0,"offset":0,"Metadata":[]}}`)
	defer done()

	_, err := c.HistoryAll(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "missing total size") {
		t.Fatalf("error = %v, want missing total size error", err)
	}
}

func TestHistoryAllAcceptsExplicitEmptyTotalSize(t *testing.T) {
	c, _, done := recorder(t, `{"MediaContainer":{"size":0,"offset":0,"totalSize":0,"Metadata":[]}}`)
	defer done()

	history, err := c.HistoryAll(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if history.MediaContainer.Size != 0 || history.MediaContainer.TotalSize != 0 || len(history.MediaContainer.Metadata) != 0 {
		t.Fatalf("history = %+v, want explicit empty history", history.MediaContainer)
	}
}

func TestHistoryAllRejectsMissingOffset(t *testing.T) {
	c, _, done := recorder(t, `{"MediaContainer":{"size":1,"totalSize":1,"Metadata":[{"ratingKey":"1"}]}}`)
	defer done()

	_, err := c.HistoryAll(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "missing offset") {
		t.Fatalf("error = %v, want missing offset error", err)
	}
}

func TestHistoryAllRejectsIncompletePagingMetadata(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
		want string
	}{
		{"declared size mismatch", `{"MediaContainer":{"size":2,"offset":0,"totalSize":2,"Metadata":[{"ratingKey":"1"}]}}`, "declared size 2"},
		{"no progress", `{"MediaContainer":{"size":0,"offset":0,"totalSize":1,"Metadata":[]}}`, "no progress"},
		{"missing total", `{"MediaContainer":{"size":1,"offset":0,"Metadata":[{"ratingKey":"1"}]}}`, "missing total size"},
		{"unexpected offset", `{"MediaContainer":{"size":1,"offset":1,"totalSize":1,"Metadata":[{"ratingKey":"1"}]}}`, "unexpected offset"},
	} {
		t.Run(test.name, func(t *testing.T) {
			c, _, done := recorder(t, test.body)
			defer done()

			_, err := c.HistoryAll(context.Background(), nil)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
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
			_, _ = w.Write([]byte(`{"MediaContainer":{"size":2,"offset":0,"totalSize":3,"Metadata":[{"ratingKey":"2","title":"Second"},{"ratingKey":"1","title":"First"}]}}`))
		case "2":
			_, _ = w.Write([]byte(`{"MediaContainer":{"size":1,"offset":2,"totalSize":3,"Metadata":[{"ratingKey":"3","title":"Third"}]}}`))
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
	c, _, done := recorder(t, `{"MediaContainer":{"size":0,"offset":0,"totalSize":1,"Metadata":[]}}`)
	defer done()

	_, err := c.ListSectionItems(context.Background(), "7")
	if err == nil || !strings.Contains(err.Error(), "section 7") || !strings.Contains(err.Error(), "no progress") {
		t.Fatalf("error = %v, want contextual no-progress error", err)
	}
}

func TestListSectionItemsRejectsDeclaredSizeWithoutDecodedMetadata(t *testing.T) {
	c, _, done := recorder(t, `{"MediaContainer":{"size":2,"totalSize":2,"Metadata":[{"ratingKey":"1","title":"Only item"}]}}`)
	defer done()

	_, err := c.ListSectionItems(context.Background(), "7")
	if err == nil || !strings.Contains(err.Error(), "section 7") || !strings.Contains(err.Error(), "declared size 2") || !strings.Contains(err.Error(), "decoded 1") {
		t.Fatalf("error = %v, want contextual declared-size mismatch error", err)
	}
}

func TestListSectionItemsRejectsMissingTotalSizeOnEmptyPage(t *testing.T) {
	c, _, done := recorder(t, `{"MediaContainer":{"size":0,"offset":0,"Metadata":[]}}`)
	defer done()

	_, err := c.ListSectionItems(context.Background(), "7")
	if err == nil || !strings.Contains(err.Error(), "missing total size") {
		t.Fatalf("error = %v, want missing total size error", err)
	}
}

func TestListSectionItemsAcceptsExplicitEmptyTotalSize(t *testing.T) {
	c, _, done := recorder(t, `{"MediaContainer":{"size":0,"offset":0,"totalSize":0,"Metadata":[]}}`)
	defer done()

	items, err := c.ListSectionItems(context.Background(), "7")
	if err != nil {
		t.Fatal(err)
	}
	if items.MediaContainer.Size != 0 || items.MediaContainer.TotalSize != 0 || len(items.MediaContainer.Metadata) != 0 {
		t.Fatalf("items = %+v, want explicit empty section", items.MediaContainer)
	}
}

func TestListSectionItemsRejectsMissingOffset(t *testing.T) {
	c, _, done := recorder(t, `{"MediaContainer":{"size":1,"totalSize":1,"Metadata":[{"ratingKey":"1"}]}}`)
	defer done()

	_, err := c.ListSectionItems(context.Background(), "7")
	if err == nil || !strings.Contains(err.Error(), "missing offset") {
		t.Fatalf("error = %v, want missing offset error", err)
	}
}

func TestListSectionItemsRejectsInconsistentPagingMetadata(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
		want string
	}{
		{"unexpected offset", `{"MediaContainer":{"size":1,"offset":1,"totalSize":1,"Metadata":[{"ratingKey":"1"}]}}`, "unexpected offset"},
		{"total smaller than page", `{"MediaContainer":{"size":2,"offset":0,"totalSize":1,"Metadata":[{"ratingKey":"1"},{"ratingKey":"2"}]}}`, "invalid total size"},
	} {
		t.Run(test.name, func(t *testing.T) {
			c, _, done := recorder(t, test.body)
			defer done()

			_, err := c.ListSectionItems(context.Background(), "7")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestListSectionItemsRejectsChangingTotalSize(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("X-Plex-Container-Start") {
		case "0":
			_, _ = w.Write([]byte(`{"MediaContainer":{"size":1,"offset":0,"totalSize":2,"Metadata":[{"ratingKey":"1"}]}}`))
		case "1":
			_, _ = w.Write([]byte(`{"MediaContainer":{"size":1,"offset":1,"totalSize":3,"Metadata":[{"ratingKey":"2"}]}}`))
		case "2":
			_, _ = w.Write([]byte(`{"MediaContainer":{"size":1,"offset":2,"totalSize":3,"Metadata":[{"ratingKey":"3"}]}}`))
		default:
			http.Error(w, "unexpected page", http.StatusBadRequest)
		}
	}))
	defer s.Close()

	a, err := api.New(s.URL, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = New(a).ListSectionItems(context.Background(), "7")
	if err == nil || !strings.Contains(err.Error(), "total size changed") {
		t.Fatalf("error = %v, want total size changed error", err)
	}
}

func TestListSectionItemsRejectsDecreasingTotalSize(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("X-Plex-Container-Start") {
		case "0":
			_, _ = w.Write([]byte(`{"MediaContainer":{"size":1,"offset":0,"totalSize":3,"Metadata":[{"ratingKey":"1"}]}}`))
		case "1":
			_, _ = w.Write([]byte(`{"MediaContainer":{"size":1,"offset":1,"totalSize":2,"Metadata":[{"ratingKey":"2"}]}}`))
		default:
			http.Error(w, "unexpected page", http.StatusBadRequest)
		}
	}))
	defer s.Close()

	a, err := api.New(s.URL, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = New(a).ListSectionItems(context.Background(), "7")
	if err == nil || !strings.Contains(err.Error(), "total size changed") {
		t.Fatalf("error = %v, want total size changed error", err)
	}
}

func TestHistoryAllContinuesWhenTotalSizeGrows(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("X-Plex-Container-Start") {
		case "0":
			_, _ = w.Write([]byte(`{"MediaContainer":{"size":1,"offset":0,"totalSize":2,"Metadata":[{"ratingKey":"1"}]}}`))
		case "1":
			_, _ = w.Write([]byte(`{"MediaContainer":{"size":1,"offset":1,"totalSize":3,"Metadata":[{"ratingKey":"2"}]}}`))
		case "2":
			_, _ = w.Write([]byte(`{"MediaContainer":{"size":1,"offset":2,"totalSize":3,"Metadata":[{"ratingKey":"3"}]}}`))
		default:
			http.Error(w, "unexpected page", http.StatusBadRequest)
		}
	}))
	defer s.Close()

	a, err := api.New(s.URL, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	history, err := New(a).HistoryAll(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(history.MediaContainer.Metadata); got != 3 {
		t.Fatalf("history count = %d, want 3", got)
	}
}

func TestHistoryAllRejectsDecreasingTotalSize(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("X-Plex-Container-Start") {
		case "0":
			_, _ = w.Write([]byte(`{"MediaContainer":{"size":1,"offset":0,"totalSize":3,"Metadata":[{"ratingKey":"1"}]}}`))
		case "1":
			_, _ = w.Write([]byte(`{"MediaContainer":{"size":1,"offset":1,"totalSize":2,"Metadata":[{"ratingKey":"2"}]}}`))
		default:
			http.Error(w, "unexpected page", http.StatusBadRequest)
		}
	}))
	defer s.Close()

	a, err := api.New(s.URL, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = New(a).HistoryAll(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "total size decreased") {
		t.Fatalf("error = %v, want total size decreased error", err)
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
