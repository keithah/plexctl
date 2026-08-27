package pms

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/keithah/plexctl/internal/api"
)

func TestCollectionEndpoints(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/library/sections/2/collections" {
			w.Write([]byte(`{"MediaContainer":{"size":1,"Metadata":[{"ratingKey":"c1","title":"Sci-Fi"}]}}`))
			return
		}
		if r.URL.Path == "/library/collections/c1/items" {
			w.Write([]byte(`{"MediaContainer":{"size":3,"Metadata":[{"ratingKey":"m1","title":"Arrival"}]}}`))
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
