package tokens

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/transcript"
)

func TestCodexPeaksSumEachSegmentsPeak(t *testing.T) {
	var p codexPeaks
	for _, total := range []int64{10, 20, 5, 8} {
		p.add(transcript.CodexTokenUsage{InputTokens: total, OutputTokens: 1, TotalTokens: total})
	}
	sum, segments := p.done()
	if segments != 2 || sum.total != 28 || sum.in != 28 || sum.out != 2 {
		t.Errorf("sum %+v over %d segments, want the peaks 20 and 8 of two segments", sum, segments)
	}
	var none codexPeaks
	if sum, segments := none.done(); segments != 0 || sum != (codexUsage{}) {
		t.Errorf("no usage = %+v over %d segments", sum, segments)
	}
}

func TestModelSetKeepsFirstSeenOrderAndPricesTheLast(t *testing.T) {
	var set modelSet
	if set.last() != "unknown" {
		t.Errorf("an empty set's last = %q, want unknown", set.last())
	}
	for _, model := range []string{"gpt-a", "gpt-b", "gpt-a"} {
		set.add(model)
	}
	if len(set) != 2 || set[0] != "gpt-a" || set.last() != "gpt-b" {
		t.Errorf("set = %v", set)
	}
}

// A Codex home whose only rollout subtree cannot be read holds no rollout the
// walk could reach: the refusal names the failed look, never only "no
// rollouts".
func TestCodexNamesTheReadErrorBehindAnEmptyHome(t *testing.T) {
	home := t.TempDir()
	loop := filepath.Join(home, "archived_sessions")
	if err := os.Symlink(loop, loop); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := auditWith(t, Options{}, "--codex", "--codex-root", home,
		"--prices", fixture(t, "model-cost.json"))
	if code != 1 || stdout != "" {
		t.Fatalf("exit %d stdout %q, want 1 and nothing", code, stdout)
	}
	if !strings.Contains(stderr, "no Codex rollouts under "+home) ||
		!strings.Contains(stderr, "READ ERRORS: resolve Codex rollout root "+strconv.Quote(loop)) {
		t.Fatalf("stderr = %q, want the empty home and why %s could not be read", stderr, loop)
	}
}

// No Codex home configured is a refusal naming it, never a walk of the
// working directory's sessions/.
func TestCodexRefusesAnEmptyHome(t *testing.T) {
	code, _, stderr := auditWith(t, Options{}, "--codex", "--prices", fixture(t, "model-cost.json"))
	if code != 1 || !strings.Contains(stderr, "READ ERRORS: no Codex home is configured; pass --codex-root DIR") {
		t.Fatalf("exit %d stderr %q, want 1 naming the missing home", code, stderr)
	}
}

// TestCodexFailedCallIsTheSharedPredicate: a Codex tool call fails by
// transcript.CodexToolOutput (INTENDED: a nonzero exit code and an apply_patch
// verification failure count; "Script failed" counts only at the head).
func TestCodexFailedCallIsTheSharedPredicate(t *testing.T) {
	output := func(text string) string {
		quoted, err := json.Marshal(text)
		if err != nil {
			t.Fatal(err)
		}
		return `{"type":"response_item","payload":{"type":"function_call_output","call_id":"c1","output":` +
			string(quoted) + "}}\n"
	}
	tests := []struct {
		name   string
		output string
		failed float64
	}{
		{"exit code", "Process exited with code 2\nOutput:\nboom", 1},
		{"exit zero", "Process exited with code 0\nOutput:\nfine", 0},
		{"wrapped metadata", `{"output":"x","metadata":{"exit_code":1}}`, 1},
		{"apply_patch", "apply_patch verification failed: no match", 1},
		{"script failed at the head", "Script failed: oops", 1},
		{"script failed deep in the text", "ok\n" + strings.Repeat("x", 500) + "\nScript failed", 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "rollout.jsonl")
			call := `{"type":"response_item","payload":{"type":"function_call","call_id":"c1","name":"exec","arguments":"{}"}}` + "\n"
			if err := os.WriteFile(path, []byte(call+output(test.output)), 0o600); err != nil {
				t.Fatal(err)
			}
			a := &auditor{scan: newScanState(), loc: testZone, prices: bookOf(fixtureDocument(t))}
			if r := a.codexAuditRun(path, nil); r.failedCmds != test.failed {
				t.Fatalf("failedCmds = %v, want %v", r.failedCmds, test.failed)
			}
		})
	}
}

// TestCodexReadsEveryCodexHome: `pfm tokens --codex` reads every Codex home
// pfm resolves (config.Config.CodexHomes, runtime.Paths.Roots[codex]), never
// only the first (INTENDED: a second account's rollouts went unread).
func TestCodexReadsEveryCodexHome(t *testing.T) {
	source, err := os.ReadFile(fixture(t, "codex/sessions/2026/09/20/rollout-2026-09-20T09-05-00-cx-thread-1.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var homes []string
	for _, thread := range []string{"cx-thread-1", "cx-thread-9"} {
		home := t.TempDir()
		dir := filepath.Join(home, "sessions", "2026", "09", "20")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		content := strings.ReplaceAll(string(source), "cx-thread-1", thread)
		if err := os.WriteFile(
			filepath.Join(dir, "rollout-2026-09-20T09-05-00-"+thread+".jsonl"),
			[]byte(content),
			0o600,
		); err != nil {
			t.Fatal(err)
		}
		homes = append(homes, home)
	}
	runtime := config.Runtime{Paths: paths.Values{Roots: map[pfmengine.ID][]string{pfmengine.Codex: homes}}}
	var stdout, stderr bytes.Buffer
	code := Main(
		[]string{"--codex", "--since", "99999d", "--prices", fixture(t, "model-cost.json")},
		&stdout,
		&stderr,
		runtime,
	)
	if code != 0 || !strings.Contains(stdout.String(), homes[1]) ||
		!strings.Contains(stdout.String(), " · 2 threads of 2 rollouts") ||
		!strings.Contains(stdout.String(), "cx-thread-9") {
		t.Fatalf(
			"exit %d\nstdout:\n%s\nstderr:\n%s\nwant both homes' rollouts read",
			code,
			stdout.String(),
			stderr.String(),
		)
	}
}
