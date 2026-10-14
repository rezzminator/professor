package reload

import (
	"context"
	"strings"
	"testing"
)

// startingPaneTmux is the pane whose CHAT HAS NOT DRAWN ITS INPUT BOX YET —
// the worker reached it while the program in the pane was still starting.
// It models what a real tty does with keys typed into that window: the pane
// is still in cooked mode, so the line discipline ECHOES the text onto the
// screen, and the raw-mode switch the TUI makes as it comes up discards the
// pending bytes (tty.setraw's TCSAFLUSH) before the chat ever reads them. The
// echo is left on the screen AHEAD of the marker the TUI then prints —
// "/exit❯ " — which is the screen the fenced gate captured.
type startingPaneTmux struct {
	fakeReloadTmux
	blankCaptures int
	captures      int
	typedAt       int
	echoed        string
	redrawn       bool
}

func (tmux *startingPaneTmux) Capture(context.Context, string, string) (string, error) {
	tmux.captures++
	if tmux.captures <= tmux.blankCaptures {
		return tmux.echoed, nil
	}
	screen := "Claude\n" + tmux.echoed + "❯ " + tmux.literal
	if tmux.redrawn {
		// An Enter the chat read as an EMPTY line: it just draws the next
		// prompt, and the echoed text stays above it as scrollback.
		screen += "\n❯ "
	}
	return screen, nil
}

func (tmux *startingPaneTmux) SendLiteral(ctx context.Context, socket, pane, value string) error {
	tmux.typedAt = tmux.captures
	if tmux.captures <= tmux.blankCaptures {
		tmux.echoed += value
		return nil
	}
	return tmux.fakeReloadTmux.SendLiteral(ctx, socket, pane, value)
}

func (tmux *startingPaneTmux) SendKey(ctx context.Context, socket, pane, key string) error {
	if key == "Enter" && tmux.literal == "" {
		tmux.redrawn = true
	}
	return tmux.fakeReloadTmux.SendKey(ctx, socket, pane, key)
}

// A pane with nothing on it is not an idle chat — it is a chat that cannot be
// typed into yet, and the difference is the whole reload. Sending /exit there
// loses every keystroke to the TUI's raw-mode flush, so the pane shows neither
// the typed /exit nor an exit dialog for the whole exit loop and the reboot
// dies with "/exit did not complete after N polls; chat left running".
func TestRunWaitsForTheChatsInputBoxBeforeTypingExit(t *testing.T) {
	tmux := &startingPaneTmux{blankCaptures: 3, typedAt: -1}
	_, err := Run(
		context.Background(),
		reloadIdleWaitRequest("/tmp/tmux-1000/probe-reload-starting"),
		Options{SIDDir: t.TempDir(), Delay: -1, Poll: -1, ExitTries: 2, IdleTries: 10},
		tmux,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("reload into a starting pane failed: %v (typed at capture %d)", err, tmux.typedAt)
	}
	if tmux.typedAt <= tmux.blankCaptures {
		t.Fatalf(
			"/exit typed at capture %d with the first %d showing no input box; want it held until one appeared",
			tmux.typedAt,
			tmux.blankCaptures,
		)
	}
	if tmux.literal != "/exit" || tmux.respawn == "" || tmux.echoed != "" {
		t.Fatalf("literal=%q respawn=%q keystrokes lost to the cooked tty=%q", tmux.literal, tmux.respawn, tmux.echoed)
	}
}

// A pane that never draws an input box is not a busy chat: "still busy" would
// claim an observation of a turn nobody ever saw. The refusal has to name the
// state it actually found.
func TestRunNamesAPaneThatNeverShowsAnInputBox(t *testing.T) {
	tmux := &startingPaneTmux{blankCaptures: 1 << 30, typedAt: -1}
	_, err := Run(
		context.Background(),
		reloadIdleWaitRequest("/tmp/tmux-1000/probe-reload-no-box"),
		Options{SIDDir: t.TempDir(), Delay: -1, Poll: -1, ExitTries: 2, IdleTries: 3},
		tmux,
		nil,
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "never showed a chat input box") {
		t.Fatalf("pane with no input box error=%v, want a refusal naming the missing input box", err)
	}
	if tmux.literal != "" || tmux.typedAt != -1 || tmux.respawn != "" {
		t.Fatalf("a pane that cannot take keys was typed into: literal=%q typedAt=%d", tmux.literal, tmux.typedAt)
	}
}

