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
	"time"

	"github.com/keithah/plexctl/internal/api"
	"github.com/keithah/plexctl/internal/config"
	"github.com/keithah/plexctl/internal/pms"
	"github.com/spf13/cobra"
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
	if err := config.Save(configPath, config.Config{Current: "test", Servers: map[string]config.Server{"test": {URL: server.URL, TokenEnv: "READ_ONLY_AUDIT_TOKEN", InsecureTLS: true}}}); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"library", "integrity", "report", "--mode", "storage"}, "section_key	section_title	eligible_item_count	declared_media_record_count	known_part_count	unknown_part_count	known_bytes\n7	Films\\u009B	1	1	1	0	10\n"},
		{[]string{"library", "integrity", "report", "--mode", "unavailable-media"}, "section_key	section_title	rating_key	title	part_fingerprint	status\n"},
		{[]string{"library", "integrity", "report", "--mode", "duplicate-parts"}, "section_key	section_title	rating_key	title	part_fingerprint	status\n"},
		{[]string{"library", "integrity", "report", "--mode", "suspicious-parts"}, "section_key	section_title	rating_key	title	part_fingerprint	status\n"},
		{[]string{"sessions", "diagnostics"}, `session_id	title	grandparent_title	parent_title	user_id	user_title	client_id	client_title	client_platform	decision
s1	Movie Title			u1	User	c1	Client	Platform	direct_play

decision	count
direct_play	1
`},
		{[]string{"server", "maintenance", "status"}, "activity_id\ttype\ttitle\tprogress\tcancellable\na1\trefresh\tActivity\t0.5\ttrue\n\ntask_id\ttitle\tschedule\tenabled\tinterval\nt1\tTask\tdaily\ttrue\t60\n\ncan_install\tversion\trelease_date\nfalse\t1.2.3\t2026-01-01\n"},
		{[]string{"playlists", "audit"}, "container_id	title	kind	items_complete	empty	item_rating_keys	duplicate_item_rating_keys\np1	Playlist\\nTitle	playlist	true	false	m1	\n"},
		{[]string{"collections", "audit", "--section", "7"}, "container_id	title	kind	items_complete	empty	item_rating_keys	duplicate_item_rating_keys\nc1	Collection	collection	true	true		\n"},
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
	wantCollectionSequence := []string{"/library/sections/7", "/library/sections/7/collections", "/library/collections/c1/items"}
	foundCollectionSequence := false
	for i := 0; i+len(wantCollectionSequence) <= len(*requests); i++ {
		matches := true
		for j, want := range wantCollectionSequence {
			if (*requests)[i+j].path != want {
				matches = false
				break
			}
		}
		if matches {
			foundCollectionSequence = true
			break
		}
	}
	if !foundCollectionSequence {
		t.Fatalf("collection scope request sequence missing from %#v", *requests)
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
	if err := config.Save(configPath, config.Config{Current: "test", Servers: map[string]config.Server{"test": {URL: server.URL, TokenEnv: "READ_ONLY_AUDIT_TOKEN", InsecureTLS: true}}}); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(context.Background(), binary, "library", "integrity", "report", "--mode", "storage", "--section", "7")
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "PLEXCTL_CONFIG="+configPath, "READ_ONLY_AUDIT_TOKEN="+readOnlyAuditToken)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, output)
	}
	const want = "section_key	section_title	eligible_item_count	declared_media_record_count	known_part_count	unknown_part_count	known_bytes\n7	Selected	1	1	1	0	10\n"
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
	if err := config.Save(configPath, config.Config{Current: "test", Servers: map[string]config.Server{"test": {URL: server.URL, TokenEnv: "READ_ONLY_AUDIT_TOKEN", InsecureTLS: true}}}); err != nil {
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

func TestReadOnlyAuditCommandKeepsClientScopedToConcurrentInvocation(t *testing.T) {
	tokenA, tokenB := "concurrent-a-token-sentinel", "concurrent-b-token-sentinel"
	type observedRequest struct{ method, token string }
	newEndpoint := func(token string) (*httptest.Server, func() []observedRequest) {
		t.Helper()
		var mu sync.Mutex
		var requests []observedRequest
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			requests = append(requests, observedRequest{method: r.Method, token: r.Header.Get("X-Plex-Token")})
			mu.Unlock()
			if r.Method != http.MethodGet || r.Header.Get("X-Plex-Token") != token {
				http.Error(w, "wrong audit client", http.StatusUnauthorized)
				return
			}
			fmt.Fprint(w, `{"MediaContainer":{"size":0,"Metadata":[]}}`)
		}))
		return server, func() []observedRequest {
			mu.Lock()
			defer mu.Unlock()
			return append([]observedRequest(nil), requests...)
		}
	}

	serverA, requestsA := newEndpoint(tokenA)
	defer serverA.Close()
	serverB, requestsB := newEndpoint(tokenB)
	defer serverB.Close()
	readOnlyAuditConfig(t, serverA.URL)
	t.Setenv("READ_ONLY_AUDIT_TOKEN_A", tokenA)
	t.Setenv("READ_ONLY_AUDIT_TOKEN_B", tokenB)
	if err := config.Save(config.Path(), config.Config{Current: "test", Servers: map[string]config.Server{
		"test": {URL: serverA.URL, TokenEnv: "READ_ONLY_AUDIT_TOKEN_A", InsecureTLS: true},
	}}); err != nil {
		t.Fatal(err)
	}

	o := &options{timeout: time.Second}
	enteredValidation := make(chan struct{})
	releaseValidation := make(chan struct{})
	var firstValidation sync.Once
	readOnlyAuditTestHooks.Lock()
	readOnlyAuditTestHooks.afterValidation = func() {
		firstValidation.Do(func() {
			close(enteredValidation)
			<-releaseValidation
		})
	}
	readOnlyAuditTestHooks.Unlock()
	defer func() {
		readOnlyAuditTestHooks.Lock()
		readOnlyAuditTestHooks.afterValidation = nil
		readOnlyAuditTestHooks.Unlock()
	}()

	enteredHandler := make(chan struct{}, 2)
	releaseHandlers := make(chan struct{})
	newCommand := func() *cobra.Command {
		return readOnlyAuditCommand(o, &cobra.Command{}, func(_ *cobra.Command, _ []string, client *pms.Client) error {
			enteredHandler <- struct{}{}
			<-releaseHandlers
			_, err := client.Sessions(context.Background())
			return err
		})
	}
	first, second := newCommand(), newCommand()
	errs := make(chan error, 2)
	go func() { errs <- first.RunE(first, nil) }()
	<-enteredValidation
	if err := config.Save(config.Path(), config.Config{Current: "test", Servers: map[string]config.Server{
		"test": {URL: serverB.URL, TokenEnv: "READ_ONLY_AUDIT_TOKEN_B", InsecureTLS: true},
	}}); err != nil {
		t.Fatal(err)
	}
	go func() { errs <- second.RunE(second, nil) }()
	close(releaseValidation)
	<-enteredHandler
	<-enteredHandler
	close(releaseHandlers)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent audit invocation: %v", err)
		}
	}
	for name, expected := range map[string]struct {
		requests []observedRequest
		token    string
	}{
		"first":  {requests: requestsA(), token: tokenA},
		"second": {requests: requestsB(), token: tokenB},
	} {
		if len(expected.requests) != 1 {
			t.Fatalf("%s endpoint requests = %#v, want one request", name, expected.requests)
		}
		if got := expected.requests[0]; got.method != http.MethodGet || got.token != expected.token {
			t.Fatalf("%s endpoint request = %#v, want GET with its token", name, got)
		}
	}
}

