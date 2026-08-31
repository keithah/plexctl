package pms

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

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
func (c *Client) Playlists(ctx context.Context) (PlaylistContainer, error) {
	var v PlaylistContainer
	e := c.API.Do(ctx, "GET", "/playlists", nil, nil, &v)
	return v, e
}
func (c *Client) Playlist(ctx context.Context, id string) (PlaylistContainer, error) {
	var v PlaylistContainer
	e := c.API.Do(ctx, "GET", "/playlists/"+url.PathEscape(id), nil, nil, &v)
	return v, e
}
func (c *Client) PlaylistItems(ctx context.Context, id string) (MetadataContainer, error) {
	var v MetadataContainer
	e := c.API.Do(ctx, "GET", "/playlists/"+url.PathEscape(id)+"/items", nil, nil, &v)
	return v, e
}
func (c *Client) Collections(ctx context.Context, sectionID string) (MetadataContainer, error) {
	var v MetadataContainer
	e := c.API.Do(ctx, "GET", "/library/sections/"+url.PathEscape(sectionID)+"/collections", nil, nil, &v)
	return v, e
}
func (c *Client) CollectionItems(ctx context.Context, collectionID string) (MetadataContainer, error) {
	var v MetadataContainer
	e := c.API.Do(ctx, "GET", "/library/collections/"+url.PathEscape(collectionID)+"/items", nil, nil, &v)
	return v, e
}
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
func metadataPath(key string) string {
	if strings.HasPrefix(key, "/") {
		return key
	}
	return "/library/metadata/" + url.PathEscape(key)
}

func (c *Client) Metadata(ctx context.Context, key string) (MetadataContainer, error) {
	var v MetadataContainer
	e := c.API.Do(ctx, "GET", metadataPath(key), nil, nil, &v)
	return v, e
}

func (c *Client) ProbeMedia(ctx context.Context, itemKey string) error {
	metadata, err := c.Metadata(ctx, itemKey)
	if err != nil {
		return err
	}
	if len(metadata.MediaContainer.Metadata) == 0 {
		return fmt.Errorf("media metadata is empty")
	}
	for _, media := range metadata.MediaContainer.Metadata[0].Media {
		if len(media.Part) == 0 || media.Part[0].Key == "" {
			continue
		}
		body, err := c.API.DoRawHeaders(ctx, "GET", media.Part[0].Key, nil, nil, http.Header{"Range": []string{"bytes=0-1048575"}})
		if err == nil && len(body) > 0 {
			return nil
		}
	}
	return fmt.Errorf("no playable media part returned bytes")
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
func (c *Client) DownloadQueue(ctx context.Context, id string) (DownloadQueueContainer, error) {
	var v DownloadQueueContainer
	e := c.API.Do(ctx, "GET", "/downloadQueue/"+url.PathEscape(id), nil, nil, &v)
	return v, e
}
func (c *Client) DownloadQueueItems(ctx context.Context, id string) (DownloadQueueContainer, error) {
	var v DownloadQueueContainer
	e := c.API.Do(ctx, "GET", "/downloadQueue/"+url.PathEscape(id)+"/items", nil, nil, &v)
	return v, e
}
func (c *Client) DownloadQueueItem(ctx context.Context, queueID, itemID string) (DownloadQueueContainer, error) {
	var v DownloadQueueContainer
	e := c.API.Do(ctx, "GET", "/downloadQueue/"+url.PathEscape(queueID)+"/items/"+url.PathEscape(itemID), nil, nil, &v)
	return v, e
}
func (c *Client) DownloadQueueDecision(ctx context.Context, queueID, itemID string) (TranscodeContainer, error) {
	var v TranscodeContainer
	e := c.API.Do(ctx, "GET", "/downloadQueue/"+url.PathEscape(queueID)+"/item/"+url.PathEscape(itemID)+"/decision", nil, nil, &v)
	return v, e
}
func (c *Client) TranscodeDecision(ctx context.Context, transcodeType, sessionID string, q url.Values) (TranscodeContainer, error) {
	var v TranscodeContainer
	q = cloneValues(q)
	q.Set("transcodeSessionId", sessionID)
	e := c.API.Do(ctx, "GET", "/"+url.PathEscape(transcodeType)+"/:/transcode/universal/decision", q, nil, &v)
	return v, e
}

// TranscodeSubtitles returns the raw subtitle payload. Plex serves WebVTT here
// rather than JSON, so the body must not be JSON-decoded.
func (c *Client) TranscodeSubtitles(ctx context.Context, transcodeType, sessionID string, q url.Values) (string, error) {
	q = cloneValues(q)
	q.Set("transcodeSessionId", sessionID)
	data, err := c.API.DoRaw(ctx, "GET", "/"+url.PathEscape(transcodeType)+"/:/transcode/universal/subtitles", q, nil)
	return string(data), err
}
func cloneValues(in url.Values) url.Values {
	out := url.Values{}
	for k, vs := range in {
		out[k] = append([]string(nil), vs...)
	}
	return out
}
