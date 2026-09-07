package cli

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/keithah/plexctl/internal/config"
)

type maintenanceRequest struct {
	method string
	path   string
	query  string
	range_ string
	token  string
}

const maintenanceToken = "library-maintenance-token-sentinel"

func TestLibraryMaintenancePreviewCommandTree(t *testing.T) {
	cmd, _, err := NewRoot().Find([]string{"library", "maintenance", "preview"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := cmd.CommandPath(), "plexctl library maintenance preview"; got != want {
		t.Fatalf("command path = %q, want %q", got, want)
	}
	for _, name := range []string{"mode", "section"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("preview is missing --%s", name)
		}
	}
}

func TestLibraryMaintenancePreviewRejectsValidationBeforeRequests(t *testing.T) {
	server, requests := maintenanceServer(t, nil)
	defer server.Close()
	maintenanceConfig(t, server.URL)

	for _, args := range [][]string{
		{"library", "maintenance", "preview"},
		{"library", "maintenance", "preview", "--mode", "unknown"},
		{"--json", "library", "maintenance", "preview", "--mode", "duplicates"},
		{"--json=false", "library", "maintenance", "preview", "--mode", "duplicates"},
	} {
		stdout, err := captureHistoryReportStdout(t, func() error { _, err := run(t, args...); return err })
		if err == nil {
			t.Errorf("%v succeeded", args)
		}
		if stdout != "" {
			t.Errorf("%v printed report rows: %q", args, stdout)
		}
	}
	if got := len(*requests); got != 0 {
		t.Fatalf("validation made %d PMS requests", got)
	}
}

func TestLibraryMaintenancePreviewReportsSafeDeterministicTables(t *testing.T) {
	server, requests := maintenanceServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/library/sections/all":
			fmt.Fprint(w, `{"MediaContainer":{"size":2,"Directory":[{"key":"2","title":"TV","type":"show"},{"key":"1","title":"Films","type":"movie"}]}}`)
		case "/library/sections/1/all":
			fmt.Fprint(w, `{"MediaContainer":{"size":4,"offset":0,"totalSize":4,"Metadata":[{"ratingKey":"b","title":"  ALPHA  ","type":"movie","year":2000,"thumb":"/library/metadata/b/thumb","Guid":[{"id":"plex://movie/b"}]},{"ratingKey":"a","title":"Alpha","type":"movie","year":2020,"thumb":"","Guid":[{"id":"plex://movie/a"}]},{"ratingKey":"u","title":"Unmatched","type":"movie","year":2021,"thumb":"/library/metadata/u/thumb","Guid":[]},{"ratingKey":"m","title":"Matched","type":"movie","year":2022,"thumb":"/library/metadata/m/thumb","Guid":[{"id":"plex://movie/m"}]}]}}`)
		case "/library/sections/2/all":
			fmt.Fprint(w, `{"MediaContainer":{"size":0,"offset":0,"totalSize":0,"Metadata":[]}}`)
		case "/library/sections/1/collections":
			fmt.Fprint(w, `{"MediaContainer":{"size":1,"Metadata":[{"ratingKey":"c","title":"Empty"}]}}`)
		case "/library/sections/2/collections":
			fmt.Fprint(w, `{"MediaContainer":{"size":0,"Metadata":[]}}`)
		case "/library/collections/c/items":
			fmt.Fprint(w, `{"MediaContainer":{"size":0,"Metadata":[]}}`)
		case "/library/metadata/b/thumb", "/library/metadata/u/thumb", "/library/metadata/m/thumb":
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write([]byte("x"))
		default:
			http.Error(w, "unexpected path", http.StatusNotFound)
		}
	})
	defer server.Close()
	maintenanceConfig(t, server.URL)

	cases := []struct{ mode, want string }{
		{"empty-collections", "section_key\tsection_title\tcollection_rating_key\tcollection_title\n1\tFilms\tc\tEmpty\n"},
		{"duplicates", "section_key\tsection_title\tnormalized_title\trating_key\ttitle\tmedia_type\tyear\n1\tFilms\talpha\ta\tAlpha\tmovie\t2020\n1\tFilms\talpha\tb\t  ALPHA  \tmovie\t2000\n"},
		{"missing-posters", "section_key\tsection_title\trating_key\ttitle\tmedia_type\treason\n1\tFilms\ta\tAlpha\tmovie\tmissing_thumb\n"},
		{"unmatched", "section_key\tsection_title\trating_key\ttitle\tmedia_type\tyear\n1\tFilms\tu\tUnmatched\tmovie\t2021\n"},
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			stdout, err := captureHistoryReportStdout(t, func() error { _, err := run(t, "library", "maintenance", "preview", "--mode", tc.mode); return err })
			if err != nil {
				t.Fatal(err)
			}
			if stdout != tc.want {
				t.Fatalf("output = %q, want %q", stdout, tc.want)
			}
			for _, secret := range []string{maintenanceToken, server.URL, "/library/metadata/"} {
				if strings.Contains(stdout, secret) {
					t.Errorf("output exposed %q: %q", secret, stdout)
				}
			}
		})
	}
	for _, request := range *requests {
		if request.method != http.MethodGet {
			t.Errorf("method = %s, want GET", request.method)
		}
		if strings.HasPrefix(request.path, "/library/metadata/") && request.range_ != "bytes=0-1023" {
			t.Errorf("probe %s Range = %q", request.path, request.range_)
		}
	}
}

