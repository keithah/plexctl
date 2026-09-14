package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/keithah/plexctl/internal/api"
	"github.com/keithah/plexctl/internal/pms"
)

func TestHandlerHealthyAndUnhealthy(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/identity":
			w.Write([]byte(`{"MediaContainer":{"size":0}}`))
		case "/library/sections/all":
			w.Write([]byte(`{"MediaContainer":{"size":1,"Directory":[{"key":"1","type":"movie"}]}}`))
		case "/library/sections/1/all":
			w.Write([]byte(`{"MediaContainer":{"Metadata":[{"key":"11"},{"key":"12"}]}}`))
		case "/library/metadata/11", "/library/metadata/12":
			w.Write([]byte(`{"MediaContainer":{"Metadata":[{"Media":[{"Part":[{"key":"/library/parts/1/file.mkv"}]}]}]}}`))
		case "/library/parts/1/file.mkv":
			w.WriteHeader(http.StatusPartialContent)
			w.Write(make([]byte, 1024))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	a, err := api.New(upstream.URL, "tok", nil)
	if err != nil {
		t.Fatal(err)
	}
	client := pms.New(a)
	h := Handler{Timeout: time.Second, Resolve: func(_ context.Context, account, server string) (ResolvedTarget, error) {
		if account != "keith" || server != "SF2" {
			t.Fatalf("unexpected target %s/%s", account, server)
		}
		return ResolvedTarget{Client: client}, nil
	}}
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/plex/keith/SF2", nil))
	if r.Code != http.StatusOK {
		t.Fatalf("healthy status = %d, body=%s", r.Code, r.Body)
	}
	var got map[string]any
	if err := json.Unmarshal(r.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["ok"] != true || got["classification"] != "ok" || got["duration_ms"] == nil {
		t.Fatalf("unexpected response: %v", got)
	}
	wantFields := map[string]bool{"ok": true, "classification": true, "stage": true, "duration_ms": true}
	if len(got) != len(wantFields) {
		t.Fatalf("response fields=%v, want only %v", got, wantFields)
	}
	for field := range got {
		if !wantFields[field] {
			t.Fatalf("response exposed unexpected field %q: %v", field, got)
		}
	}

	r = httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest(http.MethodPost, "/plex/keith/SF2", nil))
	if r.Code != http.StatusMethodNotAllowed {
		t.Fatalf("method status = %d", r.Code)
	}
}

func TestHandlerTimeoutBoundsResolution(t *testing.T) {
	resolutionStarted := make(chan struct{})
	h := Handler{
		Timeout: 20 * time.Millisecond,
		Resolve: func(ctx context.Context, _ string, _ string) (ResolvedTarget, error) {
			close(resolutionStarted)
			select {
			case <-ctx.Done():
				return ResolvedTarget{}, ctx.Err()
			case <-time.After(200 * time.Millisecond):
				return ResolvedTarget{}, errors.New("resolver did not receive handler deadline")
			}
		},
	}
	r := httptest.NewRecorder()
	startedAt := time.Now()
	h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/plex/account/server", nil))
	if elapsed := time.Since(startedAt); elapsed > time.Second {
		t.Fatalf("handler returned after %s, want resolver bounded by handler timeout", elapsed)
	}
	select {
	case <-resolutionStarted:
	default:
		t.Fatal("handler did not invoke resolver")
	}
	if r.Code != http.StatusServiceUnavailable || !contains(r.Body.String(), `"classification":"discovery"`) {
		t.Fatalf("status=%d body=%s, want bounded resolution 503 discovery", r.Code, r.Body)
	}
}

