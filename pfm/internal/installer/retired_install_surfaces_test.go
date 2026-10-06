package installer

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestApplyRetiresInstalledBBCardsAndHook(t *testing.T) {
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	managed := managedRootForHome(home)
	commandTarget := filepath.Join(config, "commands", "bb.md")
	skillTarget := filepath.Join(home, ".agents", "skills", "bb")
	writeFixture(t, filepath.Join(managed, "bb.command.md"), "old managed command\n")
	writeFixture(t, filepath.Join(managed, "codex-skills", "bb", "SKILL.md"), "old managed skill\n")
	writeFixture(t, filepath.Join(managed, "codex-skills", "bb", "agents", "openai.yaml"), "old managed metadata\n")
	if err := os.MkdirAll(filepath.Dir(commandTarget), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(managed, "bb.command.md"), commandTarget); err != nil {
		t.Fatal(err)
	}
	backup := commandTarget + ".pre-professor-20300102-030405"
	writeFixture(t, backup, "operator command\n")
	if err := os.MkdirAll(filepath.Dir(skillTarget), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(managed, "codex-skills", "bb"), skillTarget); err != nil {
		t.Fatal(err)
	}
	now := func() time.Time { return time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC) }
	if _, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: home, Now: now, Runner: &fakeRunner{},
		CodexHomes: []string{}, MCPConfigPath: testConfigPath(t),
	}); err != nil {
		t.Fatal(err)
	}
	for _, retired := range []string{
		commandTarget, skillTarget,
		filepath.Join(managed, "bb.command.md"),
		filepath.Join(managed, "codex-skills", "bb", "SKILL.md"),
		filepath.Join(managed, "codex-skills", "bb", "agents", "openai.yaml"),
		filepath.Join(managed, "codex-skills"),
	} {
		requireNoPath(t, retired, "retired /bb surface remains")
	}
	if content := readFixture(t, backup); content != "operator command\n" {
		t.Fatalf("retirement changed operator backup: %q", content)
	}
	var second bytes.Buffer
	report, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: home, Now: now, Stdout: &second, Runner: &fakeRunner{},
		CodexHomes: []string{}, MCPConfigPath: testConfigPath(t),
	})
	if err != nil || report.Changed != 0 {
		t.Fatalf("second apply report=%#v err=%v\n%s", report, err, second.String())
	}
}

func TestApplyLeavesUnrelatedBBSymlinkAlone(t *testing.T) {
	home := t.TempDir()
	target := filepath.Join(home, ".claude", "commands", "bb.md")
	operatorSource := filepath.Join(home, "mine.md")
	writeFixture(t, operatorSource, "operator command\n")
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(operatorSource, target); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if _, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: home, Runner: &fakeRunner{}, Stdout: &output,
		CodexHomes: []string{}, MCPConfigPath: testConfigPath(t),
	}); err != nil {
		t.Fatal(err)
	}
	assertLink(t, target, operatorSource)
	want := "  skip    " + target + " is not an installed /bb link\n"
	if !strings.Contains(output.String(), want) {
		t.Fatalf("output missing %q:\n%s", want, output.String())
	}
}

func TestApplyRetiresDanglingBBLinksFromTheRecordedProfessorClone(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "professor-clone")
	if err := os.MkdirAll(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := paths.WriteSourceRepoMarker(home, repo); err != nil {
		t.Fatal(err)
	}
	commandTarget := filepath.Join(home, ".claude", "commands", "bb.md")
	skillTarget := filepath.Join(home, ".agents", "skills", "bb")
	for _, target := range []string{commandTarget, skillTarget} {
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	legacyRoot := filepath.Join(repo, "blueprint", "templates", "host-swap")
	if err := os.Symlink(filepath.Join(legacyRoot, "bb.command.md"), commandTarget); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(legacyRoot, "codex-skills", "bb"), skillTarget); err != nil {
		t.Fatal(err)
	}
	installer := &engine{
		options: Options{Mode: ModeApply, Home: home, ConfigDir: filepath.Join(home, ".claude"), Stdout: io.Discard},
		apply:   true, managedRoot: managedRootForHome(home),
	}
	if err := installer.retireBBInstall(); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{commandTarget, skillTarget} {
		requireNoPath(t, target, "recorded legacy /bb link remains")
	}
}

