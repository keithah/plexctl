package pms

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	pathpkg "path"
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
	return c.playlists(ctx, nil)
}
func (c *Client) playlists(ctx context.Context, q url.Values) (PlaylistContainer, error) {
	var v PlaylistContainer
	e := c.API.Do(ctx, "GET", "/playlists", q, nil, &v)
	return v, e
}
func (c *Client) Playlist(ctx context.Context, id string) (PlaylistContainer, error) {
	if strings.TrimSpace(id) == "" {
		return PlaylistContainer{}, fmt.Errorf("playlist identifier is blank")
	}
	var v PlaylistContainer
	e := c.API.Do(ctx, "GET", "/playlists/"+url.PathEscape(id), nil, nil, &v)
	return v, e
}
func (c *Client) PlaylistItems(ctx context.Context, id string) (MetadataContainer, error) {
	if strings.TrimSpace(id) == "" {
		return MetadataContainer{}, fmt.Errorf("playlist identifier is blank")
	}
	return c.playlistItems(ctx, id, nil)
}
func (c *Client) playlistItems(ctx context.Context, id string, q url.Values) (MetadataContainer, error) {
	var v MetadataContainer
	e := c.API.Do(ctx, "GET", "/playlists/"+url.PathEscape(id)+"/items", q, nil, &v)
	return v, e
}
func (c *Client) Collections(ctx context.Context, sectionID string) (MetadataContainer, error) {
	if strings.TrimSpace(sectionID) == "" {
		return MetadataContainer{}, fmt.Errorf("section identifier is blank")
	}
	return c.collections(ctx, sectionID, nil)
}
func (c *Client) collections(ctx context.Context, sectionID string, q url.Values) (MetadataContainer, error) {
	var v MetadataContainer
	e := c.API.Do(ctx, "GET", "/library/sections/"+url.PathEscape(sectionID)+"/collections", q, nil, &v)
	return v, e
}
func (c *Client) CollectionItems(ctx context.Context, collectionID string) (MetadataContainer, error) {
	if strings.TrimSpace(collectionID) == "" {
		return MetadataContainer{}, fmt.Errorf("collection identifier is blank")
	}
	return c.collectionItems(ctx, collectionID, nil)
}
func (c *Client) collectionItems(ctx context.Context, collectionID string, q url.Values) (MetadataContainer, error) {
	var v MetadataContainer
	e := c.API.Do(ctx, "GET", "/library/collections/"+url.PathEscape(collectionID)+"/items", q, nil, &v)
	return v, e
}
func (c *Client) Sections(ctx context.Context) (LibrarySections, error) {
	var v LibrarySections
	e := c.API.Do(ctx, "GET", "/library/sections/all", nil, nil, &v)
	return v, e
}
func (c *Client) Section(ctx context.Context, sectionID string) (LibrarySection, error) {
	var v LibrarySection
	e := c.API.Do(ctx, "GET", "/library/sections/"+url.PathEscape(sectionID), nil, nil, &v)
	return v, e
}
func (c *Client) Items(ctx context.Context, key string, q url.Values) (MetadataContainer, error) {
	var v MetadataContainer
	e := c.API.Do(ctx, "GET", "/library/sections/"+url.PathEscape(key)+"/all", q, nil, &v)
	return v, e
}

const thumbProbeLimit int64 = 1024

func (c *Client) probePart(ctx context.Context, path string) error {
	if !IsInternalPartPath(path) {
		return fmt.Errorf("invalid media part path")
	}
	body, err := c.API.DoRawHeadersLimited(ctx, "GET", path, nil, nil, http.Header{"Range": {"bytes=0-1023"}}, thumbProbeLimit)
	if err != nil {
		return fmt.Errorf("media part probe failed")
	}
	if len(body) == 0 {
		return fmt.Errorf("media part probe returned no bytes")
	}
	return nil
}

func (c *Client) ProbeMediaPart(ctx context.Context, path string) error {
	return c.probePart(ctx, path)
}

