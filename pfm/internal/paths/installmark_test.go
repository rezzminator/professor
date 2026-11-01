package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestInstallMark: one home reached by two spellings is one install; two
// homes are two installs, so a jail never shares the operator's mark.
func TestInstallMark(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	jail := filepath.Join(root, "jail")
	for _, directory := range []string{home, jail} {
		if err := os.Mkdir(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	linked := filepath.Join(root, "home-link")
	if err := os.Symlink(home, linked); err != nil {
		t.Fatal(err)
	}
	mark := InstallMark(home)
	if len(mark) != 16 {
		t.Fatalf("InstallMark(%s) = %q; want 16 hex characters", home, mark)
	}
	if got := InstallMark(linked); got != mark {
		t.Fatalf("InstallMark(%s) = %q; want %q, the mark of %s", linked, got, mark, home)
	}
	if got := InstallMark(jail); got == mark {
		t.Fatalf("InstallMark(%s) = %q; want a mark other than %s's", jail, got, home)
	}
	if got := InstallMark("/nonexistent/pfm-home"); got != "652b713ae48eca3e" {
		t.Fatalf("InstallMark(/nonexistent/pfm-home) = %q; want 652b713ae48eca3e", got)
	}
}

// TestMarkProcess: pfm marks its own environment with its home's mark, so the
// processes it launches carry it; with no resolved home it marks nothing.
func TestMarkProcess(t *testing.T) {
	for _, row := range []struct {
		name, home, wantErr, wantMark string
	}{
		{name: "a resolved home", home: "/nonexistent/pfm-home", wantMark: "652b713ae48eca3e"},
		{
			name:     "no home",
			wantErr:  "mark this install's processes: no resolved home",
			wantMark: "left-alone",
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Setenv(EnvInstallMark, "left-alone")
			err := MarkProcess(row.home)
			if got := fmt.Sprint(err); (err != nil || row.wantErr != "") && got != row.wantErr {
				t.Fatalf("MarkProcess(%q) = %v; want %q", row.home, err, row.wantErr)
			}
			if got := os.Getenv(EnvInstallMark); got != row.wantMark {
				t.Fatalf("%s = %q after MarkProcess(%q); want %q", EnvInstallMark, got, row.home, row.wantMark)
			}
		})
	}
}
