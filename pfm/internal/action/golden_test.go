package action

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/workbench"
)

func TestGoldenCommandLines(t *testing.T) {
	previous := newSessionID
	newSessionID = func() (string, error) { return "00000000-0000-4000-8000-000000000004", nil }
	t.Cleanup(func() { newSessionID = previous })
	home := t.TempDir()
	var actual bytes.Buffer
	lastRoute := Route(0)
	personaRows, prompt := personaGoldenRequests(t, home)
	for _, request := range append(stressRequests(home), personaRows...) {
		plan, err := Synthesize(request)
		if err != nil {
			t.Fatal(err)
		}
		if plan.Route != lastRoute {
			fmt.Fprintln(&actual, goldenSourceComment(plan.Route))
			lastRoute = plan.Route
		}
		line := plan.Line
		if plan.Run != "" {
			line = replaceGoldenRun(line, plan.Run)
		}
		line = strings.ReplaceAll(line, prompt, "/work/acme/docs/scribe/.professor/scribe.md")
		plan.Run = strings.ReplaceAll(plan.Run, prompt, "/work/acme/docs/scribe/.professor/scribe.md")
		line = strings.ReplaceAll(line, home, "/home/test")
		plan.Run = strings.ReplaceAll(plan.Run, home, "/home/test")
		fmt.Fprintf(
			&actual,
			"%c\tb=%t\ta=%d\th=%t\trun=%s\tline=%s\n",
			plan.Route,
			request.Bunker,
			request.PrimaryAccount,
			request.Cache1H,
			plan.Run,
			line,
		)
	}
	path := filepath.Join("..", "..", "testdata", "golden", "cmdlines.txt")
	if os.Getenv("PFM_UPDATE_GOLDENS") == "1" {
		if err := os.WriteFile(path, actual.Bytes(), 0o644); err != nil {
			t.Fatalf("regenerate command golden: %v", err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read command golden: %v\nactual:\n%s", err, actual.String())
	}
	if !bytes.Equal(want, actual.Bytes()) {
		t.Fatalf(
			"command golden mismatch (%s)\nwant:\n%s\nactual:\n%s",
			firstGoldenDifference(want, actual.Bytes()),
			want,
			actual.String(),
		)
	}
}

func replaceGoldenRun(line, run string) string {
	quotedRun := Quote(run)
	index := bytes.Index([]byte(line), []byte(quotedRun))
	if index < 0 {
		return line
	}
	return line[:index] + "'<RUN>'" + line[index+len(quotedRun):]
}

func goldenSourceComment(route Route) string {
	switch route {
	case NewClaude:
		return "# N — fresh Claude launch"
	case NewCodex:
		return "# C — fresh Codex launch"
	case NewOpenCode:
		return "# P — fresh OpenCode launch"
	case Live:
		return "# L — live chat attach"
	case Agent:
		return "# A — agent chat attach or resume"
	case ResumeClaude:
		return "# R — Claude resume"
	case ResumeCodex:
		return "# X — Codex resume"
	case ResumeOpenCode:
		return "# O — OpenCode resume"
	default:
		panic("unknown golden route")
	}
}

func firstGoldenDifference(want, got []byte) string {
	limit := len(want)
	if len(got) < limit {
		limit = len(got)
	}
	for index := 0; index < limit; index++ {
		if want[index] != got[index] {
			return fmt.Sprintf(
				"byte %d: want %#x got %#x; lengths %d/%d",
				index,
				want[index],
				got[index],
				len(want),
				len(got),
			)
		}
	}
	return fmt.Sprintf("lengths %d/%d", len(want), len(got))
}

func personaGoldenRequests(t *testing.T, home string) ([]Request, string) {
	t.Helper()
	prompt := filepath.Join(t.TempDir(), "scribe.md")
	if err := os.WriteFile(prompt, []byte("You are scribe."), 0o600); err != nil {
		t.Fatal(err)
	}
	var requests []Request
	for _, kind := range []compose.Kind{compose.NewClaude, compose.ResumeClaude, compose.Agent, compose.ResumeCodex, compose.NewOpenCode, compose.ResumeOpenCode, compose.NewCodex} {
		request := stressRequests(home)[0]
		request.Row = compose.Row{Kind: kind, CWD: "/work/acme/docs/scribe", ID: "44444444-4444-4444-8444-444444444444"}
		request.Persona = workbench.Persona{Prompt: prompt, Body: "You are scribe.", Effort: "xhigh", Model: "gpt-x"}
		request.OpenCodePlugin = filepath.Join(home, ".local", "state", "pfm", "opencode-workbench-plugin.mjs")
		request.OpenCodeFleetPrompt = "/work/clone/pfm/harness-prompts/composed/opencode.md"
		if kind == compose.NewClaude {
			request.LaunchName = "_SCRIBE:3"
		}
		requests = append(requests, request)
	}
	return requests, prompt
}
