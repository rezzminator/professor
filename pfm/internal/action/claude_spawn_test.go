package action

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/cmdparse"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/loginshell"
	"github.com/rezzminator/professor/pfm/internal/paths"
	seatspawn "github.com/rezzminator/professor/pfm/internal/spawn"
	"github.com/rezzminator/professor/pfm/internal/testjail"
	"github.com/rezzminator/professor/pfm/internal/testwait"
)

// installCostLauncher stands a fake pfm where the launch registry looks for the cost launcher.
func installCostLauncher(t *testing.T, home string) string {
	t.Helper()
	path := filepath.Join(home, ".local", "bin", "pfm")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := testjail.WriteExecutableNeverRun(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
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
				if err := testjail.WriteExecutableNeverRun(path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
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
			// The runner finds the engine as the host would: a bare name on PATH is the managed launcher.
			resolved := launch.Binary
			if !filepath.IsAbs(resolved) {
				resolved = managed
			}
			lookup := &deps.FakeRunner{}
			lookup.ScriptLookPath(launch.Binary, resolved, nil)
			spawn.Runner = lookup
			command, err := spawn.Command(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			wantArgs := append(
				append(append([]string{}, launch.Wrapper...), resolved),
				launch.Argv...)
			wantPath := resolved
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
	for _, word := range []string{pfm, "internal", "cost-launch", "--", machine.Claude.Binary} {
		words = append(words, cmdparse.Quote(word))
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
	claude := machine.Claude.Binary
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
		!strings.Contains(shell, " PFM_COST_LEDGER='failed' "+cmdparse.Quote(machine.Claude.Binary)+" ") {
		t.Errorf(
			"no launcher installed: want claude started directly after PFM_COST_LEDGER='failed', got %.400s",
			shell,
		)
	}
	command, err := spawn.Command(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if command.Path != machine.Claude.Binary ||
		!slices.Contains(command.Env, "PFM_COST_LEDGER=failed") {
		t.Errorf(
			"no launcher installed: Path %q Env has the marker = %v",
			command.Path,
			slices.Contains(command.Env, "PFM_COST_LEDGER=failed"),
		)
	}
}

func TestClaudeSpawnCommandNamesAMissingClaudeBinary(t *testing.T) {
	home := t.TempDir()
	machine := configuredMachinePolicy(home)
	present := ClaudeSpawn{Purpose: PurposeInteractive, Account: 42, Home: home, Machine: machine}
	command, err := present.Command(context.Background())
	if err != nil {
		t.Fatalf("Command with the configured binary present: %v", err)
	}
	if command.Path != machine.Claude.Binary {
		t.Fatalf("Command Path = %q, want the configured binary %q", command.Path, machine.Claude.Binary)
	}

	missing := filepath.Join(home, "absent", "claude")
	machine.Claude.Binary = missing
	absent := ClaudeSpawn{Purpose: PurposeInteractive, Account: 42, Home: home, Machine: machine}
	_, err = absent.Command(context.Background())
	want := `resolve Claude binary "` + missing + `": exec: "` + missing + `": stat ` + missing +
		`: no such file or directory`
	if err == nil || err.Error() != want {
		t.Fatalf("Command with the binary missing: error = %v, want %q", err, want)
	}
}

// A pane shell runs ShellCommand: a PATH whose base is pfm's own leaves that PATH
// out and hands only the login shell's folders to the /bin/sh words that add them
// after the pane shell's PATH (their run: TestLaunchPaneEnvPATHWordsInARealShell);
// a persona's PATH is written as merged. SeatRun execs with no shell between, so
// its PATH is pfm's own with the login folders placed as the login PATH orders them.
func TestClaudeSpawnShellCommandKeepsThePaneShellPATH(t *testing.T) {
	const loginShell = "/login/bin/zsh"
	const script = `PATH="${PATH:+$PATH:}$1"; export PATH; shift; exec "$@"`
	t.Setenv("SHELL", loginShell)
	t.Setenv("PATH", "/usr/bin:/bin")
	for _, test := range []struct {
		name      string
		envs      map[string]string
		wantPATH  []string
		wantLogin []string
		wantSeat  string
	}{
		{
			name:      "pfm PATH",
			wantPATH:  []string{},
			wantLogin: []string{"/bin/sh", "-c", script, "sh", "/shims:/opt/x"},
			wantSeat:  "/shims:/usr/bin:/bin:/opt/x",
		},
		{
			name:     "persona PATH",
			envs:     map[string]string{"PATH": "/persona/bin"},
			wantPATH: []string{"/persona/bin:/shims:/usr/bin:/opt/x"},
			wantSeat: "/persona/bin:/shims:/usr/bin:/opt/x",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := &deps.FakeRunner{}
			runner.ScriptLookPath("bash", "/scripted/bin/bash", nil)
			loginshell.ScriptAccount(runner, os.Getuid(), loginShell, nil)
			// The markers claudelaunch's probe prints around the login shell's PATH.
			runner.Script([]string{loginShell}, deps.RunResult{
				Stdout: []byte("__PFM_LOGIN_PATH_START__/shims:/usr/bin:/opt/x__PFM_LOGIN_PATH_END__"),
			}, nil)
			spawn := ClaudeSpawn{
				Purpose: PurposeInteractive, Home: t.TempDir(), Envs: test.envs,
				launchRunner: runner,
			}
			testwait.ProcessClock(t, clock.NewFake(time.Unix(0, 0)))
			command, err := spawn.ShellCommand()
			if err != nil {
				t.Fatal(err)
			}
			file, err := syntax.NewParser().Parse(strings.NewReader(command), "pane")
			if err != nil {
				t.Fatalf("parse %q: %v", command, err)
			}
			config := &expand.Config{Env: expand.ListEnviron()}
			args := []string{}
			syntax.Walk(file, func(node syntax.Node) bool {
				call, ok := node.(*syntax.CallExpr)
				if !ok {
					return true
				}
				for _, word := range call.Args {
					value, err := expand.Literal(config, word)
					if err != nil {
						t.Fatalf("expand %q: %v", command, err)
					}
					args = append(args, value)
				}
				return false
			})
			// The words ahead of the login ones are the assignments; the script among
			// the login words is no PATH assignment of the pane command.
			var login []string
			assignments := args
			if at := slices.Index(args, "/bin/sh"); at >= 0 && at+5 <= len(args) {
				login, assignments = args[at:at+5], args[:at]
			}
			pathArgs := []string{}
			for _, arg := range assignments {
				if path, found := strings.CutPrefix(arg, "PATH="); found {
					pathArgs = append(pathArgs, path)
				}
			}
			if !slices.Equal(pathArgs, test.wantPATH) || !slices.Equal(login, test.wantLogin) {
				t.Fatalf("pane command PATH= %q and login words %q, want %q and %q; command %q",
					pathArgs, login, test.wantPATH, test.wantLogin, command)
			}
			seat, err := spawn.SeatRun("/work")
			if err != nil {
				t.Fatal(err)
			}
			if got := lastEnvironmentValue(seat.Env, "PATH"); got != test.wantSeat {
				t.Fatalf("SeatRun PATH = %q, want %q", got, test.wantSeat)
			}
			// A caller's own Runner (a picker's, a resume's: the real one in
			// production) never runs the login shell, so the process's cache holds.
			callerRunner := &deps.FakeRunner{}
			callerSpawn := ClaudeSpawn{
				Purpose: PurposeInteractive, Home: t.TempDir(), Envs: test.envs,
				Runner: callerRunner,
			}
			if _, err := callerSpawn.ShellCommand(); err != nil {
				t.Fatal(err)
			}
			for _, call := range callerRunner.Calls() {
				if call.Argv[0] == loginShell {
					t.Fatalf("ShellCommand ran the login shell through the spawn's Runner: %q", call.Argv)
				}
			}
		})
	}
}
