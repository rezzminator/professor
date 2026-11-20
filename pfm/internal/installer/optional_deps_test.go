package installer

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

// TestOptionalDepsRecordNamesWhatThisInstallFound proves install records the
// optional dependencies it found, only those, and rewrites nothing on a
// second run; a lookup that could not run keeps the last record.
func TestOptionalDepsRecordNamesWhatThisInstallFound(t *testing.T) {
	home := t.TempDir()
	runner := &deps.FakeRunner{}
	runner.ScriptLookPath("herdr", "/fixture/bin/herdr", nil)
	runner.ScriptLookPath("git", "/fixture/bin/git", nil)
	var out bytes.Buffer
	installer := &engine{
		options: Options{Mode: ModeApply, Home: home, Stdout: &out, ProcessRunner: runner},
		apply:   true,
	}
	if err := installer.writeOptionalDepsRecord(); err != nil {
		t.Fatalf("writeOptionalDepsRecord: %v", err)
	}
	record, err := ReadOptionalDepsRecord(home)
	if err != nil {
		t.Fatalf("ReadOptionalDepsRecord: %v", err)
	}
	if !slices.Equal(record.Present, []string{"herdr"}) {
		t.Errorf("present = %v, want [herdr] (git is required, so never recorded)", record.Present)
	}
	if !slices.Contains(record.Checked, "herdr") || !slices.Contains(record.Checked, "uv") ||
		slices.Contains(record.Checked, "git") || slices.Contains(record.Checked, "launchctl") {
		t.Errorf("checked = %v, want herdr and uv, never required git or darwin-only launchctl", record.Checked)
	}
	path := filepath.Join(home, ".local", "share", "pfm", "install", "optional-deps.json")
	before, _ := os.ReadFile(path)
	out.Reset()
	if err := installer.writeOptionalDepsRecord(); err != nil {
		t.Fatalf("second writeOptionalDepsRecord: %v", err)
	}
	if want := "  ok      " + path + "\n"; out.String() != want {
		t.Errorf("second run said %q, want %q", out.String(), want)
	}
	runner.ScriptLookPath("uv", "", errors.New("permission denied"))
	out.Reset()
	if err := installer.writeOptionalDepsRecord(); err != nil {
		t.Fatalf("third writeOptionalDepsRecord: %v", err)
	}
	if want := "  skip    optional-deps record kept: look up uv: permission denied\n"; out.String() != want {
		t.Errorf("unprobeable run said %q, want %q", out.String(), want)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(before, after) {
		t.Errorf("unprobeable run rewrote %s:\n%s\nwant\n%s", path, after, before)
	}
}

func TestOptionalDepsAppearedNamesOnlyWhatTheRecordLookedForAndMissed(t *testing.T) {
	record := OptionalDepsRecord{Checked: []string{"herdr", "uv", "zsh"}, Present: []string{"zsh"}}
	now := OptionalDepsRecord{
		Checked: []string{"herdr", "uv", "zsh", "newtool"},
		Present: []string{"herdr", "zsh", "newtool"},
	}
	if got := OptionalDepsAppeared(record, now); !slices.Equal(got, []string{"herdr"}) {
		t.Errorf("appeared = %v, want [herdr]", got)
	}
}
