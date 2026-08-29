package health

import (
	"context"
	"github.com/keithah/plexctl/internal/pms"
	"time"
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

func Ping(ctx context.Context, c *pms.Client) Result {
	start := time.Now()
	_, e := c.Identity(ctx)
	r := Result{OK: e == nil, Classification: OK, Stage: "identity", Duration: time.Since(start)}
	if e != nil {
		r.Classification = IdentityFailure
		r.Detail = e.Error()
	}
	if ctx.Err() != nil {
		r.OK = false
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
	if _, e := c.Sections(ctx); e != nil {
		r := Result{OK: false, Classification: LibraryFailure, Stage: "library", Detail: e.Error(), Duration: time.Since(start)}
		if ctx.Err() != nil {
			r.Classification = Timeout
			r.Detail = ctx.Err().Error()
		}
		return r
	}
	return Result{OK: true, Classification: OK, Stage: "library", Detail: "identity and library access verified", Duration: time.Since(start)}
}
