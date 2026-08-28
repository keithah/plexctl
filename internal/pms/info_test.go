package pms

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/keithah/plexctl/internal/api"
)

func TestServerInfoParsesRootConfiguration(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			t.Errorf("path: %s", r.URL.Path)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{"MediaContainer":{"friendlyName":"Living Room Plex","version":"1.42.0","machineIdentifier":"machine-1","Directory":[{"key":"1","title":"Movies","count":42}],"transcoderVideo":true}}`)); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer s.Close()
	a, err := api.New(s.URL, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	v, err := New(a).Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v.MediaContainer.FriendlyName != "Living Room Plex" || v.MediaContainer.Version != "1.42.0" || !v.MediaContainer.TranscoderVideo {
		t.Fatalf("info: %+v", v)
	}
	if len(v.MediaContainer.Directory) != 1 || v.MediaContainer.Directory[0].Count != 42 {
		t.Fatalf("directories: %+v", v.MediaContainer.Directory)
	}
}
