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

// RestorePending is SyncOn plus any surface snapshot an earlier off left behind.
func TestRestorePendingReadsTheStateFile(t *testing.T) {
	cases := []struct {
		name    string
		content string // "" = no state file
		want    bool
	}{
		{name: "no state file", content: "", want: false},
		{name: "enabled", content: `{"enabled":true}`, want: true},
		{name: "off with every surface restored", content: `{"enabled":false}`, want: false},
		{name: "claude snapshot left", content: `{"enabled":false,"claude":{"/p":"x"}}`, want: true},
		{
			name:    "herdr snapshot left",
			content: `{"enabled":false,"herdr":{"span":"s","added":"","backup":"b"}}`,
			want:    true,
		},
		{
			name:    "ghostty snapshot left",
			content: `{"enabled":false,"ghostty":{"config":"c","added":"a","backup":"b"}}`,
			want:    true,
		},
		{name: "vscode snapshot left", content: `{"enabled":false,"vscode":{"/p":"x"}}`, want: true},
		{name: "off left pending", content: `{"enabled":false,"offPending":true}`, want: true},
		{name: "unparseable state", content: `{`, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.content != "" {
				if err := os.WriteFile(filepath.Join(dir, StateFileName), []byte(tc.content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if got := RestorePending(dir); got != tc.want {
				t.Fatalf("RestorePending = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDefaultStateDir(t *testing.T) {
	if got, want := DefaultStateDir("/h"), "/h/.local/state/pfm/theme"; got != want {
		t.Fatalf("DefaultStateDir = %q, want %q", got, want)
	}
}
