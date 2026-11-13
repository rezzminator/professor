package installer

// A git whose grandchild keeps stderr open must still return within its bound: the real process group and pipes are the subject.

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// TestRunSkillGitReturnsWithinItsBound pins F4: a git call whose grandchild
// keeps stderr open still returns shortly after its timeout, carrying git's
// stderr tail.
func TestRunSkillGitReturnsWithinItsBound(t *testing.T) {
	t.Parallel()
	script := filepath.Join(t.TempDir(), "git")
	if err := testjail.WriteExecutable(
		script,
		[]byte("#!/bin/sh\necho 'fatal: slow remote' >&2\nsleep 12 &\nsleep 12\n"),
		0o755,
	); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err := runSkillGitWith(
		deps.RealRunner{},
		200*time.Millisecond,
		100*time.Millisecond,
		script,
		t.TempDir(),
		"clone",
	)
	if elapsed := time.Since(start); elapsed > 8*time.Second {
		t.Fatalf("git returned after %s, past its bound (err=%v)", elapsed, err)
	}
	if err == nil || !strings.Contains(err.Error(), "timed out") ||
		!strings.Contains(err.Error(), "fatal: slow remote") {
		t.Fatalf("timeout error does not carry git's stderr tail: %v", err)
	}
}
