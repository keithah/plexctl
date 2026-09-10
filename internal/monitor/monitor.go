package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/keithah/plexctl/internal/health"
	"github.com/keithah/plexctl/internal/pms"
)

// Resolver returns the PMS client for an account/server monitor target.
type Resolver func(account, server string) (*pms.Client, error)

// ErrDiscoveryUnavailable marks an otherwise valid monitor target whose Plex
// discovery data or advertised endpoints are temporarily unavailable.
var ErrDiscoveryUnavailable = errors.New("plex discovery unavailable")

// Handler exposes the stable HTTP contract used by external monitors.
type Handler struct {
	Resolve       Resolver
	Timeout       time.Duration
	Correlation   *CorrelationTracker
	OnCorrelation func(CorrelationEvent)
}

func (h Handler) reportFailure(account, server string) {
	if event := h.Correlation.ObserveFailure(account + "\x00" + server); event != nil && h.OnCorrelation != nil {
		h.OnCorrelation(*event)
	}
}

// ServeHTTP handles GET /plex/{account}/{server}.
func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
	client, err := h.Resolve(account, server)
	if err != nil {
		h.reportFailure(account, server)
		if errors.Is(err, ErrDiscoveryUnavailable) {
			writeError(w, http.StatusServiceUnavailable, "discovery", "discovery")
			return
		}
		writeError(w, http.StatusNotFound, "configuration", "configuration")
		return
	}
	if client == nil {
		h.reportFailure(account, server)
		writeError(w, http.StatusInternalServerError, "configuration", "configuration")
		return
	}
	ctx := r.Context()
	cancel := func() {}
	if h.Timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, h.Timeout)
	}
	defer cancel()
	result := health.Check(ctx, client)
	status := http.StatusOK
	if !result.OK {
		h.reportFailure(account, server)
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
