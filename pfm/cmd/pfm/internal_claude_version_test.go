package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hostops/pfm/internal/testjail"
)

// TestInternalClaudeVersionCommandNeverConstructsAProcessTable is DEFECT 1's
// regression: on a real host, DarwinProcFS.Image forks `lsof` once per pid,
// and `pfm internal claude-version` runs on EVERY `claude` launch — a
// process-table probe there is a whole-host cost the shim never uses (it
// only wants the newest build's path). installer.InspectClaudeVersions,
// this command's only call, structurally takes no gather.ProcFS argument at
// all (see claude_versions.go), so there is nothing this command COULD hand
// a poisoned process table even by accident; this test enforces that at the
// source, the same way TestNoEngineLiteralOutsideEnginePackage enforces its
// own invariant, so a future edit that reintroduces gather.NewProcFS here
// (a fake ProcFS whose PIDs() would fail any real caller) fails loudly.
func TestInternalClaudeVersionCommandNeverConstructsAProcessTable(t *testing.T) {
	source, err := os.ReadFile("internal_claude_version.go")
	if err != nil {
		t.Fatalf("read internal_claude_version.go: %v", err)
	}
	if strings.Contains(string(source), "gather.") {
		t.Fatalf(
			"internal_claude_version.go references the process table (gather.*) — every `claude` launch would pay for a probe it never uses:\n%s",
			source,
		)
	}
}

// TestInternalClaudeVersionPrintsNewestOrExits127 is D's Go half: the shim
// delegates the versions/ choice to `pfm internal claude-version` instead of
// picking a candidate by mtime in shell. The fixture puts the older mtime on
// the higher version, so a regression back to mtime selection fails this
// test the same way it fails the shim's own launcher_test.go fixture.
func TestInternalClaudeVersionPrintsNewestOrExits127(t *testing.T) {
	runtime := testjail.CleanHome(t)
	home := runtime.Paths.Home
	versions := filepath.Join(home, ".local", "share", "claude", "versions")
	if err := os.MkdirAll(versions, 0o700); err != nil {
		t.Fatal(err)
	}
	older := filepath.Join(versions, "2.1.263")
	newest := filepath.Join(versions, "2.1.270")
	for _, path := range []string{older, newest} {
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
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
