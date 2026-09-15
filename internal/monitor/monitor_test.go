package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
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

func TestHandlerUsesRetryResolverForClassifiedDiscoveryFailure(t *testing.T) {
	var primaryCalls atomic.Int32
	var retryCalls atomic.Int32
	h := Handler{
		Resolve: func(context.Context, string, string) (ResolvedTarget, error) {
			primaryCalls.Add(1)
			return ResolvedTarget{CorrelationKey: "target"}, ErrDiscoveryUnavailable
		},
		RetryResolve: func(context.Context, string, string) (ResolvedTarget, error) {
			retryCalls.Add(1)
			return ResolvedTarget{CorrelationKey: "target"}, nil
		},
		ResolveRetry: 1,
		RetryDelay:   0,
	}
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/plex/account/server", nil))
	if r.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d, want nil retry target to reach handler-state failure", r.Code)
	}
	if got := primaryCalls.Load(); got != 1 {
		t.Fatalf("primary resolver calls=%d, want 1", got)
	}
	if got := retryCalls.Load(); got != 1 {
		t.Fatalf("retry resolver calls=%d, want 1", got)
	}
}

func TestHandlerRetryResolverDoesNotJoinNormalResolver(t *testing.T) {
	firstReturned := make(chan struct{})
	normalStarted := make(chan struct{})
	releaseNormal := make(chan struct{})
	retryStarted := make(chan struct{})
	releaseRetry := make(chan struct{})
	var normalCalls atomic.Int32
	var retryCalls atomic.Int32
	defer func() {
		select {
		case <-releaseRetry:
		default:
			close(releaseRetry)
		}
		select {
		case <-releaseNormal:
		default:
			close(releaseNormal)
		}
	}()

	h := Handler{
		Timeout:      time.Second,
		ResolveRetry: 1,
		RetryDelay:   100 * time.Millisecond,
		Resolve: func(context.Context, string, string) (ResolvedTarget, error) {
			switch normalCalls.Add(1) {
			case 1:
				close(firstReturned)
				return ResolvedTarget{CorrelationKey: "target"}, ErrDiscoveryUnavailable
			case 2:
				close(normalStarted)
				<-releaseNormal
				return ResolvedTarget{CorrelationKey: "target"}, ErrDiscoveryUnavailable
			default:
				return ResolvedTarget{CorrelationKey: "target"}, ErrDiscoveryUnavailable
			}
		},
		RetryResolve: func(context.Context, string, string) (ResolvedTarget, error) {
			retryCalls.Add(1)
			close(retryStarted)
			<-releaseRetry
			return ResolvedTarget{CorrelationKey: "target"}, nil
		},
	}

	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/plex/account/server", nil))
		firstDone <- r
	}()
	<-firstReturned

	normalCtx, cancelNormal := context.WithCancel(context.Background())
	defer cancelNormal()
	normalDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/plex/account/server", nil).WithContext(normalCtx))
		normalDone <- r
	}()
	<-normalStarted
	cancelNormal()
	if r := <-normalDone; r.Code != http.StatusServiceUnavailable {
		t.Fatalf("normal waiter status=%d, want canceled discovery failure", r.Code)
	}

	select {
	case <-retryStarted:
	case <-time.After(time.Second):
		t.Fatal("retry resolver joined the in-flight normal resolver instead of forcing refresh")
	}
	close(releaseRetry)
	if r := <-firstDone; r.Code != http.StatusInternalServerError {
		t.Fatalf("retrying request status=%d, want retry resolver's nil-client state", r.Code)
	}
	if got := retryCalls.Load(); got != 1 {
		t.Fatalf("retry resolver calls=%d, want 1", got)
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

func TestHandlerSharedResolutionDoesNotInheritLeaderDeadline(t *testing.T) {
	leaderCtx, cancelLeader := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancelLeader()
	waiterCtx, cancelWaiter := context.WithTimeout(context.Background(), time.Second)
	defer cancelWaiter()

	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseResolver := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseResolver)
	var calls atomic.Int32
	h := Handler{
		Resolve: func(ctx context.Context, _ string, _ string) (ResolvedTarget, error) {
			if calls.Add(1) == 1 {
				close(started)
			}
			<-release
			if err := ctx.Err(); err != nil {
				return ResolvedTarget{}, err
			}
			return ResolvedTarget{CorrelationKey: "profile-alpha"}, nil
		},
	}

	leaderDone := make(chan error, 1)
	go func() {
		_, err := h.resolve(leaderCtx, "account", "alpha")
		leaderDone <- err
	}()
	<-started

	// The second waiter joins before the leader's deadline, but its own deadline
	// remains healthy after the leader expires.
	waiterDone := make(chan resolveResult, 1)
	go func() {
		target, err := h.resolve(waiterCtx, "account", "alpha")
		waiterDone <- resolveResult{target: target, err: err}
	}()
	waitForResolutionWaiters(t, &h, resolveKey{account: "account", server: "alpha"}, 2)
	if err := <-leaderDone; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("leader error=%v, want its own deadline", err)
	}
	releaseResolver()
	result := <-waiterDone
	if result.err != nil || result.target.CorrelationKey != "profile-alpha" {
		t.Fatalf("waiter result=%+v, want healthy shared resolution", result)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("resolver calls=%d, want one shared resolver", got)
	}
}

