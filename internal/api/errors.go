package api

import (
	"fmt"
	"net/http"
)

type HTTPError struct {
	StatusCode int
	Method     string
	Path       string
	Detail     string
}

// transportError keeps network errors inspectable without allowing their URL
// text (which may contain a private PMS base URL) into user-facing output.
type transportError struct {
	Method string
	Path   string
	Err    error
}

func (e *transportError) Error() string {
	return fmt.Sprintf("plex API %s %s: transport request failed", e.Method, e.Path)
}

func (e *transportError) Unwrap() error { return e.Err }

func (e *HTTPError) Error() string {
	return fmt.Sprintf("plex API %s %s: HTTP %d: %s", e.Method, e.Path, e.StatusCode, http.StatusText(e.StatusCode))
}
