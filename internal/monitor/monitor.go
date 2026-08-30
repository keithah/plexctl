package monitor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/keithah/plexctl/internal/health"
	"github.com/keithah/plexctl/internal/pms"
)

// Resolver returns the PMS client for an account/server monitor target.
type Resolver func(account, server string) (*pms.Client, error)

// Handler exposes the stable HTTP contract used by external monitors.
type Handler struct {
	Resolve Resolver
	Timeout time.Duration
}

// ServeHTTP handles GET /plex/{account}/{server}.
func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 3 || parts[0] != "plex" || parts[1] == "" || parts[2] == "" {
		writeError(w, http.StatusNotFound, "not_found", "monitor target not found")
		return
	}
	account, err := url.PathUnescape(parts[1])
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid account path")
		return
	}
	server, err := url.PathUnescape(parts[2])
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid server path")
		return
	}
	if h.Resolve == nil {
		writeError(w, http.StatusInternalServerError, "configuration", "monitor resolver is not configured")
		return
	}
	client, err := h.Resolve(account, server)
	if err != nil {
		writeError(w, http.StatusNotFound, "configuration", err.Error())
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
		status = http.StatusServiceUnavailable
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(response{
		OK: result.OK, Account: account, Server: server,
		Classification: result.Classification, Stage: result.Stage,
		Detail: result.Detail, DurationMS: result.Duration.Milliseconds(),
	})
}

type response struct {
	OK             bool                  `json:"ok"`
	Account        string                `json:"account"`
	Server         string                `json:"server"`
	Classification health.Classification `json:"classification"`
	Stage          string                `json:"stage"`
	Detail         string                `json:"detail,omitempty"`
	DurationMS     int64                 `json:"duration_ms"`
}

type errorResponse struct {
	OK             bool   `json:"ok"`
	Classification string `json:"classification"`
	Detail         string `json:"detail"`
}

func writeError(w http.ResponseWriter, status int, classification, detail string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorResponse{Classification: classification, Detail: detail})
}