func waitForResolutionWaiters(t *testing.T, h *Handler, key resolveKey, want int) {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		state := h.resolutionState()
		state.mu.Lock()
		call := state.calls[key]
		got := 0
		if call != nil {
			got = call.waiters
		}
		state.mu.Unlock()
		if got >= want {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("resolver waiters=%d, want at least %d", got, want)
		case <-time.After(time.Millisecond):
		}
	}
}

func TestHandlerExpiredWorkerCannotDeleteReplacement(t *testing.T) {
	firstCtx, cancelFirst := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelFirst()
	secondCtx, cancelSecond := context.WithTimeout(context.Background(), time.Second)
	defer cancelSecond()
	thirdCtx, cancelThird := context.WithTimeout(context.Background(), time.Second)
	defer cancelThird()

	firstStarted := make(chan struct{})
	firstRelease := make(chan struct{})
	firstReturned := make(chan struct{})
	secondStarted := make(chan struct{})
	secondRelease := make(chan struct{})
	var releaseFirstOnce sync.Once
	releaseFirst := func() { releaseFirstOnce.Do(func() { close(firstRelease) }) }
	var releaseSecondOnce sync.Once
	releaseSecond := func() { releaseSecondOnce.Do(func() { close(secondRelease) }) }
	t.Cleanup(func() {
		releaseFirst()
		releaseSecond()
	})
	var calls atomic.Int32
	h := Handler{Resolve: func(ctx context.Context, _ string, _ string) (ResolvedTarget, error) {
		switch calls.Add(1) {
		case 1:
			close(firstStarted)
			<-firstRelease
			close(firstReturned)
			return ResolvedTarget{}, ctx.Err()
		case 2:
			close(secondStarted)
			<-secondRelease
			return ResolvedTarget{CorrelationKey: "profile-alpha"}, nil
		default:
			return ResolvedTarget{}, errors.New("unexpected replacement resolver")
		}
	}}

	firstDone := make(chan error, 1)
	go func() {
		_, err := h.resolve(firstCtx, "account", "alpha")
		firstDone <- err
	}()
	<-firstStarted
	if err := <-firstDone; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("first error=%v, want deadline exceeded", err)
	}

	secondDone := make(chan resolveResult, 1)
	go func() {
		target, err := h.resolve(secondCtx, "account", "alpha")
		secondDone <- resolveResult{target: target, err: err}
	}()
	<-secondStarted
	releaseFirst()
	<-firstReturned
	waitForResolverWorkers(t, &h, 1)

	thirdDone := make(chan resolveResult, 1)
	go func() {
		target, err := h.resolve(thirdCtx, "account", "alpha")
		thirdDone <- resolveResult{target: target, err: err}
	}()
	select {
	case <-time.After(50 * time.Millisecond):
		if got := calls.Load(); got != 2 {
			t.Fatalf("resolver calls=%d, want replacement worker to remain shared", got)
		}
	}
	releaseSecond()
	for range 2 {
		var result resolveResult
		select {
		case result = <-secondDone:
		case result = <-thirdDone:
		}
		if result.err != nil || result.target.CorrelationKey != "profile-alpha" {
			t.Fatalf("replacement result=%+v, want shared target", result)
		}
	}
}

