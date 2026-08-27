package plexauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
	Owned            bool         `json:"owned"`
	Connections      []Connection `json:"connections"`
}
type Connection struct {
	URI   string `json:"uri"`
	Local bool   `json:"local"`
	Relay bool   `json:"relay"`
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
		return LoginResult{}, fmt.Errorf("Plex returned an incomplete authentication PIN")
	}
	result := LoginResult{ID: pin.ID, Code: pin.Code, LinkURL: "https://app.plex.tv/auth#?clientID=" + url.QueryEscape(c.ClientID) + "&code=" + url.QueryEscape(pin.Code)}
	if c.OnPIN != nil {
		c.OnPIN(result.LinkURL)
	}
	deadline := time.Now().Add(c.Timeout)
	for {
		if pin.Token != "" {
			result.Token = pin.Token
			return result, nil
		}
		if time.Now().After(deadline) {
			return LoginResult{}, fmt.Errorf("timed out waiting for Plex authorization")
		}
		wait := c.PollInterval
		if wait <= 0 {
			wait = time.Millisecond
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
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
	var v []Resource
	err := c.getJSON(ctx, "/api/v2/resources", token, &v)
	return v, err
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
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Plex request failed: HTTP %d", resp.StatusCode)
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
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Plex authentication request failed: HTTP %d", resp.StatusCode)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode Plex authentication response: %w", err)
	}
	return nil
}
