package cli

import (
	"bufio"
	"context"
	"strings"
	"testing"

	"github.com/jammutkarsh/wandersort/pkg/location"
)

// fakeSearcher stubs placeSearcher with a fixed candidate list, regardless of prefix.
type fakeSearcher struct {
	matches []location.PlaceMatch
}

func (f *fakeSearcher) SearchByName(_ context.Context, _ string, _ int) ([]location.PlaceMatch, error) {
	return f.matches, nil
}

func TestPickPlaceSelectsByNumber(t *testing.T) {
	fs := &fakeSearcher{matches: []location.PlaceMatch{{Name: "Delhi"}, {Name: "Delray Beach"}}}
	reader := bufio.NewReader(strings.NewReader("del\n1\n"))

	name, ok := pickPlace(context.Background(), reader, fs, "Home town")
	if !ok || name != "Delhi" {
		t.Fatalf("pickPlace = (%q, %v), want (Delhi, true)", name, ok)
	}
}

func TestPickPlaceRefinesSearch(t *testing.T) {
	fs := &fakeSearcher{matches: []location.PlaceMatch{{Name: "Delhi"}}}
	// first input isn't a valid selection ("nah"), so it's treated as a refined search
	reader := bufio.NewReader(strings.NewReader("del\nnah\n1\n"))

	name, ok := pickPlace(context.Background(), reader, fs, "Home town")
	if !ok || name != "Delhi" {
		t.Fatalf("pickPlace = (%q, %v), want (Delhi, true)", name, ok)
	}
}

func TestPickPlaceBlankSkips(t *testing.T) {
	fs := &fakeSearcher{matches: []location.PlaceMatch{{Name: "Delhi"}}}
	reader := bufio.NewReader(strings.NewReader("\n"))

	name, ok := pickPlace(context.Background(), reader, fs, "Home town")
	if ok || name != "" {
		t.Fatalf("pickPlace = (%q, %v), want (\"\", false) on blank input", name, ok)
	}
}

// TestPickPlaceExactMatchAutoSelects covers the reported friction: typing the
// exact name that's also the only/an exact suggestion should select it
// immediately, no separate number-entry step required.
func TestPickPlaceExactMatchAutoSelects(t *testing.T) {
	fs := &fakeSearcher{matches: []location.PlaceMatch{{Name: "Indore"}}}
	reader := bufio.NewReader(strings.NewReader("Indore\n")) // single line — no "1" needed

	name, ok := pickPlace(context.Background(), reader, fs, "Home town")
	if !ok || name != "Indore" {
		t.Fatalf("pickPlace = (%q, %v), want (Indore, true) with no extra input consumed", name, ok)
	}
}

func TestPickPlaceExactMatchIsCaseInsensitive(t *testing.T) {
	fs := &fakeSearcher{matches: []location.PlaceMatch{{Name: "Indore"}}}
	reader := bufio.NewReader(strings.NewReader("indore\n"))

	name, ok := pickPlace(context.Background(), reader, fs, "Home town")
	if !ok || name != "Indore" {
		t.Fatalf("pickPlace = (%q, %v), want (Indore, true)", name, ok)
	}
}

func TestSameAsHomeAnswer(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"", true},
		{"y", true},
		{"Y", true},
		{"yes", true},
		{"YES", true},
		{"n", false},
		{"no", false},
		{"nope", false},
	}
	for _, tc := range tests {
		if got := sameAsHomeAnswer(tc.in); got != tc.want {
			t.Errorf("sameAsHomeAnswer(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
