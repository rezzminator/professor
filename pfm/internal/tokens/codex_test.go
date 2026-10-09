package tokens

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

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

func TestCodexOutputText(t *testing.T) {
	cases := map[string]string{
		`"plain"`: "plain",
		`[{"type":"input_image"},{"type":"input_text","text":"first"},{"text":"second"}]`: "first",
		`[{"text":""}]`: "",
		`{"text":"x"}`:  "",
		`[`:             "",
		``:              "",
	}
	for output, want := range cases {
		if got := codexOutputText(json.RawMessage(output)); got != want {
			t.Errorf("codexOutputText(%s) = %q, want %q", output, got, want)
		}
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
