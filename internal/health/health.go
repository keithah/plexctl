package health

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/keithah/plexctl/internal/api"
	"github.com/keithah/plexctl/internal/pms"
)

type Classification string

const (
	OK              Classification = "ok"
	IdentityFailure Classification = "identity"
	LibraryFailure  Classification = "library"
	Timeout         Classification = "timeout"
	AuthFailure     Classification = "auth"
)

type Result struct {
	OK             bool           `json:"ok"`
	Classification Classification `json:"classification"`
	Stage          string         `json:"stage"`
	Detail         string         `json:"detail,omitempty"`
	Duration       time.Duration  `json:"duration"`
}

// isAuthFailure reports whether err is a PMS rejection of the credentials,
// which is actionable in a different way from an unreachable server.
func isAuthFailure(err error) bool {
	var he *api.HTTPError
	if !errors.As(err, &he) {
		return false
	}
	return he.StatusCode == http.StatusUnauthorized || he.StatusCode == http.StatusForbidden
}

func Ping(ctx context.Context, c *pms.Client) Result {
	start := time.Now()
	_, e := c.Identity(ctx)
	r := Result{OK: e == nil, Classification: OK, Stage: "identity", Duration: time.Since(start)}
	if e == nil {
		// The call succeeded. A context that expired afterwards must not
		// relabel a healthy server as unreachable.
		return r
	}
	r.Classification = IdentityFailure
	if isAuthFailure(e) {
		r.Classification = AuthFailure
	}
	r.Detail = e.Error()
	if ctx.Err() != nil {
		r.Classification = Timeout
		r.Detail = ctx.Err().Error()
	}
	return r
}
func Check(ctx context.Context, c *pms.Client) Result {
	start := time.Now()
	if r := Ping(ctx, c); !r.OK {
		return r
	}
	sections, e := c.Sections(ctx)
	if e != nil {
		r := Result{OK: false, Classification: LibraryFailure, Stage: "library", Detail: e.Error(), Duration: time.Since(start)}
		if isAuthFailure(e) {
			r.Classification = AuthFailure
		}
		if ctx.Err() != nil {
			r.Classification = Timeout
			r.Detail = ctx.Err().Error()
		}
		return r
	}
	if len(sections.MediaContainer.Directory) == 0 {
		return Result{OK: false, Classification: LibraryFailure, Stage: "library", Detail: "no libraries found", Duration: time.Since(start)}
	}
	for _, library := range sections.MediaContainer.Directory {
		items, itemErr := c.Items(ctx, library.Key, nil)
		if itemErr != nil {
			continue
		}
		probed := 0
		for _, item := range items.MediaContainer.Metadata {
			if probed >= 2 {
				break
			}
			if err := c.ProbeMedia(ctx, item.Key); err != nil {
				return Result{OK: false, Classification: LibraryFailure, Stage: "media", Detail: err.Error(), Duration: time.Since(start)}
			}
			probed++
		}
		if probed > 0 {
			return Result{OK: true, Classification: OK, Stage: "media", Detail: "identity, library, and media access verified", Duration: time.Since(start)}
		}
	}
	return Result{OK: false, Classification: LibraryFailure, Stage: "media", Detail: "no media items found", Duration: time.Since(start)}
}