// ProbeThumb verifies that an internal PMS thumbnail path returns at least one byte.
func (c *Client) ProbeThumb(ctx context.Context, path string) error {
	if !IsInternalThumbPath(path) {
		return fmt.Errorf("invalid thumbnail path")
	}
	body, err := c.API.DoRawHeadersLimited(ctx, "GET", path, nil, nil, http.Header{"Range": {"bytes=0-1023"}}, thumbProbeLimit)
	if err != nil {
		return fmt.Errorf("thumbnail probe failed")
	}
	if len(body) == 0 {
		return fmt.Errorf("thumbnail probe returned no bytes")
	}
	return nil
}

// IsInternalThumbPath reports whether value is a safe relative PMS thumbnail path.
func IsInternalThumbPath(path string) bool {
	parsed, err := url.Parse(path)
	if err != nil || parsed.Scheme != "" || parsed.Host != "" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return false
	}
	return strings.HasPrefix(pathpkg.Clean(parsed.Path), "/library/")
}

// IsInternalPartPath reports whether value is a safe relative PMS media part path.
func IsInternalPartPath(path string) bool {
	parsed, err := url.Parse(path)
	if err != nil || parsed.Scheme != "" || parsed.Host != "" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return false
	}
	return strings.HasPrefix(pathpkg.Clean(parsed.Path), "/library/parts/")
}

const sectionItemsPageSize = 100

