package installer

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// stageGlobalFanoutSource writes the one source repository every global
// wiring step reads from: one global command, one global skill directory,
// and one global agent (engine-sync's to link). It returns the repository root.
func stageGlobalFanoutSource(t *testing.T, home string) string {
	t.Helper()
	repo := filepath.Join(home, "blueprint")
	writeFixture(t, filepath.Join(repo, "templates", "global", "commands", "alpha.md"), "a global command\n")
	writeFixture(
		t,
		filepath.Join(repo, "templates", "global", "skills", "beta", "SKILL.md"),
		"---\nname: beta\n---\n\nbody\n",
	)
	writeFixture(t, filepath.Join(repo, "templates", "global", "agents", "gamma.md"),
		"---\nname: gamma\ndescription: Gamma role for testing.\n---\n\nbody\n")
	return repo
}

// globalFanoutEngine stages three accounts linked to one machine store.
func globalFanoutEngine(t *testing.T, home string, apply bool, stdout io.Writer) (*engine, string, string) {
	t.Helper()
	repo := stageGlobalFanoutSource(t, home)
	first := filepath.Join(home, ".claude")
	second := filepath.Join(home, ".cc", "2")
	for _, id := range []string{"1", "2", "3"} {
		testjail.StageAccountLinks(
			t,
			home,
			filepath.Join(home, ".cc", id),
			[]string{"agents", "commands", "skills"},
			nil,
		)
	}
	return &engine{
		options: Options{
			Home:      home,
			ConfigDir: first,
			ClaudeAccounts: []pfmconfig.Account{
				{ID: 1, ConfigDir: filepath.Join(home, ".cc", "1")},
				{ID: 2, ConfigDir: second},
				{ID: 3, ConfigDir: filepath.Join(home, ".cc", "3")},
			},
			SourceRepo: repo,
			Stdout:     stdout,
		},
		managedRoot: filepath.Join(home, ".local", "share", "pfm", "install"),
		apply:       apply,
		stamp:       "fixture",
	}, first, second
}

// TestGlobalWiringWritesTheStoreForThreeAccounts checks the shared store and all three account views.
func TestGlobalWiringWritesTheStoreForThreeAccounts(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	var output bytes.Buffer
	installer, first, _ := globalFanoutEngine(t, home, true, &output)
	assets, err := assetFiles()
	if err != nil {
		t.Fatalf("assetFiles: %v", err)
	}
	if _, err := installer.stageAssets(assets); err != nil {
		t.Fatalf("stageAssets: %v", err)
	}
	// Asset staging also links account-local hooks; judge only registry writes below.
	output.Reset()

	for _, step := range []struct {
		name string
		run  func() error
	}{
		{"wireGlobalCommands", installer.wireGlobalCommands},
		{"wireGlobalSkills", installer.wireGlobalSkills},
		{"wireSkills", func() error { return installer.wireSkills(assets) }},
	} {
		if err := step.run(); err != nil {
			t.Fatalf("%s: %v\n%s", step.name, err, output.String())
		}
	}

	repo := filepath.Join(home, "blueprint")
	for _, config := range []string{first} {
		assertFanoutLink(t, filepath.Join(config, "commands", "alpha.md"),
			filepath.Join(repo, "templates", "global", "commands", "alpha.md"))
		assertFanoutLink(t, filepath.Join(config, "skills", "beta"),
			filepath.Join(repo, "templates", "global", "skills", "beta"))
		assertFanoutLink(t, filepath.Join(config, "skills", "handoff", "SKILL.md"),
			filepath.Join(installer.managedRoot, "handoff.skill.md"))
	}
	header := "skills -> " + filepath.Join(first, "skills") + "\n"
	if strings.Count(output.String(), header) != 1 {
		t.Fatalf("header %q: %s", header, output.String())
	}
	for _, account := range installer.options.ClaudeAccounts {
		for _, registry := range []string{"agents", "commands", "skills"} {
			assertLink(t, filepath.Join(account.ConfigDir, registry), filepath.Join(first, registry))
		}
		assertFanoutLink(
			t,
			filepath.Join(account.ConfigDir, "commands", "alpha.md"),
			filepath.Join(repo, "templates", "global", "commands", "alpha.md"),
		)
		if strings.Contains(output.String(), account.ConfigDir) {
			t.Fatalf("registry transcript names account path %s: %s", account.ConfigDir, output.String())
		}
	}
}

