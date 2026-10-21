package action

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/professor"
	"github.com/rezzminator/professor/pfm/internal/testjail"
	"github.com/rezzminator/professor/pfm/internal/workbench"
)

func TestSynthesizeWorkbenchPersona(t *testing.T) {
	requests, prompt := personaGoldenRequests(t, t.TempDir())
	for _, request := range append([]Request(nil), requests...) {
		if request.Row.Kind == compose.NewOpenCode || request.Row.Kind == compose.ResumeOpenCode {
			request.OpenCodeFleetPrompt = ""
			requests = append(requests, request)
		}
	}
	for _, request := range requests {
		name := request.Row.Kind.String()
		if (request.Row.Kind == compose.NewOpenCode || request.Row.Kind == compose.ResumeOpenCode) &&
			request.OpenCodeFleetPrompt == "" {
			name += "/without-fleet"
		}
		t.Run(name, func(t *testing.T) {
			plan, err := Synthesize(request)
			if err != nil {
				t.Fatal(err)
			}
			var words []string
			switch request.Row.Kind {
			case compose.NewClaude:
				words = []string{
					Quote("--system-prompt-file") + " " + Quote(prompt),
					"'--effort' 'xhigh'",
					"'--model' 'gpt-x'",
					"'--name' '_SCRIBE:3'",
				}
			case compose.ResumeClaude, compose.Agent:
				words = []string{
					"'--resume' '44444444-4444-4444-8444-444444444444'",
					Quote("--system-prompt-file") + " " + Quote(prompt),
					"'--effort' 'xhigh'",
					"'--model' 'gpt-x'",
				}
			case compose.ResumeCodex:
				words = []string{
					`'--model' 'gpt-x' '-c' 'model_reasoning_effort="xhigh"' '-c' 'developer_instructions="""` + "\n" + `You are scribe."""' 'resume' '44444444-4444-4444-8444-444444444444'`,
				}
			case compose.NewOpenCode, compose.ResumeOpenCode:

				fleetEnv := ""
				if request.OpenCodeFleetPrompt != "" {
					fleetEnv = " PFM_OPENCODE_FLEET_FILE=" + Quote(request.OpenCodeFleetPrompt)
				}
				words = []string{
					`OPENCODE_CONFIG_CONTENT='{"instructions":["` + prompt + `"],"plugin":["file://` + request.OpenCodePlugin + `","file://` + request.OpenCodeHerdrPlugin + `"]}'` + fleetEnv + " PFM_OPENCODE_SYSTEM_FILE=" + Quote(
						prompt,
					) + " opencode",
				}

			case compose.NewCodex:
				control := request
				control.Persona = requests[0].Persona
				control.Persona.Prompt, control.Persona.Body, control.Persona.Effort, control.Persona.Model = "", "", "", ""
				plain, err := Synthesize(control)
				if err != nil || plan.Line != plain.Line {
					t.Fatalf("cx changed: %s, %v", plan.Line, err)
				}
				return
			}
			for _, word := range words {
				if !strings.Contains(plan.Run, word) {
					t.Errorf("run lacks %q: %s", word, plan.Run)
				}
			}
			if plan.ChatServer == nil || plan.ChatServer.Run != plan.Run {
				t.Fatalf("server run differs: %#v", plan.ChatServer)
			}
		})
	}
}

