//go:build jail

package usagehook

// The logged-out claude binary is the subject: its real runs must land in throwaway homes, never the real
// Claude home.

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// TestClaudeProbesNeverWriteTheRealClaudeHome: the dependency probe (version
// and self-doctor, as pfm doctor and pfm install run it) and the usage hook's
// version read each run a logged-out claude. Run with the ambient env, it
// recreated ~/.claude/backups and $HOME/.claude.json and the host check then
// blocked the machine; each run now gets a throwaway home that is gone after.
func TestClaudeProbesNeverWriteTheRealClaudeHome(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	sid := filepath.Join(t.TempDir(), "sid")
	t.Setenv(paths.EnvSIDDir, sid)
	scratch := t.TempDir()
	record := filepath.Join(scratch, "config-dirs.log")
	binary := filepath.Join(scratch, "claude")
	if err := testjail.WriteLoggedOutClaude(binary, record, ""); err != nil {
		t.Fatal(err)
	}
	var entries []deps.Entry
	registry := deps.Registry(deps.Options{Home: home, ClaudeBinary: binary})
	for index := range registry {
		if registry[index].Engine == pfmengine.Claude {
			entries = append(entries, registry[index])
		}
	}
	results := deps.Probe(context.Background(), entries, deps.ProbeOptions{GOOS: runtime.GOOS})
	if len(results) != 1 || results[0].State != deps.StateOK || results[0].SelfDoctor != "ok" {
		t.Fatalf("dependency probe = %#v, want one healthy claude", results)
	}
	version, err := claudeCodeVersion(context.Background(), binary)
	if err != nil || version != "2.1.238" {
		t.Fatalf("usage hook version = %q, %v", version, err)
	}
	if runs := testjail.AssertClaudeRanInThrowawayHomes(t, home, sid, record); runs != 4 {
		t.Errorf("the fake recorded %d runs, want 4 (--version, doctor --help, doctor, the usage --version)", runs)
	}
}
