package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/keithah/plexctl/internal/api"
	"github.com/keithah/plexctl/internal/config"
)

const readOnlyAuditToken = "read-only-audit-token-sentinel"
const readOnlyAuditPart = "/library/parts/opaque/file-name-sentinel.mkv"

type readOnlyAuditRequest struct{ method, path, range_ string }

func TestReadOnlyAuditErrorRetainsHTTPStatusClassification(t *testing.T) {
	original := &api.HTTPError{StatusCode: http.StatusInternalServerError, Method: http.MethodGet, Path: "/activities"}
	err := readOnlyAuditError(original)
	if got, want := err.Error(), "read-only audit request failed: HTTP 500: Internal Server Error"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
	var statusErr *api.HTTPError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusInternalServerError {
		t.Fatalf("HTTP error classification = %#v", statusErr)
	}
}

func TestReadOnlyAuditCommandTree(t *testing.T) {
	for _, path := range [][]string{{"library", "integrity", "report"}, {"sessions", "diagnostics"}, {"server", "maintenance", "status"}, {"playlists", "audit"}, {"collections", "audit"}} {
		cmd, _, err := NewRoot().Find(path)
		if err != nil {
			t.Fatalf("find %v: %v", path, err)
		}
		if got, want := cmd.CommandPath(), "plexctl "+strings.Join(path, " "); got != want {
			t.Fatalf("path = %q, want %q", got, want)
		}
	}
}

func TestReadOnlyAuditRejectsValidationBeforeRequests(t *testing.T) {
	server, requests := readOnlyAuditServer(t, nil)
	defer server.Close()
	readOnlyAuditConfig(t, server.URL)
	for _, args := range [][]string{
		{"library", "integrity", "report"}, {"library", "integrity", "report", "--mode", "bad"},
		{"--json", "sessions", "diagnostics"}, {"--json=false", "server", "maintenance", "status"},
		{"--json", "playlists", "audit"}, {"collections", "audit"}, {"collections", "audit", "--section", ""},
	} {
		stdout, err := captureReadOnlyAuditStdout(t, func() error { _, err := run(t, args...); return err })
		if err == nil {
			t.Errorf("%v succeeded", args)
		}
		if stdout != "" {
			t.Errorf("%v wrote stdout %q", args, stdout)
		}
	}
	if got := len(*requests); got != 0 {
		t.Fatalf("validation made %d requests", got)
	}
}

func TestReadOnlyAuditRejectsBlankIntegritySectionBeforeRequests(t *testing.T) {
	server, requests := readOnlyAuditServer(t, readOnlyAuditFixture)
	defer server.Close()
	readOnlyAuditConfig(t, server.URL)
	stdout, err := captureReadOnlyAuditStdout(t, func() error {
		_, err := run(t, "library", "integrity", "report", "--mode", "storage", "--section", " ")
		return err
	})
	if got, want := fmt.Sprint(err), "read-only audit failed"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if got := len(*requests); got != 0 {
		t.Fatalf("validation made %d requests", got)
	}
}

