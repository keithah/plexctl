package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	BaseURL     *url.URL
	Token       string
	HTTP        *http.Client
	ClientID    string
	APIVersion  string
	InsecureTLS bool
}

func New(base, token string, hc *http.Client) (*Client, error) {
	u, e := url.Parse(strings.TrimRight(base, "/"))
	if e != nil {
		return nil, e
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("server URL must use http or https")
	}
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{BaseURL: u, Token: token, HTTP: hc, ClientID: "plexctl", APIVersion: "1"}, nil
}
func (c *Client) Do(ctx context.Context, method, path string, query url.Values, body io.Reader, out any) error {
	if path == "" || !strings.HasPrefix(path, "/") {
		return fmt.Errorf("API path must start with /")
	}
	u := *c.BaseURL
	u.Path = strings.TrimRight(c.BaseURL.Path, "/") + path
	u.RawQuery = query.Encode()
	req, e := http.NewRequestWithContext(ctx, method, u.String(), body)
	if e != nil {
		return e
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Plex-Client-Identifier", c.ClientID)
	req.Header.Set("X-Plex-Pms-Api-Version", c.APIVersion)
	if c.Token != "" {
		req.Header.Set("X-Plex-Token", c.Token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, e := c.HTTP.Do(req)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &HTTPError{resp.StatusCode, method, path, safeDetail(data, c.Token)}
	}
	if out == nil || len(data) == 0 {
		return nil
	}
	if e = json.Unmarshal(data, out); e != nil {
		return fmt.Errorf("decode %s %s: %w", method, path, e)
	}
	return nil
}
func safeDetail(b []byte, token string) string {
	s := string(b)
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	s = strings.ReplaceAll(s, "X-Plex-Token", "[redacted]")
	if token != "" {
		s = strings.ReplaceAll(s, token, "[redacted]")
	}
	return s
}
