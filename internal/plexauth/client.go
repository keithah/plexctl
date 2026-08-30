package plexauth

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	BaseURL      string
	ClientID     string
	Product      string
	HTTP         *http.Client
	PollInterval time.Duration
	Timeout      time.Duration
	OnPIN        func(string)
	// OnWarning, when set, receives non-fatal discovery problems that would
	// otherwise be discarded, such as a legacy endpoint that failed before the
	// JSON fallback succeeded.
	OnWarning func(string)
}

type LoginResult struct {
	ID      int    `json:"id"`
	Code    string `json:"code"`
	Token   string `json:"authToken"`
	LinkURL string `json:"-"`
}
type pinResponse struct {
	ID    int    `json:"id"`
	Code  string `json:"code"`
	Token string `json:"authToken"`
}
type User struct {
	ID       int    `json:"id"`
	Username string `json:"username"`
	Email    string `json:"email"`
}
type Resource struct {
	Name             string       `json:"name"`
	ClientIdentifier string       `json:"clientIdentifier"`
	AccessToken      string       `json:"accessToken"`
	Provides         string       `json:"provides"`
	Owned            bool         `json:"owned"`
	Connections      []Connection `json:"connections"`
}
type Connection struct {
	URI      string `json:"uri"`
	Protocol string `json:"protocol"`
	Local    bool   `json:"local"`
	Relay    bool   `json:"relay"`
}

type legacyResources struct {
	XMLName xml.Name       `xml:"MediaContainer"`
	Devices []legacyDevice `xml:"Device"`
}
type legacyDevice struct {
	Name             string             `xml:"name,attr"`
	ClientIdentifier string             `xml:"clientIdentifier,attr"`
	AccessToken      string             `xml:"accessToken,attr"`
	Provides         string             `xml:"provides,attr"`
	Owned            string             `xml:"owned,attr"`
	Connections      []legacyConnection `xml:"Connection"`
}
type legacyConnection struct {
	URI      string `xml:"uri,attr"`
	Protocol string `xml:"protocol,attr"`
	Local    string `xml:"local,attr"`
	Relay    string `xml:"relay,attr"`
}

func New(baseURL, clientID string, hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), ClientID: clientID, Product: "plexctl", HTTP: hc, PollInterval: 2 * time.Second, Timeout: 10 * time.Minute}
}

func (c *Client) Login(ctx context.Context) (LoginResult, error) {
	var pin pinResponse
	if err := c.request(ctx, http.MethodPost, "/api/v2/pins", url.Values{"strong": {"true"}}, &pin); err != nil {
		return LoginResult{}, err
	}
	if pin.ID == 0 || pin.Code == "" {
		return LoginResult{}, fmt.Errorf("plex returned an incomplete authentication PIN")
	}
	result := LoginResult{ID: pin.ID, Code: pin.Code, LinkURL: "https://app.plex.tv/auth#?clientID=" + url.QueryEscape(c.ClientID) + "&code=" + url.QueryEscape(pin.Code)}
	if c.OnPIN != nil {
		c.OnPIN(result.LinkURL)
	}
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	for {
		if pin.Token != "" {
			result.Token = pin.Token
			return result, nil
		}
		wait := c.PollInterval
		if wait <= 0 {
			wait = time.Millisecond
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			if ctx.Err() == context.DeadlineExceeded {
				return LoginResult{}, fmt.Errorf("timed out waiting for Plex authorization")
			}
			return LoginResult{}, ctx.Err()
		case <-t.C:
		}
		if err := c.request(ctx, http.MethodGet, "/api/v2/pins/"+strconv.Itoa(pin.ID), nil, &pin); err != nil {
			return LoginResult{}, err
		}
	}
}

