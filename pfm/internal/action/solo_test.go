package action

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// The Solo trail names what the pass closed, so a reader of the trail sees
// whether it killed anything; each kill is also an obs record.
func TestSoloTrailCauseNamesWhatItKilled(t *testing.T) {
	id := "66666666-6666-4666-8666-666666666666"
	for _, test := range []struct {
		name       string
		duplicate  bool
		stray      bool
		wantCause  string
		wantRecord []string
	}{
		{name: "nothing killed", wantCause: "no duplicates"},
		{
			name:       "one pane and one stray process",
			duplicate:  true,
			stray:      true,
			wantCause:  "closed 1 duplicate panes, 1 stray processes",
			wantRecord: []string{"solo.kill_pane", "solo.kill_process"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			jailAction(t)
			sidDir := os.Getenv("PFM_SID_DIR")
			ctx, recorder := obs.Test(t)
			tmux := &fakeActionTmux{
				alive: map[string]bool{"cc-100-1-1": true},
				panes: map[string][]ActionPane{
					"cc-100-1-1": {{PaneID: "%1", TTY: "pts/1"}},
				},
			}
			writeActionFile(t, filepath.Join(sidDir, "cc-100-1-1"), "/tx/"+id+".jsonl", 0o600)
			processes := &fakeProcesses{}
			if test.duplicate {
				writeActionFile(t, filepath.Join(sidDir, "cc-200-1-1.%2"), "/tx/"+id+".jsonl", 0o600)
				tmux.alive["cc-200-1-1"] = true
				tmux.panes["cc-200-1-1"] = []ActionPane{
					{PaneID: "%2", TTY: "pts/2"},
					{PaneID: "%9", TTY: "pts/9"},
				}
			}
			if test.stray {
				processes.processes = []Process{
					{PID: 10, Argv: []string{"claude", "--resume", id}, TTY: "pts/1"},
					{PID: 11, Argv: []string{"claude", "--resume", id}, TTY: "pts/8"},
				}
			}
			executor, err := New(Dependencies{
				Tmux:      tmux,
				Processes: processes,
				Gate:      fixedGate(false),
				Runner:    &captureRunner{},
				Stderr:    io.Discard,
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := executor.Solo(ctx, id, "cc-100-1-1", false); err != nil {
				t.Fatal(err)
			}
			var causes []string
			for _, record := range recorder.Records() {
				if record.Message != "state.transition" {
					continue
				}
				if kind, _ := record.Field("kind"); kind != "action" {
					continue
				}
				if next, _ := record.Field("next"); next != "solo" {
					continue
				}
				cause, _ := record.Field("cause")
				causes = append(causes, fmt.Sprint(cause))
			}
			if !reflect.DeepEqual(causes, []string{test.wantCause}) {
				t.Fatalf("solo causes = %q, want %q: %s", causes, test.wantCause, recorder.Raw())
			}
			for _, message := range test.wantRecord {
				var found bool
				for _, record := range recorder.Records() {
					found = found || record.Message == message
				}
				if !found {
					t.Fatalf("no %s record: %s", message, recorder.Raw())
				}
			}
		})
	}
}
