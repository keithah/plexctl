package monitor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/keithah/plexctl/internal/api"
	"github.com/keithah/plexctl/internal/pms"
)

func TestHandlerRetriesOneDiscoveryFailureAndReportsSafeEvent(t *testing.T) {
	client := healthyRetryClient(t)
	secret := "https://private.example/?token=secret"
	attempts := 0
	var events []ResolutionEvent
	retryDelay := 25 * time.Millisecond
	h := Handler{
		Timeout:      time.Second,
		ResolveRetry: 1,
		RetryDelay:   retryDelay,
		OnResolution: func(event ResolutionEvent) {
			events = append(events, event)
		},
		Resolve: func(context.Context, string, string) (ResolvedTarget, error) {
			attempts++
			if attempts == 1 {
				return ResolvedTarget{CorrelationKey: "private-target"}, fmt.Errorf("%w: %s", ErrDiscoveryUnavailable, secret)
			}
			return ResolvedTarget{Client: client, CorrelationKey: "private-target"}, nil
		},
	}
	r := httptest.NewRecorder()
	startedAt := time.Now()
	h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/plex/account/server", nil))
	if r.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s, want recovered healthy response", r.Code, r.Body)
	}
	if attempts != 2 {
		t.Fatalf("resolve attempts=%d, want exactly two", attempts)
	}
	if elapsed := time.Since(startedAt); elapsed < retryDelay {
		t.Fatalf("retry elapsed=%s, want at least configured delay %s", elapsed, retryDelay)
	}
	if len(events) != 1 || events[0].Outcome != "retry" || events[0].Attempt != 1 {
		t.Fatalf("resolution events=%+v, want one retry event", events)
	}
	assertSafeResolutionEvents(t, events, secret, "private-target")
}

func TestHandlerStopsAfterOneDiscoveryRetry(t *testing.T) {
	secret := "https://private.example/?token=secret"
	attempts := 0
	var events []ResolutionEvent
	h := Handler{
		ResolveRetry: 1,
		OnResolution: func(event ResolutionEvent) {
			events = append(events, event)
		},
		Resolve: func(context.Context, string, string) (ResolvedTarget, error) {
			attempts++
			return ResolvedTarget{CorrelationKey: "private-target"}, fmt.Errorf("%w: %s", ErrDiscoveryUnavailable, secret)
		},
	}
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/plex/account/server", nil))
	if r.Code != http.StatusServiceUnavailable || !contains(r.Body.String(), `"classification":"discovery"`) {
		t.Fatalf("status=%d body=%s, want bounded discovery failure", r.Code, r.Body)
	}
	if attempts != 2 {
		t.Fatalf("resolve attempts=%d, want exactly one retry", attempts)
	}
	if len(events) != 2 || events[0].Outcome != "retry" || events[0].Attempt != 1 || events[1].Outcome != "failed" || events[1].Attempt != 2 {
		t.Fatalf("resolution events=%+v, want retry then bounded failure", events)
	}
	assertSafeResolutionEvents(t, events, secret, "private-target")
}

func TestHandlerCapsPositiveRetryConfigurationAtOne(t *testing.T) {
	attempts := 0
	h := Handler{
		ResolveRetry: 2,
		Resolve: func(context.Context, string, string) (ResolvedTarget, error) {
			attempts++
			return ResolvedTarget{}, ErrDiscoveryUnavailable
		},
	}
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/plex/account/server", nil))
	if r.Code != http.StatusServiceUnavailable || !contains(r.Body.String(), `"classification":"discovery"`) {
		t.Fatalf("status=%d body=%s, want bounded discovery failure", r.Code, r.Body)
	}
	if attempts != 2 {
		t.Fatalf("resolve attempts=%d, want initial attempt plus one retry", attempts)
	}
}

func TestHandlerDoesNotRetryConfigurationFailure(t *testing.T) {
	secret := "https://private.example/?token=secret"
	attempts := 0
	var events []ResolutionEvent
	h := Handler{
		ResolveRetry: 1,
		RetryDelay:   time.Second,
		OnResolution: func(event ResolutionEvent) {
			events = append(events, event)
		},
		Resolve: func(context.Context, string, string) (ResolvedTarget, error) {
			attempts++
			return ResolvedTarget{}, errors.New(secret)
		},
	}
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/plex/account/server", nil))
	if r.Code != http.StatusNotFound || contains(r.Body.String(), secret) {
		t.Fatalf("status=%d body=%s, want private configuration failure", r.Code, r.Body)
	}
	if attempts != 1 {
		t.Fatalf("resolve attempts=%d, want no configuration retry", attempts)
	}
	if len(events) != 0 {
		t.Fatalf("resolution events=%+v, want none for configuration failure", events)
	}
}

