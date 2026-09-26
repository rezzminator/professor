package kill

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/store"
)

// TestFinisherSurfacesAKillItNeverSawLand: a PaneExists probe that never
// resolves ("could not ask", never read as "gone") burns the whole grace
// window, and the fallback KillPane also fails — the pane may still be
// alive. The finisher must not write a Killed row it never saw land, and
// its returned error must carry the KillPane failure instead of discarding
// it (F6).
func TestFinisherSurfacesAKillItNeverSawLand(t *testing.T) {
	jail := newKillJail(t)
	database := jail.open(t)
	ctx := context.Background()
	id := "88888888-8888-4888-8888-888888888888"
	transcriptPath := filepath.Join(jail.claudeRoot, id+".jsonl")
	if err := database.UpsertTranscript(ctx, store.Transcript{
		UUID:        id,
		Path:        transcriptPath,
		PromptCount: 1,
	}); err != nil {
		t.Fatal(err)
	}

	killPaneErr := errors.New("kill-pane: pane still attached")
	tmux := &fakeTmux{existsErrFor: 10, killPaneErr: killPaneErr}
	finisher, err := NewFinisher(database, Dependencies{
		Tmux:         tmux,
		Refresher:    refreshFunc(func(context.Context) error { return nil }),
		Delay:        time.Millisecond,
		PollEvery:    time.Millisecond,
		PollAttempts: 3,
		Now:          func() time.Time { return time.Unix(999, 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	runErr := finisher.Run(ctx, ExitArgs{
		Engine:     pfmengine.Claude,
		ID:         id,
		DataPath:   transcriptPath,
		SocketPath: filepath.Join(jail.tmuxDir, "cc-600-1-1"),
		SocketName: "cc-600-1-1",
		PaneID:     "%9",
	})
	if runErr == nil {
		t.Fatal("Run succeeded even though the pane was never confirmed gone")
	}
	if !strings.Contains(runErr.Error(), "never confirmed gone") {
		t.Fatalf("error = %v, want it to name the unconfirmed kill", runErr)
	}
	if !strings.Contains(runErr.Error(), killPaneErr.Error()) {
		t.Fatalf("error = %v, want it to surface the discarded KillPane failure %q", runErr, killPaneErr)
	}
	if _, found, err := database.Killed(ctx, id); err != nil {
		t.Fatalf("Killed(%q): %v", id, err)
	} else if found {
		t.Fatalf("a kill the finisher never saw land was recorded Killed")
	}
}