func TestGlobalCommandsWriteTheStore(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	repo := stageGlobalFanoutSource(t, home)
	primary := filepath.Join(home, ".claude")
	second := filepath.Join(home, ".cc", "2")
	third := filepath.Join(home, ".cc", "3")
	installer := &engine{
		options: Options{
			Home:           home,
			ConfigDir:      primary,
			ClaudeAccounts: []pfmconfig.Account{{ID: 2, ConfigDir: second}, {ID: 3, ConfigDir: third}},
			SourceRepo:     repo,
			Stdout:         io.Discard,
		},
		managedRoot: filepath.Join(home, ".local", "share", "pfm", "install"),
		apply:       true,
		stamp:       "fixture",
	}

	if err := installer.wireGlobalCommands(); err != nil {
		t.Fatalf("wireGlobalCommands: %v", err)
	}
	source := filepath.Join(repo, "templates", "global", "commands", "alpha.md")
	for _, configDir := range []string{primary} {
		assertFanoutLink(t, filepath.Join(configDir, "commands", "alpha.md"), source)
	}
}

// TestGlobalWiringDryRunPlansTheStore checks that preview names the store paths.
func TestGlobalWiringDryRunPlansTheStore(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	var output bytes.Buffer
	installer, first, _ := globalFanoutEngine(t, home, false, &output)
	assets, err := assetFiles()
	if err != nil {
		t.Fatalf("assetFiles: %v", err)
	}
	for _, step := range []func() error{
		installer.wireGlobalCommands,
		installer.wireGlobalSkills,
		func() error { return installer.wireSkills(assets) },
	} {
		if err := step(); err != nil {
			t.Fatalf("dry run: %v\n%s", err, output.String())
		}
	}

	repo := filepath.Join(home, "blueprint")
	for _, config := range []string{first} {
		for _, want := range []string{
			"link " + filepath.Join(config, "commands", "alpha.md") + " -> " + filepath.Join(repo, "templates", "global", "commands", "alpha.md"),
			"link " + filepath.Join(config, "skills", "beta") + " -> " + filepath.Join(repo, "templates", "global", "skills", "beta"),
			"link " + filepath.Join(config, "skills", "handoff", "SKILL.md") + " -> " + filepath.Join(installer.managedRoot, "handoff.skill.md"),
		} {
			if !strings.Contains(output.String(), want) {
				t.Fatalf("dry run never planned %q:\n%s", want, output.String())
			}
		}
		if _, err := os.Lstat(filepath.Join(config, "commands", "alpha.md")); !os.IsNotExist(err) {
			t.Fatalf("dry run wrote into %s: %v", config, err)
		}
	}
}

func assertFanoutLink(t *testing.T, target, source string) {
	t.Helper()
	resolved, linked := resolvedLink(target)
	if !linked {
		info, err := os.Lstat(target)
		t.Fatalf("%s is not a symlink (lstat=%v err=%v), want -> %s", target, info, err, source)
	}
	if resolved != filepath.Clean(source) {
		t.Fatalf("%s -> %s, want -> %s", target, resolved, source)
	}
}

