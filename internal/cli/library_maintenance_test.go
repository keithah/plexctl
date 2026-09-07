package cli

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
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

func TestLibraryMaintenanceTSVFieldEscapesControls(t *testing.T) {
	input := "first" + string(rune(0x09)) + "column" + string(rune(0x0a)) + "second" + string(rune(0x0d)) + "third" + string(rune(0x1b)) + "[2J" + string(rune(0x009b)) + "C" + string(rune(0x008d))
	got := libraryMaintenanceTSVField(input)
	want := "first" + string(rune(0x5c)) + "tcolumn" + string(rune(0x5c)) + "nsecond" + string(rune(0x5c)) + "rthird" + string(rune(0x5c)) + "u001B[2J" + string(rune(0x5c)) + "u009BC" + string(rune(0x5c)) + "u008D"
	if got != want {
		t.Fatalf("field = %q, want %q", got, want)
	}
}

func TestLibraryMaintenancePreviewRedactsPMSFailureDetail(t *testing.T) {
	server, _ := maintenanceServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "http://private-pms.invalid Authorization: Bearer header-secret", http.StatusInternalServerError)
	})
	defer server.Close()
	maintenanceConfig(t, server.URL)

	_, err := run(t, "library", "maintenance", "preview", "--mode", "unmatched")
	if err == nil {
		t.Fatal("PMS failure succeeded")
	}
	for _, forbidden := range []string{"private-pms", "Authorization", "header-secret"} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatalf("PMS failure leaked %q: %v", forbidden, err)
		}
	}
}

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
			fmt.Fprint(w, `{"MediaContainer":{"size":4,"offset":0,"totalSize":4,"Metadata":[{"ratingKey":"b","title":"  ALPHA  ","type":"movie","year":2000,"thumb":"/library/metadata/b/thumb","Guid":[{"id":"plex://movie/b"}]},{"ratingKey":"a","title":"Alpha","type":"movie","year":2020,"thumb":"","Guid":[{"id":"plex://movie/a"}]},{"ratingKey":"u","title":"Unsafe\u009BTitle\u008D","type":"movie","year":2021,"thumb":"/library/metadata/u/thumb","Guid":[]},{"ratingKey":"m","title":"Matched","type":"movie","year":2022,"thumb":"/library/metadata/m/thumb","Guid":[{"id":"plex://movie/m"}]}]}}`)
		case "/library/sections/2/all":
			fmt.Fprint(w, `{"MediaContainer":{"size":0,"offset":0,"totalSize":0,"Metadata":[]}}`)
		case "/library/sections/1/collections":
			fmt.Fprint(w, `{"MediaContainer":{"size":1,"offset":0,"totalSize":1,"Metadata":[{"ratingKey":"c","title":"Empty"}]}}`)
		case "/library/sections/2/collections":
			fmt.Fprint(w, `{"MediaContainer":{"size":0,"offset":0,"totalSize":0,"Metadata":[]}}`)
		case "/library/collections/c/items":
			fmt.Fprint(w, `{"MediaContainer":{"size":0,"offset":0,"totalSize":0,"Metadata":[]}}`)
		case "/library/metadata/b/thumb", "/library/metadata/u/thumb":
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write([]byte("x"))
		case "/library/metadata/m/thumb":
			http.Error(w, "fixture poster unavailable", http.StatusNotFound)
		default:
			http.Error(w, "unexpected path", http.StatusNotFound)
		}
	})
	defer server.Close()
	maintenanceConfig(t, server.URL)

	cases := []struct{ mode, want string }{
		{"empty-collections", "section_key	section_title	collection_rating_key	collection_title\n1	Films	c	Empty\n"},
		{"duplicates", "section_key	section_title	normalized_title	rating_key	title	media_type	year\n1	Films	alpha	a	Alpha	movie	2020\n1	Films	alpha	b	  ALPHA  	movie	2000\n"},
		{"missing-posters", "section_key	section_title	rating_key	title	media_type	reason\n1	Films	a	Alpha	movie	missing_thumb\n1	Films	m	Matched	movie	probe_failed\n"},
		{"unmatched", "section_key	section_title	rating_key	title	media_type	year\n1	Films	u	Unsafe\\u009BTitle\\u008D	movie	2021\n"},
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
		case "/library/sections/7":
			fmt.Fprint(w, `{"MediaContainer":{"title1":"Films","key":"7","type":"movie"}}`)
		case "/library/sections/7/all":
			fmt.Fprint(w, `{"MediaContainer":{"size":1,"offset":0,"totalSize":1,"Metadata":[{"ratingKey":"x","title":"No poster","type":"movie","thumb":"https://outside.invalid/image"}]}}`)
		case "/library/sections/7/collections":
			fmt.Fprint(w, `{"MediaContainer":{"size":1,"offset":0,"totalSize":1,"Metadata":[{"ratingKey":"bad","title":"Bad"}]}}`)
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
		if r.URL.Path == "/library/sections/7" {
			fmt.Fprint(w, `{"MediaContainer":{"title1":"Films","key":"7","type":"movie"}}`)
			return
		}
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
		case "/library/sections/7":
			fmt.Fprint(w, `{"MediaContainer":{"title1":"Films","key":"7","type":"movie"}}`)
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
	want := "section_key	section_title	rating_key	title	media_type	reason\n7	Films	gone	Gone	movie	probe_failed\n7	Films	zero	Zero	movie	probe_failed\n"
	if stdout != want {
		t.Fatalf("output = %q, want %q", stdout, want)
	}
}

