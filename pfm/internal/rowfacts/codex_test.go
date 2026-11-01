package rowfacts

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/codexmeta"
	"github.com/rezzminator/professor/pfm/internal/compose"
)

func codexEvent(kind string) string {
	return fmt.Sprintf(`{"type":"event_msg","payload":{"type":%q}}`, kind)
}

func codexContext(model, effort string) string {
	return fmt.Sprintf(`{"type":"turn_context","payload":{"model":%q,"effort":%q}}`, model, effort)
}

func TestScanCodexReadsTheNewestTurnEventAndContext(t *testing.T) {
	filler := `{"type":"response_item","payload":{"type":"function_call_output"}}`
	for _, test := range []struct {
		name    string
		lines   []string
		working bool
		model   string
		effort  string
	}{
		{"an open turn", []string{codexEvent("task_started"), codexContext("gpt-6-astra", "low"), filler}, true, "gpt-6-astra", "low"},
		{"a completed turn", []string{codexEvent("task_started"), codexContext("gpt-6-astra", "high"), codexEvent("task_complete")}, false, "gpt-6-astra", "high"},
		{"an aborted turn", []string{codexEvent("task_started"), codexContext("m", "low"), codexEvent("turn_aborted")}, false, "m", "low"},
		{"a user message opens a turn", []string{codexEvent("task_complete"), codexEvent("user_message")}, true, "", ""},
		{"the newest context wins", []string{codexContext("old", "low"), codexEvent("task_complete"), codexContext("new", "max")}, false, "new", "max"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var tail cachedFile
			decided := false
			scanCodex([]byte(strings.Join(test.lines, "\n")+"\n"), &tail, &decided)
			if tail.working != test.working || tail.model != test.model || tail.effort != test.effort {
				t.Errorf("working=%v model=%q effort=%q, want %v %q %q",
					tail.working, tail.model, tail.effort, test.working, test.model, test.effort)
			}
		})
	}
}

func TestCodexFactsWidensPastALongRunOfToolOutputToFindTheContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	filler := `{"type":"response_item","payload":{"type":"function_call_output","output":"` +
		strings.Repeat("x", 1000) + `"}}`
	lines := []string{codexEvent("task_started"), codexContext("gpt-6-astra", "medium")}
	for range 600 { // ~600 KB of output between the context and the tail
		lines = append(lines, filler)
	}
	writeFile(t, path, strings.Join(lines, "\n")+"\n")
	row := compose.Row{Kind: compose.LiveCodex, Path: path}
	facts, err := NewReader(t.TempDir()).codexFacts(&row, time.Now().UnixNano())
	if err != nil || facts.Model != "gpt-6-astra" || facts.Effort != "medium" || !facts.Working {
		t.Fatalf("facts = %+v, err %v", facts, err)
	}
	if facts.AgentsWorking != 0 {
		t.Error("a Codex row reads no agent count: the fleet folds its sub-agent threads into the parent")
	}
}

// A compressed rollout is a finished chat whose tail is never scanned: zstd
// stores an incompressible block raw, so its bytes can hold a line verbatim
// that a plain scan would read as the chat's facts.
func TestCodexFactsLeaveACompressedRolloutUnread(t *testing.T) {
	noise := make([]byte, 16384)
	random := rand.New(rand.NewPCG(1, 2))
	for at := range noise {
		noise[at] = byte(random.IntN(255))
		if noise[at] >= '\n' {
			noise[at]++
		}
	}
	context := codexContext("gpt-6-astra", "low")
	path := filepath.Join(t.TempDir(), "rollout-2026-01-01T00-00-00-t1.jsonl")
	writeFile(t, path, string(noise)+"\n"+context+"\n"+codexEvent("task_complete")+"\n")
	reader := NewReader(t.TempDir())
	row := compose.Row{Kind: compose.ResumeCodex, Path: path}
	if facts, err := reader.codexFacts(&row, time.Now().UnixNano()); err != nil || facts.Model != "gpt-6-astra" {
		t.Fatalf("plain facts = %+v, %v; want model gpt-6-astra", facts, err)
	}
	compressed, err := codexmeta.CompressRollout(path)
	if err != nil {
		t.Fatal(err)
	}
	if stored, err := os.ReadFile(compressed); err != nil || !bytes.Contains(stored, []byte(context)) {
		t.Fatalf(
			"fixture %s holds no verbatim %s (%v): the encoder no longer stores the block raw",
			compressed,
			context,
			err,
		)
	}
	row.Path = compressed
	if facts, err := reader.codexFacts(&row, time.Now().UnixNano()); err != nil || facts != (Facts{}) {
		t.Fatalf("compressed facts = %+v, %v; want none", facts, err)
	}
}
