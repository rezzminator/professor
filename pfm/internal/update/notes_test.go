package update

import (
	"reflect"
	"testing"
)

func TestReleaseNotesReturnsOldestFirstExcludingPreviousAndAboveTarget(t *testing.T) {
	files := []string{
		"releases/v0.75.0.md",
		"releases/v0.77.0.md",
		"releases/v0.76.0.md",
		"releases/v0.78.0.md",
		"CHANGELOG.md",
	}
	got, err := ReleaseNotes("v0.75.0", "v0.77.0", files)
	if err != nil {
		t.Fatalf("ReleaseNotes() error = %v", err)
	}
	want := []string{"releases/v0.76.0.md", "releases/v0.77.0.md"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ReleaseNotes() = %v, want %v", got, want)
	}
}

func TestReleaseNotesNoneBetweenIsEmptyNotAnError(t *testing.T) {
	got, err := ReleaseNotes("v0.77.0", "v0.77.1", []string{"releases/v0.77.0.md"})
	if err != nil {
		t.Fatalf("ReleaseNotes() error = %v, want none", err)
	}
	if len(got) != 0 {
		t.Fatalf("ReleaseNotes() = %v, want empty", got)
	}
}

func TestReleaseNotesUnparseableTagIsAnError(t *testing.T) {
	if _, err := ReleaseNotes("not-a-tag", "v0.77.0", []string{"releases/v0.76.0.md"}); err == nil {
		t.Fatal("ReleaseNotes() with an unparseable previous tag: error = nil, want an error")
	}
	if _, err := ReleaseNotes("v0.76.0", "not-a-tag", []string{"releases/v0.76.0.md"}); err == nil {
		t.Fatal("ReleaseNotes() with an unparseable target tag: error = nil, want an error")
	}
}
