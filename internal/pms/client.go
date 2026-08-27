package pms

import (
	"context"
	"github.com/keithah/plexctl/internal/api"
	"net/url"
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
func (c *Client) Metadata(ctx context.Context, key string) (MetadataContainer, error) {
	var v MetadataContainer
	e := c.API.Do(ctx, "GET", "/library/metadata/"+url.PathEscape(key), nil, nil, &v)
	return v, e
}
func (c *Client) Sessions(ctx context.Context) (MetadataContainer, error) {
	var v MetadataContainer
	e := c.API.Do(ctx, "GET", "/status/sessions", nil, nil, &v)
	return v, e
}
