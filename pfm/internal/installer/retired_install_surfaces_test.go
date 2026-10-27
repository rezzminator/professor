package installer

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

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
			proc := emptyProcRoot(t, home)
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

func TestStagedRetirementChecksEachAccount(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		t.Run(map[bool]string{false: "roster", true: "config fallback"}[fallback], func(t *testing.T) {
			home := t.TempDir()
			prompts := paths.LegacyHarnessPromptsDir(home)
			writeFixture(t, filepath.Join(prompts, "claude.md"), "old prompt\n")
			proc := emptyProcRoot(t, home)
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
