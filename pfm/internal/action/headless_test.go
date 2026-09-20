package action

import (
	"strings"
	"testing"

	pfmengine "hostops/pfm/internal/engine"
)

// TestHeadlessClaudeCarriesTheFullLaunchCeremony pins the command a headless
// Claude chat runs. Every clause is load-bearing: the environment strip (a
// chat born from another chat's Bash tool would otherwise inherit its identity
// and account), the account's config dir, an explicit cache decision, the
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
	want := "env -u CLAUDE_CODE_SESSION_ID -u CLAUDECODE -u CLAUDE_CODE_CHILD_SESSION -u CLAUDE_CONFIG_DIR" +
		" -u CLAUDE_PROJECT_DIR" +
		" -u ENABLE_PROMPT_CACHING_1H -u FORCE_PROMPT_CACHING_5M -u CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT" +
		" -u ANTHROPIC_BASE_URL -u ANTHROPIC_AUTH_TOKEN -u ANTHROPIC_MODEL" +
		" -u ANTHROPIC_SMALL_FAST_MODEL -u CLAUDE_CODE_AUTO_COMPACT_WINDOW" +
		" -u CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC" +
		" -u CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK" +
		" -u CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY" +
		" -u CODEX_THREAD_ID" +
		" CLAUDE_CONFIG_DIR='/home/tester/.cc/2' FORCE_PROMPT_CACHING_5M=1" +
		" " + webSearchBudgetName + "=" + Quote(webSearchBudgetValue) +
		" " + truecolorName + "=" + Quote("1") +
		" " + spawnDepthName + "=" + Quote("8") +
		" claude '--name' '_KILL worker 3' 'audit the firewall rules'" +
		" '--settings' " + Quote(pfmengine.OutputStyleDefaultSettings) +
		" --allow-dangerously-skip-permissions --dangerously-skip-permissions"
	if plan.Run != want {
		t.Fatalf("run command\n got: %s\nwant: %s", plan.Run, want)
	}
}

func TestHeadlessClaudeAccountOneAndCacheArmed(t *testing.T) {
	plan, err := headlessWithTestConfig(HeadlessRequest{
		Engine:         "cc",
		Name:           "worker",
		CWD:            "/work/alpha",
		Home:           "/home/tester",
		PrimaryAccount: 1,
		Cache1H:        true,
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
	if !strings.Contains(plan.Run, " ENABLE_PROMPT_CACHING_1H=1 ") {
		t.Fatalf("1h cache not armed: %s", plan.Run)
	}
	if strings.Contains(plan.Run, "FORCE_PROMPT_CACHING_5M=1") {
		t.Fatalf("both cache modes set: %s", plan.Run)
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
