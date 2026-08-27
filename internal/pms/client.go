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
func (c *Client) Sections(ctx context.Context) (LibrarySections, error) {
	var v LibrarySections
	e := c.API.Do(ctx, "GET", "/library/sections", nil, nil, &v)
	return v, e
}
func (c *Client) Items(ctx context.Context, key string, q url.Values) (MetadataContainer, error) {
	var v MetadataContainer
	e := c.API.Do(ctx, "GET", "/library/sections/"+url.PathEscape(key)+"/all", q, nil, &v)
	return v, e
}
func (c *Client) Search(ctx context.Context, key, term string) (MetadataContainer, error) {
	return c.Items(ctx, key, url.Values{"title": []string{term}})
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
func (c *Client) History(ctx context.Context, q url.Values) (MetadataContainer, error) {
	var v MetadataContainer
	e := c.API.Do(ctx, "GET", "/status/sessions/history", q, nil, &v)
	return v, e
}