func TestApplyRetiresInstalledChatCommands(t *testing.T) {
	home := t.TempDir()
	managed := managedRootForHome(home)
	source := filepath.Join(managed, "chat", "ls.command.md")
	link := filepath.Join(home, ".claude", "commands", "chat", "ls.md")
	writeFixture(t, source, "legacy command card\n")
	if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(source, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: home, Runner: &fakeRunner{},
		CodexHomes: []string{}, MCPConfigPath: testConfigPath(t),
	}); err != nil {
		t.Fatal(err)
	}
	for _, retired := range []string{link, source, filepath.Dir(source), filepath.Dir(link)} {
		requireNoPath(t, retired, "retired /chat: surface remains")
	}
}

func TestRetiredManagedSurfacesRefuseOperatorLeftovers(t *testing.T) {
	home := t.TempDir()
	managed := managedRootForHome(home)
	fixtures := []struct{ path, content string }{
		{"chat/stray-note.md", "operator chat note\n"},
		{"codex-skills/bb/keepme.txt", "operator skill note\n"},
		{"chat/ls.command.md", "legacy command card\n"},
	}
	for _, fixture := range fixtures {
		writeFixture(t, filepath.Join(managed, filepath.FromSlash(fixture.path)), fixture.content)
	}
	_, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: home, Runner: &fakeRunner{},
		CodexHomes: []string{}, MCPConfigPath: testConfigPath(t),
	})
	for _, relative := range []string{"chat/stray-note.md", "codex-skills/bb/keepme.txt"} {
		path := filepath.Join(managed, filepath.FromSlash(relative))
		want := "refuse to retire non-empty directory " + filepath.Dir(path) +
			" — move or delete " + path + ", then rerun"
		if err == nil || !strings.Contains(err.Error(), want) ||
			!strings.Contains(err.Error(), "preflight apply plan") {
			t.Errorf("install error=%v, want preflight refusal %q", err, want)
		}
	}
	for _, fixture := range fixtures {
		path := filepath.Join(managed, filepath.FromSlash(fixture.path))
		if got := readFixture(t, path); got != fixture.content {
			t.Errorf("%s=%q, want %q", fixture.path, got, fixture.content)
		}
	}
}