// slowExitTmux is the incident pane: Enter on the typed /exit is accepted —
// the composer clears to its bare prompt, no dialog asks — and Claude takes
// its time to end (transcript flush, SessionEnd hooks), its pane dying only
// dieAfter polls later; dieAfter 0 never dies.
type slowExitTmux struct {
	fakeReloadTmux
	submitted bool
	died      bool
	polls     int
	dieAfter  int
	// after is what the pane shows once the /exit is submitted; empty is the
	// bare composer the incident showed.
	after string
}

func (tmux *slowExitTmux) Capture(ctx context.Context, socket, pane string) (string, error) {
	if tmux.submitted && tmux.after != "" {
		return tmux.after, nil
	}
	return tmux.fakeReloadTmux.Capture(ctx, socket, pane)
}

// ListPanes counts the exit loop's polls: those after the submitted /exit and
// before the pane died, never the respawn's own look at the pane.
func (tmux *slowExitTmux) ListPanes(ctx context.Context, socket string) ([]Pane, error) {
	if tmux.submitted && !tmux.died {
		tmux.polls++
		if tmux.dieAfter > 0 && tmux.polls >= tmux.dieAfter {
			tmux.dead, tmux.died = true, true
		}
	}
	return tmux.fakeReloadTmux.ListPanes(ctx, socket)
}

func (tmux *slowExitTmux) SendKey(_ context.Context, _, _, key string) error {
	if key == "Enter" && tmux.literal == "/exit" {
		tmux.submitted = true
		tmux.literal = ""
	}
	return nil
}

// TestRunWaitsOutAnExitAlreadyInFlight pins the reload that stranded a chat:
// an /exit the chat accepted is not a try spent while the chat ends, so a
// slow end past ExitTries is still waited out and respawned — and the wait
// stays bounded, 60 polls past ExitTries, for an end that never comes.
func TestRunWaitsOutAnExitAlreadyInFlight(t *testing.T) {
	const exitTries = 3
	for _, test := range []struct {
		name      string
		dieAfter  int
		after     string
		wantErr   bool
		wantPolls int
		wantText  string
	}{
		{name: "a slow end past ExitTries respawns", dieAfter: exitTries + 2, wantPolls: exitTries + 2},
		{name: "an end that never comes fails bounded", wantErr: true, wantPolls: exitTries + 60},
		{
			// No composer drawn at all — an overlay, a survey, a blank pane —
			// proves nothing about the /exit: those polls are tries, and the
			// failure never claims the chat is ending.
			name:      "a pane showing no composer earns no grace",
			after:     "How is Claude doing this session? (optional)\n  Bad   Fine   Good   Dismiss",
			wantErr:   true,
			wantPolls: exitTries,
			wantText:  "shows no composer",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			tmux := &slowExitTmux{dieAfter: test.dieAfter, after: test.after}
			_, err := Run(
				context.Background(),
				reloadIdleWaitRequest("/tmp/tmux-1000/probe-reload-slow-exit"),
				Options{SIDDir: t.TempDir(), Delay: -1, Poll: -1, ExitTries: exitTries},
				tmux,
				nil,
				nil,
			)
			if test.wantErr {
				if err == nil || !strings.Contains(err.Error(), "did not complete") || tmux.respawn != "" {
					t.Fatalf(
						"never-ending exit: err=%v respawn=%q, want 'did not complete' and no respawn",
						err,
						tmux.respawn,
					)
				}
			} else if err != nil || tmux.respawn == "" {
				t.Fatalf(
					"slow exit: err=%v respawn=%q after %d polls, want the pane respawned",
					err,
					tmux.respawn,
					tmux.polls,
				)
			}
			if err != nil && !strings.Contains(err.Error(), test.wantText) {
				t.Fatalf("error %q lacks %q", err, test.wantText)
			}
			if tmux.polls != test.wantPolls {
				t.Fatalf("exit polls = %d, want %d", tmux.polls, test.wantPolls)
			}
		})
	}
}
