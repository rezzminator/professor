//go:build jail

package action

import (
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/compose"
)

func TestActionStress(t *testing.T) {
	previous := newSessionID
	newSessionID = func() (string, error) { return "00000000-0000-4000-8000-000000000004", nil }
	t.Cleanup(func() { newSessionID = previous })
	strict := os.Getenv("PFM_STRESS_STRICT") == "1"
	requests := stressRequests(t.TempDir())
	expected := make([]Plan, len(requests))
	for index, request := range requests {
		plan, err := Synthesize(request)
		if err != nil {
			t.Fatal(err)
		}
		expected[index] = plan
	}
	started := time.Now()
	const syntheses = 10_000
	for index := 0; index < syntheses; index++ {
		requestIndex := index % len(requests)
		actual, err := Synthesize(requests[requestIndex])
		if err != nil {
			t.Fatal(err)
		}
		if actual.Run != expected[requestIndex].Run ||
			actual.Line != expected[requestIndex].Line {
			t.Fatalf("nondeterministic synthesis at %d", index)
		}
	}
	elapsed := time.Since(started)
	limit := 10 * time.Second
	if strict {
		limit = time.Second
	}
	if elapsed >= limit {
		t.Fatalf(
			"10k deterministic syntheses took %s, want <%s (strict=%t)",
			elapsed,
			limit,
			strict,
		)
	}
	t.Logf(
		"STRESS syntheses=%d combinations=%d deterministic=true elapsed=%s strict=%t",
		syntheses,
		len(requests),
		elapsed,
		strict,
	)

	stressHostileProjectDirectories(t)
}

func stressHostileProjectDirectories(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	root := t.TempDir()
	marker := filepath.Join(root, "EVAL_BREAKOUT")
	roundTrip := filepath.Join(root, "roundtrip")
	random := rand.New(rand.NewSource(7))
	const cases = 1_000
	for index := 0; index < cases; index++ {
		hostile := fmt.Sprintf(
			"p-%04d-%08x space ' \" $HOME $(printf hacked > \"$ACTION_MARKER\") `printf hacked > \"$ACTION_MARKER\"`; newline\nnext-\x01",
			index,
			random.Uint32(),
		)
		if index%7 == 0 {
			hostile += "\n"
		}
		projectDir := filepath.Join(root, hostile)
		if err := os.Mkdir(projectDir, 0o700); err != nil {
			t.Fatalf("mkdir hostile case %d: %v", index, err)
		}
		plan, err := synthesizeWithTestConfig(Request{
			Row: compose.Row{
				Kind: compose.NewClaude,
				CWD:  projectDir,
			},
			PrimaryAccount: 1,
			Home:           home,
			FreshSocket:    "cc-stress-1",
		})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(plan.Line, "TMUX= tmux -L ") {
			t.Fatalf("native fresh plan %q did not call tmux directly", plan.Line)
		}
		// The directory travels to the creator as argv, never through the
		// eval line: the line is only the attach, and must still eval clean.
		if plan.ChatServer == nil || plan.ChatServer.CWD != projectDir {
			t.Fatalf("hostile case %d server = %#v, want the directory verbatim", index, plan.ChatServer)
		}
		if strings.Contains(plan.Line, hostile) {
			t.Fatalf("hostile case %d leaked the directory into the eval line: %q", index, plan.Line)
		}
		script := `tmux() {
  while [ "$#" -gt 0 ]; do
    if [ "$1" = -t ]; then printf %s "$2" > "$ACTION_ROUNDTRIP"; return; fi
    shift
  done
  return 9
}
` + plan.Line
		if output, err := exec.Command("sh", "-n", "-c", script).CombinedOutput(); err != nil {
			t.Fatalf("sh -n hostile case %d: %v: %s", index, err, output)
		}
		command := exec.Command("sh", "-c", script)
		command.Env = append(
			os.Environ(),
			"ACTION_MARKER="+marker,
			"ACTION_ROUNDTRIP="+roundTrip,
		)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("eval hostile case %d: %v: %s", index, err, output)
		}
		content, err := os.ReadFile(roundTrip)
		if err != nil {
			t.Fatal(err)
		}
		if string(content) != "cc-stress-1" {
			t.Fatalf(
				"hostile path %d attach target round trip=%q, want=%q",
				index,
				content,
				"cc-stress-1",
			)
		}
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatalf("eval breakout in hostile case %d: %v", index, err)
		}
	}
	t.Logf(
		"STRESS hostile_projdirs=%d sh_parse_failures=0 eval_breakouts=0 roundtrip_failures=0",
		cases,
	)
}
