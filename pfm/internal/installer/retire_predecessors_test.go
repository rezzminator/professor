package installer

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/retired"
)

// TestRetirePredecessorsRemovesConfigFiles pins the configFiles door: a
// literal path and every glob match retire under their reason, and a file
// the lists do not name stays.
func TestRetirePredecessorsRemovesConfigFiles(t *testing.T) {
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	literal := filepath.Join(config, "bin", "cx-kill.sh")
	backup := filepath.Join(config, "bin", "cx-recover.sh.pre-professor-20260101")
	kept := filepath.Join(config, "bin", "cx-recover.sh")
	for _, path := range []string{literal, backup, kept} {
		writeFixture(t, path, "old\n")
	}
	var output bytes.Buffer
	installer := &engine{
		options: Options{
			Home: home, ConfigDir: config, Stdout: &output,
			retired: &retired.Lists{ConfigFiles: []retired.ConfigFile{
				{Path: "bin/cx-kill.sh", Reason: "native pfm command"},
				{Path: "bin/cx-recover.sh.pre-professor-*", Reason: "retired recovery artifact"},
			}},
		},
		apply: true,
	}
	if err := installer.retirePredecessors(); err != nil {
		t.Fatal(err)
	}
	want := "  change  retire " + literal + " (native pfm command)\n" +
		"  change  retire " + backup + " (retired recovery artifact)\n"
	if output.String() != want {
		t.Fatalf("output = %q, want %q", output.String(), want)
	}
	for _, path := range []string{literal, backup} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("retired %s survives: %v", path, err)
		}
	}
	if got := readFixture(t, kept); got != "old\n" {
		t.Fatalf("unlisted %s = %q, want it untouched", kept, got)
	}
}