func TestReadOnlyAuditBuiltCLIAcceptance(t *testing.T) {
	server, requests := readOnlyAuditServer(t, readOnlyAuditFixture)
	defer server.Close()
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "plexctl")
	build := exec.CommandContext(context.Background(), "go", "build", "-o", binary, "./cmd/plexctl")
	build.Dir = repoRoot
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := config.Save(configPath, config.Config{Current: "test", Servers: map[string]config.Server{"test": {URL: server.URL, TokenEnv: "READ_ONLY_AUDIT_TOKEN"}}}); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"library", "integrity", "report", "--mode", "storage"}, "section_key\tsection_title\tknown_part_count\tunknown_part_count\tknown_bytes\n7\tFilms\\u009B\t1\t0\t10\n"},
		{[]string{"library", "integrity", "report", "--mode", "unavailable"}, "section_key	section_title	rating_key	title	part_fingerprint	status\n"},
		{[]string{"library", "integrity", "report", "--mode", "duplicates"}, "section_key	section_title	rating_key	title	part_fingerprint	status\n"},
		{[]string{"library", "integrity", "report", "--mode", "suspicious"}, "section_key	section_title	rating_key	title	part_fingerprint	status\n"},
		{[]string{"sessions", "diagnostics"}, "session_id\ttitle\tgrandparent_title\tparent_title\tuser_id\tuser_title\tclient_id\tclient_title\tclient_platform\tdecision\ns1\tMovie\\tTitle\t\t\tu1\tUser\tc1\tClient\tPlatform\ttranscode\n\ndecision\tcount\ntranscode\t1\n"},
		{[]string{"server", "maintenance", "status"}, "activity_id\ttype\ttitle\tprogress\tcancellable\na1\trefresh\tActivity\t0.5\ttrue\n\ntask_id\ttitle\tschedule\tenabled\tinterval\nt1\tTask\tdaily\ttrue\t60\n\ncan_install\tversion\trelease_date\nfalse\t1.2.3\t2026-01-01\n"},
		{[]string{"playlists", "audit"}, "container_id\ttitle\tkind\titems_complete\tempty\titem_rating_keys\np1\tPlaylist\\nTitle\tplaylist\ttrue\tfalse\tm1\n"},
		{[]string{"collections", "audit", "--section", "7"}, "container_id\ttitle\tkind\titems_complete\tempty\titem_rating_keys\nc1\tCollection\tcollection\ttrue\ttrue\t\n"},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			cmd := exec.CommandContext(context.Background(), binary, tc.args...)
			cmd.Dir = t.TempDir()
			cmd.Env = append(os.Environ(), "PLEXCTL_CONFIG="+configPath, "READ_ONLY_AUDIT_TOKEN="+readOnlyAuditToken)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("run: %v\n%s", err, output)
			}
			if got := string(output); got != tc.want {
				t.Fatalf("output = %q, want %q", got, tc.want)
			}
			assertReadOnlyAuditSafeTSV(t, string(output), server.URL)
		})
	}
	for _, request := range *requests {
		if request.method != http.MethodGet {
			t.Errorf("method = %s", request.method)
		}
		if request.path == readOnlyAuditPart && request.range_ != "bytes=0-1023" {
			t.Errorf("part range = %q", request.range_)
		}
	}
}

func TestReadOnlyAuditBuiltCLIIntegritySectionScope(t *testing.T) {
	server, requests := readOnlyAuditServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/library/sections/7":
			fmt.Fprint(w, `{"MediaContainer":{"title1":"Selected","key":"7","type":"movie"}}`)
		case "/library/sections/7/all":
			fmt.Fprint(w, `{"MediaContainer":{"size":1,"offset":0,"totalSize":1,"Metadata":[{"ratingKey":"selected","title":"Selected item","Media":[{"Part":[{"key":"/library/parts/selected/file.mkv","size":10}]}]}]}}`)
		case "/library/sections/all", "/library/sections/8", "/library/sections/8/all":
			http.Error(w, "unrelated section enumeration", http.StatusInternalServerError)
		default:
			http.Error(w, "unexpected request", http.StatusNotFound)
		}
	})
	defer server.Close()
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "plexctl")
	build := exec.CommandContext(context.Background(), "go", "build", "-o", binary, "./cmd/plexctl")
	build.Dir = repoRoot
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := config.Save(configPath, config.Config{Current: "test", Servers: map[string]config.Server{"test": {URL: server.URL, TokenEnv: "READ_ONLY_AUDIT_TOKEN"}}}); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(context.Background(), binary, "library", "integrity", "report", "--mode", "storage", "--section", "7")
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "PLEXCTL_CONFIG="+configPath, "READ_ONLY_AUDIT_TOKEN="+readOnlyAuditToken)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, output)
	}
	const want = "section_key	section_title	known_part_count	unknown_part_count	known_bytes\n7	Selected	1	0	10\n"
	if got := string(output); got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
	if got := len(*requests); got != 2 {
		t.Fatalf("requests = %d, want 2: %#v", got, *requests)
	}
	if got, want := (*requests)[0].path, "/library/sections/7"; got != want {
		t.Errorf("first request = %q, want %q", got, want)
	}
	if got, want := (*requests)[1].path, "/library/sections/7/all"; got != want {
		t.Errorf("second request = %q, want %q", got, want)
	}
}