func TestHandlerDoesNotRetryTerminalDiscoveryFailure(t *testing.T) {
	for _, terminal := range []error{context.Canceled, context.DeadlineExceeded} {
		t.Run(terminal.Error(), func(t *testing.T) {
			attempts := 0
			var events []ResolutionEvent
			h := Handler{
				ResolveRetry: 1,
				OnResolution: func(event ResolutionEvent) {
					events = append(events, event)
				},
				Resolve: func(context.Context, string, string) (ResolvedTarget, error) {
					attempts++
					return ResolvedTarget{CorrelationKey: "private-target"}, errors.Join(ErrDiscoveryUnavailable, terminal)
				},
			}
			r := httptest.NewRecorder()
			h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/plex/account/server", nil))
			if r.Code != http.StatusServiceUnavailable || !contains(r.Body.String(), `"classification":"discovery"`) {
				t.Fatalf("status=%d body=%s, want terminal discovery failure", r.Code, r.Body)
			}
			if attempts != 1 {
				t.Fatalf("resolve attempts=%d, want no terminal-error retry", attempts)
			}
			if len(events) != 0 {
				t.Fatalf("resolution events=%+v, want none for terminal failure", events)
			}
		})
	}
}

func TestHandlerDoesNotRetryDiscoveryFailureAfterDeadline(t *testing.T) {
	attempts := 0
	finished := make(chan struct{})
	var events []ResolutionEvent
	h := Handler{
		Timeout:      20 * time.Millisecond,
		ResolveRetry: 1,
		RetryDelay:   time.Second,
		OnResolution: func(event ResolutionEvent) {
			events = append(events, event)
		},
		Resolve: func(ctx context.Context, _ string, _ string) (ResolvedTarget, error) {
			attempts++
			<-ctx.Done()
			close(finished)
			return ResolvedTarget{CorrelationKey: "private-target"}, ErrDiscoveryUnavailable
		},
	}
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/plex/account/server", nil))
	if r.Code != http.StatusServiceUnavailable || !contains(r.Body.String(), `"classification":"discovery"`) {
		t.Fatalf("status=%d body=%s, want deadline-bounded discovery failure", r.Code, r.Body)
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("resolver worker did not finish after its deadline")
	}
	if attempts != 1 {
		t.Fatalf("resolve attempts=%d, want no retry after deadline", attempts)
	}
	if len(events) != 0 {
		t.Fatalf("resolution events=%+v, want none after deadline", events)
	}
}

func TestHandlerDoesNotStartRetryAfterTimeoutDuringRetryDelay(t *testing.T) {
	attempts := 0
	var events []ResolutionEvent
	h := Handler{
		Timeout:      20 * time.Millisecond,
		ResolveRetry: 1,
		RetryDelay:   time.Second,
		OnResolution: func(event ResolutionEvent) {
			events = append(events, event)
		},
		Resolve: func(context.Context, string, string) (ResolvedTarget, error) {
			attempts++
			return ResolvedTarget{CorrelationKey: "private-target"}, ErrDiscoveryUnavailable
		},
	}
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/plex/account/server", nil))
	if r.Code != http.StatusServiceUnavailable || !contains(r.Body.String(), `"classification":"discovery"`) {
		t.Fatalf("status=%d body=%s, want deadline-bounded discovery failure", r.Code, r.Body)
	}
	if attempts != 1 {
		t.Fatalf("resolve attempts=%d, want no retry after timeout", attempts)
	}
	if len(events) != 0 {
		t.Fatalf("resolution events=%+v, want no started retry after timeout", events)
	}
}

func healthyRetryClient(t *testing.T) *pms.Client {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/identity":
			_, _ = w.Write([]byte(`{"MediaContainer":{"machineIdentifier":"machine"}}`))
		case "/library/sections/all":
			_, _ = w.Write([]byte(`{"MediaContainer":{"Directory":[{"key":"1"}]}}`))
		case "/library/sections/1/all":
			_, _ = w.Write([]byte(`{"MediaContainer":{"Metadata":[{"key":"11"}]}}`))
		case "/library/metadata/11":
			_, _ = w.Write([]byte(`{"MediaContainer":{"Metadata":[{"Media":[{"Part":[{"key":"/library/parts/1/file.mkv"}]}]}]}}`))
		case "/library/parts/1/file.mkv":
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(make([]byte, 1024))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.Close)
	a, err := api.New(upstream.URL, "tok", nil)
	if err != nil {
		t.Fatal(err)
	}
	return pms.New(a)
}

func assertSafeResolutionEvents(t *testing.T, events []ResolutionEvent, forbidden ...string) {
	t.Helper()
	for _, event := range events {
		for _, value := range forbidden {
			if contains(event.String(), value) {
				t.Fatalf("resolution event leaked private resolver detail: %q", event.String())
			}
		}
	}
}