func TestStagedManagedSurfacesRetirement(t *testing.T) {
	for _, scenario := range []string{
		"shim", "no live chat", "dry run", "re-pinned baseline", "operator file", "live chat", "read fails",
		"uninstall both",
	} {
		t.Run(scenario, func(t *testing.T) {
			home := t.TempDir()
			managed := managedRootForHome(home)
			shim := filepath.Join(managed, "shim", "pfm.zsh")
			prompts := paths.LegacyHarnessPromptsDir(home)
			account := filepath.Join(home, "acct1")
			proc := filepath.Join(home, "proc")
			mode := ModeApply
			var wants []string
			keep := false
			staged := []string{
				"claude.md", "codex.md", "claude/baselines/harness-opus.model", "share/head.md",
			}
			switch scenario {
			case "shim":
				writeFixture(t, shim, "old\n")
				wants = []string{"  change  retire " + shim + " (retired staged shim)\n"}
			case "no live chat", "dry run":
				for _, relative := range staged {
					writeFixture(t, filepath.Join(prompts, filepath.FromSlash(relative)), "old\n")
				}
				wants = []string{
					"  change  retire " + filepath.Join(prompts, "claude", "baselines", "harness-opus.model") +
						" (retired staged harness prompts)\n",
					"  change  remove empty " + filepath.Join(prompts, "claude", "baselines") + "\n",
					"  change  remove empty " + prompts + "\n",
				}
				if scenario == "dry run" {
					mode = ModeDryRun
					wants = append(wants,
						"  change  retire "+filepath.Join(prompts, "claude.md")+" (retired staged harness prompts)\n",
					)
				}
			case "re-pinned baseline":
				// Staged by a pfm whose embed has since re-pinned the baseline.
				retired := filepath.Join(prompts, "claude", "baselines", "harness-original-v2.1.278.md")
				writeFixture(t, retired, "old\n")
				writeFixture(t, filepath.Join(prompts, "claude.md"), "old\n")
				wants = []string{
					"  change  retire " + retired + " (retired staged harness prompts)\n",
					"  change  remove empty " + prompts + "\n",
				}
			case "operator file":
				writeFixture(t, filepath.Join(prompts, "claude.md"), "old\n")
				writeFixture(t, filepath.Join(prompts, "operator-file"), "keep")
				wants = []string{
					"  change  retire " + filepath.Join(prompts, "claude.md") + " (retired staged harness prompts)\n",
					"  skip    keep " + prompts + ": holds files pfm did not write: " +
						filepath.Join(prompts, "operator-file") + "\n",
				}
			case "live chat":
				writeFixture(t, filepath.Join(prompts, "claude.md"), "old prompt\n")
				writeFixture(t, filepath.Join(account, "sessions", "4242.json"), "{}")
				if err := os.MkdirAll(filepath.Join(proc, "4242"), 0o700); err != nil {
					t.Fatal(err)
				}
				wants = []string{
					"  skip    keep " + prompts + ": live Claude chats 4242 in " + account +
						" may still read it — rerun pfm install --yes once they close\n",
				}
				keep = true
			case "uninstall both":
				mode = ModeUninstall
				writeFixture(t, filepath.Join(prompts, "codex.md"), "old\n")
				writeFixture(t, filepath.Join(prompts, "claude", "notes.md"), "mine\n")
				writeFixture(t, filepath.Join(account, "sessions", "4242.json"), "{}")
				if err := os.MkdirAll(filepath.Join(proc, "4242"), 0o700); err != nil {
					t.Fatal(err)
				}
				writeFixture(t, shim, "old\n")
				wants = []string{
					"  change  retire " + filepath.Join(prompts, "codex.md") + " (retired staged harness prompts)\n",
					"  skip    keep " + prompts + ": holds files pfm did not write: " + filepath.Join(
						prompts,
						"claude",
					) + "\n",
				}
			case "read fails":
				writeFixture(t, filepath.Join(prompts, "claude.md"), "old prompt\n")
				writeFixture(t, filepath.Join(account, "sessions"), "not a directory")
				_, readErr := os.ReadDir(filepath.Join(account, "sessions"))
				wants = []string{"  skip    keep " + prompts + ": " + readErr.Error() + "\n"}
				keep = true
			}
			var output bytes.Buffer
			if _, err := Run(context.Background(), Options{
				Mode: mode, Home: home, Runner: &fakeRunner{}, Stdout: &output,
				RosterConfigDirs: []string{account}, ProcRoot: proc,
				CodexHomes: []string{}, MCPConfigPath: testConfigPath(t),
			}); err != nil {
				t.Fatalf("retirement error=%v:\n%s", err, output.String())
			}
			for _, want := range wants {
				if !strings.Contains(output.String(), want) {
					t.Errorf("output missing %q:\n%s", want, output.String())
				}
			}
			switch {
			case scenario == "dry run":
				for _, line := range strings.Split(output.String(), "\n") {
					if strings.HasPrefix(line, "  skip    keep "+prompts) {
						t.Errorf("dry run kept staged prompts: %s", line)
					}
				}
				for _, relative := range staged {
					if got := readFixture(t, filepath.Join(prompts, filepath.FromSlash(relative))); got != "old\n" {
						t.Errorf("dry run changed %s: %q", relative, got)
					}
				}
			case scenario == "operator file":
				requireNoPath(t, filepath.Join(prompts, "claude.md"), "staged prompt remains")
				if got := readFixture(t, filepath.Join(prompts, "operator-file")); got != "keep" {
					t.Fatalf("operator file=%q", got)
				}
			case scenario == "uninstall both":
				requireNoPath(t, filepath.Join(prompts, "codex.md"), "staged prompt remains")
				if got := readFixture(t, filepath.Join(prompts, "claude", "notes.md")); got != "mine\n" {
					t.Fatalf("operator notes=%q", got)
				}
			case keep:
				if got := readFixture(t, filepath.Join(prompts, "claude.md")); got != "old prompt\n" {
					t.Fatalf("kept prompt=%q", got)
				}
			default:
				requireNoPath(t, prompts, "staged prompts remain")
			}
			if scenario == "shim" || scenario == "uninstall both" {
				requireNoPath(t, shim, "staged shim remains")
				requireNoPath(t, filepath.Dir(shim), "staged shim directory remains")
			}
		})
	}
}

