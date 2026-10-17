package action

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
	seatspawn "github.com/rezzminator/professor/pfm/internal/spawn"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// installCostLauncher stands a fake pfm where the launch registry looks for the cost launcher.
func installCostLauncher(t *testing.T, home string) string {
	t.Helper()
	path := filepath.Join(home, ".local", "bin", "pfm")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := testjail.WriteExecutable(path, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestClaudeSeatRunRetainsCanonicalArgsEnvironmentAndLedgerPolicy(t *testing.T) {
	for _, test := range []struct {
		name, resolution string
		policy           *bool
	}{
		{"configured-default-ledger", "configured", nil},
		{"newest-version-explicit-ledger", "version", new(true)},
		{"path-after-managed-launcher-ledger-off", "path", new(false)},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "home with 'apostrophe'")
			pfm := installCostLauncher(t, home)
			managed := filepath.Join(home, ".local", "bin", "claude")
			engineBinary := filepath.Join(home, "engine", "claude")
			if test.resolution == "version" {
				engineBinary = filepath.Join(home, ".local", "share", "claude", "versions", "2.1.270")
			}
			for _, path := range []string{managed, engineBinary} {
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := testjail.WriteExecutable(path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", filepath.Dir(managed)+string(os.PathListSeparator)+filepath.Join(home, "engine"))
			t.Setenv("ANTHROPIC_API_KEY", "fixture-inherited-key")
			t.Setenv("CLAUDE_CONFIG_DIR", "inherited account must not win")
			configDir := filepath.Join(home, "account's profile with spaces")
			if err := os.MkdirAll(configDir, 0o700); err != nil {
				t.Fatal(err)
			}
			prefs := map[string]any{"systemPrompt": "lean"}
			if test.policy != nil {
				prefs["costLedger"] = *test.policy
			}
			if test.resolution == "configured" {
				prefs["binary"] = engineBinary
			}
			body, err := json.Marshal(map[string]any{
				"version":  pfmconfig.Version,
				"accounts": []map[string]any{{"id": 42, "configDir": configDir}}, "claude": prefs,
			})
			if err != nil {
				t.Fatal(err)
			}
			configPath := filepath.Join(home, "pfm.config.json")
			if err := os.WriteFile(configPath, body, 0o600); err != nil {
				t.Fatal(err)
			}
			machine, err := pfmconfig.Load(configPath, home, nil)
			if err != nil {
				t.Fatal(err)
			}
			promptFile := filepath.Join(home, "role's prompt.md")
			if err := os.WriteFile(promptFile, []byte("fixture role prompt\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			arguments := []string{
				"--add-dir",
				filepath.Join(home, "project's docs"),
				"prompt with spaces, 'apostrophe' and \"quotes\"",
			}
			spawn := ClaudeSpawn{
				Purpose: PurposeInteractive, Account: 42, Home: home, Machine: machine,
				SessionID: "native-session", Name: "NATIVE 'quoted' name", Args: arguments,
				Model: "sonnet", Effort: "high", PromptFile: promptFile,
			}
			// The registry is the source of truth, not the adapter under test or a
			// shell parse of its quoted command. Build its typed request independently.
			launch, err := claudelaunch.Render(claudelaunch.Request{
				Purpose: claudelaunch.PurposeInteractive,
				Account: 42, Home: home, SessionID: "native-session", Name: "NATIVE 'quoted' name",
				Args: arguments, Model: "sonnet", Effort: "high", PromptFile: promptFile,
			}, machine)
			if err != nil {
				t.Fatal(err)
			}
			cwd := filepath.Join(home, "working directory's path")
			run, err := spawn.SeatRun(cwd)
			if err != nil {
				t.Fatal(err)
			}
			if run.Binary != launch.Binary || !slices.Equal(run.Args, launch.Argv) || run.CWD != cwd {
				t.Fatalf(
					"typed native recipe=%+v; source binary=%q argv=%q cwd=%q",
					run,
					launch.Binary,
					launch.Argv,
					cwd,
				)
			}
			if want := machine.EffectiveClaude(
				42,
			).CostLedger; run.CostLedger != want ||
				want != (test.policy == nil || *test.policy) {
				t.Fatalf(
					"native ledger policy=%v; source effective=%v raw policy=%v",
					run.CostLedger,
					want,
					test.policy,
				)
			}
			var wantEnv []string
			for _, entry := range os.Environ() {
				name, _, _ := strings.Cut(entry, "=")
				if !slices.Contains(launch.Unset, name) {
					wantEnv = append(wantEnv, entry)
				}
			}
			wantEnv = append(wantEnv, launch.Env...)
			if !slices.Equal(run.Env, wantEnv) {
				t.Fatalf("native env=%q; source env=%q", run.Env, wantEnv)
			}
			settingsIndex := slices.Index(run.Args, "--settings")
			var settings map[string]any
			if settingsIndex < 0 || settingsIndex+1 >= len(run.Args) {
				t.Fatalf("native args lost settings: %q", run.Args)
			}
			if err := json.Unmarshal([]byte(run.Args[settingsIndex+1]), &settings); err != nil {
				t.Fatalf("native settings are not raw JSON: %v", err)
			}
			if env, ok := settings["env"].(map[string]any); !ok || env["CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT"] != "1" {
				t.Fatalf("native settings lost source lean policy: %+v", settings)
			}
			for _, word := range append(append([]string{}, arguments...), promptFile) {
				if !slices.Contains(run.Args, word) {
					t.Fatalf("native argv lost exact quoted word %q: %q", word, run.Args)
				}
			}
			command, err := spawn.Command(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			wantArgs := append(
				append(append([]string{}, launch.Wrapper...), deps.Executable(launch.Binary)),
				launch.Argv...)
			wantPath := deps.Executable(launch.Binary)
			if len(launch.Wrapper) > 0 {
				wantPath = pfm
			}
			if command.Path != wantPath || !slices.Equal(command.Args, wantArgs) ||
				!slices.Equal(command.Env, wantEnv) {
				t.Fatalf("ProcessCommand changed wrapper/argv/env: path=%q argv=%q", command.Path, command.Args)
			}
			values := paths.Values{Home: home, StateDB: filepath.Join(home, "state.db")}
			prepared, err := seatspawn.PrepareHerdr(context.Background(), seatspawn.HerdrRequest{
				Choice: seatspawn.BackendChoice{
					Backend:        "herdr",
					HerdrSocket:    herdrPickerSocket(t),
					HerdrWorkspace: "w1",
					HerdrPane:      "w1:p2",
				},
				Seat: fleetdb.Seat{
					SeatID:    "parity-seat",
					SessionID: "native-session",
					Engine:    "cc",
				},
				Run:   run,
				Paths: values,
			})
			if err != nil {
				t.Fatal(err)
			}
			state := fleetdb.OpenSharedState(context.Background(), values)
			t.Cleanup(func() {
				if err := state.Close(); err != nil {
					t.Error(err)
				}
			})
			stored, err := state.ReadSeatRun(prepared.Seat.SeatID)
			if err != nil {
				t.Fatal(err)
			}
			if stored.Binary != engineBinary || !slices.Equal(stored.Args, launch.Argv) ||
				!slices.Equal(stored.Env, wantEnv) ||
				stored.CWD != cwd ||
				stored.CostLedger != run.CostLedger {
				t.Fatalf(
					"persisted native recipe=%+v; want real engine %q and exact source recipe",
					stored,
					engineBinary,
				)
			}
		})
	}
}

func TestClaudeSpawnRunsTheCostLauncherInFrontOfClaude(t *testing.T) {
	home := t.TempDir()
	pfm := installCostLauncher(t, home)
	machine := configuredMachinePolicy(home)
	machine.Claude.CostLedger = true
	spawn := ClaudeSpawn{Purpose: PurposeInteractive, Account: 42, Home: home, Machine: machine}

	shell, err := spawn.ShellCommand()
	if err != nil {
		t.Fatal(err)
	}
	var words []string
	for _, word := range []string{pfm, "internal", "cost-launch", "--", "/opt/tools/claude enterprise"} {
		words = append(words, Quote(word))
	}
	launcher := strings.Join(words, " ")
	if !strings.Contains(shell, " "+launcher+" ") {
		t.Fatalf("ShellCommand lacks the launcher in front of claude %q: %.400s", launcher, shell)
	}
	if strings.Index(shell, "PFM_COST_LEDGER") >= strings.Index(shell, launcher) &&
		strings.Contains(shell, "PFM_COST_LEDGER") {
		t.Errorf("the marker assignment comes after the launcher; env words must lead: %.400s", shell)
	}

	command, err := spawn.Command(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	claude := deps.Executable("/opt/tools/claude enterprise")
	if command.Path != pfm {
		t.Errorf("Command Path = %q, want the launcher %q", command.Path, pfm)
	}
	if want := []string{pfm, "internal", "cost-launch", "--", claude}; !slices.Equal(command.Args[:len(want)], want) {
		t.Errorf("Command Args begin %q, want %q", command.Args[:min(len(want), len(command.Args))], want)
	}
	if len(command.Args) < 6 {
		t.Errorf("Command Args = %q, want claude's own argv after the launcher", command.Args)
	}
}

func TestClaudeSpawnWithoutAnInstalledLauncherStartsClaudeDirectlyAndSaysSo(t *testing.T) {
	home := t.TempDir()
	machine := configuredMachinePolicy(home)
	machine.Claude.CostLedger = true
	spawn := ClaudeSpawn{Purpose: PurposeInteractive, Account: 42, Home: home, Machine: machine}

	shell, err := spawn.ShellCommand()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(shell, "cost-launch") ||
		!strings.Contains(shell, " PFM_COST_LEDGER='failed' '/opt/tools/claude enterprise' ") {
		t.Errorf(
			"no launcher installed: want claude started directly after PFM_COST_LEDGER='failed', got %.400s",
			shell,
		)
	}
	command, err := spawn.Command(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if command.Path != deps.Executable("/opt/tools/claude enterprise") ||
		!slices.Contains(command.Env, "PFM_COST_LEDGER=failed") {
		t.Errorf(
			"no launcher installed: Path %q Env has the marker = %v",
			command.Path,
			slices.Contains(command.Env, "PFM_COST_LEDGER=failed"),
		)
	}
}