func actionWorkbenchFixture(t *testing.T, manifest string) (Request, string) {
	t.Helper()
	root := jailAction(t)
	project := filepath.Join(root, "acme")
	dir := filepath.Join(project, "docs", "scribe")
	for path, body := range map[string]string{
		professor.BaselinePath(project):               "{}",
		paths.WorkbenchManifest(dir):                  manifest,
		filepath.Join(dir, ".professor", "scribe.md"): "You are scribe.",
		filepath.Join(dir, "CLAUDE.md"):               "Scribe.\n",
	} {
		if err := atomicfile.Write(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	request := stressRequests(filepath.Join(root, "home"))[0]
	request.Row = compose.Row{
		Kind:      compose.ResumeClaude,
		ID:        "44444444-4444-4444-8444-444444444444",
		CWD:       dir,
		Workbench: dir,
	}
	return request, dir
}

func TestWorkbenchPersonaEffort(t *testing.T) {
	for _, test := range []struct {
		name, manifest, wantEffort, quotedEffort string
		engines                                  []pfmengine.ID
		mode                                     workbench.Mode
		refused                                  map[pfmengine.ID]bool
		passThrough                              bool
	}{
		{
			name: "unknown Claude effort", manifest: `{"prompt":"scribe.md","effort":"turbo"}`,
			engines: []pfmengine.ID{pfmengine.Claude}, mode: workbench.New,
			refused: map[pfmengine.ID]bool{pfmengine.Claude: true}, quotedEffort: `"turbo"`,
		},
		{
			name:     "Codex-only effort refused for Claude",
			manifest: `{"prompt":"scribe.md","effort":"minimal","engines":["claude","codex"]}`,
			engines:  []pfmengine.ID{pfmengine.Claude, pfmengine.Codex}, mode: workbench.New,
			refused: map[pfmengine.ID]bool{pfmengine.Claude: true}, wantEffort: "minimal",
		},
		{
			name:     "unknown Codex effort",
			manifest: `{"prompt":"scribe.md","effort":"turbo","engines":["claude","codex"]}`,
			engines:  []pfmengine.ID{pfmengine.Codex}, mode: workbench.Resume,
			refused: map[pfmengine.ID]bool{pfmengine.Codex: true},
		},
		{
			name:     "mixed case lowered",
			manifest: `{"prompt":"scribe.md","effort":"XHigh","engines":["claude","codex"]}`,
			engines:  []pfmengine.ID{pfmengine.Claude, pfmengine.Codex}, mode: workbench.New,
			wantEffort: "xhigh",
		},
		{
			name:     "OpenCode effort unvalidated",
			manifest: `{"prompt":"scribe.md","effort":"turbo","engines":["opencode"]}`,
			engines:  []pfmengine.ID{pfmengine.OpenCode}, mode: workbench.New, wantEffort: "turbo",
		},
		{
			name: "ForLaunch error passes through", manifest: `{"prompt":""}`,
			engines: []pfmengine.ID{pfmengine.Claude}, mode: workbench.New, passThrough: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, dir := actionWorkbenchFixture(t, test.manifest)
			for _, engine := range test.engines {
				t.Run(string(engine), func(t *testing.T) {
					persona, err := WorkbenchPersona(dir, engine, test.mode)
					if test.passThrough {
						_, wantErr := workbench.ForLaunch(dir, engine, test.mode)
						if wantErr == nil || err == nil || err.Error() != wantErr.Error() {
							t.Fatalf("WorkbenchPersona = %v; want ForLaunch error %v", err, wantErr)
						}
						return
					}
					if test.refused[engine] {
						if err == nil || !strings.Contains(err.Error(), "workbench "+dir) ||
							(test.quotedEffort != "" && !strings.Contains(err.Error(), test.quotedEffort)) ||
							!reflect.DeepEqual(persona, workbench.Persona{}) {
							t.Fatalf(
								"WorkbenchPersona = %#v, %v; want contextual refusal and zero persona",
								persona,
								err,
							)
						}
						return
					}
					if err != nil || !persona.Applies() || persona.Effort != test.wantEffort {
						t.Fatalf("WorkbenchPersona = %#v, %v; want effort %q", persona, err, test.wantEffort)
					}
				})
			}
		})
	}
}

func TestExecutorWorkbenchDoors(t *testing.T) {
	for _, test := range []struct {
		name, manifest string
		kind           compose.Kind
		both           bool
	}{
		{"refusal before any side effect", `{"prompt":""}`, compose.ResumeClaude, true},
		{"allowed", `{"prompt":"scribe.md","effort":"xhigh","engines":["claude","codex","opencode"]}`, compose.ResumeClaude, true},
		{"disabled resume", `{"prompt":"scribe.md"}`, compose.ResumeCodex, false},
		{"booting row ignores the manifest", `{"prompt":""}`, compose.Booting, false},
	} {
		doors := []bool{false}
		if test.both {
			doors = append(doors, true)
		}
		for _, detached := range doors {
			t.Run(test.name+map[bool]string{false: "/open", true: "/detached"}[detached], func(t *testing.T) {
				request, dir := actionWorkbenchFixture(t, test.manifest)
				request.Row.Kind = test.kind
				processes := &fakeProcesses{}
				var wantError, wantRun string
				switch test.name {
				case "refusal before any side effect":
					processes.processes = []Process{
						{PID: 42, Argv: []string{"claude", "--resume", request.Row.ID}, TTY: "pts/1"},
					}
					_, err := workbench.ForLaunch(dir, pfmengine.Claude, workbench.Resume)
					if err == nil {
						t.Fatal("fixture ForLaunch did not fail")
					}
					wantError = err.Error()
				case "disabled resume":
					plain, err := Synthesize(request)
					if err != nil {
						t.Fatal(err)
					}
					wantRun = plain.Run
				case "booting row ignores the manifest":
					request.Row = compose.Row{
						Kind: compose.Booting, ID: "cc-new-fixture-1", Socket: "cc-new-fixture-1",
						SessionName: "cc-new-fixture-1", CWD: dir, Workbench: dir,
					}
				}
				tmux := &fakeActionTmux{alive: map[string]bool{}}
				executor, err := New(Dependencies{
					Tmux:      tmux,
					Processes: processes,
					Gate:      fixedGate(false),
					Runner:    &captureRunner{},
					Stderr:    io.Discard,
				})
				if err != nil {
					t.Fatal(err)
				}
				var line string
				if detached {
					_, err = executor.OpenDetached(context.Background(), request)
				} else {
					line, err = executor.Open(context.Background(), request)
				}
				if wantError != "" {
					if err == nil || !strings.Contains(err.Error(), wantError) || len(tmux.created) != 0 ||
						len(processes.terminated) != 0 {
						t.Fatalf(
							"refusal=%v, created=%v, terminated=%v; want %q",
							err,
							tmux.created,
							processes.terminated,
							wantError,
						)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if test.kind == compose.Booting {
					if !strings.Contains(line, "cc-new-fixture-1") || len(tmux.created) != 0 {
						t.Fatalf("booting attach = %q, created=%v", line, tmux.created)
					}
					return
				}
				if len(tmux.created) != 1 {
					t.Fatalf("created=%d; want one server", len(tmux.created))
				}
				run := tmux.created[0].Run
				if test.name == "disabled resume" {
					if run != wantRun {
						t.Fatalf("disabled resume=%s; want %s", run, wantRun)
					}
					return
				}
				if !stringsContainsAll(run, filepath.Join(dir, ".professor", "scribe.md"), "'--effort' 'xhigh'") {
					t.Fatalf("persona missing: %s", run)
				}
			})
		}
	}
}

func TestExecutorWorkbenchPrep(t *testing.T) {
	for _, test := range []struct {
		name string
		kind compose.Kind
		fail bool
	}{
		{"Codex mirror before server", compose.ResumeCodex, false},
		{"OpenCode before server", compose.NewOpenCode, false},
		{"mirror write fails", compose.ResumeCodex, true},
		{"plugin write fails", compose.NewOpenCode, true},
	} {
		for _, detached := range []bool{false, true} {
			t.Run(test.name+map[bool]string{false: "/open", true: "/detached"}[detached], func(t *testing.T) {
				request, dir := actionWorkbenchFixture(
					t, `{"prompt":"scribe.md","effort":"xhigh","engines":["claude","codex","opencode"]}`,
				)
				request.Row.Kind = test.kind
				engine, outputs := "codex", []string(nil)
				if test.kind == compose.NewOpenCode {
					engine, outputs = "opencode", []string{paths.OpenCodeWorkbenchPlugin(request.Home)}
				}
				reply, code := testjail.CleanMirrorReply, 0
				if test.fail && test.kind == compose.ResumeCodex {
					reply, code = testjail.FailedMirrorReply, 1
				}
				record := testjail.FakeEngineSync(t, reply, code)
				if test.fail && test.kind == compose.NewOpenCode {
					if err := atomicfile.Write(
						filepath.Join(request.Home, ".local", "state", "pfm"),
						nil,
						0o600,
					); err != nil {
						t.Fatal(err)
					}
				}
				tmux := &fakeActionTmux{alive: map[string]bool{}}
				if !test.fail {
					tmux.onCreate = func() {
						if !testjail.MirrorBuilt(t, record, engine, dir) {
							t.Errorf("not prepared before spawn: no %s mirror of %s", engine, dir)
						}
						for _, path := range outputs {
							if _, err := os.Stat(path); err != nil {
								t.Errorf("not prepared before spawn: %s: %v", path, err)
							}
						}
					}
				}
				executor, err := New(Dependencies{Tmux: tmux, Processes: &fakeProcesses{}, Stderr: io.Discard})
				if err != nil {
					t.Fatal(err)
				}
				if detached {
					_, err = executor.OpenDetached(context.Background(), request)
				} else {
					_, err = executor.Open(context.Background(), request)
				}
				if test.fail {
					if err == nil || len(tmux.created) != 0 {
						t.Fatalf("preparation refusal=%v, created=%v", err, tmux.created)
					}
				} else if err != nil || len(tmux.created) != 1 {
					t.Fatalf("prepared open=%v, created=%v; want one server", err, tmux.created)
				}
			})
		}
	}
}

func TestExecutorWorkbenchOpenCodeFleetPrompt(t *testing.T) {
	for _, marker := range []string{"clone recorded", "no clone recorded", "recorded clone gone", "marker unreadable"} {
		t.Run(marker, func(t *testing.T) {
			request, dir := actionWorkbenchFixture(t, `{"prompt":"scribe.md","engines":["opencode"]}`)
			request.Row.Kind = compose.NewOpenCode
			clone := filepath.Join(t.TempDir(), "clone")
			switch marker {
			case "clone recorded", "recorded clone gone":
				if err := os.MkdirAll(clone, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := paths.WriteSourceRepoMarker(request.Home, clone); err != nil {
					t.Fatal(err)
				}
				if marker == "recorded clone gone" {
					if err := os.Remove(clone); err != nil {
						t.Fatal(err)
					}
				}
			case "marker unreadable":
				if err := os.MkdirAll(paths.SourceRepoPath(request.Home), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			tmux := &fakeActionTmux{alive: map[string]bool{}}
			executor, err := New(Dependencies{Tmux: tmux, Processes: &fakeProcesses{}, Stderr: io.Discard})
			if err != nil {
				t.Fatal(err)
			}
			_, err = executor.Open(context.Background(), request)
			if marker == "marker unreadable" {
				if err == nil ||
					!strings.Contains(err.Error(), "resolve the fleet prompt an OpenCode workbench launch replaces") ||
					!strings.Contains(err.Error(), "read source repository marker") ||
					len(tmux.created) != 0 {
					t.Fatalf("marker refusal=%v, created=%d", err, len(tmux.created))
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(tmux.created) != 1 {
				t.Fatalf("created=%d", len(tmux.created))
			}
			run := tmux.created[0].Run
			prompt := filepath.Join(dir, ".professor", "scribe.md")
			want := `OPENCODE_CONFIG_CONTENT='{"instructions":["` + prompt + `"],"plugin":["file://` + paths.OpenCodeWorkbenchPlugin(
				request.Home,
			) + `","file://` + paths.OpenCodeHerdrPlugin(
				request.Home,
			) + `"]}'`
			if marker == "clone recorded" {
				want += " PFM_OPENCODE_FLEET_FILE=" + Quote(paths.GeneratedHarnessPromptIn(clone, pfmengine.OpenCode))
			}
			want += " PFM_OPENCODE_SYSTEM_FILE=" + Quote(prompt) + " opencode"
			if !strings.Contains(run, want) {
				t.Fatalf("run lacks %q: %s", want, run)
			}
		})
	}
}

// TestExecutorOpenCodeHerdrPlugin: every pfm OpenCode chat loads the Herdr
// reporter plugin staged with the absolute pfm binary, a chat without a
// persona included; a Claude chat stages none.
func TestExecutorOpenCodeHerdrPlugin(t *testing.T) {
	for _, test := range []struct {
		name, manifest string
		kind           compose.Kind
		want           string
	}{
		{"persona chat", `{"prompt":"scribe.md","engines":["opencode"]}`, compose.NewOpenCode, "persona"},
		{"no persona, fresh, outside a workbench", `{"prompt":"scribe.md"}`, compose.NewOpenCode, "plain"},
		{"no persona, resume", `{"prompt":"scribe.md","engines":["claude"]}`, compose.ResumeOpenCode, "plain"},
		{"claude chat", `{"prompt":"scribe.md","engines":["claude"]}`, compose.ResumeClaude, "none"},
		// The reporter is optional: a staging failure launches the chat
		// without it.
		{"staging fails", `{"prompt":"scribe.md","engines":["claude"]}`, compose.ResumeOpenCode, "unstaged"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, dir := actionWorkbenchFixture(t, test.manifest)
			request.Row.Kind = test.kind
			if test.want == "unstaged" {
				if err := os.MkdirAll(paths.OpenCodeHerdrPlugin(request.Home), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if strings.Contains(test.name, "outside a workbench") {
				request.Row.CWD, request.Row.Workbench = filepath.Dir(filepath.Dir(dir)), ""
			}
			tmux := &fakeActionTmux{alive: map[string]bool{}}
			executor, err := New(Dependencies{Tmux: tmux, Processes: &fakeProcesses{}, Stderr: io.Discard})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := executor.Open(context.Background(), request); err != nil {
				t.Fatal(err)
			}
			if len(tmux.created) != 1 {
				t.Fatalf("created=%d", len(tmux.created))
			}
			run := tmux.created[0].Run
			plugin := paths.OpenCodeHerdrPlugin(request.Home)
			herdrURL := `"file://` + plugin + `"`
			staged, stageErr := os.ReadFile(plugin)
			switch test.want {
			case "unstaged":
				if strings.Contains(run, plugin) || strings.Contains(run, "OPENCODE_CONFIG_CONTENT") {
					t.Fatalf("a chat whose Herdr plugin failed to stage loads it: %s", run)
				}
				return
			case "none":
				if stageErr == nil || strings.Contains(run, plugin) {
					t.Fatalf("a Claude chat staged or loaded the OpenCode Herdr plugin: %v\n%s", stageErr, run)
				}
				return
			case "plain":
				want := `OPENCODE_CONFIG_CONTENT='{"plugin":[` + herdrURL + `]}'`
				if !strings.Contains(run, want) || strings.Contains(run, "PFM_OPENCODE_SYSTEM_FILE") {
					t.Fatalf("run lacks %q or carries a persona env: %s", want, run)
				}
			case "persona":
				want := `"plugin":["file://` + paths.OpenCodeWorkbenchPlugin(request.Home) + `",` + herdrURL + `]}'`
				if !strings.Contains(run, want) {
					t.Fatalf("run lacks %q: %s", want, run)
				}
			}
			binary := filepath.Join(request.Home, ".local", "bin", "pfm")
			if stageErr != nil || !strings.Contains(string(staged), `const PFM_BINARY = "`+binary+`"`) {
				t.Fatalf("staged plugin %s does not carry the pfm binary %s: %v", plugin, binary, stageErr)
			}
		})
	}
}

func TestExtensionLaunchDoors(t *testing.T) {
	machine := testMachineConfig(t.TempDir())
	machine.OpenCodeAccounts = []pfmconfig.OpenCodeAccount{{ID: 1, Home: t.TempDir()}}
	machine.Extensions = map[string]pfmconfig.Extension{
		"ponytail": {
			Enabled: true,
			Engines: []string{"codex", "opencode"},
			Env:     map[string]string{"PONYTAIL_SUBAGENT_MATCHER": "^worker$"},
		},
	}
	for _, kind := range []compose.Kind{compose.NewCodex, compose.ResumeCodex, compose.NewOpenCode, compose.ResumeOpenCode} {
		t.Run(kind.String(), func(t *testing.T) {
			request := Request{
				Config:         machine,
				Home:           t.TempDir(),
				PrimaryAccount: 1,
				FreshSocket:    "fixture",
				Row:            compose.Row{Kind: kind, ID: "session", CWD: t.TempDir()},
			}
			plan, err := Synthesize(request)
			if err != nil {
				t.Fatal(err)
			}
			if plan.SeatRun == nil ||
				!strings.Contains(strings.Join(plan.SeatRun.Env, "\n"), "PONYTAIL_SUBAGENT_MATCHER=^worker$") {
				t.Fatalf("extension env lost in typed recipe: %+v", plan.SeatRun)
			}
			if kind != compose.NewCodex && !strings.Contains(plan.Run, "PONYTAIL_SUBAGENT_MATCHER='^worker$'") {
				t.Fatalf("extension env lost in shell recipe: %s", plan.Run)
			}
		})
	}
}
