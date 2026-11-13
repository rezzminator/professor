package picker

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// A test selection flag bounds the old binary to this assertion, not the suite.
func TestNativeEndWorkerInvocationIsOwned(t *testing.T) {
	if os.Getenv("PFM_PICKER_WORKER_PROBE") == "1" {
		t.Fatal("unowned worker re-entered picker test suite")
	}
	marker := filepath.Join(t.TempDir(), "worker")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(
		ctx,
		os.Args[0],
		"-test.run=^TestNativeEndWorkerInvocationIsOwned$",
		"chat",
		"end",
		"--native-end-worker",
		"probe-seat",
	)
	command.Env = append(os.Environ(), "PFM_PICKER_WORKER_PROBE=1", "PFM_PICKER_END_WORKER="+marker)
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("worker invocation was not owned: %v: %s", err, out)
	}
	body, err := os.ReadFile(marker)
	if err != nil || !strings.HasSuffix(string(body), " probe-seat") {
		t.Fatalf("worker acknowledgement=%q err=%v", body, err)
	}
}