func TestReadOnlyAuditUsesValidatedConnectionAfterConfigReplacement(t *testing.T) {
	tlsServer, tlsRequests := readOnlyAuditServer(t, readOnlyAuditFixture)
	defer tlsServer.Close()
	var httpRequests int
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpRequests++
		if got := r.Header.Get("X-Plex-Token"); got != "later-http-token-sentinel" {
			t.Errorf("HTTP token = %q, want later configured token", got)
		}
		http.Error(w, "must not be reached", http.StatusInternalServerError)
	}))
	defer httpServer.Close()

	readOnlyAuditConfig(t, tlsServer.URL)
	t.Setenv("READ_ONLY_AUDIT_HTTP_TOKEN", "later-http-token-sentinel")
	readOnlyAuditTestHooks.Lock()
	readOnlyAuditTestHooks.afterValidation = func() {
		if err := config.Save(config.Path(), config.Config{Current: "test", Servers: map[string]config.Server{
			"test": {URL: httpServer.URL, TokenEnv: "READ_ONLY_AUDIT_HTTP_TOKEN"},
		}}); err != nil {
			t.Fatal(err)
		}
	}
	readOnlyAuditTestHooks.Unlock()
	defer func() {
		readOnlyAuditTestHooks.Lock()
		readOnlyAuditTestHooks.afterValidation = nil
		readOnlyAuditTestHooks.Unlock()
	}()

	stdout, err := captureReadOnlyAuditStdout(t, func() error {
		_, err := run(t, "library", "integrity", "report", "--mode", "storage")
		return err
	})
	if err != nil {
		t.Fatalf("audit after replacement: %v", err)
	}
	if got := httpRequests; got != 0 {
		t.Fatalf("HTTP replacement received %d requests", got)
	}
	if got := len(*tlsRequests); got == 0 {
		t.Fatal("validated HTTPS server received no requests")
	}
	assertReadOnlyAuditSafeTSV(t, stdout, tlsServer.URL)
	for _, forbidden := range []string{httpServer.URL, "later-http-token-sentinel"} {
		if strings.Contains(stdout, forbidden) {
			t.Errorf("output exposed %q: %q", forbidden, stdout)
		}
	}
}

