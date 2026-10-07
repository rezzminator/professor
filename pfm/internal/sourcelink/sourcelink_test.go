package sourcelink_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/sourcelink"
)

func TestLinkTargetReadsTheLinkTextAndNamesAnUnreadableLink(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "labber.md")
	if err := os.Symlink("../vendor/lab/labber.md", link); err != nil {
		t.Fatal(err)
	}
	if got := sourcelink.LinkTarget(link); got != "../vendor/lab/labber.md" {
		t.Fatalf("LinkTarget = %q, want the link text", got)
	}
	if got := sourcelink.LinkTarget(filepath.Join(dir, "missing.md")); !strings.HasPrefix(got, "unreadable link (") {
		t.Fatalf("LinkTarget of a missing link = %q, want the read failure named", got)
	}
}

func TestKeepTwinWarnsOnAnExistingTwinAndIsSilentWithoutOne(t *testing.T) {
	dir := t.TempDir()
	twin := filepath.Join(dir, "labber.toml")
	warning, problem := sourcelink.KeepTwin(twin, "/home/test/src.md", "../gone.md")
	if warning != "" || problem != "" {
		t.Fatalf("absent twin: warning=%q problem=%q, want neither", warning, problem)
	}
	if err := os.WriteFile(twin, []byte("twin\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	warning, problem = sourcelink.KeepTwin(twin, "/home/test/src.md", "../gone.md")
	if warning != "source unresolvable: /home/test/src.md → ../gone.md; twin kept" || problem != "" {
		t.Fatalf("existing twin: warning=%q problem=%q", warning, problem)
	}
}

func TestKeepTwinNamesATwinItCannotInspect(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A path under a regular file fails Lstat with ENOTDIR, not ENOENT, even as root.
	warning, problem := sourcelink.KeepTwin(filepath.Join(file, "twin"), "/home/test/src.md", "../gone.md")
	if warning != "" || !strings.HasPrefix(problem, "inspect kept twin ") {
		t.Fatalf("uninspectable twin: warning=%q problem=%q", warning, problem)
	}
}