func waitForResolverWorkers(t *testing.T, h *Handler, want int) {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		state := h.resolutionState()
		if got := len(state.workers); got == want {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("resolver workers=%d, want %d", len(state.workers), want)
		case <-time.After(time.Millisecond):
		}
	}
}

func TestHandlerDoesNotCoalesceDistinctNULSelectors(t *testing.T) {
	type selector struct {
		account string
		server  string
	}

	started := make(chan selector, 2)
	release := make(chan struct{})
	done := make(chan *httptest.ResponseRecorder, 2)
	released := false
	releaseResolvers := func() {
		if !released {
			close(release)
			released = true
		}
	}
	defer releaseResolvers()

	h := Handler{
		Timeout: time.Second,
		Resolve: func(ctx context.Context, account, server string) (ResolvedTarget, error) {
			started <- selector{account: account, server: server}
			select {
			case <-release:
				return ResolvedTarget{}, ErrDiscoveryUnavailable
			case <-ctx.Done():
				return ResolvedTarget{}, ctx.Err()
			}
		},
	}
	paths := []string{
		"/plex/a/b%00c",
		"/plex/a%00b/c",
	}
	for _, path := range paths {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		if !contains(request.URL.Path, "\x00") {
			t.Fatalf("path %q did not decode its NUL selector", path)
		}
		go func(request *http.Request) {
			r := httptest.NewRecorder()
			h.ServeHTTP(r, request)
			done <- r
		}(request)
	}

	seen := make(map[selector]struct{}, 2)
	for range paths {
		select {
		case got := <-started:
			seen[got] = struct{}{}
		case <-time.After(100 * time.Millisecond):
			releaseResolvers()
			for range paths {
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Fatal("coalesced resolver did not finish after release")
				}
			}
			t.Fatalf("resolver calls=%d, want distinct calls for NUL selectors", len(seen))
		}
	}
	want := map[selector]struct{}{
		{account: "a", server: "b\x00c"}: {},
		{account: "a\x00b", server: "c"}: {},
	}
	if len(seen) != len(want) {
		t.Fatalf("resolver selectors=%v, want %v", seen, want)
	}
	for expected := range want {
		if _, ok := seen[expected]; !ok {
			t.Fatalf("resolver selectors=%v, missing %q/%q", seen, expected.account, expected.server)
		}
	}

	releaseResolvers()
	for range paths {
		select {
		case r := <-done:
			if r.Code != http.StatusServiceUnavailable || !contains(r.Body.String(), `"classification":"discovery"`) {
				t.Fatalf("status=%d body=%s, want discovery failure", r.Code, r.Body)
			}
		case <-time.After(time.Second):
			t.Fatal("resolver request did not finish after release")
		}
	}
}

func TestHandlerCapsStuckResolvers(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	started := make(chan struct{}, maxStuckResolvers)
	release := make(chan struct{})
	results := make(chan error, maxStuckResolvers)
	var events []ResolutionEvent
	h := Handler{
		Timeout:      time.Second,
		ResolveRetry: 1,
		Resolve: func(context.Context, string, string) (ResolvedTarget, error) {
			started <- struct{}{}
			<-release
			return ResolvedTarget{}, ErrDiscoveryUnavailable
		},
		OnResolution: func(event ResolutionEvent) { events = append(events, event) },
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

	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/plex/account/capacity-overflow", nil))
	if r.Code != http.StatusServiceUnavailable || !contains(r.Body.String(), `"classification":"discovery"`) {
		t.Fatalf("capacity status=%d body=%s, want safe discovery 503", r.Code, r.Body)
	}
	if len(events) != 0 {
		t.Fatalf("capacity events=%+v, want no retry", events)
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
