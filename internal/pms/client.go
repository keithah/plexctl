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

const sectionItemsPageSize = 100

// ListSectionItems returns all metadata items in a library section using
// documented container paging. It leaves Items available for legacy callers
// that need to control the request query themselves.
func (c *Client) ListSectionItems(ctx context.Context, key string) (MetadataContainer, error) {
	var items MetadataContainer
	var totalSize = -1
	for start := 0; ; {
		q := url.Values{}
		q.Set("X-Plex-Container-Start", strconv.Itoa(start))
		q.Set("X-Plex-Container-Size", strconv.Itoa(sectionItemsPageSize))
		page, err := c.Items(ctx, key, q)
		if err != nil {
			return MetadataContainer{}, fmt.Errorf("list section %s at offset %d: %w", key, start, err)
		}

		container := page.MediaContainer
		decodedSize := len(container.Metadata)
		if container.Size != decodedSize {
			return MetadataContainer{}, fmt.Errorf("list section %s at offset %d: declared size %d but decoded %d metadata items", key, start, container.Size, decodedSize)
		}
		if container.Offset != start {
			return MetadataContainer{}, fmt.Errorf("list section %s: unexpected offset %d, want %d", key, container.Offset, start)
		}
		if !container.totalSizeSet {
			return MetadataContainer{}, fmt.Errorf("list section %s at offset %d: missing total size", key, start)
		}
		if container.TotalSize < start+container.Size {
			return MetadataContainer{}, fmt.Errorf("list section %s at offset %d: invalid total size %d for page size %d", key, start, container.TotalSize, container.Size)
		}
		if totalSize == -1 {
			totalSize = container.TotalSize
		} else if container.TotalSize != totalSize {
			return MetadataContainer{}, fmt.Errorf("list section %s: total size changed from %d to %d", key, totalSize, container.TotalSize)
		}
		if container.Size == 0 {
			if start == container.TotalSize {
				return items, nil
			}
			return MetadataContainer{}, fmt.Errorf("list section %s: no progress at offset %d", key, start)
		}

		items.MediaContainer.Metadata = append(items.MediaContainer.Metadata, container.Metadata...)
		items.MediaContainer.Size = len(items.MediaContainer.Metadata)
		items.MediaContainer.TotalSize = container.TotalSize
		start += container.Size
		if start == container.TotalSize {
			return items, nil
		}
		if start > container.TotalSize {
			return MetadataContainer{}, fmt.Errorf("list section %s: offset %d exceeds total size %d", key, start, container.TotalSize)
		}
	}
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
	return c.probeMedia(ctx, itemKey, 0, map[string]struct{}{})
}

const maxProbeDepth = 8

func (c *Client) probeMedia(ctx context.Context, itemKey string, depth int, visited map[string]struct{}) error {
	if depth > maxProbeDepth {
		return fmt.Errorf("media metadata nesting exceeds depth limit %d", maxProbeDepth)
	}
	if _, ok := visited[itemKey]; ok {
		return fmt.Errorf("cyclic media metadata at %s", itemKey)
	}
	visited[itemKey] = struct{}{}
	metadata, err := c.Metadata(ctx, itemKey)
	if err != nil {
		return err
	}
	if c.hasMediaBytes(ctx, metadata) {
		return nil
	}
	if len(metadata.MediaContainer.Metadata) > 0 {
		switch metadata.MediaContainer.Metadata[0].Type {
		case "show", "season", "artist", "album":
			children, childErr := c.Children(ctx, itemKey)
			if childErr == nil {
				for _, child := range children.MediaContainer.Metadata {
					if child.Key == "" {
						continue
					}
					if child.Type == "show" || child.Type == "season" || child.Type == "episode" || child.Type == "artist" || child.Type == "album" || child.Type == "track" {
						if c.probeMedia(ctx, child.Key, depth+1, visited) == nil {
							return nil
						}
					}
				}
			}
		}
	}
	return fmt.Errorf("no playable media part returned bytes for %s (metadata=%d)", itemKey, len(metadata.MediaContainer.Metadata))
}

func (c *Client) hasMediaBytes(ctx context.Context, metadata MetadataContainer) bool {
	if len(metadata.MediaContainer.Metadata) == 0 {
		return false
	}
	for _, media := range metadata.MediaContainer.Metadata[0].Media {
		if len(media.Part) == 0 || media.Part[0].Key == "" {
			continue
		}
		body, err := c.API.DoRawHeadersLimited(ctx, "GET", media.Part[0].Key, url.Values{"download": []string{"1"}}, nil, http.Header{"Range": []string{"bytes=0-1024"}}, 1024)
		if err == nil && len(body) > 0 {
			return true
		}
	}
	return false
}

// Children is served by some PMS versions but is absent from the pinned OpenAPI contract.
func (c *Client) Children(ctx context.Context, key string) (MetadataContainer, error) {
	var v MetadataContainer
	e := c.API.Do(ctx, "GET", metadataPath(key)+"/children", nil, nil, &v)
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

const historyPageSize = 100

// HistoryAll returns complete playback history for reporting. It uses documented
// container paging and refuses responses whose metadata cannot prove completeness.
func (c *Client) HistoryAll(ctx context.Context, q url.Values) (MetadataContainer, error) {
	var history MetadataContainer
	var totalSize = -1
	for start := 0; ; {
		pageQuery := cloneValues(q)
		pageQuery.Set("X-Plex-Container-Start", strconv.Itoa(start))
		pageQuery.Set("X-Plex-Container-Size", strconv.Itoa(historyPageSize))
		page, err := c.History(ctx, pageQuery)
		if err != nil {
			return MetadataContainer{}, fmt.Errorf("list playback history at offset %d: %w", start, err)
		}

		container := page.MediaContainer
		decodedSize := len(container.Metadata)
		if container.Size != decodedSize {
			return MetadataContainer{}, fmt.Errorf("list playback history at offset %d: declared size %d but decoded %d metadata items", start, container.Size, decodedSize)
		}
		if container.Offset != start {
			return MetadataContainer{}, fmt.Errorf("list playback history: unexpected offset %d, want %d", container.Offset, start)
		}
		if !container.totalSizeSet {
			return MetadataContainer{}, fmt.Errorf("list playback history at offset %d: missing total size", start)
		}
		if container.TotalSize < start+container.Size {
			return MetadataContainer{}, fmt.Errorf("list playback history at offset %d: invalid total size %d for page size %d", start, container.TotalSize, container.Size)
		}
		if totalSize == -1 {
			totalSize = container.TotalSize
		} else if container.TotalSize != totalSize {
			return MetadataContainer{}, fmt.Errorf("list playback history: total size changed from %d to %d", totalSize, container.TotalSize)
		}
		if container.Size == 0 {
			if start == container.TotalSize {
				return history, nil
			}
			return MetadataContainer{}, fmt.Errorf("list playback history: no progress at offset %d", start)
		}

		history.MediaContainer.Metadata = append(history.MediaContainer.Metadata, container.Metadata...)
		history.MediaContainer.Size = len(history.MediaContainer.Metadata)
		history.MediaContainer.TotalSize = container.TotalSize
		start += container.Size
		if start == container.TotalSize {
			return history, nil
		}
		if start > container.TotalSize {
			return MetadataContainer{}, fmt.Errorf("list playback history: offset %d exceeds total size %d", start, container.TotalSize)
		}
	}
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
