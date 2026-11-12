package action

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/cmdparse"
	"github.com/rezzminator/professor/pfm/internal/compose"
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
			// The adopter's settings envs ride ahead of pfm's own, which win a shared name.
			owned := map[compose.Kind]string{
				compose.NewClaude: "CLAUDE_CONFIG_DIR", compose.ResumeClaude: "CLAUDE_CONFIG_DIR",
				compose.Agent: "CLAUDE_CONFIG_DIR", compose.ResumeCodex: "CODEX_HOME",
				compose.NewOpenCode: "OPENCODE_CONFIG_CONTENT", compose.ResumeOpenCode: "OPENCODE_CONFIG_CONTENT",
				compose.NewCodex: "CODEX_HOME",
			}[request.Row.Kind]
			request.Persona.Envs = map[string]string{"TEAM_MODE": "strict", owned: "/adopter/value"}
			plan, err := Synthesize(request)
			if err != nil {
				t.Fatal(err)
			}
			seatEnv := []string{"TEAM_MODE=strict"} // the agent router's plan is a run line alone
			switch {
			case plan.SeatRun != nil:
				seatEnv = plan.SeatRun.Env
			case request.Row.Kind != compose.Agent:
				t.Fatalf("plan carries no seat recipe: %#v", plan)
			}
			var adopted []string
			for _, entry := range seatEnv {
				if strings.HasPrefix(entry, "TEAM_MODE=") || strings.Contains(entry, "/adopter/value") {
					adopted = append(adopted, entry)
				}
			}
			if !reflect.DeepEqual(adopted, []string{"TEAM_MODE=strict"}) {
				t.Errorf("seat env adopter entries = %q, want [TEAM_MODE=strict]", adopted)
			}
			envWord := " TEAM_MODE='strict' " + owned + "="
			if request.Row.Kind == compose.NewCodex {
				envWord = "" // cx's own launcher applies the settings (hookentry.CodexLaunch)
			}
			if !strings.Contains(plan.Run, envWord) || strings.Contains(plan.Run, "/adopter/value") {
				t.Errorf("run lacks %q or carries the adopter's %s: %s", envWord, owned, plan.Run)
			}
			var words []string
			switch request.Row.Kind {
			case compose.NewClaude:
				words = []string{
					cmdparse.Quote("--system-prompt-file") + " " + cmdparse.Quote(prompt),
					"'--effort' 'xhigh'",
					"'--model' 'gpt-x'",
					"'--name' '_SCRIBE:3'",
				}
			case compose.ResumeClaude, compose.Agent:
				words = []string{
					"'--resume' '44444444-4444-4444-8444-444444444444'",
					cmdparse.Quote("--system-prompt-file") + " " + cmdparse.Quote(prompt),
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
					fleetEnv = " PFM_OPENCODE_FLEET_FILE=" + cmdparse.Quote(request.OpenCodeFleetPrompt)
				}
				words = []string{
					`OPENCODE_CONFIG_CONTENT='{"instructions":["` + prompt + `"],"plugin":["file://` + request.OpenCodePlugin + `","file://` + request.OpenCodeHerdrPlugin + `"]}' PFM_OPENCODE_MODEL='openai/gpt-x' PFM_OPENCODE_VARIANT='xhigh'` + fleetEnv + " PFM_OPENCODE_SYSTEM_FILE=" + cmdparse.Quote(
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
			name:     "unknown OpenCode effort",
			manifest: `{"prompt":"scribe.md","effort":"turbo","engines":["opencode"]}`,
			engines:  []pfmengine.ID{pfmengine.OpenCode}, mode: workbench.New,
			refused: map[pfmengine.ID]bool{pfmengine.OpenCode: true},
		},
		{
			name: "OpenCode effort lowered", manifest: `{"prompt":"scribe.md","effort":"High","engines":["opencode"]}`,
			engines: []pfmengine.ID{pfmengine.OpenCode}, mode: workbench.New, wantEffort: "high",
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
					persona, err := WorkbenchPersona(dir, "", engine, test.mode)
					if test.passThrough {
						_, wantErr := workbench.ForLaunch(dir, "", engine, test.mode)
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
		{"broken persona opens without it", `{"prompt":""}`, compose.ResumeClaude, true},
		{"disabled new chat refused before any side effect", `{"prompt":"scribe.md","engines":["codex"]}`, compose.NewClaude, false},
		{"allowed", `{"prompt":"scribe.md","effort":"xhigh","engines":["claude","codex","opencode"]}`, compose.ResumeClaude, true},
		{"disabled resume", `{"prompt":"scribe.md"}`, compose.ResumeCodex, false},
		{"booting row ignores the manifest", `{"prompt":""}`, compose.Booting, false},
		{"settings envs beside a broken persona", `{"prompt":""}`, compose.ResumeClaude, true},
		{"faulty settings beside a persona", `{"prompt":"scribe.md","effort":"xhigh"}`, compose.ResumeClaude, true},
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
				var wantError, wantRun, wantWarning string
				var wantWarnings []string
				settings := paths.Settings(filepath.Dir(filepath.Dir(dir)))
				switch test.name {
				case "settings envs beside a broken persona":
					if err := atomicfile.Write(settings, []byte(`{"envs":{"TEAM_MODE":"strict"}}`), 0o600); err != nil {
						t.Fatal(err)
					}
					wantWarning = "workbench persona not applied (" + paths.WorkbenchManifest(dir) +
						`: "prompt" is required); launching without it`
				case "faulty settings beside a persona":
					if err := atomicfile.Write(settings, []byte(`{"envs":{"team_mode":"strict"}}`), 0o600); err != nil {
						t.Fatal(err)
					}
					wantWarning = settings + `: envs name "team_mode" must be upper snake: A-Z, 0-9 and _, ` +
						"starting with a letter; launching without the settings"
				case "broken persona opens without it":
					wantWarning = "workbench persona not applied (" + paths.WorkbenchManifest(dir) +
						`: "prompt" is required); launching without it`
				case "disabled new chat refused before any side effect":
					wantError = "workbench " + dir + ` does not enable claude: add "claude" to "engines" in ` +
						paths.WorkbenchManifest(dir)
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
				var stderr bytes.Buffer
				executor, err := New(Dependencies{
					Tmux:      tmux,
					Processes: processes,
					Gate:      fixedGate(false),
					Runner:    &captureRunner{},
					Stderr:    &stderr,
				})
				if err != nil {
					t.Fatal(err)
				}
				var line string
				var told []string
				if detached {
					var result OpenResult
					result, err = executor.Open(context.Background(), detachedRequest(request))
					told = result.Warnings
				} else {
					line, err = openLine(executor, context.Background(), request)
					if stderr.Len() > 0 {
						told = strings.Split(strings.TrimSuffix(stderr.String(), "\n"), "\n")
					}
				}
				if wantError != "" {
					if err == nil || err.Error() != wantError || len(tmux.created) != 0 ||
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
				want := wantWarnings
				if wantWarning != "" {
					want = []string{wantWarning}
					if !detached {
						want = []string{"pfm: " + wantWarning}
					}
				}
				if !reflect.DeepEqual(told, want) {
					t.Fatalf("open told %q, want %q", told, want)
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
				if test.name == "settings envs beside a broken persona" &&
					!strings.Contains(run, " TEAM_MODE='strict' CLAUDE_CONFIG_DIR=") {
					t.Fatalf("settings envs missing beside the broken persona: %s", run)
				}
				if test.name == "broken persona opens without it" ||
					test.name == "settings envs beside a broken persona" {
					prompt := filepath.Join(dir, ".professor", "scribe.md")
					if !stringsContainsAll(run, "'--resume' '"+request.Row.ID+"'") || strings.Contains(run, prompt) {
						t.Fatalf("broken persona run=%s; want the resume without %s", run, prompt)
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
				var stderr bytes.Buffer
				executor, err := New(Dependencies{Tmux: tmux, Processes: &fakeProcesses{}, Stderr: &stderr})
				if err != nil {
					t.Fatal(err)
				}
				var told []string
				if detached {
					var result OpenResult
					result, err = executor.Open(context.Background(), detachedRequest(request))
					told = result.Warnings
				} else {
					_, err = executor.Open(context.Background(), request)
					if stderr.Len() > 0 {
						told = strings.Split(strings.TrimSuffix(stderr.String(), "\n"), "\n")
					}
				}
				switch {
				case test.fail && test.kind == compose.NewOpenCode:
					if err == nil || len(tmux.created) != 0 {
						t.Fatalf("preparation refusal=%v, created=%v", err, tmux.created)
					}
				case test.fail:
					// A mirror that will not build opens the chat on the mirror as it stands, said.
					persona, personaErr := workbench.ForLaunch(dir, "", pfmengine.Codex, workbench.Resume)
					if personaErr != nil {
						t.Fatal(personaErr)
					}
					mirrorErr := workbench.EnsureMirror(persona.Bench, pfmengine.Codex, request.Home)
					if mirrorErr == nil {
						t.Fatal("mirror fixture did not fail")
					}
					want := mirrorErr.Error() + "; launching on the mirror as it stands"
					if !detached {
						want = "pfm: " + want
					}
					if err != nil || len(tmux.created) != 1 || !reflect.DeepEqual(told, []string{want}) {
						t.Fatalf("mirror failure open=%v, created=%d, told %q; want one server and %q",
							err, len(tmux.created), told, want)
					}
					if !strings.Contains(tmux.created[0].Run, "developer_instructions") {
						t.Fatalf("mirror failure dropped the persona: %s", tmux.created[0].Run)
					}
				case err != nil || len(tmux.created) != 1:
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
				want += " PFM_OPENCODE_FLEET_FILE=" + cmdparse.Quote(
					paths.GeneratedHarnessPromptIn(clone, pfmengine.OpenCode),
				)
			}
			want += " PFM_OPENCODE_SYSTEM_FILE=" + cmdparse.Quote(prompt) + " opencode"
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
				if !strings.Contains(run, want) || strings.Contains(run, "PFM_OPENCODE_SYSTEM_FILE=") {
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
