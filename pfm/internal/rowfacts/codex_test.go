package rowfacts

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
