package pms

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/keithah/plexctl/internal/api"
)

func TestPlaylistEndpointsAndEscaping(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.EscapedPath() {
		case "/playlists":
			if _, err := w.Write([]byte(`{"MediaContainer":{"size":1,"Metadata":[{"ratingKey":"p1","title":"Favorites","playlistType":"video","smart":true}]}}`)); err != nil {
				t.Errorf("write response: %v", err)
			}
		case "/playlists/p%2F1":
			if _, err := w.Write([]byte(`{"MediaContainer":{"size":0}}`)); err != nil {
				t.Errorf("write response: %v", err)
			}
		case "/playlists/p1/items":
			if _, err := w.Write([]byte(`{"MediaContainer":{"size":2,"Metadata":[{"ratingKey":"m1","title":"One"}]}}`)); err != nil {
				t.Errorf("write response: %v", err)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer s.Close()
	a, err := api.New(s.URL, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	c := New(a)
	v, err := c.Playlists(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(v.MediaContainer.Metadata) != 1 || !v.MediaContainer.Metadata[0].Smart {
		t.Fatalf("playlists: %+v", v)
	}
	if _, err = c.Playlist(context.Background(), "p/1"); err != nil {
		t.Fatal(err)
	}
	items, err := c.PlaylistItems(context.Background(), "p1")
	if err != nil {
		t.Fatal(err)
	}
	if items.MediaContainer.Size != 2 {
		t.Fatalf("items: %+v", items)
	}
}
