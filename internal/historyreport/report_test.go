package historyreport

import (
	"reflect"
	"testing"
	"time"
)

func TestSummarizeViewsGroupsByAccountAndSection(t *testing.T) {
	first := time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)
	second := first.Add(time.Hour)
	third := second.Add(time.Hour)
	tenMinutes := 10 * time.Minute
	fiveMinutes := 5 * time.Minute

	for _, test := range []struct {
		name  string
		views []View
		want  []Summary
	}{
		{
			name: "groups and sorts normalized views with optional metadata",
			views: []View{
				{AccountID: "a2", AccountTitle: "B", SectionID: "s1", SectionTitle: "Films", ViewedAt: second, Duration: &fiveMinutes},
				{AccountID: "a1", AccountTitle: "A", SectionID: "s1", SectionTitle: "Films", ViewedAt: first, Duration: &tenMinutes},
				{AccountID: "a1", AccountTitle: "A", SectionID: "s1", SectionTitle: "Films", ViewedAt: third},
				{ViewedAt: first},
			},
			want: []Summary{
				{ViewCount: 1, FirstViewedAt: first, LastViewedAt: first},
				{AccountID: "a1", AccountTitle: "A", SectionID: "s1", SectionTitle: "Films", ViewCount: 2, FirstViewedAt: first, LastViewedAt: third, TotalDuration: tenMinutes},
				{AccountID: "a2", AccountTitle: "B", SectionID: "s1", SectionTitle: "Films", ViewCount: 1, FirstViewedAt: second, LastViewedAt: second, TotalDuration: fiveMinutes},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := SummarizeViews(test.views); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("SummarizeViews() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestUnwatchedExcludesEveryHistoryRatingKey(t *testing.T) {
	for _, test := range []struct {
		name  string
		items []LibraryItem
		views []View
		want  []LibraryItem
	}{
		{
			name: "uses stable keys and skips ineligible items",
			items: []LibraryItem{
				{RatingKey: "unwatched-b", Title: "Beta", SectionID: "2", SectionTitle: "TV", MediaType: "episode"},
				{RatingKey: "watched", Title: "Already watched", SectionID: "1", SectionTitle: "Films", MediaType: "movie"},
				{RatingKey: "", Title: "No key", SectionID: "1", SectionTitle: "Films", MediaType: "movie"},
				{RatingKey: "container", Title: "A container", SectionID: "1", SectionTitle: "Films", MediaType: "directory"},
				{RatingKey: "unknown", Title: "Unknown", SectionID: "1", SectionTitle: "Films", MediaType: "unknown"},
				{RatingKey: "unwatched-a", Title: "Alpha", SectionID: "1", SectionTitle: "Films", MediaType: "movie"},
			},
			views: []View{
				{RatingKey: "watched"},
				{RatingKey: "watched"},
				{RatingKey: ""},
			},
			want: []LibraryItem{
				{RatingKey: "unwatched-a", Title: "Alpha", SectionID: "1", SectionTitle: "Films", MediaType: "movie"},
				{RatingKey: "unwatched-b", Title: "Beta", SectionID: "2", SectionTitle: "TV", MediaType: "episode"},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := UnwatchedItems(test.items, test.views); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("UnwatchedItems() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestInactiveUsesStrictCutoffAndExcludesNeverViewed(t *testing.T) {
	cutoff := time.Date(2026, time.September, 5, 12, 0, 0, 0, time.UTC)
	older := cutoff.Add(-time.Hour)
	newer := cutoff.Add(time.Hour)

	for _, test := range []struct {
		name  string
		items []LibraryItem
		views []View
		want  []InactiveItem
	}{
		{
			name: "uses latest view and strict cutoff",
			items: []LibraryItem{
				{RatingKey: "old", Title: "Alpha", SectionID: "1", SectionTitle: "Films", MediaType: "movie"},
				{RatingKey: "equal", Title: "At cutoff", SectionID: "1", SectionTitle: "Films", MediaType: "movie"},
				{RatingKey: "new", Title: "Newer", SectionID: "2", SectionTitle: "TV", MediaType: "episode"},
				{RatingKey: "never", Title: "Never viewed", SectionID: "1", SectionTitle: "Films", MediaType: "movie"},
				{RatingKey: "", Title: "Missing key", SectionID: "1", SectionTitle: "Films", MediaType: "movie"},
			},
			views: []View{
				{RatingKey: "old", ViewedAt: cutoff.Add(-2 * time.Hour)},
				{RatingKey: "old", ViewedAt: older},
				{RatingKey: "equal", ViewedAt: cutoff},
				{RatingKey: "new", ViewedAt: newer},
				{RatingKey: "", ViewedAt: older},
			},
			want: []InactiveItem{
				{LibraryItem: LibraryItem{RatingKey: "old", Title: "Alpha", SectionID: "1", SectionTitle: "Films", MediaType: "movie"}, LastViewedAt: older},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := InactiveItems(test.items, test.views, cutoff); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("InactiveItems() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestNormalizeAndSortViews(t *testing.T) {
	earlier := time.Date(2026, time.September, 1, 8, 0, 0, 0, time.FixedZone("PDT", -7*60*60))
	later := earlier.Add(2 * time.Hour)
	duration := 42 * time.Minute

	got := NormalizeViews([]SourceView{
		{RatingKey: "9", Title: "Later", ViewedAt: later},
		{RatingKey: "3", Title: "Same time", ViewedAt: earlier},
		{RatingKey: "2", Title: "Earlier", ViewedAt: earlier, Duration: &duration},
	})

	want := []View{
		{RatingKey: "2", Title: "Earlier", ViewedAt: earlier.UTC(), Duration: &duration},
		{RatingKey: "3", Title: "Same time", ViewedAt: earlier.UTC()},
		{RatingKey: "9", Title: "Later", ViewedAt: later.UTC()},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NormalizeViews() = %#v, want %#v", got, want)
	}
}
