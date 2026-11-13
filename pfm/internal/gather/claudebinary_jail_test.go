//go:build jail

package gather

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestResolveClaudeBinaryUnreadableCandidate(t *testing.T) {
	home, dir := t.TempDir(), t.TempDir()
	path := filepath.Join(dir, "claude")
	if err := testjail.WriteExecutable(path, []byte("0123456789"), 0o755); err != nil {
		t.Fatal(err)
	}
	original := readClaudeShim
	readClaudeShim = func(string) ([]byte, error) { return nil, errors.New("input/output error") }
	t.Cleanup(func() { readClaudeShim = original })
	if !isPfmClaudeShim(path) {
		t.Error("unreadable candidate did not count as a shim")
	}
	if resolved, err := ResolveClaudeBinary(home, "", dir); !errors.Is(err, ErrClaudeBinaryNotFound) {
		t.Errorf("resolution = %q, error = %v; want ErrClaudeBinaryNotFound", resolved, err)
	}
}

func TestResolveClaudeBinaryAcceptsALargeBinaryMentioningTheShimMarker(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	directory := t.TempDir()
	body := "#!/bin/sh\n# internal claude-launch\n" + strings.Repeat("x", claudeShimMaxBytes) + "\n"
	big := filepath.Join(directory, "claude")
	if err := testjail.WriteExecutable(big, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	resolved, err := ResolveClaudeBinary(home, "", directory)
	if err != nil || resolved != big {
		t.Fatalf("resolution=%q err=%v, want the oversized binary %q accepted", resolved, err, big)
	}
}

// TestInspectClaudeVersions: no displaced-target record is no displaced
// target, but a record that exists and cannot be read names a target the prune
// cannot protect unseen, so the inspection refuses. A version removed between
// the listing and its stat (an update pruning old builds) is no version: the
// report keeps the rest.
func TestInspectClaudeVersions(t *testing.T) {
	removeOld := func(dir string) {
		if err := os.Remove(filepath.Join(dir, "2.0.0")); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct {
		name         string
		record       func(path string)
		readDir      func(dir string) ([]os.DirEntry, error)
		wantVersions []string
		wantErr      func(path string) string
	}{
		{name: "absent record", record: func(string) {}, wantVersions: []string{"2.1.0", "2.0.0"}},
		{
			name: "unreadable record",
			record: func(path string) {
				if err := os.MkdirAll(path, 0o700); err != nil { // a directory: read fails for root too
					t.Fatal(err)
				}
			},
			wantErr: func(path string) string {
				return "read displaced Claude target record " + path + ": read " + path + ": is a directory"
			},
		},
		{
			name:   "version removed before its stat",
			record: func(string) {},
			readDir: func(dir string) ([]os.DirEntry, error) {
				entries, err := os.ReadDir(dir)
				removeOld(dir)
				return entries, err
			},
			wantVersions: []string{"2.1.0"},
		},
		{
			name:   "version removed before its identity",
			record: func(string) {},
			readDir: func(dir string) ([]os.DirEntry, error) {
				entries, err := os.ReadDir(dir)
				for index, entry := range entries {
					info, infoErr := entry.Info()
					if infoErr != nil {
						t.Fatal(infoErr)
					}
					entries[index] = fs.FileInfoToDirEntry(info) // stat already taken
				}
				removeOld(dir)
				return entries, err
			},
			wantVersions: []string{"2.1.0"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			dir := ClaudeVersionsDir(home)
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"2.1.0", "2.0.0"} {
				if err := testjail.WriteExecutable(filepath.Join(dir, name), []byte("claude"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if test.readDir != nil {
				original := readClaudeVersionsDir
				readClaudeVersionsDir = test.readDir
				t.Cleanup(func() { readClaudeVersionsDir = original })
			}
			statePath := ClaudeLauncherStatePath(home)
			test.record(statePath)
			report, err := InspectClaudeVersions(home, "")
			if test.wantErr != nil {
				if want := test.wantErr(statePath); err == nil || err.Error() != want {
					t.Fatalf("InspectClaudeVersions() error = %v, want %q", err, want)
				}
				return
			}
			got := make([]string, 0, len(report.Versions))
			for _, version := range report.Versions {
				got = append(got, filepath.Base(version.Path))
			}
			if err != nil || !slices.Equal(got, test.wantVersions) {
				t.Fatalf(
					"InspectClaudeVersions() versions = %q, err = %v; want %q and no error",
					got, err, test.wantVersions,
				)
			}
		})
	}
}
