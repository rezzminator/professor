package action

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/professor"
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
					`OPENCODE_CONFIG_CONTENT='{"instructions":["` + prompt + `"],"plugin":["file://` + request.OpenCodePlugin + `"]}'` + fleetEnv + " PFM_OPENCODE_SYSTEM_FILE=" + Quote(
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

func TestExecutorWorkbenchPersona(t *testing.T) {
	for _, detached := range []bool{false, true} {
		for _, kind := range []compose.Kind{compose.ResumeClaude, compose.Agent, compose.ResumeCodex, compose.NewOpenCode, compose.ResumeOpenCode} {
			t.Run(kind.String()+map[bool]string{false: "/open", true: "/detached"}[detached], func(t *testing.T) {
				request, dir := actionWorkbenchFixture(
					t,
					`{"prompt":"scribe.md","effort":"XHigh","engines":["claude","codex","opencode"]}`,
				)
				request.Row.Kind = kind
				tmux := &fakeActionTmux{alive: map[string]bool{}}
				tmux.onCreate = func() {
					var outputs []string
					if kind == compose.ResumeCodex {
						outputs = []string{filepath.Join(dir, "AGENTS.md")}
					}
					if kind == compose.NewOpenCode || kind == compose.ResumeOpenCode {
						outputs = []string{
							filepath.Join(dir, ".opencode", "opencode.jsonc"),
							paths.OpenCodeWorkbenchPlugin(request.Home),
						}
					}
					for _, path := range outputs {
						if _, err := os.Stat(path); err != nil {
							t.Errorf("not prepared before spawn: %s: %v", path, err)
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
				if err != nil {
					t.Fatal(err)
				}
				if len(tmux.created) != 1 {
					t.Fatalf("created=%d", len(tmux.created))
				}
				want := filepath.Join(dir, ".professor", "scribe.md")
				if kind == compose.ResumeCodex {
					want = "You are scribe."
				}
				if !strings.Contains(tmux.created[0].Run, want) {
					t.Fatalf("persona missing: %s", tmux.created[0].Run)
				}
				effort := map[compose.Kind]string{
					compose.ResumeClaude: "'--effort' 'xhigh'", compose.Agent: "'--effort' 'xhigh'",
					compose.ResumeCodex: `model_reasoning_effort="xhigh"`,
				}[kind]
				if !strings.Contains(tmux.created[0].Run, effort) {
					t.Fatalf("run lacks the roster's effort %s: %s", effort, tmux.created[0].Run)
				}
			})
		}
	}
}

func TestExecutorWorkbenchUnknownEffort(t *testing.T) {
	for _, kind := range []compose.Kind{compose.ResumeClaude, compose.ResumeCodex} {
		t.Run(kind.String(), func(t *testing.T) {
			request, dir := actionWorkbenchFixture(
				t,
				`{"prompt":"scribe.md","effort":"turbo","engines":["claude","codex"]}`,
			)
			request.Row.Kind = kind
			tmux := &fakeActionTmux{alive: map[string]bool{}}
			executor, err := New(Dependencies{Tmux: tmux, Processes: &fakeProcesses{}, Stderr: io.Discard})
			if err != nil {
				t.Fatal(err)
			}
			_, err = executor.Open(context.Background(), request)
			if err == nil || !strings.Contains(err.Error(), "workbench "+dir) ||
				!strings.Contains(err.Error(), `effort "turbo"`) || len(tmux.created) != 0 {
				t.Fatalf(
					"Open = %v, created=%d; want the bench's unknown-effort refusal, nothing spawned",
					err,
					len(tmux.created),
				)
			}
		})
	}
}

func TestExecutorWorkbenchRefusals(t *testing.T) {
	for _, detached := range []bool{false, true} {
		for _, failure := range []string{"manifest", "disabled-new", "mirror", "plugin"} {
			t.Run(failure+map[bool]string{false: "/open", true: "/detached"}[detached], func(t *testing.T) {
				request, dir := actionWorkbenchFixture(
					t,
					`{"prompt":"scribe.md","engines":["claude","codex","opencode"]}`,
				)
				want := ""
				switch failure {
				case "manifest":
					if err := atomicfile.Write(
						paths.WorkbenchManifest(dir),
						[]byte(`{"prompt":""}`),
						0o600,
					); err != nil {
						t.Fatal(err)
					}
					want = paths.WorkbenchManifest(dir) + `: "prompt" is required`
				case "disabled-new":
					if err := atomicfile.Write(
						paths.WorkbenchManifest(dir),
						[]byte(`{"prompt":"scribe.md"}`),
						0o600,
					); err != nil {
						t.Fatal(err)
					}
					request.Row.Kind = compose.NewCodex
					want = "workbench " + dir + ` does not enable codex: add "codex" to "engines" in ` + paths.WorkbenchManifest(
						dir,
					)
				case "mirror":
					request.Row.Kind = compose.ResumeCodex
					if err := atomicfile.Write(
						filepath.Join(dir, ".claude", "codex-build.json"),
						[]byte("{"),
						0o600,
					); err != nil {
						t.Fatal(err)
					}
					want = "build the codex mirror of workbench " + dir
				case "plugin":
					request.Row.Kind = compose.NewOpenCode
					blocker := filepath.Join(request.Home, ".local", "state", "pfm")
					if err := atomicfile.Write(blocker, nil, 0o600); err != nil {
						t.Fatal(err)
					}
					want = paths.OpenCodeWorkbenchPlugin(request.Home)
				}
				tmux := &fakeActionTmux{alive: map[string]bool{}}
				executor, err := New(Dependencies{Tmux: tmux, Processes: &fakeProcesses{}, Stderr: io.Discard})
				if err != nil {
					t.Fatal(err)
				}
				if detached {
					_, err = executor.OpenDetached(context.Background(), request)
				} else {
					_, err = executor.Open(context.Background(), request)
				}
				if err == nil || !strings.Contains(err.Error(), want) || len(tmux.created) != 0 {
					t.Fatalf("refusal=%v, created=%d; want %q", err, len(tmux.created), want)
				}
			})
		}
	}
}

func TestExecutorWorkbenchDisabledResume(t *testing.T) {
	request, _ := actionWorkbenchFixture(t, `{"prompt":"scribe.md"}`)
	request.Row.Kind = compose.ResumeCodex
	plain, err := Synthesize(request)
	if err != nil {
		t.Fatal(err)
	}
	tmux := &fakeActionTmux{alive: map[string]bool{}}
	executor, err := New(Dependencies{Tmux: tmux, Processes: &fakeProcesses{}, Stderr: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Open(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if len(tmux.created) != 1 || tmux.created[0].Run != plain.Run {
		t.Fatalf("disabled resume=%#v, want %s", tmux.created, plain.Run)
	}
}

// A refused workbench launch must leave the chat's competing seat alone: the
// refusal comes before Solo closes anything that holds the transcript.
func TestExecutorWorkbenchRefusalKeepsCompetingSeat(t *testing.T) {
	for _, detached := range []bool{false, true} {
		t.Run(map[bool]string{false: "open", true: "detached"}[detached], func(t *testing.T) {
			request, dir := actionWorkbenchFixture(t, `{"prompt":""}`)
			processes := &fakeProcesses{processes: []Process{
				{PID: 42, Argv: []string{"claude", "--resume", request.Row.ID}, TTY: "pts/1"},
			}}
			tmux := &fakeActionTmux{alive: map[string]bool{}}
			executor, err := New(Dependencies{
				Tmux: tmux, Processes: processes, Gate: fixedGate(false), Runner: &captureRunner{}, Stderr: io.Discard,
			})
			if err != nil {
				t.Fatal(err)
			}
			if detached {
				_, err = executor.OpenDetached(context.Background(), request)
			} else {
				_, err = executor.Open(context.Background(), request)
			}
			want := paths.WorkbenchManifest(dir) + `: "prompt" is required`
			if err == nil || !strings.Contains(err.Error(), want) || len(tmux.created) != 0 {
				t.Fatalf("refusal=%v, created=%d; want %q", err, len(tmux.created), want)
			}
			if len(processes.terminated) != 0 {
				t.Fatalf("refused open closed the competing seat: terminated=%v", processes.terminated)
			}
		})
	}
}

// Enter on a booting row attaches to a seat already launched: it relaunches
// nothing, so the workbench manifest has no say and cannot refuse it.
func TestExecutorWorkbenchBootingRowAttaches(t *testing.T) {
	request, dir := actionWorkbenchFixture(t, `{"prompt":""}`)
	request.Row = compose.Row{
		Kind: compose.Booting, ID: "cc-new-fixture-1", Socket: "cc-new-fixture-1",
		SessionName: "cc-new-fixture-1", CWD: dir, Workbench: dir,
	}
	tmux := &fakeActionTmux{alive: map[string]bool{}}
	executor, err := New(Dependencies{Tmux: tmux, Processes: &fakeProcesses{}, Stderr: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	line, err := executor.Open(context.Background(), request)
	if err != nil || !strings.Contains(line, "cc-new-fixture-1") || len(tmux.created) != 0 {
		t.Fatalf("booting attach = %q, %v, created=%d", line, err, len(tmux.created))
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
			) + `"]}'`
			if marker == "clone recorded" {
				want += " PFM_OPENCODE_FLEET_FILE=" + Quote(paths.ComposedHarnessPromptIn(clone, pfmengine.OpenCode))
			}
			want += " PFM_OPENCODE_SYSTEM_FILE=" + Quote(prompt) + " opencode"
			if !strings.Contains(run, want) {
				t.Fatalf("run lacks %q: %s", want, run)
			}
		})
	}
}