func TestLibraryMaintenancePreviewRejectsNormalizedUnsafeThumbWithoutOutput(t *testing.T) {
	server, requests := maintenanceServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/library/sections/7":
			fmt.Fprint(w, `{"MediaContainer":{"title1":"Films","key":"7","type":"movie"}}`)
		case "/library/sections/7/all":
			fmt.Fprint(w, `{"MediaContainer":{"size":1,"offset":0,"totalSize":1,"Metadata":[{"ratingKey":"x","title":"X","type":"movie","thumb":"/library/../metadata/x/thumb"}]}}`)
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
	if err == nil {
		t.Fatal("unsafe thumbnail path succeeded")
	}
	if stdout != "" {
		t.Fatalf("unsafe thumbnail path printed rows: %q", stdout)
	}
	if got := len(*requests); got != 2 {
		t.Fatalf("requests = %d, want section metadata and item listing only", got)
	}
}

func TestLibraryMaintenancePreviewRejectsMalformedCollectionItemsWithoutOutput(t *testing.T) {
	server, _ := maintenanceServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/library/sections/7":
			fmt.Fprint(w, `{"MediaContainer":{"title1":"Films","key":"7","type":"movie"}}`)
		case "/library/sections/7/collections":
			fmt.Fprint(w, `{"MediaContainer":{"size":1,"offset":0,"totalSize":1,"Metadata":[{"ratingKey":"c","title":"Not proven empty"}]}}`)
		case "/library/collections/c/items":
			fmt.Fprint(w, `{"MediaContainer":{"size":1,"Metadata":[]}}`)
		default:
			http.Error(w, "unexpected path", http.StatusNotFound)
		}
	})
	defer server.Close()
	maintenanceConfig(t, server.URL)

	stdout, err := captureHistoryReportStdout(t, func() error {
		_, err := run(t, "library", "maintenance", "preview", "--mode", "empty-collections", "--section", "7")
		return err
	})
	if err == nil {
		t.Fatal("malformed collection items succeeded")
	}
	if stdout != "" {
		t.Fatalf("malformed collection items printed rows: %q", stdout)
	}
}

func TestLibraryMaintenancePreviewRejectsCollectionWithoutRatingKeyWithoutOutput(t *testing.T) {
	server, requests := maintenanceServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/library/sections/7":
			fmt.Fprint(w, `{"MediaContainer":{"title1":"Films","key":"7","type":"movie"}}`)
		case "/library/sections/7/collections":
			fmt.Fprint(w, `{"MediaContainer":{"size":1,"offset":0,"totalSize":1,"Metadata":[{"ratingKey":"","title":"Nameless"}]}}`)
		case "/library/collections//items":
			fmt.Fprint(w, `{"MediaContainer":{"size":0,"offset":0,"totalSize":0,"Metadata":[]}}`)
		default:
			http.Error(w, "unexpected path", http.StatusNotFound)
		}
	})
	defer server.Close()
	maintenanceConfig(t, server.URL)

	stdout, err := captureHistoryReportStdout(t, func() error {
		_, err := run(t, "library", "maintenance", "preview", "--mode", "empty-collections", "--section", "7")
		return err
	})
	if err == nil {
		t.Fatal("collection without rating key succeeded")
	}
	if stdout != "" {
		t.Fatalf("collection without rating key printed rows: %q", stdout)
	}
	for _, request := range *requests {
		if request.path == "/library/collections//items" {
			t.Fatalf("malformed collection requested item path: %q", request.path)
		}
	}
}

