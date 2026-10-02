package action

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

func TestHeadlessClaudeAssignsSessionAndLaunchRecord(t *testing.T) {
	request := HeadlessRequest{
		Engine: pfmengine.Claude, Name: "worker", CWD: "/work/alpha",
		Home: "/home/tester", PrimaryAccount: 2,
	}
	plan, err := headlessWithTestConfig(request)
	if err != nil {
		t.Fatal(err)
	}
	parsed := parsedShell(t, plan.Run)
	if parsed.SessionID == "" || parsed.Name != request.Name || plan.Record == nil ||
		plan.Record.SessionID != parsed.SessionID || plan.Record.Account != 2 ||
		plan.Record.Engine != pfmengine.Claude {
		t.Fatalf("launch id=%q name=%q record=%#v", parsed.SessionID, parsed.Name, plan.Record)
	}
}

func TestHeadlessClaudeDefaultsCacheFromAccount(t *testing.T) {
	request := HeadlessRequest{
		Engine: pfmengine.Claude, Name: "worker", CWD: "/work/alpha",
		Home: "/home/tester", PrimaryAccount: 2,
	}
	machine := testMachineConfig(request.Home)
	machine.Accounts[1].Claude = &machine.Claude
	machine.Accounts[1].Claude.Cache1H = true
	request.Config = machine
	plan, err := HeadlessRun(request)
	if err != nil {
		t.Fatal(err)
	}
	if launchEnv(t, plan.Run)["CACHE_LIVE_CONTROL_MAIN_TTL"] != "1h" ||
		plan.Record == nil || !plan.Record.Cache1H {
		t.Fatalf("configured 1h cache missing: record=%#v run=%s", plan.Record, plan.Run)
	}
}

func TestCodexDeveloperInstructionsArgKeepsOneCompleteTripleQuotedValue(t *testing.T) {
	prompt := strings.Repeat("0123456789abcdef", 820) + ` a triple quote """ and slash \\ survive`
	escaped := strings.ReplaceAll(prompt, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"""`, `\"\"\"`)
	want := []string{"-c", "developer_instructions=\"\"\"\n" + escaped + "\"\"\""}
	if got := CodexDeveloperInstructionsArg(prompt); !reflect.DeepEqual(got, want) {
		t.Fatalf("CodexDeveloperInstructionsArg() = %#v, want one complete %d-byte value", got, len(want[1]))
	}
}

// TestHeadlessClaudeCarriesTheFullLaunchCeremony pins the command a headless
// Claude chat runs. Every clause is load-bearing: the environment strip (a
// chat born from another chat's Bash tool would otherwise inherit its identity
// and account), the account's config dir, its configured cache policy, the
// name, and both autonomy flags — a headless chat has nobody awake to answer a
// permission prompt.
func TestHeadlessClaudeCarriesTheFullLaunchCeremony(t *testing.T) {
	plan, err := headlessWithTestConfig(HeadlessRequest{
		Engine:         "cc",
		Name:           "_KILL worker 3",
		CWD:            "/work/alpha",
		Prompt:         "audit the firewall rules",
		Home:           "/home/tester",
		PrimaryAccount: 2,
	})
	if err != nil {
		t.Fatalf("HeadlessRun() error = %v", err)
	}
	if !plan.PromptOnCommandLine {
		t.Fatal("Claude takes its prompt on the command line")
	}
	parsed := parsedShell(t, plan.Run)
	if parsed.Name != "_KILL worker 3" || !parsed.Autonomy ||
		launchEnv(t, plan.Run)["CACHE_LIVE_CONTROL_MAIN_TTL"] != "1h" ||
		parsed.SettingsEnv[spawnDepthName] != "8" ||
		parsed.Settings["outputStyle"] != "default" {
		t.Fatalf("headless launch shape: name=%q autonomy=%t settings=%#v",
			parsed.Name, parsed.Autonomy, parsed.SettingsEnv)
	}
}

func TestHeadlessClaudeUsesRolePromptFileAndKeepsCallerPromptAlone(t *testing.T) {
	home := t.TempDir()
	stageProfessorPrompt(t, home)
	rolePrompt := filepath.Join(home, "sid", "role-prompt-cc-worker.md")
	if err := os.MkdirAll(filepath.Dir(rolePrompt), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rolePrompt, []byte("role"), 0o600); err != nil {
		t.Fatal(err)
	}
	machine := testMachineConfig(home)
	machine.Claude.SystemPrompt = "professor"
	plan, err := HeadlessRun(HeadlessRequest{
		Engine: pfmengine.Claude, Name: "worker", CWD: "/work/alpha",
		Prompt: "caller prompt", PromptChannel: rolePrompt, Home: home,
		PrimaryAccount: 1, Config: machine,
	})
	if err != nil {
		t.Fatalf("HeadlessRun() error = %v", err)
	}
	if parsedShell(t, plan.Run).PromptFile != rolePrompt {
		t.Fatalf("Claude command does not carry per-seat prompt file: %s", plan.Run)
	}
	if strings.Contains(plan.Run, Quote(mustProfessorPromptPath(t, home))) {
		t.Fatalf("Claude command still carries staged fleet prompt: %s", plan.Run)
	}
	if strings.Count(plan.Run, "caller prompt") != 1 {
		t.Fatalf("Claude caller prompt is not its own lone argument: %s", plan.Run)
	}
}

func TestHeadlessCodexCarriesWholeRoleAsDeveloperInstructions(t *testing.T) {
	constitution := strings.Repeat("0123456789abcdef", 820) + ` a triple quote """ and slash \\ survive`
	plan, err := headlessWithTestConfig(HeadlessRequest{
		Engine: pfmengine.Codex, Name: "worker", CWD: "/work/alpha",
		Prompt: "caller prompt", PromptChannel: constitution, Home: "/home/tester",
		PrimaryAccount: 1,
	})
	if err != nil {
		t.Fatalf("HeadlessRun() error = %v", err)
	}
	escaped := strings.ReplaceAll(constitution, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"""`, `\"\"\"`)
	want := Quote("developer_instructions=\"\"\"\n" + escaped + "\"\"\"")
	if !strings.Contains(plan.Run, " '-c' "+want) {
		t.Fatalf(
			"Codex command does not carry complete developer instructions (%d bytes): %s",
			len(constitution),
			plan.Run,
		)
	}
	if strings.Contains(plan.Run, "caller prompt") {
		t.Fatalf("Codex caller prompt leaked onto argv: %s", plan.Run)
	}
}

