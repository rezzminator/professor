package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestPFMPathWarningsCatchResolutionAndHashShadows(t *testing.T) {
	home := t.TempDir()
	canonicalDir := filepath.Join(home, ".local", "bin")
	shadowDir := filepath.Join(home, "toolchain", "bin")
	for _, directory := range []string{canonicalDir, shadowDir} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	canonical := filepath.Join(canonicalDir, "pfm")
	shadow := filepath.Join(shadowDir, "pfm")
	if err := testjail.WriteExecutableNeverRun(canonical, []byte("production-candidate"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := testjail.WriteExecutableNeverRun(shadow, []byte("stale-shadow"), 0o700); err != nil {
		t.Fatal(err)
	}

	warnings := pfmPathWarnings(home, shadowDir+string(os.PathListSeparator)+canonicalDir)
	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, "pfm_path_resolves=") ||
		!strings.Contains(joined, "pfm_hash_mismatch=") ||
		!strings.Contains(joined, shadow) {
		t.Fatalf("warnings = %q, want resolution and hash mismatch for shadow", joined)
	}

	if err := testjail.WriteExecutableNeverRun(shadow, []byte("production-candidate"), 0o700); err != nil {
		t.Fatal(err)
	}
	warnings = pfmPathWarnings(home, canonicalDir+string(os.PathListSeparator)+shadowDir)
	if len(warnings) != 0 {
		t.Fatalf("matching canonical-first PATH warnings = %q, want none", warnings)
	}

	// A canonical binary that cannot be hashed still leaves the resolution order reported.
	if err := os.Remove(canonical); err != nil {
		t.Fatal(err)
	}
	warnings = pfmPathWarnings(home, shadowDir+string(os.PathListSeparator)+canonicalDir)
	want := []string{
		"pfm_canonical=" + canonical + " error=open " + canonical + ": no such file or directory",
		"pfm_path_resolves=" + shadow + " canonical=" + canonical,
	}
	if strings.Join(warnings, "\n") != strings.Join(want, "\n") {
		t.Fatalf("unhashable canonical warnings = %q, want %q", warnings, want)
	}
}