// TestGlobalAgentsDoctorUnreadableMarkerIsUnresolvedNotNoClone pins the other
// half: a marker path that could not even be LOOKED at (its parent directory
// unreadable) is UNRESOLVED — "we failed to look" — never NO-CLONE, which
// would misreport a failed look as an absent clone.
func TestGlobalAgentsDoctorUnreadableMarkerIsUnresolvedNotNoClone(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip(
			"running as root — chmod 000 never blocks root's own Lstat, so the unreadable-marker fixture cannot be produced",
		)
	}
	home := t.TempDir()
	markerDir := filepath.Join(home, ".local", "share", "pfm", "install")
	if err := os.MkdirAll(markerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(markerDir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(markerDir, 0o755) })

	var output bytes.Buffer
	warnings, failures := ReportGlobalAgents(&output, home, false, paths.OSEnv{}, nil)
	if warnings != 1 || failures != 0 {
		t.Fatalf("warnings=%d failures=%d, want 1/0\n%s", warnings, failures, output.String())
	}
	if !strings.Contains(output.String(), "state=UNRESOLVED") {
		t.Fatalf("an unreadable marker was not reported as UNRESOLVED:\n%s", output.String())
	}
	if strings.Contains(output.String(), "state=NO-CLONE") {
		t.Fatalf("a failed look at the marker was rendered as an absent clone:\n%s", output.String())
	}
}

// TestReportGlobalAgentsRendersEngineSyncRows pins pfm's half of the machine
// doctor: the argv it hands `engine-sync doctor --machine` for each clone
// state, and each row printed as a doctor line and counted by its level.
func TestReportGlobalAgentsRendersEngineSyncRows(t *testing.T) {
	reply := `{"ok":false,"rows":[{"level":"ok","message":"global-agents store dir=S state=linked"},` +
		`{"level":"warn","message":"global-agents source=R state=CONFLICT names=a"},` +
		`{"level":"fail","message":"global-agents source=C state=MISSING names=b hint=\"run pfm install\""}]}`
	cases := []struct {
		name         string
		clone        bool
		claudeAbsent bool
		codexHomes   []string
		capabilities []string
		argv         func(home string) string
	}{
		{
			name: "the host capability", capabilities: []string{"gpt-agents"},
			argv: func(home string) string {
				return "doctor --machine --home " + home + " --claude-config-dir " + ClaudeStore(home) +
					" --codex-home " + filepath.Join(home, ".codex") + " --capability gpt-agents --json"
			},
		},
		{
			name: "a recorded clone and two Codex homes", clone: true, codexHomes: []string{"/x/.codex", "/x/.codex-2"},
			argv: func(home string) string {
				return "doctor --machine --home " + home + " --source-repo " + filepath.Join(home, "blueprint") +
					" --claude-config-dir " + ClaudeStore(home) + " --codex-home /x/.codex --codex-home /x/.codex-2 --json"
			},
		},
		{
			name: "no clone, the default Codex home",
			argv: func(home string) string {
				return "doctor --machine --home " + home + " --claude-config-dir " + ClaudeStore(home) +
					" --codex-home " + filepath.Join(home, ".codex") + " --json"
			},
		},
		{
			name: "Claude absent", clone: true, claudeAbsent: true,
			argv: func(home string) string {
				return "doctor --machine --home " + home + " --claude-config-dir " + ClaudeStore(home) +
					" --codex-home " + filepath.Join(home, ".codex") + " --claude-absent --json"
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			home := t.TempDir()
			if testCase.clone {
				repo := filepath.Join(home, "blueprint")
				writeFixture(t, filepath.Join(repo, "templates", "global", "agents", "a.md"), "---\nname: a\n---\n")
				if err := paths.WriteSourceRepoMarker(home, repo); err != nil {
					t.Fatal(err)
				}
			}
			record := testjail.FakeEngineSync(t, reply, 1)
			var output bytes.Buffer
			warnings, failures := ReportGlobalAgents(
				&output,
				home,
				testCase.claudeAbsent,
				paths.OSEnv{},
				testCase.capabilities,
				testCase.codexHomes...)
			if warnings != 1 || failures != 1 {
				t.Fatalf("warnings=%d failures=%d, want 1/1\n%s", warnings, failures, output.String())
			}
			want := "doctor: global-agents store dir=S state=linked\n" +
				"doctor: global-agents source=R state=CONFLICT names=a\n" +
				"doctor: global-agents source=C state=MISSING names=b hint=\"run pfm install\"\n"
			if output.String() != want {
				t.Fatalf("output:\n%s\nwant:\n%s", output.String(), want)
			}
			if calls := testjail.EngineSyncCalls(t, record); len(calls) != 1 || calls[0] != testCase.argv(home) {
				t.Fatalf("engine-sync calls = %q, want [%q]", calls, testCase.argv(home))
			}
		})
	}
}

