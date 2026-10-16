package themesync

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSyncOnReadsTheStateFile(t *testing.T) {
	cases := []struct {
		name    string
		content string // "" = no state file
		want    bool
	}{
		{name: "no state file is off", content: "", want: false},
		{name: "enabled true", content: `{"enabled":true}`, want: true},
		{name: "enabled false after off", content: `{"enabled":false}`, want: false},
		{name: "unparseable state errs toward running", content: `{`, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.content != "" {
				if err := os.WriteFile(filepath.Join(dir, StateFileName), []byte(tc.content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if got := SyncOn(dir); got != tc.want {
				t.Fatalf("SyncOn = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDefaultStateDir(t *testing.T) {
	if got, want := DefaultStateDir("/h"), "/h/.local/state/pfm/theme"; got != want {
		t.Fatalf("DefaultStateDir = %q, want %q", got, want)
	}
}
