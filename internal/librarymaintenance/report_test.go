package librarymaintenance

import (
	"reflect"
	"testing"
)

func TestUnmatchedAcceptsExactlyNormalMediaTypes(t *testing.T) {
	normalTypes := []string{"movie", "show", "season", "episode", "artist", "album", "track", "photo", "clip"}
	items := make([]Item, 0, len(normalTypes)+5)
	for _, mediaType := range normalTypes {
		items = append(items, Item{RatingKey: mediaType, Title: mediaType, MediaType: mediaType})
	}
	for _, mediaType := range []string{"collection", "directory", "unknown", "", "MOVIE"} {
		items = append(items, Item{RatingKey: mediaType + "-excluded", Title: mediaType, MediaType: mediaType})
	}

	got := Unmatched(items)
	if len(got) != len(normalTypes) {
		t.Fatalf("Unmatched() returned %d items, want %d: %#v", len(got), len(normalTypes), got)
	}
	for _, candidate := range got {
		found := false
		for _, mediaType := range normalTypes {
			if candidate.MediaType == mediaType {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("Unmatched() accepted ineligible type %q", candidate.MediaType)
		}
	}
}

func TestDuplicatesUsesUnicodeCaseFolding(t *testing.T) {
	items := []Item{
		{SectionKey: "1", RatingKey: "long-s", Title: "ſame", MediaType: "movie"},
		{SectionKey: "1", RatingKey: "ascii-s", Title: "SAME", MediaType: "movie"},
	}
	want := []Candidate{
		{SectionKey: "1", RatingKey: "ascii-s", Title: "SAME", MediaType: "movie", GroupTitle: "same"},
		{SectionKey: "1", RatingKey: "long-s", Title: "ſame", MediaType: "movie", GroupTitle: "same"},
	}
	if got := Duplicates(items); !reflect.DeepEqual(got, want) {
		t.Fatalf("Duplicates() = %#v, want %#v", got, want)
	}
}

func TestAnalysisOrderingDoesNotDependOnInputOrder(t *testing.T) {
	duplicateItems := []Item{
		{SectionKey: "2", RatingKey: "b", Title: "Same", MediaType: "movie"},
		{SectionKey: "2", RatingKey: "a", Title: "same", MediaType: "movie"},
	}
	unmatchedItems := []Item{
		{SectionKey: "2", RatingKey: "b", Title: "Beta", MediaType: "movie"},
		{SectionKey: "1", RatingKey: "a", Title: "Alpha", MediaType: "movie"},
	}
	missingPosterItems := []Item{
		{SectionKey: "1", RatingKey: "same", Title: "Same", MediaType: "movie", Thumb: ThumbPresent, Probe: ProbeFailed},
		{SectionKey: "1", RatingKey: "same", Title: "Same", MediaType: "movie", Thumb: ThumbMissing},
	}
	collections := []Collection{
		{SectionKey: "2", RatingKey: "b", Title: "Beta"},
		{SectionKey: "1", RatingKey: "a", Title: "Alpha"},
	}

	assertSameCandidatesAfterReverse(t, "duplicates", Duplicates, duplicateItems)
	assertSameCandidatesAfterReverse(t, "unmatched", Unmatched, unmatchedItems)
	assertSameCandidatesAfterReverse(t, "missing posters", MissingPosters, missingPosterItems)
	assertSameCollectionsAfterReverse(t, collections)
}

func TestEmptyCollectionsSelectsOnlyZeroItemCollections(t *testing.T) {
	collections := []Collection{
		{SectionKey: "2", SectionTitle: "TV", RatingKey: "empty-b", Title: "Zebra", ItemCount: 0},
		{SectionKey: "1", SectionTitle: "Films", RatingKey: "empty-a", Title: "  alpha  ", ItemCount: 0},
		{SectionKey: "1", RatingKey: "not-empty", Title: "Beta", ItemCount: 1},
	}
	want := []Candidate{
		{SectionKey: "1", SectionTitle: "Films", RatingKey: "empty-a", Title: "  alpha  "},
		{SectionKey: "2", SectionTitle: "TV", RatingKey: "empty-b", Title: "Zebra"},
	}
	if got := EmptyCollections(collections); !reflect.DeepEqual(got, want) {
		t.Fatalf("EmptyCollections() = %#v, want %#v", got, want)
	}
}

func TestUnmatchedSelectsEligibleItemsWithoutNonemptyGUIDs(t *testing.T) {
	items := []Item{
		{SectionKey: "2", SectionTitle: "TV", RatingKey: "empty-guid", Title: "Empty GUID", MediaType: "episode", Year: 2024, GUIDs: []string{"", "  "}},
		{SectionKey: "1", SectionTitle: "Films", RatingKey: "no-guid", Title: "No GUID", MediaType: "movie", Year: 2023},
		{SectionKey: "1", RatingKey: "matched", Title: "Matched", MediaType: "movie", GUIDs: []string{"plex://movie/1"}},
		{SectionKey: "1", RatingKey: "container", Title: "Container", MediaType: "directory"},
		{SectionKey: "1", RatingKey: "", Title: "No key", MediaType: "movie"},
	}
	want := []Candidate{
		{SectionKey: "1", SectionTitle: "Films", RatingKey: "no-guid", Title: "No GUID", MediaType: "movie", Year: 2023},
		{SectionKey: "2", SectionTitle: "TV", RatingKey: "empty-guid", Title: "Empty GUID", MediaType: "episode", Year: 2024},
	}
	if got := Unmatched(items); !reflect.DeepEqual(got, want) {
		t.Fatalf("Unmatched() = %#v, want %#v", got, want)
	}
}

func TestMissingPostersClassifiesBlankThumbAndProbeFailure(t *testing.T) {
	items := []Item{
		{SectionKey: "2", SectionTitle: "TV", RatingKey: "probe", Title: "Probe", MediaType: "episode", Thumb: ThumbPresent, Probe: ProbeFailed},
		{SectionKey: "1", SectionTitle: "Films", RatingKey: "blank", Title: "Blank", MediaType: "movie", Thumb: ThumbMissing},
		{SectionKey: "1", RatingKey: "success", Title: "Present", MediaType: "movie", Thumb: ThumbPresent, Probe: ProbeSucceeded},
		{SectionKey: "1", RatingKey: "unprobed", Title: "Unprobed", MediaType: "movie", Thumb: ThumbPresent},
		{SectionKey: "1", RatingKey: "container", Title: "Container", MediaType: "directory", Thumb: ThumbMissing},
		{SectionKey: "1", RatingKey: "", Title: "No key", MediaType: "movie", Thumb: ThumbMissing},
	}

	want := []Candidate{
		{SectionKey: "1", SectionTitle: "Films", RatingKey: "blank", Title: "Blank", MediaType: "movie", Reason: "missing_thumb"},
		{SectionKey: "2", SectionTitle: "TV", RatingKey: "probe", Title: "Probe", MediaType: "episode", Reason: "probe_failed"},
	}
	if got := MissingPosters(items); !reflect.DeepEqual(got, want) {
		t.Fatalf("MissingPosters() = %#v, want %#v", got, want)
	}
}

func TestDuplicatesOmitsIneligibleItemsAndRepeatedRatingKeys(t *testing.T) {
	items := []Item{
		{SectionKey: "1", RatingKey: "a", Title: "Same", MediaType: "movie"},
		{SectionKey: "1", RatingKey: "a", Title: "same", MediaType: "movie"},
		{SectionKey: "1", RatingKey: "b", Title: "SAME", MediaType: "movie"},
		{SectionKey: "1", RatingKey: "", Title: "same", MediaType: "movie"},
		{SectionKey: "1", RatingKey: "collection", Title: "same", MediaType: "collection"},
		{SectionKey: "1", RatingKey: "directory", Title: "same", MediaType: "directory"},
		{SectionKey: "1", RatingKey: "unknown", Title: "same", MediaType: "unknown"},
		{SectionKey: "1", RatingKey: "empty", Title: "same", MediaType: ""},
	}

	want := []Candidate{
		{SectionKey: "1", RatingKey: "a", Title: "Same", MediaType: "movie", GroupTitle: "same"},
		{SectionKey: "1", RatingKey: "b", Title: "SAME", MediaType: "movie", GroupTitle: "same"},
	}
	if got := Duplicates(items); !reflect.DeepEqual(got, want) {
		t.Fatalf("Duplicates() = %#v, want %#v", got, want)
	}
}

func TestDuplicatesNormalizesTitlesWithinSection(t *testing.T) {
	items := []Item{
		{SectionKey: "2", SectionTitle: "TV", RatingKey: "episode-1", Title: "The\tShow", MediaType: "episode", Year: 2024},
		{SectionKey: "1", SectionTitle: "Films", RatingKey: "film-2", Title: "  CAFÉ  ", MediaType: "movie", Year: 2025},
		{SectionKey: "1", SectionTitle: "Films", RatingKey: "film-1", Title: "café", MediaType: "movie", Year: 2024},
		{SectionKey: "2", SectionTitle: "TV", RatingKey: "episode-2", Title: "the  show", MediaType: "episode", Year: 2023},
	}

	want := []Candidate{
		{SectionKey: "1", SectionTitle: "Films", RatingKey: "film-1", Title: "café", MediaType: "movie", Year: 2024, GroupTitle: "café"},
		{SectionKey: "1", SectionTitle: "Films", RatingKey: "film-2", Title: "  CAFÉ  ", MediaType: "movie", Year: 2025, GroupTitle: "café"},
		{SectionKey: "2", SectionTitle: "TV", RatingKey: "episode-1", Title: "The\tShow", MediaType: "episode", Year: 2024, GroupTitle: "the show"},
		{SectionKey: "2", SectionTitle: "TV", RatingKey: "episode-2", Title: "the  show", MediaType: "episode", Year: 2023, GroupTitle: "the show"},
	}

	if got := Duplicates(items); !reflect.DeepEqual(got, want) {
		t.Fatalf("Duplicates() = %#v, want %#v", got, want)
	}
}

func assertSameCandidatesAfterReverse(t *testing.T, name string, analyze func([]Item) []Candidate, items []Item) {
	t.Helper()
	reversed := append([]Item(nil), items...)
	reverseItems(reversed)
	if first, second := analyze(items), analyze(reversed); !reflect.DeepEqual(first, second) {
		t.Fatalf("%s output depends on input order: first=%#v second=%#v", name, first, second)
	}
}

func assertSameCollectionsAfterReverse(t *testing.T, collections []Collection) {
	t.Helper()
	reversed := append([]Collection(nil), collections...)
	for left, right := 0, len(reversed)-1; left < right; left, right = left+1, right-1 {
		reversed[left], reversed[right] = reversed[right], reversed[left]
	}
	if first, second := EmptyCollections(collections), EmptyCollections(reversed); !reflect.DeepEqual(first, second) {
		t.Fatalf("empty collection output depends on input order: first=%#v second=%#v", first, second)
	}
}

func reverseItems(items []Item) {
	for left, right := 0, len(items)-1; left < right; left, right = left+1, right-1 {
		items[left], items[right] = items[right], items[left]
	}
}
