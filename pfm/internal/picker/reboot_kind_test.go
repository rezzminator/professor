package picker

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// The picker's reboot kills the seat's server and reopens the SAME chat. The
// reopened row must belong to the same engine: the if/else this used to spell
// sent every non-Codex live row to ResumeClaude, so rebooting a live OpenCode
// chat would have relaunched it as a Claude resume of an id Claude never had.
func TestRebootRowDemotesToTheSeatsOwnEngine(t *testing.T) {
	root := t.TempDir()
	resolved := paths.Values{
		TmuxDir: filepath.Join(root, "tmux"),
		SIDDir:  filepath.Join(root, "sid"),
	}
	for _, test := range []struct {
		kind   compose.Kind
		socket string
		want   compose.Kind
	}{
		{compose.LiveClaude, "cc-1-2-3", compose.ResumeClaude},
		{compose.LiveCodex, "cx-1-2-3", compose.ResumeCodex},
		{compose.LiveOpenCode, "ox-1-2-3", compose.ResumeOpenCode},
	} {
		var stderr bytes.Buffer
		row, err := rebootRow(context.Background(), resolved, compose.Row{
			Kind: test.kind, ID: "seat-id", Socket: test.socket,
			SessionName: test.socket, PaneID: "%0", CWD: "/work",
		}, &stderr)
		if err != nil {
			t.Fatalf("rebootRow(%s): %v", test.kind, err)
		}
		if row.Kind != test.want {
			t.Fatalf("rebootRow(%s).Kind = %s, want %s", test.kind, row.Kind, test.want)
		}
		if compose.EngineForKind(row.Kind) != compose.EngineForKind(test.kind) {
			t.Fatalf("rebootRow(%s) changed engine to %s", test.kind, compose.EngineForKind(row.Kind))
		}
		if row.Socket != "" || row.PaneID != "" || row.SessionName != "" {
			t.Fatalf("rebootRow(%s) kept a dead address: %+v", test.kind, row)
		}
	}
}

func TestNativeLifecycleRefusesPhaseTwo(t *testing.T) {
	row := compose.Row{
		Kind:    compose.LiveClaude,
		ID:      "native",
		Name:    "NATIVE",
		Backend: "herdr",
		Socket:  "/fixture/herdr.sock",
		PaneID:  "w6:p3",
	}
	resolved := paths.Values{StateDB: filepath.Join(t.TempDir(), "pfm.db")}
	for _, action := range []string{"deactivate", "reboot"} {
		t.Run(action, func(t *testing.T) {
			var err error
			var stderr bytes.Buffer
			if action == "deactivate" {
				err = deactivateApplier(context.Background(), resolved)(row)
			} else {
				_, err = rebootRow(context.Background(), resolved, row, &stderr)
			}
			if err == nil || !strings.Contains(err.Error(), "phase 2") {
				t.Fatalf("native %s error=%v output=%q, want phase 2 refusal", action, err, stderr.String())
			}
		})
	}
}