func TestManagedRootUninstallRetirement(t *testing.T) {
	for _, scenario := range []string{"fresh", "root leftover", "asset leftover", "rumdl config"} {
		t.Run(scenario, func(t *testing.T) {
			home := t.TempDir()
			managed := managedRootForHome(home)
			var output bytes.Buffer
			options := Options{
				Mode: ModeApply, Home: home, Runner: &fakeRunner{}, Stdout: &output,
				CodexHomes: []string{}, MCPConfigPath: testConfigPath(t), Env: &paths.MapEnv{HomeDir: home},
			}
			if _, err := Run(context.Background(), options); err != nil {
				t.Fatalf("install: %v\n%s", err, output.String())
			}
			var leftover, want string
			switch scenario {
			case "root leftover":
				leftover = filepath.Join(managed, "operator-note.txt")
				writeFixture(t, leftover, "operator note\n")
				want = "  skip    leave non-empty managed root " + managed + ": operator-note.txt\n"
			case "asset leftover":
				leftover = filepath.Join(managed, "bin", "operator-extra")
				writeFixture(t, leftover, "operator note\n")
				want = "  skip    leave non-empty managed directory " + filepath.Dir(leftover) + ": "
			case "rumdl config":
				writeFixture(t, filepath.Join(home, ".config", "rumdl", "rumdl.toml"), rumdlUserConfig)
			}
			output.Reset()
			options.Mode = ModeUninstall
			if _, err := Run(context.Background(), options); err != nil {
				t.Fatalf("uninstall: %v\n%s", err, output.String())
			}
			if leftover != "" {
				if !strings.Contains(output.String(), want) {
					t.Fatalf("output missing %q:\n%s", want, output.String())
				}
				if got := readFixture(t, leftover); got != "operator note\n" {
					t.Fatalf("operator leftover=%q", got)
				}
			} else {
				requireNoPath(t, managed, "uninstall left managed root")
			}
			if scenario == "rumdl config" {
				requireNoPath(t, filepath.Join(home, ".config", "rumdl", "rumdl.toml"), "owned rumdl config remains")
			}
		})
	}
}

