package paths

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	pfmengine "hostops/pfm/internal/engine"
)

func TestResolveOverrides(t *testing.T) {
	testRoot := t.TempDir()
	t.Setenv("TMUX_TMPDIR", filepath.Join(testRoot, "t"))
	home := filepath.Join(testRoot, "home")
	db := filepath.Join(testRoot, "fleet.db")
	sidDir := filepath.Join(testRoot, "sid")
	claudeRoots := []string{
		filepath.Join(testRoot, "claude-1"),
		filepath.Join(testRoot, "claude-2"),
	}
	codexHome := filepath.Join(testRoot, "codex")
	tmuxDir := filepath.Join(testRoot, "tmux")
	procRoot := filepath.Join(testRoot, "proc")
	cgroupRoot := filepath.Join(testRoot, "cgroup")
	fleetDB := filepath.Join(testRoot, "shared", "fleet.db")

	t.Setenv(EnvHome, home)
	t.Setenv(EnvDB, db)
	t.Setenv(EnvFleetDB, fleetDB)
	t.Setenv(EnvSIDDir, sidDir)
	t.Setenv(EnvClaudeRoots, strings.Join(claudeRoots, string(os.PathListSeparator)))
	t.Setenv(EnvCodexHome, codexHome)
	opencodeRoot := filepath.Join(testRoot, "opencode")
	t.Setenv(EnvOpenCodeRoot, opencodeRoot)
	t.Setenv(EnvTmuxDir, tmuxDir)
	t.Setenv(EnvProcRoot, procRoot)
	t.Setenv(EnvCgroupRoot, cgroupRoot)

	got, err := Resolve()
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	want := Values{
		DB:      db,
		FleetDB: fleetDB,
		SIDDir:  sidDir,
		Roots: map[pfmengine.ID][]string{
			pfmengine.Claude:   claudeRoots,
			pfmengine.Codex:    {codexHome},
			pfmengine.OpenCode: {opencodeRoot},
		},
		TmuxDir: tmuxDir,
		Home:    home,
		// The carrier and the archive have no override of their own: both are
		// defined relative to Home, and jailing Home jails them.
		ArchiveDir: filepath.Join(home, ".claude-archive"),
		ProcRoot:   procRoot,
		CgroupRoot: cgroupRoot,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Resolve() = %#v, want %#v", got, want)
	}
}

func TestDevRepoGitDirMatchesSymlinkedWorktreeRoot(t *testing.T) {
	physical := t.TempDir()
	alias := filepath.Join(t.TempDir(), "blueprint")
	if err := os.Symlink(physical, alias); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvDevRepoWorkTree, "  "+physical+"  ")
	t.Setenv(EnvDevRepoGitDir, "  /fence/git-dir  ")

	gitDir, ok := DevRepoGitDir(alias)
	if !ok || gitDir != "/fence/git-dir" {
		t.Fatalf("DevRepoGitDir(%q) = %q, %t; want physical match", alias, gitDir, ok)
	}
}

func TestDevRepoGitDirLogsPhysicalResolutionFailureBeforeCleanFallback(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing-worktree")
	t.Setenv(EnvDevRepoWorkTree, missing)
	t.Setenv(EnvDevRepoGitDir, "/fence/git-dir")
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	previousStderr := os.Stderr
	os.Stderr = writer
	gitDir, ok := DevRepoGitDir(missing)
	os.Stderr = previousStderr
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	logged, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if !ok || gitDir != "/fence/git-dir" {
		t.Fatalf("DevRepoGitDir(%q) = %q, %t; want cleaned fallback match", missing, gitDir, ok)
	}
	if !strings.Contains(string(logged), missing) || !strings.Contains(string(logged), "falling back") {
		t.Fatalf("stderr = %q, want path and fallback diagnostic", logged)
	}
}

// The fleet state store defaults to ~/.cc/fleet.db, never the private cache's
// directory, and the two overrides must not collide.
func TestResolveFleetStoreDefaultsOutsideThePrivateCache(t *testing.T) {
	testRoot := t.TempDir()
	home := filepath.Join(testRoot, "home")
	t.Setenv(EnvHome, home)
	t.Setenv(EnvDB, filepath.Join(testRoot, "cache", "fleet.db"))
	t.Setenv(EnvFleetDB, "")

	got, err := Resolve()
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	want := filepath.Join(home, ".cc", "fleet.db")
	if got.FleetDB != want {
		t.Fatalf("Resolve().FleetDB = %q, want %q", got.FleetDB, want)
	}
	if got.FleetDB == got.DB {
		t.Fatalf("fleet store and private cache resolved to the same file %q", got.DB)
	}
}

