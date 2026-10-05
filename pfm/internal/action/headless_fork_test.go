package action

import (
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/workbench"
)

func TestHeadlessForkCodexUsesRealForkAndSelectedAccount(t *testing.T) {
	machine := pfmconfig.Defaults("/tmp/fork-home", nil, "/tmp/fork-home/.codex")
	machine.CodexAccounts = []pfmconfig.CodexAccount{{ID: 1, Home: "/tmp/fork-home/.codex"}}
	plan, err := HeadlessFork(HeadlessForkRequest{
		Engine: pfmengine.Codex, SessionID: "thread-123", Name: "review fork",
		CWD: "/work/project", Home: "/tmp/fork-home", PrimaryAccount: 1,
		Config: machine,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{"-u CODEX_THREAD_ID", "CODEX_HOME=", " 'fork' 'thread-123'"} {
		if !strings.Contains(plan.Run, fragment) {
			t.Fatalf("fork command %q lacks %q", plan.Run, fragment)
		}
	}
	if strings.Contains(plan.Run, "resume") {
		t.Fatalf("Codex fork command used resume: %q", plan.Run)
	}
}

func TestHeadlessForkWorkbench(t *testing.T) {
	for _, name := range []string{"Claude", "Claude no parent model", "Codex", "outside"} {
		t.Run(name, func(t *testing.T) {
			_, dir := actionWorkbenchFixture(t, `{"prompt":"scribe.md","effort":"xhigh","model":"sonnet"}`)
			home := t.TempDir()
			machine := testMachineConfig(home)
			machine.CodexAccounts = []pfmconfig.CodexAccount{{ID: 1, Home: home + "/.codex"}}
			request := HeadlessForkRequest{
				Engine:         pfmengine.Claude,
				SessionID:      "thread-123",
				Name:           "branch",
				CWD:            dir,
				Home:           home,
				PrimaryAccount: 1,
				Config:         machine,
				Model:          "opus",
			}
			request.Persona = workbench.Persona{
				Prompt: dir + "/.professor/scribe.md",
				Body:   "You are scribe.",
				Effort: "xhigh",
				Model:  "sonnet",
			}
			want := "'--resume' 'thread-123'"
			words := []string{
				"'--fork-session'",
				"'--system-prompt-file' " + Quote(request.Persona.Prompt),
				"'--effort' 'xhigh'",
				"'--model' 'opus'",
			}
			switch name {
			case "Claude no parent model":
				request.Model = ""
				words[3] = "'--model' 'sonnet'"
			case "Codex":
				request.Engine, request.Model = pfmengine.Codex, ""
				request.Persona = workbench.Persona{
					Prompt: "/work/acme/docs/duo/.professor/duo.md",
					Body:   "You are duo.",
					Effort: "high",
					Model:  "gpt-x",
				}
				want = `'--model' 'gpt-x' '-c' 'model_reasoning_effort="high"' '-c' 'developer_instructions="""` + "\n" + `You are duo."""' 'fork' 'thread-123'`
				words = nil
			case "outside":
				request.CWD = "/work/acme/src"
				request.Persona = workbench.Persona{}
				words = []string{"'--fork-session'", "'--model' 'opus'"}
			}
			plan, err := HeadlessFork(request)
			if err != nil {
				t.Fatal(err)
			}
			for _, word := range append(words, want) {
				if !strings.Contains(plan.Run, word) {
					t.Errorf("fork lacks %q: %s", word, plan.Run)
				}
			}
			if name == "outside" {
				control, err := (ClaudeSpawn{Purpose: PurposeResume, Home: home, Account: 1, Cache1H: &request.Cache1H, Resume: request.SessionID, Fork: true, Name: request.Name, Model: request.Model, Machine: machine}).ShellCommand()
				if err != nil || plan.Run != control {
					t.Fatalf("outside command = %q, want %q; err=%v", plan.Run, control, err)
				}
			}
		})
	}
}
