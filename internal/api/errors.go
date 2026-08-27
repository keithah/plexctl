package api

import (
	"fmt"
	"net/http"
	"strings"
)

type HTTPError struct {
	StatusCode int
	Method     string
	Path       string
	Detail     string
}

func (e *HTTPError) Error() string {
	detail := strings.TrimSpace(e.Detail)
	if detail == "" {
		detail = http.StatusText(e.StatusCode)
	}
	return fmt.Sprintf("plex API %s %s: HTTP %d: %s", e.Method, e.Path, e.StatusCode, detail)
}
