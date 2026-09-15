package kill

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/store"
)

// A chat is watched THROUGH a bunker pane. Ending the chat has to end that pane
// too, or the tab it lives in falls back to a shell prompt nobody asked for —
// which is what happens whenever the pane spawned its viewport as a child
// instead of being exec'd into it. The tty is the join between the two.

func exitFinisher(t *testing.T, jail killJail, tmux *fakeTmux) (*Finisher, *store.Store, string) {
	t.Helper()
	database := jail.open(t)
	ctx := context.Background()
	id := "77777777-7777-4777-8777-777777777777"
	path := filepath.Join(jail.claudeRoot, "projects", "p", id+".jsonl")
	if err := database.UpsertTranscript(ctx, store.Transcript{
		UUID:        id,
		Path:        path,
		PromptCount: 2,
	}); err != nil {
		t.Fatal(err)
	}
	finisher, err := NewFinisher(database, Dependencies{
		Tmux:         tmux,
		Refresher:    refreshFunc(func(context.Context) error { return nil }),
		Delay:        time.Millisecond,
		PollEvery:    time.Millisecond,
		PollAttempts: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return finisher, database, id
}

func TestExitClosesTheBunkerPaneWatchingTheChat(t *testing.T) {
	jail := newKillJail(t)
	tmux := &fakeTmux{
		clientTTYs: []string{"/dev/pts/9"},
		panesByTTY: map[string]string{"/dev/pts/9": "%42", "/dev/pts/3": "%7"},
	}
	finisher, database, id := exitFinisher(t, jail, tmux)
	defer database.Close()

	if err := finisher.Run(context.Background(), ExitArgs{
		Engine:     pfmengine.Claude,
		ID:         id,
		DataPath:   filepath.Join(jail.claudeRoot, "projects", "p", id+".jsonl"),
		SocketPath: filepath.Join(jail.tmuxDir, "cc-500-1-1"),
		SocketName: "cc-500-1-1",
		PaneID:     "%10",
	}); err != nil {
		t.Fatal(err)
	}
	// the chat's own pane first, then the bunker pane that was watching it
	if !reflect.DeepEqual(tmux.killedPanes, []string{"cc-500-1-1\t%10", "vsct\t%42"}) {
		t.Fatalf("killed panes = %q, want the chat pane then the viewport", tmux.killedPanes)
	}
}

func TestExitLeavesPanesThatWereNotWatchingAlone(t *testing.T) {
	jail := newKillJail(t)
	// a client on a real terminal (an ssh viewer) — no bunker pane is that tty
	tmux := &fakeTmux{
		clientTTYs: []string{"/dev/pts/77"},
		panesByTTY: map[string]string{"/dev/pts/3": "%7", "/dev/pts/4": "%8"},
	}
	finisher, database, id := exitFinisher(t, jail, tmux)
	defer database.Close()

	if err := finisher.Run(context.Background(), ExitArgs{
		Engine:     pfmengine.Claude,
		ID:         id,
		DataPath:   filepath.Join(jail.claudeRoot, "projects", "p", id+".jsonl"),
		SocketPath: filepath.Join(jail.tmuxDir, "cc-500-1-2"),
		SocketName: "cc-500-1-2",
		PaneID:     "%11",
	}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(tmux.killedPanes, []string{"cc-500-1-2\t%11"}) {
		t.Fatalf("killed panes = %q, want only the chat's own pane", tmux.killedPanes)
	}
}

func TestExitWithNoViewportKillsOnlyTheChat(t *testing.T) {
	jail := newKillJail(t)
	tmux := &fakeTmux{}
	finisher, database, id := exitFinisher(t, jail, tmux)
	defer database.Close()

	if err := finisher.Run(context.Background(), ExitArgs{
		Engine:     pfmengine.Claude,
		ID:         id,
		DataPath:   filepath.Join(jail.claudeRoot, "projects", "p", id+".jsonl"),
		SocketPath: filepath.Join(jail.tmuxDir, "cc-500-1-3"),
		SocketName: "cc-500-1-3",
		PaneID:     "%12",
	}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(tmux.killedPanes, []string{"cc-500-1-3\t%12"}) {
		t.Fatalf("killed panes = %q, want only the chat's own pane", tmux.killedPanes)
	}
}
