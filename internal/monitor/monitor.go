package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/keithah/plexctl/internal/health"
	"github.com/keithah/plexctl/internal/pms"
)

// ResolvedTarget is the monitor resolver's internal result. CorrelationKey is
// a stable configured-target key used only for aggregate correlation; it is
// never sent to a monitor client or emitted in logs. An empty key disables
// correlation rather than falling back to the raw request selector.
type ResolvedTarget struct {
	Client         *pms.Client
	CorrelationKey string
}

// Resolver returns the PMS client and canonical internal target identity for an
// account/server monitor selector under the monitor request context. Aliases for
// one configured profile must use the same CorrelationKey.
type Resolver func(ctx context.Context, account, server string) (ResolvedTarget, error)

// ErrDiscoveryUnavailable marks an otherwise valid monitor target whose Plex
// discovery data or advertised endpoints are temporarily unavailable.
var ErrDiscoveryUnavailable = errors.New("plex discovery unavailable")

// ResolutionEvent describes a retryable discovery failure without carrying a
// requested target, endpoint, credential, or upstream error detail.
type ResolutionEvent struct {
	Outcome string
	Attempt int
}

func (e ResolutionEvent) String() string {
	switch e.Outcome {
	case "retry":
		return fmt.Sprintf("plex monitor discovery retry attempt=%d", e.Attempt)
	case "failed":
		return fmt.Sprintf("plex monitor discovery failed after attempt=%d", e.Attempt)
	default:
		return "plex monitor discovery event"
	}
}

// Handler exposes the stable HTTP contract used by external monitors. It owns
// shared resolver state and must be used through a pointer; do not copy it after
// first use.
type Handler struct {
	Resolve Resolver
	Timeout time.Duration
	// ResolveRetry enables one retry for a classified discovery failure. Values
	// above one never increase the fixed retry limit.
	ResolveRetry int
	// RetryDelay is waited under the request context before the one retry starts.
	RetryDelay    time.Duration
	Correlation   *CorrelationTracker
	OnCorrelation func(CorrelationEvent)
	OnResolution  func(ResolutionEvent)

	stateMu sync.Mutex
	state   *resolveState
}

type resolveResult struct {
	target ResolvedTarget
	err    error
}

type resolveCall struct {
	done   chan struct{}
	cancel context.CancelFunc
	result resolveResult
}

type resolveKey struct {
	account string
	server  string
}

type resolveState struct {
	mu      sync.Mutex
	calls   map[resolveKey]*resolveCall
	workers chan struct{}
}

const maxStuckResolvers = 16

var errResolverCapacity = errors.New("monitor resolver capacity exhausted")

func (h *Handler) reportFailure(target ResolvedTarget) {
	if event := h.Correlation.ObserveFailure(target.CorrelationKey); event != nil && h.OnCorrelation != nil {
		h.OnCorrelation(*event)
	}
}

func (h *Handler) reportResolution(event ResolutionEvent) {
	if h.OnResolution != nil {
		h.OnResolution(event)
	}
}

func (h *Handler) resolve(ctx context.Context, account, server string) (ResolvedTarget, error) {
	if err := contextTerminalError(ctx); err != nil {
		return ResolvedTarget{}, err
	}

	state := h.resolutionState()
	key := resolveKey{account: account, server: server}
	state.mu.Lock()
	call := state.calls[key]
	if call == nil {
		select {
		case state.workers <- struct{}{}:
			resolveCtx, cancel := h.resolutionContext(ctx)
			call = &resolveCall{done: make(chan struct{}), cancel: cancel}
			state.calls[key] = call
			go h.runResolution(state, key, call, resolveCtx, account, server)
		default:
			state.mu.Unlock()
			return ResolvedTarget{}, fmt.Errorf("%w: %w", ErrDiscoveryUnavailable, errResolverCapacity)
		}
	}
	state.mu.Unlock()

	select {
	case <-call.done:
		return call.result.target, call.result.err
	case <-ctx.Done():
		return ResolvedTarget{}, ctx.Err()
	}
}

func (h *Handler) resolutionState() *resolveState {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	if h.state == nil {
		h.state = &resolveState{
			calls:   make(map[resolveKey]*resolveCall),
			workers: make(chan struct{}, maxStuckResolvers),
		}
	}
	return h.state
}

