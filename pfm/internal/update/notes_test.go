package update

import (
	"reflect"
	"strings"
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

// TestUpdateReportsReleaseNotesToRead pins the "files between" branch of the
// release-notes report: the printed line names the previous and target tags
// and the release count, and lists each file oldest first.
func TestUpdateReportsReleaseNotesToRead(t *testing.T) {
	repo := newReleaseNotesUpdateFixture(t, []string{"v0.9.5.md", "v0.9.1.md"})
	stdout := updateWithReleaseNotesFakes(t, repo)
	if !strings.Contains(stdout, "release notes to read (v0.9.0 → v0.10.0, 2 release(s)):") {
		t.Fatalf("stdout=%q, want the release-notes-to-read header", stdout)
	}
	first := strings.Index(stdout, "v0.9.1.md")
	second := strings.Index(stdout, "v0.9.5.md")
	if first < 0 || second < 0 || second < first {
		t.Fatalf("stdout=%q, want v0.9.1.md listed before v0.9.5.md (oldest first)", stdout)
	}
}

// TestUpdateReportsNoReleaseNotesBetween is a REGRESSION test for the
// none-between branch: watched failing against a build that collapsed
// releaseNotesForUpdate's error branch into this one (see
// TestUpdateReportsReleaseNotesCannotList) — the two must stay visibly
// distinct, since one names a real absence and the other a failed read.
func TestUpdateReportsNoReleaseNotesBetween(t *testing.T) {
	repo := newReleaseNotesUpdateFixture(t, nil)
	stdout := updateWithReleaseNotesFakes(t, repo)
	if !strings.Contains(stdout, "release notes: none between v0.9.0 and v0.10.0") {
		t.Fatalf("stdout=%q, want the none-between report line", stdout)
	}
}

// TestUpdateReportsReleaseNotesCannotList pins the error branch: a previous
// revision `git describe` cannot resolve to any tag must render as "cannot
// list", never silently fold into "none between" — an error is never
// absence.
func TestUpdateReportsReleaseNotesCannotList(t *testing.T) {
	repo := newUntaggedPreviousReleaseNotesFixture(t)
	stdout := updateWithReleaseNotesFakes(t, repo)
	if !strings.Contains(stdout, "release notes: cannot list (") {
		t.Fatalf("stdout=%q, want the cannot-list report line", stdout)
	}
	if strings.Contains(stdout, "none between") {
		t.Fatalf("stdout=%q, cannot-list collapsed into none-between", stdout)
	}
}
