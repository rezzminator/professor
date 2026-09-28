package paths

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

func TestResolveOverrides(t *testing.T) {
	testRoot := t.TempDir()
	t.Setenv("TMUX_TMPDIR", filepath.Join(testRoot, "t"))
	home := filepath.Join(testRoot, "home")
	db := filepath.Join(testRoot, "pfm-cache.db")
	sidDir := filepath.Join(testRoot, "sid")
	claudeRoots := []string{
		filepath.Join(testRoot, "claude-1"),
		filepath.Join(testRoot, "claude-2"),
	}
	codexHome := filepath.Join(testRoot, "codex")
	tmuxDir := filepath.Join(testRoot, "tmux")
	procRoot := filepath.Join(testRoot, "proc")
	cgroupRoot := filepath.Join(testRoot, "cgroup")
	managedSettingsDir := filepath.Join(testRoot, "managed-settings.d")
	stateDB := filepath.Join(testRoot, "shared", "pfm.db")

	t.Setenv(EnvHome, home)
	t.Setenv(EnvCacheDB, db)
	t.Setenv(EnvStateDB, stateDB)
	t.Setenv(EnvSIDDir, sidDir)
	t.Setenv(EnvClaudeRoots, strings.Join(claudeRoots, string(os.PathListSeparator)))
	t.Setenv(EnvCodexHome, codexHome)
	opencodeRoot := filepath.Join(testRoot, "opencode")
	t.Setenv(EnvOpenCodeRoot, opencodeRoot)
	t.Setenv(EnvTmuxDir, tmuxDir)
	t.Setenv(EnvProcRoot, procRoot)
	t.Setenv(EnvCgroupRoot, cgroupRoot)
	t.Setenv(EnvManagedSettingsDir, managedSettingsDir)

	got, err := Resolve()
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	want := Values{
		CacheDB: db,
		StateDB: stateDB,
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
		ArchiveDir:         filepath.Join(home, ".claude-archive"),
		ProcRoot:           procRoot,
		CgroupRoot:         cgroupRoot,
		ManagedSettingsDir: managedSettingsDir,
		LogFile:            filepath.Join(home, ".local", "state", "pfm", "log", "pfm.jsonl"),
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

// The state and cache databases have distinct names in the pfm state directory.
func TestResolveStateAndCacheDefaults(t *testing.T) {
	testRoot := t.TempDir()
	home := filepath.Join(testRoot, "home")
	t.Setenv(EnvHome, home)
	t.Setenv(EnvCacheDB, filepath.Join(testRoot, "cache", "pfm-cache.db"))
	t.Setenv(EnvStateDB, "")

	got, err := Resolve()
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	want := DefaultStateDB(home)
	if got.StateDB != want {
		t.Fatalf("Resolve().StateDB = %q, want %q", got.StateDB, want)
	}
	if got.StateDB == got.CacheDB {
		t.Fatalf("fleet store and private cache resolved to the same file %q", got.CacheDB)
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
	t.Setenv(EnvCacheDB, filepath.Join(testRoot, "pfm-cache.db"))
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

// The activity log hangs off the same pfm state directory as the fleet cache,
// so a jail, a fence and the live host each keep their own and never mix.
func TestResolveLogFileHangsOffTheHomesStateDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv(EnvHome, home)
	resolved, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".local", "state", "pfm", "log", "pfm.jsonl")
	if resolved.LogFile != want {
		t.Fatalf("LogFile = %q, want %q", resolved.LogFile, want)
	}
	other := t.TempDir()
	t.Setenv(EnvHome, other)
	second, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if second.LogFile == resolved.LogFile {
		t.Fatalf("two homes resolved to one activity log: %s", second.LogFile)
	}
}

func TestResolveIgnoresRetiredDatabaseEnvironment(t *testing.T) {
	home := t.TempDir()
	t.Setenv(EnvHome, home)
	t.Setenv(EnvStateDB, "")
	t.Setenv(EnvCacheDB, "")
	t.Setenv("PFM_"+"DB", filepath.Join(home, "retired-cache.db"))
	t.Setenv("PFM_FLEET_"+"DB", filepath.Join(home, "retired-state.db"))
	got, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if got.StateDB != DefaultStateDB(home) || got.CacheDB != DefaultCacheDB(home) {
		t.Fatalf("paths = %q, %q", got.StateDB, got.CacheDB)
	}
}

func TestHarnessBaselineDirUsesRecordedClone(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	if _, err := HarnessBaselineDir(home); !errors.Is(err, ErrNoSourceRepoMarker) {
		t.Fatalf("missing marker error = %v", err)
	}
	if err := WriteSourceRepoMarker(home, repo); err != nil {
		t.Fatal(err)
	}
	got, err := HarnessBaselineDir(home)
	want := filepath.Join(repo, "pfm", "harness-prompts", "claude", "baselines")
	if err != nil || got != want {
		t.Fatalf("baseline dir = %q, %v; want %q", got, err, want)
	}
	legacy := filepath.Join(home, ".local", "share", "pfm", "install", "harness-prompts")
	if got := LegacyHarnessPromptsDir(home); got != legacy {
		t.Fatalf("legacy dir = %q; want %q", got, legacy)
	}
}

func TestSourceRepoMarkerContentResolvesAlias(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.Mkdir(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(repo, alias); err != nil {
		t.Fatal(err)
	}
	content, err := SourceRepoMarkerContent(alias)
	if err != nil || string(content) != repo+"\n" {
		t.Fatalf("content=%q err=%v, want %q", content, err, repo+"\n")
	}
	missing := filepath.Join(root, "missing")
	if _, err := SourceRepoMarkerContent(missing); err == nil || !strings.Contains(err.Error(), missing) {
		t.Fatalf("missing error=%v", err)
	}
}

func TestResolveManagedSettingsDirUsesJailOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv(EnvHome, home)
	t.Setenv(EnvManagedSettingsDir, filepath.Join(home, "managed-settings.d"))
	got, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if got.ManagedSettingsDir != filepath.Join(home, "managed-settings.d") {
		t.Fatalf("managed settings dir=%q", got.ManagedSettingsDir)
	}
	t.Setenv(EnvManagedSettingsDir, "")
	got, err = Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if got.ManagedSettingsDir != wantDefaultManagedSettingsDir {
		t.Fatalf("default managed settings dir=%q", got.ManagedSettingsDir)
	}
}

func TestCheckLegacyPending(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	present := filepath.Join(root, "present.db")
	if err := os.WriteFile(present, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Join(root, "blocker")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	absent := filepath.Join(root, "absent.db")
	unreadable := filepath.Join(blocker, "fleet.db") // ENOTDIR, even for root
	tests := []struct {
		name, target, legacy string
		pending              bool
		wantText             []string
	}{
		{name: "target exists", target: present, legacy: present},
		{name: "fresh home", target: absent, legacy: filepath.Join(root, "absent-legacy.db")},
		{
			name: "legacy waits", target: absent, legacy: present, pending: true,
			wantText: []string{absent, present, "run pfm install"},
		},
		{name: "legacy unreadable", target: absent, legacy: unreadable, wantText: []string{"inspect " + unreadable}},
		{name: "target unreadable", target: unreadable, legacy: present, wantText: []string{"inspect " + unreadable}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := CheckLegacyPending(test.target, test.legacy)
			if len(test.wantText) == 0 {
				if err != nil {
					t.Fatalf("CheckLegacyPending = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatal("CheckLegacyPending = nil, want an error")
			}
			if errors.Is(err, ErrLegacyPending) != test.pending {
				t.Fatalf("errors.Is(%v, ErrLegacyPending) = %v, want %v", err, !test.pending, test.pending)
			}
			for _, want := range test.wantText {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error %q lacks %q", err, want)
				}
			}
		})
	}
}

func TestLegacyDatabasePaths(t *testing.T) {
	t.Parallel()
	home := filepath.Join(t.TempDir(), "home")
	if got, want := LegacyStateDB(home), filepath.Join(home, ".cc", "fleet.db"); got != want {
		t.Fatalf("LegacyStateDB = %q, want %q", got, want)
	}
	if got, want := LegacyCacheDB(home), filepath.Join(home, ".local", "state", "pfm", "fleet.db"); got != want {
		t.Fatalf("LegacyCacheDB = %q, want %q", got, want)
	}
}