// An engine-sync that is not there is a failing doctor row naming it, never a
// clean bill and never a missing line.
func TestReportGlobalAgentsWithoutEngineSyncIsAFailingRow(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ENGINE_SYNC_BIN", "")
	t.Setenv("ENGINE_SYNC_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	var output bytes.Buffer
	warnings, failures := ReportGlobalAgents(&output, home, false, paths.OSEnv{}, nil)
	if warnings != 0 || failures != 1 {
		t.Fatalf("warnings=%d failures=%d, want 0/1\n%s", warnings, failures, output.String())
	}
	stable := filepath.Join(home, ".local", "share", "engine-sync", "bin", "engine-sync")
	if want := "doctor: global-agents state=FAIL engine-sync binary unavailable: " + stable + ": "; !strings.HasPrefix(
		output.String(),
		want,
	) {
		t.Fatalf("output = %q, want prefix %q", output.String(), want)
	}
}

// TestGlobalWiringPreservesEntriesThatAreNotProvenCopies pins every registry
// door: a user's edited file at a shipped command name and a user-built
// directory at a shipped skill name survive install and are named as
// conflicts, while byte-equal copies are replaced by the links.
func TestGlobalWiringPreservesEntriesThatAreNotProvenCopies(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	var output bytes.Buffer
	installer, first, _ := globalFanoutEngine(t, home, true, &output)
	repo := filepath.Join(home, "blueprint")
	writeFixture(t, filepath.Join(repo, "templates", "global", "commands", "copied.md"), "shipped\n")
	writeFixture(
		t,
		filepath.Join(repo, "templates", "global", "skills", "copied", "SKILL.md"),
		"---\nname: copied\n---\n",
	)
	writeFixture(t, filepath.Join(first, "commands", "alpha.md"), "my edited command\n")
	writeFixture(t, filepath.Join(first, "skills", "beta", "SKILL.md"), "my own skill\n")
	writeFixture(t, filepath.Join(first, "skills", "beta", "notes.md"), "mine\n")
	writeFixture(t, filepath.Join(first, "commands", "copied.md"), "shipped\n")
	writeFixture(t, filepath.Join(first, "skills", "copied", "SKILL.md"), "---\nname: copied\n---\n")

	if err := installer.wireGlobalCommands(); err != nil {
		t.Fatalf("wireGlobalCommands: %v", err)
	}
	if err := installer.wireGlobalSkills(); err != nil {
		t.Fatalf("wireGlobalSkills: %v", err)
	}

	for path, want := range map[string]string{
		filepath.Join(first, "commands", "alpha.md"):       "my edited command\n",
		filepath.Join(first, "skills", "beta", "SKILL.md"): "my own skill\n",
		filepath.Join(first, "skills", "beta", "notes.md"): "mine\n",
	} {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Fatalf("%s = %q, %v; want it preserved as %q", path, got, err, want)
		}
	}
	for _, name := range []string{
		filepath.Join(first, "commands", "alpha.md"),
		filepath.Join(first, "skills", "beta"),
	} {
		if !strings.Contains(output.String(), "CONFLICT "+name) || !strings.Contains(output.String(), "preserved") {
			t.Fatalf("install output does not name the conflict %s:\n%s", name, output.String())
		}
	}
	assertFanoutLink(t, filepath.Join(first, "commands", "copied.md"),
		filepath.Join(repo, "templates", "global", "commands", "copied.md"))
	assertFanoutLink(t, filepath.Join(first, "skills", "copied"),
		filepath.Join(repo, "templates", "global", "skills", "copied"))
}
