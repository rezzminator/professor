package action

import (
	"reflect"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/workbench"
)

func TestCodexArgsBuildsTheWordsInOrder(t *testing.T) {
	got, err := CodexArgs("gpt-test", t.TempDir(), "high", CodexInstructions("be brief"), CodexResume, "abc-123")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"--model", "gpt-test",
		"-c", `model_reasoning_effort="high"`,
		"-c", "developer_instructions=\"\"\"\nbe brief\"\"\"",
		"resume", "abc-123",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv = %q, want %q", got, want)
	}
}

func TestCodexArgsEmptyInputsGiveNoWordsAndResumeAloneIsLegal(t *testing.T) {
	got, err := CodexArgs("", "", "", nil, "", "")
	if err != nil || len(got) != 0 {
		t.Fatalf("empty = %q, %v", got, err)
	}
	got, err = CodexArgs("", "", "", nil, CodexResume, "abc")
	if err != nil || !reflect.DeepEqual(got, []string{"resume", "abc"}) {
		t.Fatalf("resume only = %q, %v", got, err)
	}
}

func TestCodexArgsForkVerbBadVerbAndInstructionsPointer(t *testing.T) {
	got, err := CodexArgs("", "", "", nil, CodexFork, "thread-1")
	if err != nil || !reflect.DeepEqual(got, []string{"fork", "thread-1"}) {
		t.Fatalf("fork = %q, %v", got, err)
	}
	for _, verb := range []string{"", "exec", "Resume"} {
		_, err := CodexArgs("", "", "", nil, verb, "thread-1")
		if err == nil || !strings.Contains(err.Error(), "neither") {
			t.Fatalf("verb %q err = %v", verb, err)
		}
	}
	_, err = CodexArgs("", "", "", nil, CodexFork, "a\nb")
	if err == nil || !strings.Contains(err.Error(), "safe session id") {
		t.Fatalf("unsafe fork id err = %v", err)
	}
	empty := ""
	got, err = CodexArgs("", "", "", &empty, "", "")
	if err != nil || !reflect.DeepEqual(got, []string{"-c", "developer_instructions=\"\"\"\n\"\"\""}) {
		t.Fatalf("empty non-nil instructions = %q, %v", got, err)
	}
	if CodexInstructions("") != nil || *CodexInstructions("x") != "x" {
		t.Fatal("CodexInstructions: empty must be nil, text must round-trip")
	}
}

func TestCodexArgsRefusesAnUnsafeResumeID(t *testing.T) {
	for _, id := range []string{"a\nb", "a\rb", "a\x00b"} {
		_, err := CodexArgs("", "", "", nil, CodexResume, id)
		if err == nil || !strings.Contains(err.Error(), "safe session id") {
			t.Fatalf("id %q err = %v", id, err)
		}
	}
}

// The new-Codex and fork-Codex doors pin one persona rule: an applied persona
// (a prompt file) always emits the developer-instructions override, even over
// an empty body; a zero persona emits none; the door's own words come last.
func TestCodexNewAndForkPlansPinThePersonaOverride(t *testing.T) {
	override := func(body string) []string {
		return []string{"-c", "developer_instructions=\"\"\"\n" + body + "\"\"\""}
	}
	for _, test := range []struct {
		name    string
		persona workbench.Persona
		want    []string
	}{
		{"zero persona", workbench.Persona{}, nil},
		{"applied persona with an empty body", workbench.Persona{Prompt: "p.md"}, override("")},
		{"applied persona with a body", workbench.Persona{Prompt: "p.md", Body: "be brief"}, override("be brief")},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			machine := testMachineConfig(home)
			machine.CodexAccounts = []pfmconfig.CodexAccount{{ID: 1, Home: home + "/.codex"}}
			machine.Codex.Yolo = false
			autonomy := len(codexAutonomyArgs(false))
			plan, err := Synthesize(Request{
				Row:  compose.Row{Kind: compose.NewCodex, CWD: "/work/native"},
				Home: home, PrimaryAccount: 1, Config: machine, Prompt: "go", Persona: test.persona,
			})
			if err != nil {
				t.Fatal(err)
			}
			wantNew := append(append([]string{}, test.want...), "go")
			if got := plan.SeatRun.Args[autonomy:]; !reflect.DeepEqual(got, wantNew) {
				t.Fatalf("NewCodex words = %q, want %q", got, wantNew)
			}
			fork, err := HeadlessFork(HeadlessForkRequest{
				Engine: pfmengine.Codex, SessionID: "thread-123", Name: "branch", CWD: "/work",
				Home: home, PrimaryAccount: 1, Config: machine, Persona: test.persona,
			})
			if err != nil {
				t.Fatal(err)
			}
			wantFork := append(append([]string{}, test.want...), "fork", "thread-123")
			if got := fork.SeatRun.Args[autonomy:]; !reflect.DeepEqual(got, wantFork) {
				t.Fatalf("HeadlessFork words = %q, want %q", got, wantFork)
			}
		})
	}
}