func TestHandlerReturnsAtDeadlineWhenResolverIgnoresContext(t *testing.T) {
	const timeout = 20 * time.Millisecond
	const resolverBlock = 250 * time.Millisecond

	started := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan struct{})
	h := Handler{
		Timeout: timeout,
		Resolve: func(context.Context, string, string) (ResolvedTarget, error) {
			close(started)
			<-release
			close(finished)
			return ResolvedTarget{}, ErrDiscoveryUnavailable
		},
	}
	go func() {
		<-started
		time.Sleep(resolverBlock)
		close(release)
	}()

	r := httptest.NewRecorder()
	startedAt := time.Now()
	h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/plex/account/server", nil))
	if elapsed := time.Since(startedAt); elapsed > resolverBlock/2 {
		t.Fatalf("handler waited for uncooperative resolver: %s", elapsed)
	}
	if r.Code != http.StatusServiceUnavailable || !contains(r.Body.String(), `"classification":"discovery"`) {
		t.Fatalf("status=%d body=%s, want deadline-bounded discovery failure", r.Code, r.Body)
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("uncooperative resolver did not finish after release")
	}
}

func TestHandlerDoesNotStartResolutionForCanceledRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var attempts atomic.Int32
	h := Handler{
		ResolveRetry: 1,
		Resolve: func(context.Context, string, string) (ResolvedTarget, error) {
			attempts.Add(1)
			return ResolvedTarget{}, ErrDiscoveryUnavailable
		},
	}
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/plex/account/server", nil).WithContext(ctx))
	if r.Code != http.StatusServiceUnavailable || !contains(r.Body.String(), `"classification":"discovery"`) {
		t.Fatalf("status=%d body=%s, want canceled discovery failure", r.Code, r.Body)
	}
	if got := attempts.Load(); got != 0 {
		t.Fatalf("resolve attempts=%d, want none for canceled request", got)
	}
}

func TestHandlerDoesNotCancelSharedResolutionWhenInitiatorCancels(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	secondStarted := make(chan struct{})
	var attempts atomic.Int32
	h := Handler{
		Timeout: time.Second,
		Resolve: func(ctx context.Context, _ string, _ string) (ResolvedTarget, error) {
			if attempts.Add(1) == 1 {
				close(started)
			} else {
				close(secondStarted)
			}
			select {
			case <-release:
				return ResolvedTarget{}, ErrDiscoveryUnavailable
			case <-ctx.Done():
				return ResolvedTarget{}, ctx.Err()
			}
		},
	}

	initiatorCtx, cancelInitiator := context.WithCancel(context.Background())
	defer cancelInitiator()
	initiatorDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/plex/account/server", nil).WithContext(initiatorCtx))
		initiatorDone <- r
	}()
	<-started
	cancelInitiator()
	if r := <-initiatorDone; r.Code != http.StatusServiceUnavailable {
		t.Fatalf("initiator status=%d body=%s, want canceled discovery failure", r.Code, r.Body)
	}

	waiterDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/plex/account/server", nil))
		waiterDone <- r
	}()
	select {
	case <-secondStarted:
		t.Fatal("initiator cancellation started a duplicate resolver")
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	if r := <-waiterDone; r.Code != http.StatusServiceUnavailable || !contains(r.Body.String(), `"classification":"discovery"`) {
		t.Fatalf("waiter status=%d body=%s, want shared discovery result", r.Code, r.Body)
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("resolve attempts=%d, want one shared resolver", got)
	}
}

func TestHandlerCapsStuckResolvers(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	started := make(chan struct{}, maxStuckResolvers)
	release := make(chan struct{})
	results := make(chan error, maxStuckResolvers)
	h := Handler{
		Resolve: func(context.Context, string, string) (ResolvedTarget, error) {
			started <- struct{}{}
			<-release
			return ResolvedTarget{}, ErrDiscoveryUnavailable
		},
	}
	for i := 0; i < maxStuckResolvers; i++ {
		go func(i int) {
			_, err := h.resolve(ctx, "account", fmt.Sprintf("server-%d", i))
			results <- err
		}(i)
	}
	for i := 0; i < maxStuckResolvers; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("expected resolver did not start")
		}
	}

	_, err := h.resolve(ctx, "account", "capacity-overflow")
	if !errors.Is(err, ErrDiscoveryUnavailable) || !errors.Is(err, errResolverCapacity) {
		t.Fatalf("capacity err=%v, want discovery-wrapped resolver capacity error", err)
	}

	close(release)
	for i := 0; i < maxStuckResolvers; i++ {
		select {
		case err := <-results:
			if !errors.Is(err, ErrDiscoveryUnavailable) {
				t.Fatalf("released resolver err=%v, want discovery failure", err)
			}
		case <-time.After(time.Second):
			t.Fatal("stuck resolver did not finish after release")
		}
	}
}

