package archive

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"hostops/pfm/internal/gather"
)

// failingProc reports a specific process-table error, so a test can prove
// LiveSessions wraps it rather than replacing it with a generic one.
type failingProc struct{ err error }

func (proc failingProc) PIDs() ([]int, error)              { return nil, proc.err }
func (failingProc) Cmdline(int) ([]string, error)          { return nil, nil }
func (failingProc) Environ(int) (map[string]string, error) { return nil, nil }
func (failingProc) FDLinks(int) ([]gather.FDLink, error)   { return nil, nil }
func (failingProc) Stat(int) (gather.ProcStat, error)      { return gather.ProcStat{}, nil }

// LiveSessions is the only safety gate between archive --apply and a running
// chat's transcript. A reading that could not run — the process table or the
// sid crumb directory — must be reported as an error, never folded into "no
// live chats": that silent narrowing is exactly what let a live chat's
// transcript be archived out from under it (L1-F1).

func TestLiveSessionsErrorsWhenProcessTableUnreadable(t *testing.T) {
	sidDir := t.TempDir()
	_, err := LiveSessions(emptyProc{failPIDs: true}, filepath.Join(t.TempDir(), "codex"), sidDir)
	if err == nil {
		t.Fatal("LiveSessions() with an unreadable process table returned no error")
	}
}

func TestLiveSessionsErrorsWhenSidDirUnreadable(t *testing.T) {
	root := t.TempDir()
	// A file where a directory is expected fails ReadDir with something
	// other than fs.ErrNotExist — the "could not look" case, not absence.
	sidDir := filepath.Join(root, "sid-is-a-file")
	if err := os.WriteFile(sidDir, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LiveSessions(emptyProc{}, filepath.Join(root, "codex"), sidDir)
	if err == nil {
		t.Fatal("LiveSessions() with an unreadable sid directory returned no error")
	}
}

// A sid directory that has never been created — no chat has ever spawned on
// this box — is genuine absence, not a failed reading, and must not error.
func TestLiveSessionsToleratesAMissingSidDir(t *testing.T) {
	root := t.TempDir()
	live, err := LiveSessions(emptyProc{}, filepath.Join(root, "codex"), filepath.Join(root, "never-created"))
	if err != nil {
		t.Fatalf("LiveSessions() with a never-created sid dir returned an error: %v", err)
	}
	if len(live) != 0 {
		t.Fatalf("live = %v, want empty", live)
	}
}

func TestLiveSessionsWrapsTheProcessTableError(t *testing.T) {
	sentinel := errors.New("boom")
	_, err := LiveSessions(failingProc{err: sentinel}, filepath.Join(t.TempDir(), "codex"), t.TempDir())
	if !errors.Is(err, sentinel) {
		t.Fatalf("LiveSessions() error = %v, want it to wrap %v", err, sentinel)
	}
}
