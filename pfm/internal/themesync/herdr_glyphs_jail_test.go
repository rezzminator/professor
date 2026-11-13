//go:build jail

// Subject: a child of the test binary that exhausts its own file descriptors (RLIMIT_NOFILE) before the glyph read.

package themesync

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// Exhaust descriptors only in a child: stat still succeeds while the production
// stateStore.load cannot open the body. Recovery must not need a file rewrite.
func TestHerdrTabGlyphsRecoversAfterTransientBodyReadFailure(t *testing.T) {
	if os.Getenv("PFM_GLYPH_EMFILE_CHILD") != "1" {
		command := exec.Command(os.Args[0], "-test.run=^TestHerdrTabGlyphsRecoversAfterTransientBodyReadFailure$")
		command.Env = append(os.Environ(), "PFM_GLYPH_EMFILE_CHILD=1")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("glyph EMFILE child: %v\n%s", err, output)
		}
		return
	}
	testjail.PauseFlightRecorder(t)
	dir := t.TempDir()
	path := filepath.Join(dir, StateFileName)
	content := `{"enabled":true,"applied":{"pins":{"herdr":{"tabGlyphs":{"blocked":["X","Y"]}}}}}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	var limit syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &limit); err != nil {
		t.Fatal(err)
	}
	limited := limit
	limited.Cur = 128
	if err := syscall.Setrlimit(syscall.RLIMIT_NOFILE, &limited); err != nil {
		t.Fatal(err)
	}
	var files []*os.File
	for {
		file, err := os.Open(os.DevNull)
		if errors.Is(err, syscall.EMFILE) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, file)
	}
	_, readErr := HerdrTabGlyphs(dir)
	for _, file := range files {
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if err := syscall.Setrlimit(syscall.RLIMIT_NOFILE, &limit); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(readErr, syscall.EMFILE) {
		t.Fatalf("body open = %v, want EMFILE", readErr)
	}
	after, err := os.Stat(path)
	if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() ||
		!before.ModTime().Equal(after.ModTime()) || before.Mode() != after.Mode() {
		t.Fatalf("state identity changed: %v", err)
	}
	glyphs, err := HerdrTabGlyphs(dir)
	if err != nil || !reflect.DeepEqual(glyphs.Blocked, herdr.TabFrames{"X", "Y"}) {
		t.Fatalf("unchanged state after descriptor recovery = %+v, %v; want X/Y", glyphs, err)
	}
}