// resolutionContext preserves the handler's absolute deadline while deliberately
// detaching a single caller's cancellation from shared resolver work.
func (h *Handler) resolutionContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if deadline, ok := ctx.Deadline(); ok {
		return context.WithDeadline(context.Background(), deadline)
	}
	return context.WithCancel(context.Background())
}

func contextTerminalError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
		return context.DeadlineExceeded
	}
	return nil
}

func (h *Handler) runResolution(state *resolveState, key resolveKey, call *resolveCall, ctx context.Context, account, server string) {
	target, err := h.Resolve(ctx, account, server)
	call.cancel()

	state.mu.Lock()
	call.result = resolveResult{target: target, err: err}
	delete(state.calls, key)
	close(call.done)
	<-state.workers
	state.mu.Unlock()
}

func (h *Handler) retryResolution(ctx context.Context, account, server string) (ResolvedTarget, error) {
	target, err := h.resolve(ctx, account, server)
	if !errors.Is(err, ErrDiscoveryUnavailable) ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) ||
		contextTerminalError(ctx) != nil ||
		h.ResolveRetry <= 0 {
		return target, err
	}
	if err := waitForResolutionRetry(ctx, h.RetryDelay); err != nil {
		return target, err
	}

	h.reportResolution(ResolutionEvent{Outcome: "retry", Attempt: 1})
	retryTarget, retryErr := h.resolve(ctx, account, server)
	if retryTarget.CorrelationKey == "" {
		retryTarget.CorrelationKey = target.CorrelationKey
	}
	if retryErr != nil && (errors.Is(retryErr, ErrDiscoveryUnavailable) || errors.Is(retryErr, context.DeadlineExceeded) || errors.Is(retryErr, context.Canceled)) {
		h.reportResolution(ResolutionEvent{Outcome: "failed", Attempt: 2})
	}
	return retryTarget, retryErr
}

func waitForResolutionRetry(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return contextTerminalError(ctx)
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return contextTerminalError(ctx)
	case <-timer.C:
		return contextTerminalError(ctx)
	}
}

// ServeHTTP handles GET /plex/{account}/{server}.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "request")
		return
	}
	// Trim allows a single trailing slash, matching Uptime Kuma's URL normalization.
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 3 || parts[0] != "plex" || parts[1] == "" || parts[2] == "" {
		writeError(w, http.StatusNotFound, "not_found", "request")
		return
	}
	account, server := parts[1], parts[2]
	if h.Resolve == nil {
		writeError(w, http.StatusInternalServerError, "configuration", "configuration")
		return
	}
	ctx := r.Context()
	cancel := func() {}
	if h.Timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, h.Timeout)
	}
	defer cancel()
	target, err := h.retryResolution(ctx, account, server)
	// A resolver can return a target concurrently with expiry. The target was
	// not available within the monitor budget, so preserve the resolution
	// failure classification rather than running health checks with a canceled
	// context and reporting an unrelated PMS timeout.
	if contextTerminalError(ctx) != nil {
		h.reportFailure(target)
		writeError(w, http.StatusServiceUnavailable, "discovery", "discovery")
		return
	}
	if err != nil {
		if errors.Is(err, ErrDiscoveryUnavailable) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			h.reportFailure(target)
			writeError(w, http.StatusServiceUnavailable, "discovery", "discovery")
			return
		}
		writeError(w, http.StatusNotFound, "configuration", "configuration")
		return
	}
	client := target.Client
	if client == nil {
		writeError(w, http.StatusInternalServerError, "configuration", "configuration")
		return
	}
	result := health.Check(ctx, client)
	status := http.StatusOK
	if !result.OK {
		h.reportFailure(target)
		status = http.StatusServiceUnavailable
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(response{
		OK: result.OK, Classification: result.Classification,
		Stage: result.Stage, DurationMS: result.Duration.Milliseconds(),
	})
}

type response struct {
	OK             bool                  `json:"ok"`
	Classification health.Classification `json:"classification"`
	Stage          string                `json:"stage"`
	DurationMS     int64                 `json:"duration_ms"`
}

type errorResponse struct {
	OK             bool   `json:"ok"`
	Classification string `json:"classification"`
	Stage          string `json:"stage"`
}

func writeError(w http.ResponseWriter, status int, classification, stage string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorResponse{OK: false, Classification: classification, Stage: stage})
}
