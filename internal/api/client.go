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

// maxResponseBytes bounds how much of a PMS response is buffered in memory.
// Exceeding it is reported as an explicit error rather than silently
// truncating the body.
const maxResponseBytes int64 = 2 << 20

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
	return c.doRaw(ctx, method, path, query, body, nil)
}

// DoRawHeaders is DoRaw with additional request headers, for bounded media
// probes that need a Range request while retaining the normal Plex headers.
func (c *Client) DoRawHeaders(ctx context.Context, method, path string, query url.Values, body io.Reader, headers http.Header) ([]byte, error) {
	return c.doRaw(ctx, method, path, query, body, headers)
}

func (c *Client) doRaw(ctx context.Context, method, path string, query url.Values, body io.Reader, headers http.Header) ([]byte, error) {
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
	for key, values := range headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	resp, e := c.HTTP.Do(req)
	if e != nil {
		return nil, e
	}
	defer resp.Body.Close()
	// Read one byte past the cap so a body that exactly fills the limit can be
	// distinguished from one that was truncated. Silently truncating would
	// surface as a confusing "unexpected end of JSON input" decode error.
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read %s %s response: %w", method, path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &HTTPError{resp.StatusCode, method, path, safeDetail(data, c.Token)}
	}
	if int64(len(data)) > maxResponseBytes {
		return nil, fmt.Errorf("%s %s response exceeds %d byte limit; narrow the request with a limit or filter", method, path, maxResponseBytes)
	}
	return data, nil
}
func safeDetail(b []byte, token string) string {
	// Redact before truncating. Truncating first can split the token across the
	// boundary, leaving an unredacted prefix that no longer matches the needle.
	s := strings.ReplaceAll(string(b), "X-Plex-Token", "[redacted]")
	if token != "" {
		s = strings.ReplaceAll(s, token, "[redacted]")
	}
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}
