package action

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestGoldenCommandLines(t *testing.T) {
	var actual bytes.Buffer
	lastRoute := Route(0)
	for _, request := range stressRequests() {
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
