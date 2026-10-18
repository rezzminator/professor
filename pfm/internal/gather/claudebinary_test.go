package gather

import (
	"errors"
	"path/filepath"
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
