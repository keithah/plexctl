package pms

import (
	"context"
	"net/url"
	"strconv"

	"github.com/keithah/plexctl/internal/api"
)

type Client struct{ API *api.Client }

func New(API *api.Client) *Client { return &Client{API: API} }

func (c *Client) Identity(ctx context.Context) (Identity, error) {
	var v Identity
	e := c.API.Do(ctx, "GET", "/identity", nil, nil, &v)
	return v, e
}
func (c *Client) Root(ctx context.Context) (Root, error) {
	var v Root
	e := c.API.Do(ctx, "GET", "/", nil, nil, &v)
	return v, e
}
func (c *Client) Info(ctx context.Context) (Root, error) { return c.Root(ctx) }
func (c *Client) Sections(ctx context.Context) (LibrarySections, error) {
	var v LibrarySections
	e := c.API.Do(ctx, "GET", "/library/sections/all", nil, nil, &v)
	return v, e
}
func (c *Client) Items(ctx context.Context, key string, q url.Values) (MetadataContainer, error) {
	var v MetadataContainer
	e := c.API.Do(ctx, "GET", "/library/sections/"+url.PathEscape(key)+"/all", q, nil, &v)
	return v, e
}

// Search uses the documented /hubs/search operation. sectionKey is optional;
// when empty the search covers every library the token can see.
func (c *Client) Search(ctx context.Context, sectionKey, term string, limit int) (SearchContainer, error) {
	q := url.Values{"query": []string{term}}
	if sectionKey != "" {
		q.Set("sectionId", sectionKey)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	var v SearchContainer
	e := c.API.Do(ctx, "GET", "/hubs/search", q, nil, &v)
	return v, e
}

func (c *Client) RecentlyAdded(ctx context.Context, key string, limit int) (MetadataContainer, error) {
	q := url.Values{"sort": []string{"addedAt:desc"}}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	return c.Items(ctx, key, q)
}
func (c *Client) Metadata(ctx context.Context, key string) (MetadataContainer, error) {
	var v MetadataContainer
	e := c.API.Do(ctx, "GET", "/library/metadata/"+url.PathEscape(key), nil, nil, &v)
	return v, e
}

// Children is served by some PMS versions but is absent from the pinned OpenAPI contract.
func (c *Client) Children(ctx context.Context, key string) (MetadataContainer, error) {
	var v MetadataContainer
	e := c.API.Do(ctx, "GET", "/library/metadata/"+url.PathEscape(key)+"/children", nil, nil, &v)
	return v, e
}
func (c *Client) Sessions(ctx context.Context) (SessionContainer, error) {
	var v SessionContainer
	e := c.API.Do(ctx, "GET", "/status/sessions", nil, nil, &v)
	return v, e
}

// History uses the documented /status/sessions/history/all operation.
func (c *Client) History(ctx context.Context, q url.Values) (MetadataContainer, error) {
	var v MetadataContainer
	e := c.API.Do(ctx, "GET", "/status/sessions/history/all", q, nil, &v)
	return v, e
}
