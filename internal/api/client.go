package api

import (
	"context"
	"crypto/tls"
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

// SetInsecureTLS enables or disables certificate verification for this client.
// Verification is on by default; callers must opt out explicitly.
func (c *Client) SetInsecureTLS(insecure bool) {
	c.InsecureTLS = insecure
	if c.HTTP == nil {
		c.HTTP = &http.Client{Timeout: 30 * time.Second}
	}
	if c.HTTP.Transport == nil {
		c.HTTP.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: insecure}}
		return
	}
	tr, ok := c.HTTP.Transport.(*http.Transport)
	if !ok {
		// Preserve custom RoundTrippers (test mocks, tracing, proxy wrappers)
		// instead of silently replacing them with DefaultTransport.
		return
	}
	if tr.TLSClientConfig == nil {
		tr.TLSClientConfig = &tls.Config{}
	} else {
		tr.TLSClientConfig = tr.TLSClientConfig.Clone()
	}
	tr.TLSClientConfig.InsecureSkipVerify = insecure
	c.HTTP.Transport = tr
}
func (c *Client) Do(ctx context.Context, method, path string, query url.Values, body io.Reader, out any) error {
	data, err := c.DoRaw(ctx, method, path, query, body)
	if err != nil {
		return err
	}
	if out == nil || len(data) == 0 {
		return nil
	}
	if err = json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode %s %s: %w", method, path, err)
	}
	return nil
}

// DoRaw performs the request and returns the undecoded response body. Use it
// for endpoints that do not return JSON (for example universal/subtitles,
// which returns WebVTT).
func (c *Client) DoRaw(ctx context.Context, method, path string, query url.Values, body io.Reader) ([]byte, error) {
	if path == "" || !strings.HasPrefix(path, "/") {
		return nil, fmt.Errorf("api path must start with /")
	}
	u := *c.BaseURL
	escapedPath := strings.TrimRight(c.BaseURL.EscapedPath(), "/") + path
	// Preserve single-encoding for already-escaped segments (e.g. p%2F1):
	// net/url requires Path to hold the decoded form and RawPath the encoded
	// form; String() then emits RawPath without re-escaping '%'.
	if unescaped, err := url.PathUnescape(escapedPath); err == nil {
		u.Path = unescaped
		u.RawPath = escapedPath
	} else {
		return nil, fmt.Errorf("invalid path escape %q", escapedPath)
	}
	u.RawQuery = query.Encode()
	req, e := http.NewRequestWithContext(ctx, method, u.String(), body)
	if e != nil {
		return nil, e
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
		return nil, e
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, fmt.Errorf("read %s %s response: %w", method, path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &HTTPError{resp.StatusCode, method, path, safeDetail(data, c.Token)}
	}
	return data, nil
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