func TestResolveDoesNotFabricateClaudeAccountRoots(t *testing.T) {
	testRoot := t.TempDir()
	home := filepath.Join(testRoot, "home")
	t.Setenv(EnvHome, home)
	t.Setenv(EnvClaudeRoots, "")

	got, err := Resolve()
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	wantClaude := pfmengine.MustLookup(pfmengine.Claude).DefaultRoots(home)
	if !reflect.DeepEqual(got.Roots[pfmengine.Claude], wantClaude) {
		t.Fatalf("Resolve().Roots[cc] = %#v, want %#v", got.Roots[pfmengine.Claude], wantClaude)
	}
}

// The OpenCode root defaults under Home and jails with it; the explicit
// override wins over the default.
func TestResolveOpenCodeRootDefaultsUnderHome(t *testing.T) {
	testRoot := t.TempDir()
	home := filepath.Join(testRoot, "home")
	t.Setenv(EnvHome, home)
	t.Setenv(EnvOpenCodeRoot, "")

	got, err := Resolve()
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	want := filepath.Join(home, ".local", "share", "opencode")
	if roots := got.Roots[pfmengine.OpenCode]; len(roots) != 1 || roots[0] != want {
		t.Fatalf("Resolve().Roots[ox] = %#v, want %q", roots, want)
	}
}

func TestResolveUsesScratchTmuxBase(t *testing.T) {
	testRoot := t.TempDir()
	t.Setenv(EnvHome, filepath.Join(testRoot, "home"))
	t.Setenv(EnvDB, filepath.Join(testRoot, "fleet.db"))
	t.Setenv(EnvSIDDir, filepath.Join(testRoot, "sid"))
	t.Setenv(EnvClaudeRoots, filepath.Join(testRoot, "claude"))
	t.Setenv(EnvCodexHome, filepath.Join(testRoot, "codex"))
	t.Setenv(EnvTmuxDir, "")
	t.Setenv(EnvProcRoot, filepath.Join(testRoot, "proc"))
	t.Setenv("TMUX_TMPDIR", testRoot)

	got, err := Resolve()
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	want := filepath.Join(testRoot, "tmux-"+strconvUID())
	if got.TmuxDir != want {
		t.Fatalf("Resolve().TmuxDir = %q, want %q", got.TmuxDir, want)
	}
}

// TestFirstRootIsTheEnginesFirstConfiguredRoot pins the single-root read and
// its empty answer for an engine with no roots.
func TestFirstRootIsTheEnginesFirstConfiguredRoot(t *testing.T) {
	values := Values{Roots: map[pfmengine.ID][]string{pfmengine.Codex: {"/r/codex-1", "/r/codex-2"}}}
	if got := values.FirstRoot(pfmengine.Codex); got != "/r/codex-1" {
		t.Fatalf("FirstRoot(codex) = %q", got)
	}
	if got := values.FirstRoot(pfmengine.Claude); got != "" {
		t.Fatalf("FirstRoot(claude) = %q, want empty", got)
	}
}

// TestSocketUnderKeepsTheSocketInsideTheTmuxDir pins the one socket
// guard: a bare name joins the tmux directory; anything that could dial a
// server elsewhere — absolute, nested, dot names — or an unset directory is
// refused.
func TestSocketUnderKeepsTheSocketInsideTheTmuxDir(t *testing.T) {
	values := Values{TmuxDir: "/jail/tmux"}
	if got, err := values.SocketUnder("cc-1-2-3"); err != nil || got != "/jail/tmux/cc-1-2-3" {
		t.Fatalf("SocketUnder(cc-1-2-3) = %q, %v", got, err)
	}
	for _, socket := range []string{"", ".", "..", "../escape", "/tmp/elsewhere", "nested/name"} {
		if got, err := values.SocketUnder(socket); err == nil {
			t.Fatalf("SocketUnder(%q) = %q; want it refused", socket, got)
		}
	}
	if _, err := (Values{}).SocketUnder("cc-1-2-3"); err == nil {
		t.Fatal("SocketUnder with no tmux directory answered a path")
	}
}