func TestReadOnlyAuditBuiltCLIRejectsHTTPConfigurationBeforeRequests(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if got := r.Header.Get("X-Plex-Token"); got != "" {
			t.Errorf("HTTP audit request exposed token %q", got)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	readOnlyAuditConfig(t, server.URL)
	stdout, err := captureReadOnlyAuditStdout(t, func() error {
		_, err := run(t, "library", "integrity", "report", "--mode", "storage")
		return err
	})
	if err == nil {
		t.Fatal("HTTP-configured audit succeeded")
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if got := requests; got != 0 {
		t.Fatalf("HTTP-configured audit made %d requests", got)
	}
	for _, forbidden := range []string{server.URL, readOnlyAuditToken} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatalf("audit error exposed %q: %q", forbidden, err)
		}
	}
}

func TestReadOnlyAuditFailsClosedWithoutOutput(t *testing.T) {
	for _, bad := range []string{"malformed-page", "blank-id", "unsafe-part", "failed-probe", "bad-status"} {
		t.Run(bad, func(t *testing.T) {
			server, requests := readOnlyAuditServer(t, func(w http.ResponseWriter, r *http.Request) {
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
			args := []string{"library", "integrity", "report", "--mode", "unavailable-media"}
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
			if bad == "malformed-page" || bad == "unsafe-part" {
				if !readOnlyAuditRequested(*requests, "/library/sections/7/all") {
					t.Fatalf("%s handler was not reached: %#v", bad, *requests)
				}
			}
			if bad == "failed-probe" && !readOnlyAuditRequested(*requests, readOnlyAuditPart) {
				t.Fatalf("failed-probe handler was not reached: %#v", *requests)
			}
		})
	}
}

func readOnlyAuditRequested(requests []readOnlyAuditRequest, path string) bool {
	for _, request := range requests {
		if request.path == path {
			return true
		}
	}
	return false
}

func readOnlyAuditServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *[]readOnlyAuditRequest) {
	t.Helper()
	var mu sync.Mutex
	requests := []readOnlyAuditRequest{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, readOnlyAuditRequest{r.Method, r.URL.Path, r.Header.Get("Range")})
		mu.Unlock()
		if r.Method != http.MethodGet {
			http.Error(w, "GET only", http.StatusMethodNotAllowed)
			return
		}
		if r.Header.Get("X-Plex-Token") != readOnlyAuditToken {
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
func readOnlyAuditFixture(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/library/sections/all":
		fmt.Fprint(w, `{"MediaContainer":{"size":1,"Directory":[{"key":"7","title":"Films\u009B","type":"movie"}]}}`)
	case "/library/sections/7":
		fmt.Fprint(w, `{"MediaContainer":{"key":"7","title1":"Films","type":"movie"}}`)
	case "/library/sections/7/all":
		fmt.Fprint(w, `{"MediaContainer":{"size":1,"offset":0,"totalSize":1,"Metadata":[{"ratingKey":"m1","title":"Movie\tTitle","Media":[{"Part":[{"key":"/library/parts/opaque/file-name-sentinel.mkv","size":10}]}]}]}}`)
	case readOnlyAuditPart:
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte("x"))
	case "/status/sessions":
		fmt.Fprint(w, `{"MediaContainer":{"size":1,"Metadata":[{"session":{"id":"s1"},"title":"Movie Title","User":{"id":"u1","title":"User"},"Player":{"machineIdentifier":"c1","title":"Client","platform":"Platform"},"Media":[{"videoDecision":"directplay","audioDecision":"directplay","subtitleDecision":"directplay"}]}]}}`)
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
	if err := config.Save(config.Path(), config.Config{Current: "test", Servers: map[string]config.Server{"test": {URL: url, TokenEnv: "READ_ONLY_AUDIT_TOKEN", InsecureTLS: true}}}); err != nil {
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