// ListSectionItems returns all metadata items in a library section using
// documented container paging. It leaves Items available for legacy callers
// that need to control the request query themselves.
func (c *Client) ListSectionItems(ctx context.Context, key string) (MetadataContainer, error) {
	if strings.TrimSpace(key) == "" {
		return MetadataContainer{}, fmt.Errorf("section identifier is blank")
	}
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
		if !container.offsetSet {
			return MetadataContainer{}, fmt.Errorf("list section %s at offset %d: missing offset", key, start)
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
		} else {
			totalSize = container.TotalSize
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

// ListPlaylists returns every playlist after validating complete paging metadata.
func (c *Client) ListPlaylists(ctx context.Context) (PlaylistContainer, error) {
	var result PlaylistContainer
	totalSize := -1
	for start := 0; ; {
		q := url.Values{"X-Plex-Container-Start": []string{strconv.Itoa(start)}, "X-Plex-Container-Size": []string{strconv.Itoa(sectionItemsPageSize)}}
		page, err := c.playlists(ctx, q)
		if err != nil {
			return PlaylistContainer{}, fmt.Errorf("list playlists at offset %d: %w", start, err)
		}
		container := page.MediaContainer
		if container.Size != len(container.Metadata) || !container.offsetSet || !container.totalSizeSet || container.Offset != start || container.TotalSize < start+container.Size {
			return PlaylistContainer{}, fmt.Errorf("list playlists at offset %d: invalid paging metadata", start)
		}
		for _, playlist := range container.Metadata {
			if strings.TrimSpace(playlist.RatingKey) == "" {
				return PlaylistContainer{}, fmt.Errorf("list playlists: blank playlist identifier")
			}
		}
		if totalSize >= 0 && container.TotalSize != totalSize {
			return PlaylistContainer{}, fmt.Errorf("list playlists: total size changed")
		}
		totalSize = container.TotalSize
		if container.Size == 0 {
			if start == container.TotalSize {
				return result, nil
			}
			return PlaylistContainer{}, fmt.Errorf("list playlists: no progress at offset %d", start)
		}
		result.MediaContainer.Metadata = append(result.MediaContainer.Metadata, container.Metadata...)
		result.MediaContainer.Size = len(result.MediaContainer.Metadata)
		result.MediaContainer.TotalSize = container.TotalSize
		start += container.Size
		if start == container.TotalSize {
			return result, nil
		}
		if start > container.TotalSize {
			return PlaylistContainer{}, fmt.Errorf("list playlists: offset exceeds total size")
		}
	}
}

// ListPlaylistItems returns every item in a playlist after validating complete paging metadata.
func (c *Client) ListPlaylistItems(ctx context.Context, playlistID string) (MetadataContainer, error) {
	if strings.TrimSpace(playlistID) == "" {
		return MetadataContainer{}, fmt.Errorf("playlist identifier is blank")
	}
	var result MetadataContainer
	totalSize := -1
	for start := 0; ; {
		q := url.Values{"X-Plex-Container-Start": []string{strconv.Itoa(start)}, "X-Plex-Container-Size": []string{strconv.Itoa(sectionItemsPageSize)}}
		page, err := c.playlistItems(ctx, playlistID, q)
		if err != nil {
			return MetadataContainer{}, fmt.Errorf("list playlist %s at offset %d: %w", playlistID, start, err)
		}
		container := page.MediaContainer
		if container.Size != len(container.Metadata) || !container.offsetSet || !container.totalSizeSet || container.Offset != start || container.TotalSize < start+container.Size {
			return MetadataContainer{}, fmt.Errorf("list playlist %s at offset %d: invalid paging metadata", playlistID, start)
		}
		for _, item := range container.Metadata {
			if strings.TrimSpace(item.RatingKey) == "" {
				return MetadataContainer{}, fmt.Errorf("list playlist %s: blank item identifier", playlistID)
			}
		}
		if totalSize >= 0 && container.TotalSize != totalSize {
			return MetadataContainer{}, fmt.Errorf("list playlist %s: total size changed", playlistID)
		}
		totalSize = container.TotalSize
		if container.Size == 0 {
			if start == container.TotalSize {
				return result, nil
			}
			return MetadataContainer{}, fmt.Errorf("list playlist %s: no progress at offset %d", playlistID, start)
		}
		result.MediaContainer.Metadata = append(result.MediaContainer.Metadata, container.Metadata...)
		result.MediaContainer.Size = len(result.MediaContainer.Metadata)
		result.MediaContainer.TotalSize = container.TotalSize
		start += container.Size
		if start == container.TotalSize {
			return result, nil
		}
		if start > container.TotalSize {
			return MetadataContainer{}, fmt.Errorf("list playlist %s: offset exceeds total size", playlistID)
		}
	}
}

// ListCollections returns every collection in a section after validating complete paging metadata.
func (c *Client) ListCollections(ctx context.Context, sectionID string) (MetadataContainer, error) {
	if strings.TrimSpace(sectionID) == "" {
		return MetadataContainer{}, fmt.Errorf("section identifier is blank")
	}
	var result MetadataContainer
	totalSize := -1
	for start := 0; ; {
		q := url.Values{"X-Plex-Container-Start": []string{strconv.Itoa(start)}, "X-Plex-Container-Size": []string{strconv.Itoa(sectionItemsPageSize)}}
		page, err := c.collections(ctx, sectionID, q)
		if err != nil {
			return MetadataContainer{}, fmt.Errorf("list collections for section %s at offset %d: %w", sectionID, start, err)
		}
		container := page.MediaContainer
		decoded := len(container.Metadata)
		if container.Size != decoded || !container.offsetSet || !container.totalSizeSet || container.Offset != start || container.TotalSize < start+container.Size {
			return MetadataContainer{}, fmt.Errorf("list collections for section %s at offset %d: invalid paging metadata", sectionID, start)
		}
		if totalSize >= 0 && container.TotalSize != totalSize {
			return MetadataContainer{}, fmt.Errorf("list collections for section %s: total size changed", sectionID)
		}
		totalSize = container.TotalSize
		if container.Size == 0 {
			if start == container.TotalSize {
				return result, nil
			}
			return MetadataContainer{}, fmt.Errorf("list collections for section %s: no progress at offset %d", sectionID, start)
		}
		result.MediaContainer.Metadata = append(result.MediaContainer.Metadata, container.Metadata...)
		result.MediaContainer.Size = len(result.MediaContainer.Metadata)
		result.MediaContainer.TotalSize = container.TotalSize
		start += container.Size
		if start == container.TotalSize {
			return result, nil
		}
		if start > container.TotalSize {
			return MetadataContainer{}, fmt.Errorf("list collections for section %s: offset exceeds total size", sectionID)
		}
	}
}

// ListCollectionItems returns every collection item after validating complete paging metadata.
func (c *Client) ListCollectionItems(ctx context.Context, collectionID string) (MetadataContainer, error) {
	if strings.TrimSpace(collectionID) == "" {
		return MetadataContainer{}, fmt.Errorf("collection identifier is blank")
	}
	var result MetadataContainer
	totalSize := -1
	for start := 0; ; {
		q := url.Values{"X-Plex-Container-Start": []string{strconv.Itoa(start)}, "X-Plex-Container-Size": []string{strconv.Itoa(sectionItemsPageSize)}}
		page, err := c.collectionItems(ctx, collectionID, q)
		if err != nil {
			return MetadataContainer{}, fmt.Errorf("list collection %s at offset %d: %w", collectionID, start, err)
		}
		container := page.MediaContainer
		decoded := len(container.Metadata)
		if container.Size != decoded || !container.offsetSet || !container.totalSizeSet || container.Offset != start || container.TotalSize < start+container.Size {
			return MetadataContainer{}, fmt.Errorf("list collection %s at offset %d: invalid paging metadata", collectionID, start)
		}
		if totalSize >= 0 && container.TotalSize != totalSize {
			return MetadataContainer{}, fmt.Errorf("list collection %s: total size changed", collectionID)
		}
		totalSize = container.TotalSize
		if container.Size == 0 {
			if start == container.TotalSize {
				return result, nil
			}
			return MetadataContainer{}, fmt.Errorf("list collection %s: no progress at offset %d", collectionID, start)
		}
		result.MediaContainer.Metadata = append(result.MediaContainer.Metadata, container.Metadata...)
		result.MediaContainer.Size = len(result.MediaContainer.Metadata)
		result.MediaContainer.TotalSize = container.TotalSize
		start += container.Size
		if start == container.TotalSize {
			return result, nil
		}
		if start > container.TotalSize {
			return MetadataContainer{}, fmt.Errorf("list collection %s: offset exceeds total size", collectionID)
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
		if len(media.Part) == 0 {
			continue
		}
		if c.probePart(ctx, media.Part[0].Key) == nil {
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

func (c *Client) Activities(ctx context.Context) (ActivitiesContainer, error) {
	var v ActivitiesContainer
	e := c.API.Do(ctx, "GET", "/activities", nil, nil, &v)
	if e == nil && !v.mediaContainerSet {
		e = fmt.Errorf("activities response missing MediaContainer")
	}
	if e == nil && v.MediaContainer.Size != len(v.MediaContainer.Activity) {
		e = fmt.Errorf("activities response declared size inconsistent with decoded activities")
	}
	return v, e
}

func (c *Client) ButlerTasks(ctx context.Context) (ButlerContainer, error) {
	var v ButlerContainer
	e := c.API.Do(ctx, "GET", "/butler", nil, nil, &v)
	if e == nil && !v.mediaContainerSet {
		e = fmt.Errorf("butler response missing MediaContainer")
	}
	if e == nil && v.MediaContainer.Size != len(v.MediaContainer.ButlerTask) {
		e = fmt.Errorf("butler response declared size inconsistent with decoded tasks")
	}
	return v, e
}

func (c *Client) UpdaterStatus(ctx context.Context) (UpdaterStatusContainer, error) {
	var v UpdaterStatusContainer
	e := c.API.Do(ctx, "GET", "/updater/status", nil, nil, &v)
	if e == nil && !v.mediaContainerSet {
		e = fmt.Errorf("updater status response missing MediaContainer")
	}
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
		if !container.offsetSet {
			return MetadataContainer{}, fmt.Errorf("list playback history at offset %d: missing offset", start)
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
		} else if container.TotalSize < totalSize {
			return MetadataContainer{}, fmt.Errorf("list playback history: total size decreased from %d to %d", totalSize, container.TotalSize)
		} else {
			totalSize = container.TotalSize
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