func (c *Client) User(ctx context.Context, token string) (User, error) {
	var v User
	err := c.getJSON(ctx, "/api/v2/user", token, &v)
	return v, err
}
func (c *Client) Resources(ctx context.Context, token string) ([]Resource, error) {
	resources, err := c.legacyResources(ctx, token)
	if err != nil {
		// The JSON endpoint below is authoritative, so a legacy failure is not
		// fatal. Surface it instead of discarding it, otherwise a broken legacy
		// path looks identical to an account with no servers.
		c.warn(fmt.Sprintf("legacy Plex resource discovery failed, falling back to the JSON API: %v", err))
	} else if len(resources) > 0 {
		return onlyServers(resources), nil
	}
	var v []Resource
	if err := c.getJSON(ctx, "/api/v2/resources", token, &v); err != nil {
		return nil, err
	}
	return onlyServers(v), nil
}

func (c *Client) warn(msg string) {
	if c.OnWarning != nil {
		c.OnWarning(msg)
	}
}

// onlyServers keeps resources that advertise the "server" capability. A Plex
// account also returns players, controllers, and other devices, which have no
// PMS API and would otherwise be probed and reported as unreachable servers.
// Resources that omit "provides" are kept, because older payloads do not
// always populate it and dropping them would hide real servers.
func onlyServers(in []Resource) []Resource {
	out := make([]Resource, 0, len(in))
	for _, r := range in {
		if r.Provides == "" || slices.Contains(strings.Split(r.Provides, ","), "server") {
			out = append(out, r)
		}
	}
	return out
}

func (c *Client) legacyResources(ctx context.Context, token string) ([]Resource, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/api/resources?includeHttps=1&includeRelay=1", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/xml")
	req.Header.Set("X-Plex-Client-Identifier", c.ClientID)
	req.Header.Set("X-Plex-Product", c.Product)
	req.Header.Set("X-Plex-Token", token)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := readLimited(resp.Body, 4<<20, "legacy Plex resources")
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("legacy Plex resources request failed: HTTP %d", resp.StatusCode)
	}
	var payload legacyResources
	if err := xml.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	if payload.XMLName.Local != "MediaContainer" {
		return nil, fmt.Errorf("unexpected legacy Plex resources root %q", payload.XMLName.Local)
	}
	resources := make([]Resource, 0, len(payload.Devices))
	for _, d := range payload.Devices {
		r := Resource{Name: d.Name, ClientIdentifier: d.ClientIdentifier, AccessToken: d.AccessToken, Provides: d.Provides, Owned: d.Owned == "1" || d.Owned == "true"}
		for _, x := range d.Connections {
			r.Connections = append(r.Connections, Connection{URI: x.URI, Protocol: x.Protocol, Local: x.Local == "1" || x.Local == "true", Relay: x.Relay == "1" || x.Relay == "true"})
		}
		resources = append(resources, r)
	}
	return resources, nil
}

// readLimited buffers at most limit bytes and reports an explicit error when
// the body is larger, so an oversized response is never silently truncated
// into a confusing decode failure.
func readLimited(r io.Reader, limit int64, what string) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read %s response: %w", what, err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s response exceeds %d byte limit", what, limit)
	}
	return data, nil
}

func (c *Client) getJSON(ctx context.Context, path, token string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Plex-Client-Identifier", c.ClientID)
	req.Header.Set("X-Plex-Product", c.Product)
	req.Header.Set("X-Plex-Token", token)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := readLimited(resp.Body, 2<<20, "Plex")
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("plex request failed: HTTP %d", resp.StatusCode)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode Plex response: %w", err)
	}
	return nil
}
func (c *Client) request(ctx context.Context, method, path string, query url.Values, out *pinResponse) error {
	u := c.BaseURL + path
	if len(query) != 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Plex-Client-Identifier", c.ClientID)
	req.Header.Set("X-Plex-Product", c.Product)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := readLimited(resp.Body, 1<<20, "Plex authentication")
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("plex authentication request failed: HTTP %d", resp.StatusCode)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode Plex authentication response: %w", err)
	}
	return nil
}