func TestHandlerTreatsExpiredResolutionAsDiscoveryFailure(t *testing.T) {
	a, err := api.New("http://127.0.0.1", "tok", nil)
	if err != nil {
		t.Fatal(err)
	}
	h := Handler{
		Timeout: 20 * time.Millisecond,
		Resolve: func(ctx context.Context, _ string, _ string) (ResolvedTarget, error) {
			<-ctx.Done()
			// A resolver that finishes concurrently with expiry can return its
			// target after the shared monitor deadline. That is still resolution
			// failure, not a PMS health failure.
			return ResolvedTarget{Client: pms.New(a)}, nil
		},
	}
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/plex/account/server", nil))
	if r.Code != http.StatusServiceUnavailable || !contains(r.Body.String(), `"classification":"discovery"`) || !contains(r.Body.String(), `"stage":"discovery"`) {
		t.Fatalf("status=%d body=%s, want expired resolution 503 discovery", r.Code, r.Body)
	}
}

func TestHandlerReportsCorrelationWithoutChangingFailureStatus(t *testing.T) {
	var event *CorrelationEvent
	tracker := NewCorrelationTracker(time.Minute, 2, time.Now)
	h := Handler{
		Correlation: tracker,
		OnCorrelation: func(got CorrelationEvent) {
			event = &got
		},
		Resolve: func(_ context.Context, _ string, server string) (ResolvedTarget, error) {
			return ResolvedTarget{CorrelationKey: server}, ErrDiscoveryUnavailable
		},
	}
	for _, path := range []string{"/plex/account/one", "/plex/account/two"} {
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, path, nil))
		if r.Code != http.StatusServiceUnavailable {
			t.Fatalf("status=%d, want individual 503", r.Code)
		}
	}
	if event == nil || event.TargetCount != 2 {
		t.Fatalf("event=%+v, want two-target correlation", event)
	}
}

func TestHandlerDoesNotOvercountAliasTargetsForCorrelation(t *testing.T) {
	var event *CorrelationEvent
	h := Handler{
		Correlation:   NewCorrelationTracker(time.Minute, 2, time.Now),
		OnCorrelation: func(got CorrelationEvent) { event = &got },
		Resolve: func(_ context.Context, _ string, server string) (ResolvedTarget, error) {
			key := "profile-alpha"
			if server == "Beta" {
				key = "profile-beta"
			}
			return ResolvedTarget{CorrelationKey: key}, ErrDiscoveryUnavailable
		},
	}
	for _, path := range []string{"/plex/account/Alpha", "/plex/account/alpha", "/plex/account/ALPHA"} {
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, path, nil))
		if r.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s: status=%d, want 503", path, r.Code)
		}
	}
	if event != nil {
		t.Fatalf("aliases emitted false multi-target event: %+v", event)
	}
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/plex/account/Beta", nil))
	if event == nil || event.TargetCount != 2 {
		t.Fatalf("event=%+v, want two canonical targets", event)
	}
}

