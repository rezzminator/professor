package opencodegen

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenCodeMarkerClaimsNewAndOldOutputs(t *testing.T) {
	for _, marker := range []string{newMarker, oldMarker} {
		if !hasMarker(marker + " from fixture") {
			t.Fatalf("marker %q was not claimable", marker)
		}
	}
}

func TestOpenCodeOrphanLinksOnlyClaimClaudeTargets(t *testing.T) {
	managed := t.TempDir()
	operatorTarget := filepath.Join(t.TempDir(), "operator-skill")
	if err := os.WriteFile(operatorTarget, []byte("operator\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	operatorLink := filepath.Join(managed, "operator-skill")
	if err := os.Symlink(operatorTarget, operatorLink); err != nil {
		t.Fatal(err)
	}

	claudeTarget := filepath.Join(t.TempDir(), ".claude", "skills", "owned-skill")
	if err := os.MkdirAll(filepath.Dir(claudeTarget), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(claudeTarget, []byte("compiler\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	compilerLink := filepath.Join(managed, "owned-skill")
	if err := os.Symlink(claudeTarget, compilerLink); err != nil {
		t.Fatal(err)
	}

	result := reconcileResult{}
	reconcileOpenCodeOrphans(&result, managed, map[string]bool{}, ModeBuild)

	if _, err := os.Lstat(operatorLink); err != nil {
		t.Fatalf("operator symlink was removed: %v", err)
	}
	if _, err := os.Lstat(compilerLink); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("compiler-owned symlink was not reclaimed: err=%v", err)
	}
	if result.Deleted != 1 {
		t.Fatalf("deleted=%d, want 1", result.Deleted)
	}
	if strings.Contains(strings.Join(result.Problems, "\n"), "ORPHAN "+operatorLink) {
		t.Fatalf("operator symlink was reported as orphan: %#v", result.Problems)
	}
}
