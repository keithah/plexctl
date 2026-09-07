// Package librarymaintenance provides pure, deterministic library-maintenance analysis.
package librarymaintenance

import (
	"sort"
	"strings"
	"unicode"
)

// ThumbState describes whether an item supplied a thumbnail path.
type ThumbState uint8

const (
	// ThumbMissing indicates the item has no thumbnail path.
	ThumbMissing ThumbState = iota
	// ThumbPresent indicates the caller supplied a thumbnail path for probing.
	ThumbPresent
)

// ProbeState describes a caller-provided thumbnail probe result.
type ProbeState uint8

const (
	// ProbeNotRun indicates no thumbnail probe result was supplied.
	ProbeNotRun ProbeState = iota
	// ProbeSucceeded indicates the thumbnail probe returned usable bytes.
	ProbeSucceeded
	// ProbeFailed indicates the thumbnail probe did not return usable bytes.
	ProbeFailed
)

// Item is a source-neutral library record used for maintenance analysis.
type Item struct {
	SectionKey   string
	SectionTitle string
	RatingKey    string
	Title        string
	MediaType    string
	Year         int
	Thumb        ThumbState
	Probe        ProbeState
	GUIDs        []string
}

// Collection is a collection record with the successfully retrieved item count.
type Collection struct {
	SectionKey   string
	SectionTitle string
	RatingKey    string
	Title        string
	ItemCount    int
}

// Candidate is a safe display record for a maintenance preview.
type Candidate struct {
	SectionKey   string
	SectionTitle string
	RatingKey    string
	Title        string
	MediaType    string
	Year         int
	GroupTitle   string
	Reason       string
}

// Duplicates returns normal media items that share a normalized title within a section.
func Duplicates(items []Item) []Candidate {
	groups := make(map[string][]Item)
	for _, item := range items {
		if !isNormalMedia(item.MediaType) || item.RatingKey == "" {
			continue
		}
		groupTitle := normalizeTitle(item.Title)
		key := item.SectionKey + "\x00" + groupTitle
		groups[key] = append(groups[key], item)
	}

	candidates := make([]Candidate, 0)
	for _, group := range groups {
		itemsByRatingKey := make(map[string]Item, len(group))
		for _, item := range group {
			existing, ok := itemsByRatingKey[item.RatingKey]
			if !ok || lessItem(item, existing) {
				itemsByRatingKey[item.RatingKey] = item
			}
		}
		if len(itemsByRatingKey) < 2 {
			continue
		}
		for _, item := range itemsByRatingKey {
			candidates = append(candidates, candidateFromItem(item, normalizeTitle(item.Title)))
		}
	}
	sortCandidates(candidates)
	return candidates
}

// EmptyCollections returns collections whose successful item retrieval found no items.
func EmptyCollections(collections []Collection) []Candidate {
	candidates := make([]Candidate, 0)
	for _, collection := range collections {
		if collection.ItemCount != 0 {
			continue
		}
		candidates = append(candidates, Candidate{
			SectionKey:   collection.SectionKey,
			SectionTitle: collection.SectionTitle,
			RatingKey:    collection.RatingKey,
			Title:        collection.Title,
		})
	}
	sort.Slice(candidates, func(i, j int) bool {
		left, right := candidates[i], candidates[j]
		for _, values := range [][2]string{
			{left.SectionKey, right.SectionKey},
			{normalizeTitle(left.Title), normalizeTitle(right.Title)},
			{left.RatingKey, right.RatingKey},
			{left.Title, right.Title},
			{left.SectionTitle, right.SectionTitle},
		} {
			if values[0] != values[1] {
				return values[0] < values[1]
			}
		}
		return false
	})
	return candidates
}

// Unmatched returns eligible items that have no nonempty GUID identifiers.
func Unmatched(items []Item) []Candidate {
	candidates := make([]Candidate, 0)
	for _, item := range items {
		if !isNormalMedia(item.MediaType) || item.RatingKey == "" || hasGUID(item.GUIDs) {
			continue
		}
		candidates = append(candidates, candidateFromItem(item, ""))
	}
	sortCandidates(candidates)
	return candidates
}

func hasGUID(guids []string) bool {
	for _, guid := range guids {
		if strings.TrimSpace(guid) != "" {
			return true
		}
	}
	return false
}

// MissingPosters returns eligible items without a thumbnail or with a failed caller probe.
func MissingPosters(items []Item) []Candidate {
	candidates := make([]Candidate, 0)
	for _, item := range items {
		if !isNormalMedia(item.MediaType) || item.RatingKey == "" {
			continue
		}
		reason := ""
		switch {
		case item.Thumb == ThumbMissing:
			reason = "missing_thumb"
		case item.Probe == ProbeFailed:
			reason = "probe_failed"
		}
		if reason == "" {
			continue
		}
		candidate := candidateFromItem(item, "")
		candidate.Reason = reason
		candidates = append(candidates, candidate)
	}
	sortCandidates(candidates)
	return candidates
}

func candidateFromItem(item Item, groupTitle string) Candidate {
	return Candidate{
		SectionKey:   item.SectionKey,
		SectionTitle: item.SectionTitle,
		RatingKey:    item.RatingKey,
		Title:        item.Title,
		MediaType:    item.MediaType,
		Year:         item.Year,
		GroupTitle:   groupTitle,
	}
}

func lessItem(left, right Item) bool {
	for _, values := range [][2]string{
		{left.SectionKey, right.SectionKey},
		{left.Title, right.Title},
		{left.SectionTitle, right.SectionTitle},
		{left.MediaType, right.MediaType},
	} {
		if values[0] != values[1] {
			return values[0] < values[1]
		}
	}
	return left.Year < right.Year
}

func isNormalMedia(mediaType string) bool {
	switch mediaType {
	case "movie", "show", "season", "episode", "artist", "album", "track", "photo", "clip":
		return true
	default:
		return false
	}
}

func normalizeTitle(title string) string {
	return caseFold(strings.Join(strings.FieldsFunc(strings.TrimSpace(title), unicode.IsSpace), " "))
}

func caseFold(value string) string {
	var folded strings.Builder
	for _, r := range value {
		canonical := r
		for candidate := unicode.SimpleFold(r); candidate != r; candidate = unicode.SimpleFold(candidate) {
			if candidate < canonical {
				canonical = candidate
			}
		}
		folded.WriteRune(unicode.ToLower(canonical))
	}
	return folded.String()
}

func sortCandidates(candidates []Candidate) {
	sort.Slice(candidates, func(i, j int) bool {
		left, right := candidates[i], candidates[j]
		for _, values := range [][2]string{
			{left.SectionKey, right.SectionKey},
			{left.GroupTitle, right.GroupTitle},
			{left.RatingKey, right.RatingKey},
			{left.Title, right.Title},
			{left.SectionTitle, right.SectionTitle},
			{left.MediaType, right.MediaType},
			{left.Reason, right.Reason},
		} {
			if values[0] != values[1] {
				return values[0] < values[1]
			}
		}
		return left.Year < right.Year
	})
}