func TestStagedRetirementChecksEachAccount(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		t.Run(map[bool]string{false: "roster", true: "config fallback"}[fallback], func(t *testing.T) {
			home := t.TempDir()
			prompts := paths.LegacyHarnessPromptsDir(home)
			writeFixture(t, filepath.Join(prompts, "claude.md"), "old prompt\n")
			proc := filepath.Join(home, "proc")
			accounts := []string{filepath.Join(home, "acct1"), filepath.Join(home, "acct2")}
			if fallback {
				accounts = accounts[:1]
			}
			var want strings.Builder
			for _, account := range accounts {
				writeFixture(t, filepath.Join(account, "sessions", "4242.json"), "{}")
				if err := os.MkdirAll(filepath.Join(proc, "4242"), 0o700); err != nil {
					t.Fatal(err)
				}
				want.WriteString("  skip    keep " + prompts + ": live Claude chats 4242 in " + account +
					" may still read it — rerun pfm install --yes once they close\n")
			}
			roster := accounts
			if fallback {
				roster = nil
			}
			var output bytes.Buffer
			installer := &engine{
				options: Options{
					Home:             home,
					ConfigDir:        accounts[0],
					RosterConfigDirs: roster,
					ProcRoot:         proc,
					Stdout:           &output,
				},
				apply:       true,
				managedRoot: managedRootForHome(home),
			}
			if err := installer.retireStagedManagedSurfaces(false); err != nil {
				t.Fatal(err)
			}
			if output.String() != want.String() {
				t.Fatalf("output=%q, want %q", output.String(), want.String())
			}
			if got := readFixture(t, filepath.Join(prompts, "claude.md")); got != "old prompt\n" {
				t.Fatalf("kept prompt=%q", got)
			}
		})
	}
}

func TestRetiredShellSource(t *testing.T) {
	for _, scenario := range []string{"fallback exists", "fallback absent", "marker unusable"} {
		t.Run(scenario, func(t *testing.T) {
			home := t.TempDir()
			shim := filepath.Join(home, ".professor", "pfm", "internal", "installer", "assets", "shim", "pfm.zsh")
			var want string
			switch scenario {
			case "fallback exists":
				writeFixture(t, shim, "fixture shim\n")
			case "fallback absent":
				want = "  skip    zshrc: no source repo recorded and " + shim + " does not exist\n"
			case "marker unusable":
				writeFixture(t, paths.SourceRepoPath(home), filepath.Join(home, "moved-away")+"\n")
				_, markerErr := paths.ReadSourceRepoMarker(home)
				want = "  skip    zshrc: " + markerErr.Error() +
					" — rerun pfm install --yes from inside your Professor clone\n"
			}
			var output bytes.Buffer
			installer := &engine{
				options: Options{Mode: ModeApply, Home: home, Stdout: &output},
				apply:   true, managedRoot: managedRootForHome(home),
			}
			if err := installer.wireShell(false); err != nil {
				t.Fatalf("wireShell: %v", err)
			}
			if scenario == "fallback exists" {
				if got := readFixture(t, filepath.Join(home, ".zshrc")); !strings.Contains(got, sourceLine(shim)) {
					t.Fatalf("zshrc=%q, want source line %q", got, sourceLine(shim))
				}
			} else if output.String() != want {
				t.Fatalf("output=%q, want %q", output.String(), want)
			}
		})
	}
}

func TestMovedCloneInstallRefreshesMarker(t *testing.T) {
	home, clone, _ := aliasInstallFixture(t)
	writeFixture(t, paths.SourceRepoPath(home), filepath.Join(home, "moved-away")+"\n")
	_, markerErr := paths.ReadSourceRepoMarker(home)
	var output bytes.Buffer
	if _, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: home, SourceRepo: clone, Runner: &fakeRunner{}, Stdout: &output,
		CodexHomes: []string{}, MCPConfigPath: testConfigPath(t),
	}); err != nil {
		t.Fatalf("install from moved clone: %v\n%s", err, output.String())
	}
	for _, prefix := range []string{"registry dead-link check skipped: ", "retired /bb surfaces skipped: "} {
		want := "  skip    " + prefix + "read source repository marker for registry dead-link check: " +
			markerErr.Error() + " — rerun pfm install --yes from inside your Professor clone\n"
		if !strings.Contains(output.String(), want) {
			t.Errorf("output missing %q:\n%s", want, output.String())
		}
	}
	if got, err := paths.ReadSourceRepoMarker(home); err != nil || got != clone {
		t.Fatalf("refreshed marker=%q err=%v, want %q", got, err, clone)
	}
}