func TestLibraryMaintenancePreviewBuiltCLIAcceptance(t *testing.T) {
	server, requests := maintenanceServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/library/sections/all":
			fmt.Fprint(w, `{"MediaContainer":{"size":2,"Directory":[{"key":"2","title":"TV","type":"show"},{"key":"1","title":"Films","type":"movie"}]}}`)
		case "/library/sections/1/all":
			fmt.Fprint(w, `{"MediaContainer":{"size":4,"offset":0,"totalSize":4,"Metadata":[{"ratingKey":"b","title":"  ALPHA  ","type":"movie","year":2000,"thumb":"/library/metadata/b/thumb","Guid":[{"id":"plex://movie/b"}]},{"ratingKey":"a","title":"Alpha","type":"movie","year":2020,"thumb":"","Guid":[{"id":"plex://movie/a"}]},{"ratingKey":"u","title":"Unsafe\u009BTitle\u008D","type":"movie","year":2021,"thumb":"/library/metadata/u/thumb","Guid":[]},{"ratingKey":"m","title":"Matched","type":"movie","year":2022,"thumb":"/library/metadata/m/thumb","Guid":[{"id":"plex://movie/m"}]}]}}`)
		case "/library/sections/2/all":
			fmt.Fprint(w, `{"MediaContainer":{"size":0,"offset":0,"totalSize":0,"Metadata":[]}}`)
		case "/library/sections/1/collections":
			fmt.Fprint(w, `{"MediaContainer":{"size":1,"offset":0,"totalSize":1,"Metadata":[{"ratingKey":"c","title":"Empty"}]}}`)
		case "/library/sections/2/collections":
			fmt.Fprint(w, `{"MediaContainer":{"size":0,"offset":0,"totalSize":0,"Metadata":[]}}`)
		case "/library/collections/c/items":
			fmt.Fprint(w, `{"MediaContainer":{"size":0,"offset":0,"totalSize":0,"Metadata":[]}}`)
		case "/library/metadata/b/thumb", "/library/metadata/u/thumb":
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write([]byte("x"))
		case "/library/metadata/m/thumb":
			http.Error(w, "fixture poster unavailable", http.StatusNotFound)
		default:
			http.Error(w, "unexpected path", http.StatusNotFound)
		}
	})
	defer server.Close()

	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "plexctl")
	build := exec.Command("go", "build", "-o", binary, "./cmd/plexctl")
	build.Dir = repoRoot
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fixture CLI: %v\\n%s", err, output)
	}

	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := config.Save(configPath, config.Config{Current: "test", Servers: map[string]config.Server{"test": {URL: server.URL, TokenEnv: "LIBRARY_MAINTENANCE_TOKEN"}}}); err != nil {
		t.Fatal(err)
	}
	workDir := t.TempDir()
	cases := []struct{ mode, want string }{
		{"empty-collections", "section_key	section_title	collection_rating_key	collection_title\n1	Films	c	Empty\n"},
		{"duplicates", "section_key	section_title	normalized_title	rating_key	title	media_type	year\n1	Films	alpha	a	Alpha	movie	2020\n1	Films	alpha	b	  ALPHA  	movie	2000\n"},
		{"missing-posters", "section_key	section_title	rating_key	title	media_type	reason\n1	Films	a	Alpha	movie	missing_thumb\n1	Films	m	Matched	movie	probe_failed\n"},
		{"unmatched", "section_key	section_title	rating_key	title	media_type	year\n1	Films	u	Unsafe\\u009BTitle\\u008D	movie	2021\n"},
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			command := exec.Command(binary, "library", "maintenance", "preview", "--mode", tc.mode)
			command.Dir = workDir
			command.Env = append(os.Environ(), "PLEXCTL_CONFIG="+configPath, "LIBRARY_MAINTENANCE_TOKEN="+maintenanceToken)
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("run built CLI: %v\\n%s", err, output)
			}
			if got := string(output); got != tc.want {
				t.Fatalf("output = %q, want %q", got, tc.want)
			}
			if tc.mode == "unmatched" {
				lines := strings.Split(strings.TrimSuffix(string(output), "\n"), "\n")
				if len(lines) != 2 {
					t.Fatalf("unmatched output has %d physical rows, want 2: %q", len(lines), output)
				}
				if fields := strings.Split(lines[1], "	"); len(fields) != 6 {
					t.Fatalf("unmatched data row has %d columns, want 6: %q", len(fields), lines[1])
				}
				if strings.ContainsAny(lines[1], string(rune(0x009b))+string(rune(0x008d))) {
					t.Fatalf("unmatched output contains a literal C1 control: %q", lines[1])
				}
			}
			for _, forbidden := range []string{maintenanceToken, server.URL, "/library/metadata/"} {
				if strings.Contains(string(output), forbidden) {
					t.Errorf("output exposed %q: %q", forbidden, output)
				}
			}
		})
	}
	for _, request := range *requests {
		if request.method != http.MethodGet {
			t.Errorf("method = %s, want GET", request.method)
		}
		if strings.HasPrefix(request.path, "/library/metadata/") && request.range_ != "bytes=0-1023" {
			t.Errorf("probe %s Range = %q, want bytes=0-1023", request.path, request.range_)
		}
	}
	entries, err := os.ReadDir(workDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("read-only preview created local files: %v", entries)
	}
	if err := os.Remove(binary); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(binary); !os.IsNotExist(err) {
		t.Fatalf("temporary CLI binary still exists: %v", err)
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
