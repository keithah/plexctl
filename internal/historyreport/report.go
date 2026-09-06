// Package historyreport provides pure, deterministic watch-history analysis.
package historyreport

import (
	"sort"
	"time"
)

// SourceView is a source-neutral playback-history record.
type SourceView struct {
	RatingKey        string
	Title            string
	ParentTitle      string
	GrandparentTitle string
	MediaType        string
	SectionID        string
	SectionTitle     string
	AccountID        string
	AccountTitle     string
	ViewedAt         time.Time
	Duration         *time.Duration
}

// View is a normalized playback-history record.
type View struct {
	RatingKey        string
	Title            string
	ParentTitle      string
	GrandparentTitle string
	MediaType        string
	SectionID        string
	SectionTitle     string
	AccountID        string
	AccountTitle     string
	ViewedAt         time.Time
	Duration         *time.Duration
}

// InactiveItem is a library item whose latest view precedes a cutoff.
type InactiveItem struct {
	LibraryItem
	LastViewedAt time.Time
}

// InactiveItems returns stable-key items with a recorded latest view strictly
// before cutoff. Items with no matching nonempty rating key are excluded.
func InactiveItems(items []LibraryItem, views []View, cutoff time.Time) []InactiveItem {
	latest := latestViewedAtByRatingKey(views)
	cutoff = cutoff.UTC()
	inactive := make([]InactiveItem, 0, len(items))
	for _, item := range items {
		if item.RatingKey == "" {
			continue
		}
		lastViewedAt, ok := latest[item.RatingKey]
		if !ok || !lastViewedAt.Before(cutoff) {
			continue
		}
		inactive = append(inactive, InactiveItem{LibraryItem: item, LastViewedAt: lastViewedAt})
	}
	sort.Slice(inactive, func(i, j int) bool {
		left, right := inactive[i].LibraryItem, inactive[j].LibraryItem
		if left.SectionTitle != right.SectionTitle {
			return left.SectionTitle < right.SectionTitle
		}
		if left.Title != right.Title {
			return left.Title < right.Title
		}
		if left.RatingKey != right.RatingKey {
			return left.RatingKey < right.RatingKey
		}
		if left.SectionID != right.SectionID {
			return left.SectionID < right.SectionID
		}
		return left.MediaType < right.MediaType
	})
	return inactive
}

func latestViewedAtByRatingKey(views []View) map[string]time.Time {
	latest := make(map[string]time.Time)
	for _, view := range views {
		if view.RatingKey == "" {
			continue
		}
		viewedAt := view.ViewedAt.UTC()
		if existing, ok := latest[view.RatingKey]; !ok || viewedAt.After(existing) {
			latest[view.RatingKey] = viewedAt
		}
	}
	return latest
}

// LibraryItem is a source-neutral library record used for item analysis.
type LibraryItem struct {
	RatingKey    string
	Title        string
	SectionID    string
	SectionTitle string
	MediaType    string
}

// UnwatchedItems returns stable-key items that have no nonempty matching history
// rating key, ordered by section title, item title, and rating key.
func UnwatchedItems(items []LibraryItem, views []View) []LibraryItem {
	viewed := make(map[string]struct{}, len(views))
	for _, view := range views {
		if view.RatingKey != "" {
			viewed[view.RatingKey] = struct{}{}
		}
	}

	unwatched := make([]LibraryItem, 0, len(items))
	for _, item := range items {
		if item.RatingKey == "" {
			continue
		}
		if _, ok := viewed[item.RatingKey]; !ok {
			unwatched = append(unwatched, item)
		}
	}
	sortLibraryItems(unwatched)
	return unwatched
}

func sortLibraryItems(items []LibraryItem) {
	sort.Slice(items, func(i, j int) bool {
		left, right := items[i], items[j]
		if left.SectionTitle != right.SectionTitle {
			return left.SectionTitle < right.SectionTitle
		}
		if left.Title != right.Title {
			return left.Title < right.Title
		}
		if left.RatingKey != right.RatingKey {
			return left.RatingKey < right.RatingKey
		}
		if left.SectionID != right.SectionID {
			return left.SectionID < right.SectionID
		}
		return left.MediaType < right.MediaType
	})
}

// Summary is a deterministic aggregation of views for an account and section.
type Summary struct {
	AccountID     string
	AccountTitle  string
	SectionID     string
	SectionTitle  string
	ViewCount     int
	FirstViewedAt time.Time
	LastViewedAt  time.Time
	// TotalDuration is nil when no view in the group supplied a duration.
	TotalDuration *time.Duration
}

// SummarizeViews groups views by account and section and returns composite-key
// ordered aggregate rows.
func SummarizeViews(views []View) []Summary {
	type groupKey struct {
		accountID, accountTitle, sectionID, sectionTitle string
	}
	groups := make(map[groupKey]Summary)
	for _, view := range views {
		key := groupKey{view.AccountID, view.AccountTitle, view.SectionID, view.SectionTitle}
		viewedAt := view.ViewedAt.UTC()
		summary, ok := groups[key]
		if !ok {
			summary = Summary{
				AccountID:     view.AccountID,
				AccountTitle:  view.AccountTitle,
				SectionID:     view.SectionID,
				SectionTitle:  view.SectionTitle,
				FirstViewedAt: viewedAt,
				LastViewedAt:  viewedAt,
			}
		}
		summary.ViewCount++
		if viewedAt.Before(summary.FirstViewedAt) {
			summary.FirstViewedAt = viewedAt
		}
		if viewedAt.After(summary.LastViewedAt) {
			summary.LastViewedAt = viewedAt
		}
		if view.Duration != nil {
			totalDuration := *view.Duration
			if summary.TotalDuration != nil {
				totalDuration += *summary.TotalDuration
			}
			summary.TotalDuration = &totalDuration
		}
		groups[key] = summary
	}

	summaries := make([]Summary, 0, len(groups))
	for _, summary := range groups {
		summaries = append(summaries, summary)
	}
	sort.Slice(summaries, func(i, j int) bool {
		left, right := summaries[i], summaries[j]
		if left.AccountID != right.AccountID {
			return left.AccountID < right.AccountID
		}
		if left.AccountTitle != right.AccountTitle {
			return left.AccountTitle < right.AccountTitle
		}
		if left.SectionID != right.SectionID {
			return left.SectionID < right.SectionID
		}
		return left.SectionTitle < right.SectionTitle
	})
	return summaries
}

// NormalizeViews normalizes timestamps to UTC and returns views in ascending
// viewed timestamp, then rating-key order.
func NormalizeViews(source []SourceView) []View {
	views := make([]View, len(source))
	for i, value := range source {
		views[i] = View{
			RatingKey:        value.RatingKey,
			Title:            value.Title,
			ParentTitle:      value.ParentTitle,
			GrandparentTitle: value.GrandparentTitle,
			MediaType:        value.MediaType,
			SectionID:        value.SectionID,
			SectionTitle:     value.SectionTitle,
			AccountID:        value.AccountID,
			AccountTitle:     value.AccountTitle,
			ViewedAt:         value.ViewedAt.UTC(),
			Duration:         value.Duration,
		}
	}
	sort.SliceStable(views, func(i, j int) bool {
		if !views[i].ViewedAt.Equal(views[j].ViewedAt) {
			return views[i].ViewedAt.Before(views[j].ViewedAt)
		}
		return views[i].RatingKey < views[j].RatingKey
	})
	return views
}
