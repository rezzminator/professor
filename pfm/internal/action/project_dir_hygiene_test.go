package action

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// A chat spawned from inside another chat's hook inherits that session's
// CLAUDE_PROJECT_DIR, and Claude Code keeps an inherited value instead of
// recomputing it: every $CLAUDE_PROJECT_DIR hook in the NEW project then runs
// the OLD project's script — a 127 when the path is absent, and, worse, a
// foreign guard when it exists. The fleet strip must drop it on both renderers
// of the spawn door so the harness computes it fresh.
const projectDirName = "CLAUDE_PROJECT_DIR"

func TestClaudeSpawnStripsInheritedProjectDir(t *testing.T) {
	home := t.TempDir()
	machine := configuredMachinePolicy(home)
	for _, purpose := range []Purpose{PurposeInteractive, PurposeResume, PurposeLauncher, PurposeQuery} {
		spawn := ClaudeSpawn{Purpose: purpose, Account: 42, Home: home, Machine: machine}
		shell, err := spawn.ShellCommand()
		if err != nil {
			t.Fatalf("%v shell spawn: %v", purpose, err)
		}
		if want := " -u " + projectDirName + " "; !strings.Contains(shell, want) {
			t.Fatalf("%v shell spawn %q lacks %q", purpose, shell, want)
		}
		environment, err := spawn.Environment([]string{projectDirName + "=/srv/tester/.professor", "PATH=/usr/bin"})
		if err != nil {
			t.Fatal(err)
		}
		if got := lastEnvironmentValue(environment, projectDirName); got != "" {
			t.Fatalf("%v spawn environment %q kept %s=%q, want it stripped",
				purpose, environment, projectDirName, got)
		}
	}
}

func TestDerivedStripsAlsoDropProjectDir(t *testing.T) {
	for name, names := range map[string][]string{
		"fleet":    hygieneNames,
		"headless": claudelaunch.Hygiene(),
		"opencode": opencodeHygieneNames,
	} {
		found := false
		for _, entry := range names {
			if entry == projectDirName {
				found = true
			}
		}
		if !found {
			t.Fatalf("%v strip list %v lacks %s", name, names, projectDirName)
		}
	}
}

// HygieneNames is how another package strips the fleet list without a copy of
// its own: it returns every name in hygieneNames, and the caller's slice is
// its own, so an append or overwrite there never edits the fleet strip.
func TestHygieneNamesReturnsACopyOfTheFleetList(t *testing.T) {
	names := HygieneNames()
	if strings.Join(names, " ") != strings.Join(hygieneNames, " ") {
		t.Fatalf("HygieneNames() = %v, want %v", names, hygieneNames)
	}
	names[0] = "OVERWRITTEN_BY_CALLER"
	if hygieneNames[0] == "OVERWRITTEN_BY_CALLER" {
		t.Fatal("HygieneNames() shares its backing array with hygieneNames")
	}
}

func TestClaudeSpawnRefusesInvalidConfigDir(t *testing.T) {
	jailHome, err := paths.Home()
	if err != nil {
		t.Fatal(err)
	}
	store := claudelaunch.ClaudeStore(jailHome)
	if err := os.MkdirAll(filepath.Join(store, "projects"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{
		"absent", "symlink outside", "symlink store", "symlink into store", "symlink dangling", "file",
		"uninspectable", "unknown account",
	} {
		for _, purpose := range []Purpose{PurposeInteractive, PurposeResume, PurposeLauncher, PurposeQuery} {
			t.Run(fmt.Sprintf("%s/purpose=%d", state, purpose), func(t *testing.T) {
				home := t.TempDir()
				machine := configuredMachinePolicy(home)
				dir := machine.Accounts[0].ConfigDir
				if err := os.RemoveAll(dir); err != nil {
					t.Fatal(err)
				}
				want := "account 42: " + dir
				switch state {
				case "absent":
					want += " does not exist — run pfm install"
				case "symlink outside", "symlink store", "symlink into store", "symlink dangling":
					target := map[string]string{
						"symlink outside":    t.TempDir(),
						"symlink store":      store,
						"symlink into store": filepath.Join(store, "projects"),
						"symlink dangling":   filepath.Join(home, "gone"),
					}[state]
					if err := os.Symlink(target, dir); err != nil {
						t.Fatal(err)
					}
					switch state {
					case "symlink outside":
						want = ""
					case "symlink dangling":
						_, cause := os.Stat(dir)
						if !errors.Is(cause, syscall.ENOENT) {
							t.Fatalf("fixture Stat: %v", cause)
						}
						want = fmt.Sprintf("account 42: inspect %s: %v", dir, cause)
					default:
						want += " resolves to the Claude store — run pfm doctor"
					}
				case "file", "uninspectable":
					if err := os.WriteFile(dir, nil, 0o600); err != nil {
						t.Fatal(err)
					}
					want += " is not a real directory — run pfm doctor"
					if state == "uninspectable" {
						dir = filepath.Join(dir, "child")
						machine.Accounts[0].ConfigDir = dir
						_, cause := os.Lstat(dir)
						if !errors.Is(cause, syscall.ENOTDIR) {
							t.Fatalf("fixture Lstat: %v", cause)
						}
						want = fmt.Sprintf("account 42: inspect %s: %v", dir, cause)
					}
				}
				spawn := ClaudeSpawn{Purpose: purpose, Account: 42, Home: home, Machine: machine}
				if state == "unknown account" {
					spawn.Account = 9
					want = "account 9 is not in the configured roster"
				}
				shell, shellErr := spawn.ShellCommand()
				command, commandErr := spawn.Command(context.Background())
				environment, envErr := spawn.Environment([]string{"PATH=/usr/bin"})
				if want == "" {
					for door, err := range map[string]error{"ShellCommand": shellErr, "Command": commandErr, "Environment": envErr} {
						if err != nil {
							t.Errorf("%s error=%v, want accepted", door, err)
						}
					}
					if !slices.Contains(environment, "CLAUDE_CONFIG_DIR="+dir) {
						t.Errorf("environment=%q, want the literal account dir %s", environment, dir)
					}
					return
				}
				for door, err := range map[string]error{"ShellCommand": shellErr, "Command": commandErr, "Environment": envErr} {
					if err == nil || err.Error() != want {
						t.Errorf("%s error=%v, want %q", door, err, want)
					}
					if state == "symlink dangling" && !errors.Is(err, syscall.ENOENT) {
						t.Errorf("%s did not wrap the dangling link's cause: %v", door, err)
					}
					if state == "uninspectable" && !errors.Is(err, syscall.ENOTDIR) {
						t.Errorf("%s did not wrap inspection cause: %v", door, err)
					}
				}
				if shell != "" || command != nil || environment != nil {
					t.Errorf("refused spawn returned shell=%q command=%v environment=%q", shell, command, environment)
				}
			})
		}
	}
}
