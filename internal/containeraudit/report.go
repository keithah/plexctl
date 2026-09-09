// Package containeraudit provides pure, deterministic playlist and collection audit analysis.
package containeraudit

import (
	"fmt"
	"sort"
	"strings"
)

// Kind distinguishes supported container types.
type Kind string

const (
	KindPlaylist   Kind = "playlist"
	KindCollection Kind = "collection"
)

// Item contains only the opaque item identity needed for an audit report.
type Item struct {
	RatingKey string
}

// Container is a normalized playlist or collection with its retrieved item set.
type Container struct {
	ID, Title     string
	Kind          Kind
	ItemsComplete bool
	Items         []Item
}

// Candidate is a safe report record. It intentionally contains no source location or raw provider identifiers.
type Candidate struct {
	ID, Title               string
	Kind                    Kind
	ItemsComplete           bool
	Empty                   bool
	ItemRatingKeys          []string
	DuplicateItemRatingKeys []string
}

// Report contains stable container-audit candidates.
type Report struct {
	Candidates []Candidate
}

// Analyze validates opaque identities, deduplicates item keys, and marks empty only for a complete zero-item set.
func Analyze(containers []Container) (Report, error) {
	candidates := make([]Candidate, 0, len(containers))
	containerIDs := make(map[string]struct{}, len(containers))
	for index, container := range containers {
		if strings.TrimSpace(container.ID) == "" {
			return Report{}, fmt.Errorf("container %d has blank identifier", index)
		}
		if _, exists := containerIDs[container.ID]; exists {
			return Report{}, fmt.Errorf("container %d has duplicate identifier", index)
		}
		containerIDs[container.ID] = struct{}{}
		keys := make(map[string]int, len(container.Items))
		for itemIndex, item := range container.Items {
			if strings.TrimSpace(item.RatingKey) == "" {
				return Report{}, fmt.Errorf("container %d item %d has blank rating key", index, itemIndex)
			}
			keys[item.RatingKey]++
		}
		var itemKeys []string
		var duplicateKeys []string
		if len(keys) != 0 {
			itemKeys = make([]string, 0, len(keys))
			for key := range keys {
				itemKeys = append(itemKeys, key)
				if keys[key] > 1 {
					duplicateKeys = append(duplicateKeys, key)
				}
			}
			sort.Strings(itemKeys)
			sort.Strings(duplicateKeys)
		}
		candidates = append(candidates, Candidate{
			ID: container.ID, Title: container.Title, Kind: container.Kind, ItemsComplete: container.ItemsComplete,
			Empty: container.ItemsComplete && len(itemKeys) == 0, ItemRatingKeys: itemKeys, DuplicateItemRatingKeys: duplicateKeys,
		})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].ID != candidates[j].ID {
			return candidates[i].ID < candidates[j].ID
		}
		if candidates[i].Kind != candidates[j].Kind {
			return candidates[i].Kind < candidates[j].Kind
		}
		return candidates[i].Title < candidates[j].Title
	})
	return Report{Candidates: candidates}, nil
}