func TestHandlerDoesNotCorrelateUnkeyedDiscoveryFailures(t *testing.T) {
	var event *CorrelationEvent
	h := Handler{
		Correlation:   NewCorrelationTracker(time.Minute, 2, time.Now),
		OnCorrelation: func(got CorrelationEvent) { event = &got },
		Resolve: func(context.Context, string, string) (ResolvedTarget, error) {
			return ResolvedTarget{}, ErrDiscoveryUnavailable
		},
	}
	for _, path := range []string{"/plex/account/one", "/plex/account/two"} {
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, path, nil))
		if r.Code != http.StatusServiceUnavailable {
			t.Fatalf("status=%d, want 503", r.Code)
		}
	}
	if event != nil {
		t.Fatalf("unkeyed failures emitted correlation event: %+v", event)
	}
}

func TestHandlerDoesNotCorrelateConfigurationFailures(t *testing.T) {
	var event *CorrelationEvent
	h := Handler{
		Correlation:   NewCorrelationTracker(time.Minute, 2, time.Now),
		OnCorrelation: func(got CorrelationEvent) { event = &got },
		Resolve: func(context.Context, string, string) (ResolvedTarget, error) {
			return ResolvedTarget{}, errors.New("bad local configuration")
		},
	}
	for _, path := range []string{"/plex/account/one", "/plex/account/two"} {
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, path, nil))
		if r.Code != http.StatusNotFound {
			t.Fatalf("status=%d, want configuration 404", r.Code)
		}
	}
	if event != nil {
		t.Fatalf("configuration failures emitted correlation event: %+v", event)
	}
}

func TestHandlerDoesNotExposeResolverErrorDetail(t *testing.T) {
	secret := "https://private.example/?token=secret"
	h := Handler{Resolve: func(context.Context, string, string) (ResolvedTarget, error) {
		return ResolvedTarget{}, errors.New(secret)
	}}
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/plex/account/server", nil))
	if r.Code != http.StatusNotFound || contains(r.Body.String(), secret) {
		t.Fatalf("status=%d body=%s, resolver detail leaked", r.Code, r.Body)
	}
	var body map[string]any
	if err := json.Unmarshal(r.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["classification"] != "configuration" || body["stage"] != "configuration" {
		t.Fatalf("error response=%v, want configuration classification and stage", body)
	}
	wantFields := map[string]bool{"ok": true, "classification": true, "stage": true}
	if len(body) != len(wantFields) {
		t.Fatalf("error fields=%v, want only %v", body, wantFields)
	}
	for field := range body {
		if !wantFields[field] {
			t.Fatalf("error response exposed unexpected field %q: %v", field, body)
		}
	}
}

func TestHandlerMapsDiscoveryFailureTo503(t *testing.T) {
	h := Handler{Resolve: func(context.Context, string, string) (ResolvedTarget, error) {
		return ResolvedTarget{}, ErrDiscoveryUnavailable
	}}
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/plex/account/server", nil))
	if r.Code != http.StatusServiceUnavailable || !contains(r.Body.String(), `"classification":"discovery"`) {
		t.Fatalf("status=%d body=%s, want 503 discovery", r.Code, r.Body)
	}
}

func TestHandlerMapsHealthFailureTo503AndDoesNotLeakToken(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("X-Plex-Token=tok"))
	}))
	defer upstream.Close()
	a, err := api.New(upstream.URL, "tok", nil)
	if err != nil {
		t.Fatal(err)
	}
	h := Handler{Timeout: time.Second, Resolve: func(context.Context, string, string) (ResolvedTarget, error) {
		return ResolvedTarget{Client: pms.New(a)}, nil
	}}
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/plex/a/b", nil))
	if r.Code != http.StatusServiceUnavailable {
		t.Fatalf("failure status = %d", r.Code)
	}
	if string(r.Body.Bytes()) == "" || contains(r.Body.String(), "tok") {
		t.Fatalf("token leaked: %s", r.Body)
	}
}

func TestHandlerRejectsMalformedTarget(t *testing.T) {
	h := Handler{}
	for _, path := range []string{"/", "/plex/a", "/other/a/b", "/plex/a/b/c"} {
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, path, nil))
		if r.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d", path, r.Code)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
