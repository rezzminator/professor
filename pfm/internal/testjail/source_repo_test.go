package testjail

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestStageSourceRepoMarkerRecordsACloneOfTrackedFilesOnly(t *testing.T) {
	home := t.TempDir()
	StageSourceRepoMarker(t, home)
	clone, err := paths.ReadSourceRepoMarker(home)
	if err != nil {
		t.Fatalf("read the staged marker in %s: %v", home, err)
	}
	if want := filepath.Join(filepath.Dir(runSourceRepo), "installed-clone"); clone != want {
		t.Fatalf("staged clone = %q, want %q", clone, want)
	}
	var names []string
	for _, dir := range []string{"pfm/harness-prompts", "templates/global", "pfm/internal/pricing"} {
		entries, err := os.ReadDir(filepath.Join(clone, dir))
		if err != nil {
			t.Fatalf("list %s in the clone %s: %v", dir, clone, err)
		}
		for _, entry := range entries {
			names = append(names, dir+"/"+entry.Name())
		}
	}
	want := "pfm/harness-prompts/claude pfm/harness-prompts/codex pfm/harness-prompts/opencode " +
		"pfm/harness-prompts/share templates/global/retired.json templates/global/rules templates/global/skills " +
		"pfm/internal/pricing/prices.json"
	if got := strings.Join(names, " "); got != want {
		t.Fatalf("clone %s holds %q, want %q", clone, got, want)
	}
	retired := filepath.Join("templates", "global", "retired.json")
	copied, copyErr := os.ReadFile(filepath.Join(clone, retired))
	tracked, trackedErr := os.ReadFile(filepath.Join(checkoutRoot(), retired))
	if got, wantText := fmt.Sprint(
		bytes.Equal(copied, tracked),
		copyErr,
		trackedErr,
	), "true <nil> <nil>"; got != wantText {
		t.Fatalf("retired.json copy equals the checkout's, errors = %s, want %s (clone %s)", got, wantText, clone)
	}
}

// TestCopyTrackedPathsLeavesHostLeftoversBehind: below a listed directory the
// copy keeps the tracked kinds and drops what a host leaves beside them, so the
// installed clone reads the same on a Mac with a .DS_Store or an editor's swap
// file in templates/global/rules as on a fresh clone.
func TestCopyTrackedPathsLeavesHostLeftoversBehind(t *testing.T) {
	from, to := t.TempDir(), t.TempDir()
	for _, relative := range []string{
		"rules/a.md", "rules/.DS_Store", "rules/a.md.swp", "rules/b.bak", "rules/.hidden/c.md",
		"prompts/baselines/x.model", "prompts/baselines/x.sha256", "prices.json",
	} {
		path := filepath.Join(from, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(relative), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := copyTrackedPaths(from, to, []string{"rules", "prompts", "prices.json"}); err != nil {
		t.Fatalf("copyTrackedPaths(%s, %s) = %v", from, to, err)
	}
	var copied []string
	walkErr := filepath.WalkDir(to, func(path string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			copied = append(copied, path[len(to)+1:])
		}
		return err
	})
	want := "prices.json prompts/baselines/x.model prompts/baselines/x.sha256 rules/a.md"
	if got := strings.Join(copied, " "); got != want || walkErr != nil {
		t.Fatalf("copied %q (walk %v), want %q (from %s to %s)", got, walkErr, want, from, to)
	}
}
