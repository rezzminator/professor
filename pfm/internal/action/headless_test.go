package action

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestHeadlessClaudeAssignsSessionAndLaunchRecord(t *testing.T) {
	home := t.TempDir()
	request := HeadlessRequest{
		Engine: pfmengine.Claude, Name: "worker", CWD: "/work/alpha",
		Home: home, PrimaryAccount: 2,
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
	home := t.TempDir()
	request := HeadlessRequest{
		Engine: pfmengine.Claude, Name: "worker", CWD: "/work/alpha",
		Home: home, PrimaryAccount: 2,
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
	home := t.TempDir()
	plan, err := headlessWithTestConfig(HeadlessRequest{
		Engine:         "cc",
		Name:           "_KILL worker 3",
		CWD:            "/work/alpha",
		Prompt:         "audit the firewall rules",
		Home:           home,
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
	home := t.TempDir()
	constitution := strings.Repeat("0123456789abcdef", 820) + ` a triple quote """ and slash \\ survive`
	plan, err := headlessWithTestConfig(HeadlessRequest{
		Engine: pfmengine.Codex, Name: "worker", CWD: "/work/alpha",
		Prompt: "caller prompt", PromptChannel: constitution, Home: home,
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
	home := t.TempDir()
	machine := testMachineConfig(home)
	cache1H := true
	machine.Claude.Cache1H = false
	if machine.Accounts[0].Claude != nil {
		machine.Accounts[0].Claude.Cache1H = false
	}
	plan, err := HeadlessRun(HeadlessRequest{
		Engine:         "cc",
		Name:           "worker",
		CWD:            "/work/alpha",
		Home:           home,
		PrimaryAccount: 1,
		Cache1H:        &cache1H,
		Config:         machine,
	})
	if err != nil {
		t.Fatalf("HeadlessRun() error = %v", err)
	}
	if !strings.Contains(plan.Run, "CLAUDE_CONFIG_DIR="+Quote(pfmconfig.DefaultAccountDir(home, 1))) {
		t.Fatalf("account 1 must use its config dir %s: %s", pfmconfig.DefaultAccountDir(home, 1), plan.Run)
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
	home := t.TempDir()
	plan, err := headlessWithTestConfig(HeadlessRequest{
		Engine:         pfmengine.Codex,
		Name:           "_KILL codex worker",
		CWD:            "/work/alpha",
		Prompt:         "read the incident report",
		Home:           home,
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
		" codex --no-daemon --dangerously-bypass-approvals-and-sandbox",
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
			request.Home = t.TempDir()
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

func TestHeadlessCodexStatesSelectedHome(t *testing.T) {
	home := t.TempDir()
	machine := testMachineConfig(home)
	want := filepath.Join(home, ".codex-2")
	if err := os.Remove(filepath.Join(want, "auth.json")); err != nil {
		t.Fatal(err)
	}
	plan, err := HeadlessRun(HeadlessRequest{
		Engine: pfmengine.Codex, Name: "worker", CWD: "/work",
		Home: home, PrimaryAccount: 2, Config: machine,
	})
	if err != nil || plan.CodexHome != want {
		t.Fatalf("plan CodexHome=%q error=%v; want %q", plan.CodexHome, err, want)
	}
}

// A chat the policy says must carry the fleet prompt, started without it, is
// reported by the plan the starter's surface prints: one warning naming the
// cause, none once the prompt is staged. Which policies want the prompt is
// claudelaunch.TestPromptWarning's.
func TestHeadlessClaudePlansWarnWhenTheFleetPromptCannotReachTheChat(t *testing.T) {
	for _, entry := range []struct {
		name  string
		mode  string
		stage bool
		want  string
	}{
		{"professor, no clone recorded", pfmconfig.SystemPromptProfessor, false, "no source repository recorded"},
		{"professor, prompt staged", pfmconfig.SystemPromptProfessor, true, ""},
	} {
		for _, door := range []string{"new", "fork"} {
			t.Run(entry.name+"/"+door, func(t *testing.T) {
				home := t.TempDir()
				if entry.stage {
					stageProfessorPrompt(t, home)
				}
				machine := testMachineConfig(home)
				machine.Claude.SystemPrompt = entry.mode
				var plan HeadlessPlan
				var err error
				if door == "new" {
					plan, err = HeadlessRun(HeadlessRequest{
						Engine: pfmengine.Claude, Name: "worker", CWD: "/work/alpha",
						Home: home, PrimaryAccount: 1, Config: machine,
					})
				} else {
					plan, err = HeadlessFork(HeadlessForkRequest{
						Engine: pfmengine.Claude, SessionID: "thread-123", Name: "branch",
						CWD: "/work/alpha", Home: home, PrimaryAccount: 1, Config: machine,
					})
				}
				if err != nil {
					t.Fatal(err)
				}
				if entry.want == "" {
					if len(plan.Warnings) != 0 {
						t.Fatalf("warnings = %q, want none", plan.Warnings)
					}
					return
				}
				if len(plan.Warnings) != 1 || !strings.Contains(plan.Warnings[0], entry.want) {
					t.Fatalf("warnings = %q, want one naming %q", plan.Warnings, entry.want)
				}
			})
		}
	}
}

func TestHeadlessClaudeRetainsStructuredSeatRecipe(t *testing.T) {
	request := HeadlessRequest{
		Engine:         pfmengine.Claude,
		Name:           "REVIEW",
		CWD:            "/work/alpha",
		Home:           t.TempDir(),
		PrimaryAccount: 2,
		Prompt:         "a prompt with 'quotes' and $(nope)",
	}
	plan, err := headlessWithTestConfig(request)
	if err != nil {
		t.Fatal(err)
	}
	if plan.SeatRun == nil || plan.SeatRun.CWD != request.CWD || plan.SeatRun.Binary == "" ||
		len(plan.SeatRun.Args) == 0 ||
		!slices.Contains(plan.SeatRun.Args, request.Prompt) {
		t.Fatalf("structured recipe=%+v", plan.SeatRun)
	}
	parsed := parsedShell(t, plan.Run)
	if parsed.SessionID != plan.Record.SessionID {
		t.Fatalf("recipe and launch identity diverged: %+v", plan)
	}
}

func TestHeadlessCodexRetainsStructuredSeatRecipe(t *testing.T) {
	for _, yolo := range []bool{false, true} {
		t.Run(strconv.FormatBool(yolo), func(t *testing.T) {
			home := t.TempDir()
			machine := testMachineConfig(home)
			machine.Codex.Binary = "/opt/codex stable"
			machine.Codex.Yolo = yolo
			t.Setenv("CLAUDECODE", "stale")
			t.Setenv("CODEX_HOME", "/stale")
			request := HeadlessRequest{
				Engine:         pfmengine.Codex,
				Name:           "REVIEW",
				CWD:            "/work/alpha",
				Home:           home,
				PrimaryAccount: 2,
				Model:          "next-model",
				Effort:         "high",
				PromptChannel:  "developer 'text'",
				Config:         machine,
			}
			plan, err := PlanCodex(request)
			if err != nil {
				t.Fatal(err)
			}
			prefix := []string{pfmengine.CodexNoDaemon, "--sandbox", "workspace-write"}
			if yolo {
				prefix = []string{pfmengine.CodexNoDaemon, "--dangerously-bypass-approvals-and-sandbox"}
			}
			prefix = append(prefix, "--model", request.Model)
			want := prefix
			want = append(want, CodexEffortArg("high")...)
			want = append(want, CodexDeveloperInstructionsArg(request.PromptChannel)...)
			account, _ := machine.CodexAccountByID(2)
			if plan.SeatRun == nil || plan.SeatRun.Binary != machine.Codex.Binary || plan.SeatRun.CWD != request.CWD ||
				!reflect.DeepEqual(plan.SeatRun.Args, want) {
				t.Fatalf("typed Codex recipe=%+v want args=%q", plan.SeatRun, want)
			}
			if lastEnvironmentValueNative(plan.SeatRun.Env, "CODEX_HOME") != account.Home ||
				lastEnvironmentValueNative(plan.SeatRun.Env, "CLAUDECODE") != "" {
				t.Fatalf("Codex environment=%v", plan.SeatRun.Env)
			}
		})
	}
}

// A bare roster tier is not a Codex model id: OpenAI answers `codex --model astra` with a 400. chat new and
// the MCP new tool reach PlanCodex, which resolves the tier through the registered agent persona of
// that name, the roster's own model; a full id passes through unchanged.
func TestPlanCodexResolvesBareRosterTierToCodexModelID(t *testing.T) {
	home := t.TempDir()
	testjail.StageGlobalAgents(t, home)
	// Professor ships no Codex-model agents, so the bare tiers resolve through personas staged here.
	for name, model := range map[string]string{"astra": "gpt-6-astra", "sol": "gpt-6-sol", "luna": "gpt-6-luna"} {
		persona := "---\nname: " + name + "\nmodel: " + model + "\n---\n"
		if err := os.WriteFile(
			filepath.Join(home, ".claude", "agents", name+".md"),
			[]byte(persona),
			0o644,
		); err != nil {
			t.Fatalf("stage %s persona: %v", name, err)
		}
	}
	machine := testMachineConfig(home)
	for tier, want := range map[string]string{"astra": "gpt-6-astra", "gpt-6-astra": "gpt-6-astra"} {
		plan, err := PlanCodex(HeadlessRequest{
			Engine: pfmengine.Codex, Name: "TIER", CWD: "/work/alpha", Home: home,
			PrimaryAccount: 2, Model: tier, Config: machine,
		})
		if err != nil {
			t.Fatalf("PlanCodex(--model %s): %v", tier, err)
		}
		at := slices.Index(plan.SeatRun.Args, "--model")
		if at < 0 || at+1 >= len(plan.SeatRun.Args) || plan.SeatRun.Args[at+1] != want {
			t.Fatalf("--model %s launched args %q, want --model %s", tier, plan.SeatRun.Args, want)
		}
		if !strings.Contains(plan.Run, Quote("--model")+" "+Quote(want)) || strings.Contains(plan.Run, Quote("astra")) {
			t.Fatalf("--model %s launched run %q, want --model %s", tier, plan.Run, want)
		}
	}
	for _, tier := range []string{"sol", "luna"} {
		plan, err := PlanCodex(HeadlessRequest{
			Engine: pfmengine.Codex, Name: "TIER", CWD: "/work/alpha", Home: home,
			PrimaryAccount: 2, Model: tier, Config: machine,
		})
		if err != nil {
			t.Fatalf("PlanCodex(--model %s): %v", tier, err)
		}
		at := slices.Index(plan.SeatRun.Args, "--model")
		if got := plan.SeatRun.Args[at+1]; !strings.HasPrefix(got, "gpt-") || !strings.HasSuffix(got, "-"+tier) {
			t.Fatalf("--model %s launched --model %q, want the roster's gpt-*-%s id", tier, got, tier)
		}
	}
}

func lastEnvironmentValueNative(env []string, key string) string {
	value := ""
	for _, entry := range env {
		if v, ok := strings.CutPrefix(entry, key+"="); ok {
			value = v
		}
	}
	return value
}

func TestHeadlessCanonicalEngineAndOpenCodeRecipe(t *testing.T) {
	home := t.TempDir()
	machine := testMachineConfig(home)
	machine.Claude.Binary = "/opt/custom-cc"
	machine.Codex.Binary = "/opt/custom-cx"
	machine.OpenCode.Binary = "/opt/custom-ox"
	machine.OpenCodeAccounts = []pfmconfig.OpenCodeAccount{{ID: 2, Home: home + "/ox"}}
	for _, id := range []pfmengine.ID{pfmengine.Claude, pfmengine.Codex, pfmengine.OpenCode} {
		t.Run(string(id), func(t *testing.T) {
			request := HeadlessRequest{
				Engine:         id,
				Name:           "RAW",
				CWD:            home,
				Home:           home,
				PrimaryAccount: 2,
				Config:         machine,
				Prompt:         "a\nb",
			}
			var plan HeadlessPlan
			var err error
			switch id {
			case pfmengine.Claude:
				plan, err = PlanClaude(request)
			case pfmengine.Codex:
				plan, err = PlanCodex(request)
			case pfmengine.OpenCode:
				plan, err = PlanOpenCode(request)
			}
			if err != nil {
				t.Fatalf("canonical %s recipe: %v", id, err)
			}
			if plan.Engine != id || plan.SeatRun == nil {
				t.Fatalf("canonical identity=%q recipe=%+v want=%s", plan.Engine, plan.SeatRun, id)
			}
			if id == pfmengine.OpenCode {
				if plan.Record != nil || !plan.PromptOnCommandLine ||
					!reflect.DeepEqual(plan.SeatRun.Args, []string{home, "--prompt", request.Prompt}) ||
					plan.SeatRun.Binary != machine.OpenCode.Binary {
					t.Fatalf("OpenCode typed plan=%+v recipe=%+v", plan, plan.SeatRun)
				}
			}
		})
	}
}

func TestPlanCodexPreservesResumeIdentity(t *testing.T) {
	for _, sid := range []string{"", "thread 'safe'"} {
		t.Run(sid, func(t *testing.T) {
			home := t.TempDir()
			machine := testMachineConfig(home)
			machine.Codex.Binary = "/opt/custom-cx"
			plan, err := PlanCodex(
				HeadlessRequest{
					Name:            "RAW",
					CWD:             home,
					Home:            home,
					PrimaryAccount:  2,
					Config:          machine,
					ResumeSessionID: sid,
					Model:           "next",
					Effort:          "high",
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			want := append(codexAutonomyArgs(machine.EffectiveCodex(2).Yolo), "--model", "next")
			want = append(want, CodexEffortArg("high")...)
			if sid != "" {
				want = append(want, "resume", sid)
			}
			if !reflect.DeepEqual(plan.SeatRun.Args, want) || plan.Record != nil {
				t.Fatalf("resume recipe args=%q want=%q record=%+v", plan.SeatRun.Args, want, plan.Record)
			}
			if sid != "" && !strings.Contains(plan.Run, Quote("resume")+" "+Quote(sid)) {
				t.Fatalf("resume shell=%s", plan.Run)
			}
		})
	}
}

func TestHeadlessForkRetainsTypedRecipe(t *testing.T) {
	for _, id := range []pfmengine.ID{pfmengine.Claude, pfmengine.Codex} {
		t.Run(string(id), func(t *testing.T) {
			home := t.TempDir()
			machine := testMachineConfig(home)
			plan, err := HeadlessFork(
				HeadlessForkRequest{
					Engine:         id,
					SessionID:      "session 'quoted'",
					Name:           "FORK",
					CWD:            home,
					Home:           home,
					PrimaryAccount: 2,
					Config:         machine,
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			if plan.SeatRun == nil || plan.SeatRun.CWD != home || plan.Engine != id {
				t.Fatalf("fork typed recipe=%+v identity=%s", plan.SeatRun, plan.Engine)
			}
			if !slices.Contains(plan.SeatRun.Args, "session 'quoted'") {
				t.Fatalf("fork args=%q", plan.SeatRun.Args)
			}
		})
	}
}

func TestHeadlessOpenCodeCarriesReporterAndRejectsUnsupportedPolicy(t *testing.T) {
	home := t.TempDir()
	machine := testMachineConfig(home)
	machine.OpenCodeAccounts = []pfmconfig.OpenCodeAccount{{ID: 1, Home: home}}
	request := HeadlessRequest{
		Name:           "RAW",
		CWD:            home,
		Home:           home,
		PrimaryAccount: 1,
		Config:         machine,
		Model:          "openai/gpt-6.1-sol",
		Effort:         "High",
		Prompt:         "Initial task 'quoted'\nnext line",
		PromptChannel:  "Constitution, not the initial task.",
	}
	plan, err := PlanOpenCode(request)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(
		lastEnvironmentValueNative(plan.SeatRun.Env, "OPENCODE_CONFIG_CONTENT"),
		"opencode-herdr-plugin.mjs",
	) ||
		!slices.Contains(plan.SeatRun.Args, request.Model) {
		t.Fatalf(
			"OpenCode native policy args=%q reporter=%q",
			plan.SeatRun.Args,
			lastEnvironmentValueNative(plan.SeatRun.Env, "OPENCODE_CONFIG_CONTENT"),
		)
	}
	for key, want := range map[string]string{
		"PFM_OPENCODE_MODEL":   request.Model,
		"PFM_OPENCODE_VARIANT": "high",
		"PFM_OPENCODE_POLICY":  request.PromptChannel,
	} {
		if got := lastEnvironmentValueNative(plan.SeatRun.Env, key); got != want {
			t.Errorf("%s=%q, want %q", key, got, want)
		}
	}
	if !slices.Contains(plan.SeatRun.Args, request.Prompt) || slices.Contains(plan.SeatRun.Args, "--variant") ||
		slices.Contains(plan.SeatRun.Args, request.PromptChannel) {
		t.Fatalf("native prompt/policy argv=%q", plan.SeatRun.Args)
	}
	for _, effort := range []string{"", "turbo"} {
		test := request
		test.Effort = effort
		_, err := PlanOpenCode(test)
		if effort == "" && err != nil ||
			effort != "" && (err == nil || !strings.Contains(err.Error(), "unknown OpenCode effort")) {
			t.Fatalf("effort %q error=%v", effort, err)
		}
	}
	test := request
	test.PromptChannel = "policy\x00invalid"
	if _, err := PlanOpenCode(test); err == nil || !strings.Contains(err.Error(), "prompt channel") {
		t.Fatalf("invalid prompt channel error=%v", err)
	}
}

func TestOpenCodeNativePolicyDoesNotDependOnReporter(t *testing.T) {
	request := Request{
		OpenCodePlugin:        "/fixture/seat.mjs",
		OpenCodePromptChannel: "Policy only.",
	}
	request.Persona.Model, request.Persona.Effort = "openai/gpt-6.1-sol", "high"
	assignments, err := openCodePersonaAssignments(request)
	if err != nil {
		t.Fatal(err)
	}
	for _, word := range []string{
		`OPENCODE_CONFIG_CONTENT={"plugin":["file:///fixture/seat.mjs"]}`,
		"PFM_OPENCODE_MODEL=openai/gpt-6.1-sol", "PFM_OPENCODE_VARIANT=high", "PFM_OPENCODE_POLICY=Policy only.",
	} {
		if !slices.Contains(assignments, word) {
			t.Errorf("native assignments lack %q: %q", word, assignments)
		}
	}
	request.OpenCodePlugin = ""
	if _, err := openCodePersonaAssignments(
		request,
	); err == nil ||
		!strings.Contains(err.Error(), "require the native seat plugin") {
		t.Fatalf("missing delivery mechanism error=%v", err)
	}
}