func TestReadOnlyAuditBuiltCLIUpstreamErrorsArePrivate(t *testing.T) {
	server, _ := readOnlyAuditServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "response-body-sentinel Authorization: Bearer read-only-audit-token-sentinel", http.StatusInternalServerError)
	})
	defer server.Close()
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "plexctl")
	build := exec.CommandContext(context.Background(), "go", "build", "-o", binary, "./cmd/plexctl")
	build.Dir = repoRoot
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := config.Save(configPath, config.Config{Current: "test", Servers: map[string]config.Server{"test": {URL: server.URL, TokenEnv: "READ_ONLY_AUDIT_TOKEN"}}}); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"library", "integrity", "report", "--mode", "storage"},
		{"sessions", "diagnostics"},
		{"server", "maintenance", "status"},
		{"playlists", "audit"},
		{"collections", "audit", "--section", "7"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			cmd := exec.CommandContext(context.Background(), binary, args...)
			cmd.Dir = t.TempDir()
			cmd.Env = append(os.Environ(), "PLEXCTL_CONFIG="+configPath, "READ_ONLY_AUDIT_TOKEN="+readOnlyAuditToken)
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			if err := cmd.Run(); err == nil {
				t.Fatal("command succeeded")
			}
			if stdout.Len() != 0 {
				t.Fatalf("partial stdout = %q", stdout.String())
			}
			for _, output := range []string{stdout.String(), stderr.String()} {
				for _, forbidden := range []string{server.URL, readOnlyAuditToken, "Authorization", readOnlyAuditPart, "file-name-sentinel", "response-body-sentinel", "/library/sections", "/status/sessions", "/activities", "/playlists", "/collections"} {
					if strings.Contains(output, forbidden) {
						t.Errorf("output exposed %q: %q", forbidden, output)
					}
				}
			}
		})
	}
}

func TestReadOnlyAuditFailsClosedWithoutOutput(t *testing.T) {
	for _, bad := range []string{"malformed-page", "blank-id", "unsafe-part", "failed-probe", "bad-status"} {
		t.Run(bad, func(t *testing.T) {
			server, _ := readOnlyAuditServer(t, func(w http.ResponseWriter, r *http.Request) {
				if bad == "bad-status" && r.URL.Path == "/activities" {
					http.Error(w, "response-body-sentinel", http.StatusInternalServerError)
					return
				}
				if r.URL.Path == "/library/sections/7/all" && bad == "malformed-page" {
					fmt.Fprint(w, `{"MediaContainer":{"size":1,"offset":8,"totalSize":1,"Metadata":[]}}`)
					return
				}
				if r.URL.Path == "/library/sections/7/all" && bad == "unsafe-part" {
					fmt.Fprint(w, `{"MediaContainer":{"size":1,"offset":0,"totalSize":1,"Metadata":[{"ratingKey":"m1","title":"Movie","Media":[{"Part":[{"key":"/identity","size":10}]}]}]}}`)
					return
				}
				if r.URL.Path == "/playlists" && bad == "blank-id" {
					fmt.Fprint(w, `{"MediaContainer":{"size":1,"offset":0,"totalSize":1,"Metadata":[{"ratingKey":"","title":"Broken"}]}}`)
					return
				}
				if r.URL.Path == readOnlyAuditPart && bad == "failed-probe" {
					http.Error(w, "response-body-sentinel", 500)
					return
				}
				readOnlyAuditFixture(w, r)
			})
			defer server.Close()
			readOnlyAuditConfig(t, server.URL)
			args := []string{"library", "integrity", "report", "--mode", "unavailable"}
			if bad == "blank-id" {
				args = []string{"playlists", "audit"}
			}
			if bad == "bad-status" {
				args = []string{"server", "maintenance", "status"}
			}
			stdout, err := captureReadOnlyAuditStdout(t, func() error { _, err := run(t, args...); return err })
			if err == nil {
				t.Fatal("command succeeded")
			}
			if stdout != "" {
				t.Fatalf("partial output = %q", stdout)
			}
		})
	}
}

func readOnlyAuditServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *[]readOnlyAuditRequest) {
	t.Helper()
	var mu sync.Mutex
	requests := []readOnlyAuditRequest{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, readOnlyAuditRequest{r.Method, r.URL.Path, r.Header.Get("Range")})
		mu.Unlock()
		if r.Method != http.MethodGet {
			http.Error(w, "GET only", 405)
			return
		}
		if r.Header.Get("X-Plex-Token") != readOnlyAuditToken {
			http.Error(w, "bad token", 401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if handler != nil {
			handler(w, r)
		}
	}))
	return server, &requests
}
func readOnlyAuditFixture(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/library/sections/all":
		fmt.Fprint(w, `{"MediaContainer":{"size":1,"Directory":[{"key":"7","title":"Films\u009B","type":"movie"}]}}`)
	case "/library/sections/7/all":
		fmt.Fprint(w, `{"MediaContainer":{"size":1,"offset":0,"totalSize":1,"Metadata":[{"ratingKey":"m1","title":"Movie\tTitle","Media":[{"Part":[{"key":"/library/parts/opaque/file-name-sentinel.mkv","size":10}]}]}]}}`)
	case readOnlyAuditPart:
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte("x"))
	case "/status/sessions":
		fmt.Fprint(w, `{"MediaContainer":{"size":1,"Metadata":[{"session":{"id":"s1"},"title":"Movie\tTitle","User":{"id":"u1","title":"User"},"Player":{"machineIdentifier":"c1","title":"Client","platform":"Platform"},"TranscodeSession":{"key":"x"}}]}}`)
	case "/activities":
		fmt.Fprint(w, `{"MediaContainer":{"size":1,"Activity":[{"uuid":"a1","type":"refresh","title":"Activity","progress":0.5,"cancellable":true}]}}`)
	case "/butler":
		fmt.Fprint(w, `{"MediaContainer":{"size":1,"ButlerTask":[{"name":"t1","title":"Task","schedule":"daily","enabled":true,"interval":60}]}}`)
	case "/updater/status":
		fmt.Fprint(w, `{"MediaContainer":{"canInstall":false,"version":"1.2.3","releaseDate":"2026-01-01","downloadURL":"response-body-sentinel"}}`)
	case "/playlists":
		fmt.Fprint(w, `{"MediaContainer":{"size":1,"offset":0,"totalSize":1,"Metadata":[{"ratingKey":"p1","title":"Playlist\nTitle"}]}}`)
	case "/playlists/p1/items":
		fmt.Fprint(w, `{"MediaContainer":{"size":1,"offset":0,"totalSize":1,"Metadata":[{"ratingKey":"m1"}]}}`)
	case "/library/sections/7/collections":
		fmt.Fprint(w, `{"MediaContainer":{"size":1,"offset":0,"totalSize":1,"Metadata":[{"ratingKey":"c1","title":"Collection"}]}}`)
	case "/library/collections/c1/items":
		fmt.Fprint(w, `{"MediaContainer":{"size":0,"offset":0,"totalSize":0,"Metadata":[]}}`)
	default:
		http.Error(w, "response-body-sentinel", http.StatusNotFound)
	}
}
func readOnlyAuditConfig(t *testing.T, url string) {
	t.Helper()
	t.Setenv("PLEXCTL_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	t.Setenv("READ_ONLY_AUDIT_TOKEN", readOnlyAuditToken)
	if err := config.Save(config.Path(), config.Config{Current: "test", Servers: map[string]config.Server{"test": {URL: url, TokenEnv: "READ_ONLY_AUDIT_TOKEN"}}}); err != nil {
		t.Fatal(err)
	}
}
func captureReadOnlyAuditStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	err = fn()
	_ = w.Close()
	os.Stdout = old
	var output bytes.Buffer
	if _, copyErr := output.ReadFrom(r); copyErr != nil {
		t.Fatal(copyErr)
	}
	return output.String(), err
}
func assertReadOnlyAuditSafeTSV(t *testing.T, output, serverURL string) {
	t.Helper()
	for _, forbidden := range []string{readOnlyAuditToken, serverURL, readOnlyAuditPart, "file-name-sentinel", "response-body-sentinel"} {
		if strings.Contains(output, forbidden) {
			t.Errorf("output exposed %q", forbidden)
		}
	}
	for _, line := range strings.Split(strings.TrimSuffix(output, "\n"), "\n") {
		if strings.ContainsAny(line, string(rune(0))+string(rune(0x7f))+string(rune(0x9b))) {
			t.Errorf("control in %q", line)
		}
	}
}
