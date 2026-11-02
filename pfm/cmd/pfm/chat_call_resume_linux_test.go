//go:build linux

package main

import (
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
)

// withoutDirectoryOverride runs fn with this thread's filesystem identity
// dropped to an unprivileged user, so a mode-000 directory is unreadable even
// when the suite runs as root (the fence); a non-root run needs no drop.
func withoutDirectoryOverride(t *testing.T, fn func()) {
	t.Helper()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if os.Geteuid() == 0 {
		if err := syscall.Setfsuid(65534); err != nil {
			t.Fatalf("drop the filesystem uid: %v", err)
		}
		defer func() {
			if err := syscall.Setfsuid(0); err != nil {
				t.Errorf("restore the filesystem uid: %v", err)
			}
		}()
	}
	fn()
}

// A session id prefix resolves only when no project directory went unread:
// a transcript in the sealed directory could be a second match, so the
// uniqueness proof is gone and the full id is asked for. An exact id is
// unique by itself and still resumes.
func TestResolveResumeTargetRefusesAPrefixWhenAProjectDirectoryCannotBeRead(t *testing.T) {
	const id = "c3000000-3333-4333-8333-333333333333"
	for _, testCase := range []struct {
		name    string
		target  string
		sealed  bool
		wantErr string
	}{
		{
			name: "prefix with a sealed directory", target: id[:8], sealed: true,
			wantErr: `session id prefix "c3000000": 1 transcript project director(ies) could not be read, ` +
				"so the match may not be unique; give the full session id",
		},
		{name: "exact id with a sealed directory", target: id, sealed: true},
		{name: "prefix with every directory readable", target: id[:8]},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			root := t.TempDir()
			projects := filepath.Join(root, "projects")
			writeResumeTranscript(t, projects, id, "unrelated words")
			// The fsuid drop reads the tree as "other": open every level it walks.
			for _, open := range []string{filepath.Dir(root), root, projects, filepath.Join(projects, "project")} {
				if err := os.Chmod(open, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Chmod(filepath.Join(projects, "project", id+".jsonl"), 0o644); err != nil {
				t.Fatal(err)
			}
			if testCase.sealed {
				sealed := filepath.Join(projects, "sealed")
				if err := os.Mkdir(sealed, 0o000); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(sealed, 0o700) })
			}
			withoutDirectoryOverride(t, func() {
				target, found, err := resolveResumeTarget(testCase.target, resumeTranscriptRuntime(root, projects))
				if testCase.wantErr != "" {
					if found || err == nil || err.Error() != testCase.wantErr {
						t.Fatalf("resolveResumeTarget(%q) = %+v, %v, %v; want the refusal %q",
							testCase.target, target, found, err, testCase.wantErr)
					}
					return
				}
				wantPath := filepath.Join(projects, "project", id+".jsonl")
				if err != nil || !found || target.ID != id || target.Path != wantPath {
					t.Fatalf("resolveResumeTarget(%q) = %+v, %v, %v; want %s at %s",
						testCase.target, target, found, err, id, wantPath)
				}
			})
		})
	}
}
