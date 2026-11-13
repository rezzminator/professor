package chat

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/testjail"
	pfmtmux "github.com/rezzminator/professor/pfm/internal/tmux"
)

// TestCheckAwaitReadsTheVisibleScreenOnEveryHost: the startup-dialog check
// reads a tmux chat through the one pane door's visible-screen read — the
// frame Herdr's read has always been — never a history scroll that can still
// hold a dismissed dialog.
func TestCheckAwaitReadsTheVisibleScreenOnEveryHost(t *testing.T) {
	values := hungValues(t)
	fake := testjail.TmuxRunner(deps.RunResult{
		Stdout: []byte("  Do you trust the files in this folder?\n  1. Yes, I trust this folder\n  2. No, exit\n"),
	})
	chat := headless.Chat{
		Name: "fresh", ID: "sid-fresh", Engine: pfmengine.Claude, Live: true,
		Socket: "cc-fresh-1-1", Pane: "%3",
	}
	err := CheckAwait(pfmtmux.WithClientRunner(context.Background(), fake), chat, &config.Runtime{Paths: values})
	if !errors.Is(err, headless.ErrTrustDialog) {
		t.Fatalf("CheckAwait() = %v, want the trust dialog named", err)
	}
	var read []string
	for _, call := range fake.Calls() {
		read = append(read, strings.Join(call.Argv[1:], " "))
	}
	for _, line := range read {
		if strings.Contains(line, "capture-pane") && strings.Contains(line, " -S ") {
			t.Fatalf("CheckAwait() scrolled history instead of reading the screen: %q", line)
		}
	}
	if !strings.Contains(strings.Join(read, "\n"), "capture-pane") {
		t.Fatalf("CheckAwait() never read the pane: %q", read)
	}
}
