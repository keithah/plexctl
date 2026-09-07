package pms

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/keithah/plexctl/internal/api"
)

func TestCollectionEndpoints(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/library/sections/2/collections" {
			if _, err := w.Write([]byte(`{"MediaContainer":{"size":1,"Metadata":[{"ratingKey":"c1","title":"Sci-Fi"}]}}`)); err != nil {
				t.Errorf("write response: %v", err)
			}
			return
		}
		if r.URL.Path == "/library/collections/c1/items" {
			if _, err := w.Write([]byte(`{"MediaContainer":{"size":3,"Metadata":[{"ratingKey":"m1","title":"Arrival"}]}}`)); err != nil {
				t.Errorf("write response: %v", err)
			}
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
	collections, err := c.Collections(context.Background(), "2")
	if err != nil {
		t.Fatal(err)
	}
	if len(collections.MediaContainer.Metadata) != 1 || collections.MediaContainer.Metadata[0].Title != "Sci-Fi" {
		t.Fatalf("collections: %+v", collections)
	}
	items, err := c.CollectionItems(context.Background(), "c1")
	if err != nil {
		t.Fatal(err)
	}
	if items.MediaContainer.Size != 3 {
		t.Fatalf("items: %+v", items)
	}
}

func TestListCollectionItemsValidatesCompletePaging(t *testing.T) {
	t.Run("paginates", func(t *testing.T) {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/library/collections/c1/items" {
				http.NotFound(w, r)
				return
			}
			switch r.URL.Query().Get("X-Plex-Container-Start") {
			case "0":
				_, _ = w.Write([]byte(`{"MediaContainer":{"size":1,"offset":0,"totalSize":2,"Metadata":[{"ratingKey":"a"}]}}`))
			case "1":
				_, _ = w.Write([]byte(`{"MediaContainer":{"size":1,"offset":1,"totalSize":2,"Metadata":[{"ratingKey":"b"}]}}`))
			default:
				http.Error(w, "unexpected page", http.StatusNotFound)
			}
		}))
		defer s.Close()
		a, err := api.New(s.URL, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		items, err := New(a).ListCollectionItems(context.Background(), "c1")
		if err != nil {
			t.Fatal(err)
		}
		if got := len(items.MediaContainer.Metadata); got != 2 {
			t.Fatalf("items = %d, want 2", got)
		}
	})
	t.Run("rejects incomplete empty page", func(t *testing.T) {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"MediaContainer":{"size":0,"offset":0,"totalSize":1,"Metadata":[]}}`))
		}))
		defer s.Close()
		a, err := api.New(s.URL, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := New(a).ListCollectionItems(context.Background(), "c1"); err == nil {
			t.Fatal("incomplete empty page succeeded")
		}
	})
}

func TestListCollectionsValidatesCompletePaging(t *testing.T) {
	t.Run("paginates", func(t *testing.T) {
		var starts []string
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/library/sections/2/collections" {
				http.NotFound(w, r)
				return
			}
			start := r.URL.Query().Get("X-Plex-Container-Start")
			starts = append(starts, start)
			switch start {
			case "0":
				_, _ = w.Write([]byte(`{"MediaContainer":{"size":1,"offset":0,"totalSize":2,"Metadata":[{"ratingKey":"c1"}]}}`))
			case "1":
				_, _ = w.Write([]byte(`{"MediaContainer":{"size":1,"offset":1,"totalSize":2,"Metadata":[{"ratingKey":"c2"}]}}`))
			default:
				http.Error(w, "unexpected page", http.StatusNotFound)
			}
		}))
		defer s.Close()
		a, err := api.New(s.URL, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		collections, err := New(a).ListCollections(context.Background(), "2")
		if err != nil {
			t.Fatal(err)
		}
		if got := len(collections.MediaContainer.Metadata); got != 2 {
			t.Fatalf("collections = %d, want 2", got)
		}
		if got := strings.Join(starts, ","); got != "0,1" {
			t.Fatalf("page starts = %q, want 0,1", got)
		}
	})
	t.Run("rejects incomplete listing", func(t *testing.T) {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"MediaContainer":{"size":1,"offset":0,"totalSize":2,"Metadata":[{"ratingKey":"c1"}]}}`))
		}))
		defer s.Close()
		a, err := api.New(s.URL, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := New(a).ListCollections(context.Background(), "2"); err == nil {
			t.Fatal("incomplete collection listing succeeded")
		}
	})
}
