package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// TestInternalClaudeVersionPrintsNewestOrExits127 is D's Go half: the shim
// delegates the versions/ choice to `pfm internal claude-version` instead of
// picking a candidate by mtime in shell. The fixture puts the older mtime on
// the higher version, so a regression back to mtime selection fails this
// test the same way it fails the shim's own launcher_test.go fixture.
func TestInternalClaudeVersionPrintsNewestOrExits127(t *testing.T) {
	dirs, files := storeLayout()
	runtime := testjail.CleanHome(t, dirs, files)
	home := runtime.Paths.Home
	versions := filepath.Join(home, ".local", "share", "claude", "versions")
	if err := os.MkdirAll(versions, 0o700); err != nil {
		t.Fatal(err)
	}
	older := filepath.Join(versions, "2.1.263")
	newest := filepath.Join(versions, "2.1.270")
	for _, path := range []string{older, newest} {
		if err := testjail.WriteExecutable(path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	stamp := time.Now().Add(-time.Minute)
	if err := os.Chtimes(older, stamp.Add(time.Minute), stamp.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newest, stamp, stamp); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"internal", "claude-version"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("internal claude-version code=%d stderr=%q", code, stderr.String())
	}
	if strings.TrimSpace(stdout.String()) != newest {
		t.Fatalf("internal claude-version=%q, want highest version %q", stdout.String(), newest)
	}

	if err := os.RemoveAll(versions); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	code = run([]string{"internal", "claude-version"}, &stdout, &stderr)
	if code != 127 {
		t.Fatalf("internal claude-version with no versions code=%d, want 127", code)
	}
	if stdout.String() != "" {
		t.Fatalf("internal claude-version with no versions stdout=%q, want empty", stdout.String())
	}
}