func TestHeadlessClaudeAccountOneAndCacheArmed(t *testing.T) {
	machine := testMachineConfig("/home/tester")
	cache1H := true
	machine.Claude.Cache1H = false
	if machine.Accounts[0].Claude != nil {
		machine.Accounts[0].Claude.Cache1H = false
	}
	plan, err := HeadlessRun(HeadlessRequest{
		Engine:         "cc",
		Name:           "worker",
		CWD:            "/work/alpha",
		Home:           "/home/tester",
		PrimaryAccount: 1,
		Cache1H:        &cache1H,
		Config:         machine,
	})
	if err != nil {
		t.Fatalf("HeadlessRun() error = %v", err)
	}
	if strings.Contains(plan.Run, "CLAUDE_CONFIG_DIR=/home") {
		t.Fatalf("account 1 must keep the default config dir: %s", plan.Run)
	}
	// Match the assignments themselves, never "…=1 claude": launch-env words
	// sit between the cache assignment and the binary, so an adjacency check
	// would pass vacuously with both cache modes set.
	if launchEnv(t, plan.Run)["CACHE_LIVE_CONTROL_MAIN_TTL"] != "1h" {
		t.Fatalf("1h cache not armed: %s", plan.Run)
	}
	if parsedShell(t, plan.Run).SettingsEnv["ENABLE_PROMPT_CACHING_1H"] != "" {
		t.Fatalf("global 1h switch set, lifting sub-agents to 1h: %s", plan.Run)
	}
}

// TestHeadlessCodexTakesNeitherNameNorPrompt records WHY the Codex command is
// bare: codex 0.147 has no launch flag for a thread name, so the name is typed
// into its rename UI, and a prompt on the command line would start a turn
// before that can happen.
func TestHeadlessCodexTakesNeitherNameNorPrompt(t *testing.T) {
	plan, err := headlessWithTestConfig(HeadlessRequest{
		Engine:         pfmengine.Codex,
		Name:           "_KILL codex worker",
		CWD:            "/work/alpha",
		Prompt:         "read the incident report",
		Home:           "/home/tester",
		PrimaryAccount: 1,
	})
	if err != nil {
		t.Fatalf("HeadlessRun() error = %v", err)
	}
	if plan.PromptOnCommandLine {
		t.Fatal("Codex must be prompted through its TUI, not its command line")
	}
	if !strings.HasSuffix(
		plan.Run,
		" codex --dangerously-bypass-approvals-and-sandbox",
	) {
		t.Fatalf("unexpected codex command: %s", plan.Run)
	}
	if strings.Contains(plan.Run, "read the incident report") ||
		strings.Contains(plan.Run, "_KILL codex worker") {
		t.Fatalf("codex command carried a name or prompt: %s", plan.Run)
	}
	if !strings.Contains(plan.Run, "-u CODEX_THREAD_ID") {
		t.Fatalf("codex thread id not stripped: %s", plan.Run)
	}
}

func TestHeadlessRunRefusals(t *testing.T) {
	base := HeadlessRequest{
		Engine:         "cc",
		Name:           "worker",
		CWD:            "/work/alpha",
		Home:           "/home/tester",
		PrimaryAccount: 1,
	}
	for _, testCase := range []struct {
		name    string
		mutate  func(*HeadlessRequest)
		message string
	}{
		{"no name", func(r *HeadlessRequest) { r.Name = "" }, "requires a name"},
		{"newline in name", func(r *HeadlessRequest) { r.Name = "a\nb" }, "newlines"},
		{"no directory", func(r *HeadlessRequest) { r.CWD = "" }, "project directory"},
		{"unknown engine", func(r *HeadlessRequest) { r.Engine = "gpt" }, "no headless planner registered"},
		{"account off roster", func(r *HeadlessRequest) { r.PrimaryAccount = 9 }, "primary account"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request := base
			testCase.mutate(&request)
			_, err := headlessWithTestConfig(request)
			if err == nil {
				t.Fatal("HeadlessRun() accepted the request")
			}
			if !strings.Contains(err.Error(), testCase.message) {
				t.Fatalf("error = %v, want it to mention %q", err, testCase.message)
			}
		})
	}
}