func TestLibraryMaintenancePreviewScopesSectionsAndFailsClosed(t *testing.T) {
	server, requests := maintenanceServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/library/sections/7/all":
			fmt.Fprint(w, `{"MediaContainer":{"size":1,"offset":0,"totalSize":1,"Metadata":[{"ratingKey":"x","title":"No poster","type":"movie","thumb":"https://outside.invalid/image"}]}}`)
		case "/library/sections/7/collections":
			fmt.Fprint(w, `{"MediaContainer":{"size":1,"Metadata":[{"ratingKey":"bad","title":"Bad"}]}}`)
		case "/library/collections/bad/items":
			http.Error(w, "broken", http.StatusInternalServerError)
		default:
			http.Error(w, "unexpected path", http.StatusNotFound)
		}
	})
	defer server.Close()
	maintenanceConfig(t, server.URL)

	for _, mode := range []string{"missing-posters", "empty-collections"} {
		stdout, err := captureHistoryReportStdout(t, func() error {
			_, err := run(t, "library", "maintenance", "preview", "--mode", mode, "--section", "7")
			return err
		})
		if err == nil {
			t.Errorf("%s succeeded", mode)
		}
		if stdout != "" {
			t.Errorf("%s printed partial output: %q", mode, stdout)
		}
	}
	for _, request := range *requests {
		if request.path == "/library/sections/all" {
			t.Error("scoped scan listed all sections")
		}
	}
}

func TestLibraryMaintenancePreviewPropagatesPaginationFailureWithoutRows(t *testing.T) {
	server, _ := maintenanceServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/library/sections/7/all" {
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}
		fmt.Fprint(w, `{"MediaContainer":{"size":1,"offset":99,"totalSize":1,"Metadata":[{"ratingKey":"x","title":"X","type":"movie"}]}}`)
	})
	defer server.Close()
	maintenanceConfig(t, server.URL)
	stdout, err := captureHistoryReportStdout(t, func() error {
		_, err := run(t, "library", "maintenance", "preview", "--mode", "unmatched", "--section", "7")
		return err
	})
	if err == nil {
		t.Fatal("inconsistent page succeeded")
	}
	if stdout != "" {
		t.Fatalf("pagination failure printed rows: %q", stdout)
	}
}

func TestLibraryMaintenancePreviewClassifiesRelativePosterProbeFailures(t *testing.T) {
	server, _ := maintenanceServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/library/sections/7/all":
			fmt.Fprint(w, `{"MediaContainer":{"size":2,"offset":0,"totalSize":2,"Metadata":[{"ratingKey":"zero","title":"Zero","type":"movie","thumb":"/library/metadata/zero/thumb"},{"ratingKey":"gone","title":"Gone","type":"movie","thumb":"/library/metadata/gone/thumb"}]}}`)
		case "/library/metadata/zero/thumb":
			w.WriteHeader(http.StatusOK)
		case "/library/metadata/gone/thumb":
			http.Error(w, "gone", http.StatusNotFound)
		default:
			http.Error(w, "unexpected path", http.StatusNotFound)
		}
	})
	defer server.Close()
	maintenanceConfig(t, server.URL)
	stdout, err := captureHistoryReportStdout(t, func() error {
		_, err := run(t, "library", "maintenance", "preview", "--mode", "missing-posters", "--section", "7")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "section_key	section_title	rating_key	title	media_type	reason\n7		gone	Gone	movie	probe_failed\n7		zero	Zero	movie	probe_failed\n"
	if stdout != want {
		t.Fatalf("output = %q, want %q", stdout, want)
	}
}

func maintenanceServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *[]maintenanceRequest) {
	t.Helper()
	var mu sync.Mutex
	requests := []maintenanceRequest{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, maintenanceRequest{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("Range"), r.Header.Get("X-Plex-Token")})
		mu.Unlock()
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
			http.Error(w, "GET only", http.StatusMethodNotAllowed)
			return
		}
		if got := r.Header.Get("X-Plex-Token"); got != maintenanceToken {
			t.Errorf("token = %q", got)
			http.Error(w, "bad token", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if handler != nil {
			handler(w, r)
		}
	}))
	return server, &requests
}

func maintenanceConfig(t *testing.T, url string) {
	t.Helper()
	t.Setenv("PLEXCTL_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	t.Setenv("LIBRARY_MAINTENANCE_TOKEN", maintenanceToken)
	if err := config.Save(config.Path(), config.Config{Current: "test", Servers: map[string]config.Server{"test": {URL: url, TokenEnv: "LIBRARY_MAINTENANCE_TOKEN"}}}); err != nil {
		t.Fatal(err)
	}
}
