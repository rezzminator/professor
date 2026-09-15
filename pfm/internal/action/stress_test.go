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

	"hostops/pfm/internal/compose"
	pfmconfig "hostops/pfm/internal/config"
)

func TestActionStress(t *testing.T) {
	strict := os.Getenv("PFM_STRESS_STRICT") == "1"
	requests := stressRequests()
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

func stressRequests() []Request {
	machine := testMachineConfig("/home/test")
	machine.OpencodeAccounts = []pfmconfig.OpenCodeAccount{{
		ID: 1, Home: "/home/test/.local/share/opencode",
	}}
	machine.OpenCode.Binary = "opencode"
	rows := []compose.Row{
		{Kind: compose.NewClaude, CWD: "/work/project"},
		{Kind: compose.NewCodex, CWD: "/work/project"},
		{Kind: compose.NewOpencode, CWD: "/work/project"},
		{
			Kind:        compose.LiveClaude,
			ID:          "11111111-1111-4111-8111-111111111111",
			Socket:      "cc-1700000000-123-456",
			SessionName: "live-session",
			CWD:         "/work/project",
		},
		{
			Kind:      compose.Agent,
			ID:        "22222222-2222-4222-8222-222222222222",
			CWD:       "/work/project",
			ConfigDir: "/home/test/.cc/2",
		},
		{
			Kind: compose.ResumeClaude,
			ID:   "33333333-3333-4333-8333-333333333333",
			CWD:  "/work/project",
		},
		{
			Kind: compose.ResumeOpencode,
			ID:   "ses_stress_opencode",
			CWD:  "/work/project",
		},
		{
			Kind: compose.ResumeCodex,
			ID:   "44444444-4444-4444-8444-444444444444",
			CWD:  "/work/project",
		},
	}
	requests := make([]Request, 0, len(rows)*2*3*2)
	for index := range rows {
		row := rows[index]
		for _, bunker := range []bool{false, true} {
			accounts := []int{1, 2, 3}
			if row.Kind == compose.NewOpencode || row.Kind == compose.ResumeOpencode {
				accounts = []int{1}
			}
			for _, account := range accounts {
				for _, cache1H := range []bool{false, true} {
					freshPrefix := "cc"
					switch row.Kind {
					case compose.NewCodex, compose.ResumeCodex:
						freshPrefix = "cx"
					case compose.NewOpencode, compose.ResumeOpencode:
						freshPrefix = "ox"
					}
					requests = append(requests, Request{
						Row:            row,
						PrimaryAccount: account,
						Cache1H:        cache1H,
						Bunker:         bunker,
						Home:           "/home/test",
						Config:         machine,
						FreshSocket: fmt.Sprintf(
							"%s-1700000001-123-456",
							freshPrefix,
						),
					})
				}
			}
		}
	}
	return requests
}

func stressHostileProjectDirectories(t *testing.T) {
	t.Helper()
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
			Home:           "/home/test",
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
