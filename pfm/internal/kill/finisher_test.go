package kill

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
)

// Capture, CaptureStyled and SendKey complete fakeTmux as a TmuxClient for the
// tests that never read the screen: an idle, empty composer, and no key.
func (tmux *fakeTmux) Capture(context.Context, string, string) (string, error) { return idleFrame, nil }

func (tmux *fakeTmux) CaptureStyled(context.Context, string, string) (string, error) {
	return idleFrame, nil
}

func (tmux *fakeTmux) SendKey(context.Context, string, string, string) error { return nil }

// screenTmux is fakeTmux with a screen: the pane frames Capture answers in
// order, the last one repeating, and a key whose press ends the pane (the
// exit dialog's Enter). styled answers the styled capture; a captureErr or
// styledErr fails that read. events is every pane-facing call in order.
type screenTmux struct {
	*fakeTmux
	screen                []string
	styled                string
	captureErr, styledErr error
	keyGone               string
	gone                  bool
	events                []string
}

func (tmux *screenTmux) Capture(context.Context, string, string) (string, error) {
	tmux.mutex.Lock()
	defer tmux.mutex.Unlock()
	tmux.events = append(tmux.events, "capture")
	if tmux.captureErr != nil {
		return "", tmux.captureErr
	}
	frame := tmux.screen[0]
	if len(tmux.screen) > 1 {
		tmux.screen = tmux.screen[1:]
	}
	return frame, nil
}

func (tmux *screenTmux) CaptureStyled(context.Context, string, string) (string, error) {
	tmux.mutex.Lock()
	defer tmux.mutex.Unlock()
	tmux.events = append(tmux.events, "styled")
	return tmux.styled, tmux.styledErr
}

func (tmux *screenTmux) SendKey(_ context.Context, _, _, key string) error {
	tmux.mutex.Lock()
	defer tmux.mutex.Unlock()
	tmux.events = append(tmux.events, "key "+key)
	tmux.gone = tmux.gone || key == tmux.keyGone
	return nil
}

func (tmux *screenTmux) SendLine(ctx context.Context, socketPath, paneID, line string) error {
	tmux.mutex.Lock()
	tmux.events = append(tmux.events, "line "+line)
	tmux.mutex.Unlock()
	return tmux.fakeTmux.SendLine(ctx, socketPath, paneID, line)
}

func (tmux *screenTmux) PaneExists(ctx context.Context, socketPath, paneID string) (bool, error) {
	tmux.mutex.Lock()
	gone := tmux.gone
	tmux.mutex.Unlock()
	if gone {
		return false, nil
	}
	return tmux.fakeTmux.PaneExists(ctx, socketPath, paneID)
}

type reapTmux struct {
	*fakeTmux
	serverErr error
}

func (tmux *reapTmux) KillServer(_ context.Context, socketPath string) error {
	if filepath.Base(socketPath) == "cc-gone" {
		return errors.New("no server running on " + socketPath)
	}
	return tmux.serverErr
}

func (tmux *reapTmux) KillPane(ctx context.Context, socketPath, pane string) error {
	if pane == "%8" {
		return nil
	}
	return tmux.fakeTmux.KillPane(ctx, socketPath, pane)
}

func TestReapTeammatesRetainsFailedChildren(t *testing.T) {
	for _, gone := range []bool{false, true} {
		t.Run(map[bool]string{false: "live failures", true: "already gone"}[gone], func(t *testing.T) {
			jail := newKillJail(t)
			database := jail.open(t)
			ctx := context.Background()
			serverErr, paneErr := errors.New("kill-server permission denied"), errors.New("kill-pane permission denied")
			if gone {
				serverErr, paneErr = errors.New("no server running on /x"), errors.New("can't find pane: %7")
			}
			tmux := &reapTmux{fakeTmux: &fakeTmux{killPaneErr: paneErr}, serverErr: serverErr}
			finisher, err := NewFinisher(database, Dependencies{Tmux: tmux})
			if err != nil {
				t.Fatal(err)
			}
			const id, socket, pane = "parent", "cc-child", "shared\t%7"
			for kind, value := range map[string]string{fleetdb.KindNew: socket, fleetdb.KindPane: pane} {
				successful := "cc-gone"
				if kind == fleetdb.KindPane {
					successful = "shared\t%8"
				}
				for _, child := range []string{value, successful} {
					if err := database.Shared().AddChild(ctx, kind, id, child, 1); err != nil {
						t.Fatal(err)
					}
				}
				flat := filepath.Join(jail.home, ".claude", ".cc-"+kind+"-children", id)
				if err := os.MkdirAll(filepath.Dir(flat), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(flat, []byte(value+"\n"+successful+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			socketPath := filepath.Join(jail.tmuxDir, socket)
			if err := os.WriteFile(socketPath, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			err = finisher.reapTeammates(ctx, id)
			if gone && err != nil {
				t.Fatalf("already-gone reap = %v", err)
			}
			if !gone && (!errors.Is(err, serverErr) || !errors.Is(err, paneErr)) {
				t.Errorf("reap error = %v, want both kill failures", err)
			}
			for kind, value := range map[string]string{fleetdb.KindNew: socket, fleetdb.KindPane: pane} {
				values, _, err := database.Shared().Children(ctx, kind, id)
				if err != nil || gone && len(values) != 0 || !gone && (len(values) != 1 || values[0] != value) {
					t.Errorf("%s registrations = %v, %v; gone=%v", kind, values, err, gone)
				}
				flat := filepath.Join(jail.home, ".claude", ".cc-"+kind+"-children", id)
				contents, readErr := os.ReadFile(flat)
				if gone && !errors.Is(readErr, os.ErrNotExist) ||
					!gone && (readErr != nil || string(contents) != value+"\n") {
					t.Errorf("%s legacy registrations = %q, %v; gone=%v", kind, contents, readErr, gone)
				}
			}
			_, statErr := os.Stat(socketPath)
			if gone && !errors.Is(statErr, os.ErrNotExist) || !gone && statErr != nil {
				t.Errorf("detached socket stat = %v; gone=%v", statErr, gone)
			}
		})
	}
}

// Claude Code frames the finisher reads: a running turn's footer, the idle
// composer, one holding a draft, one showing a dim suggestion (plain, then
// styled), the transcript view covering the composer, and the background-work
// confirmation /exit opens over it.
const (
	busyFrame  = "⏺ one line, then the turn ends\n✻ Thinking… (3s · ↑ 40 tokens · esc to interrupt)\n"
	idleFrame  = "⏺ done\n────────\n❯ \n────────\n"
	draftFrame = "⏺ done\n────────\n❯ fix the\n────────\n"
	hintFrame  = "⏺ done\n────────\n❯ Try \"fix lint\"\n────────\n"
	hintStyled = "⏺ done\n────────\n❯ \x1b[2mTry \"fix lint\"\x1b[0m\n────────\n"
	// A draft led by a newline leaves the marker row empty, its text a row
	// lower; a box with no closing rule has no known end to read down to.
	secondRowDraftFrame = "⏺ done\n────────\n❯ \n  fix the\n────────\n"
	// A list begun on the second row: one character, no spinner frame.
	secondRowDashFrame = "⏺ done\n────────\n❯ \n  -\n────────\n"
	openBoxFrame       = "⏺ done\n────────\n❯ \n"
	// Codex's idle composer: a › row, then its footer, and no box to bound a
	// draft that wraps or starts with a newline.
	codexIdleFrame = "› \n\n  gpt-6.1-sol high · ~/work\n"
	// The transcript view (ctrl+o) covers the composer: an idle footer, no box.
	transcriptFrame = "⏺ done\n  Showing detailed transcript · ctrl+o to toggle\n"
	// A turn parked on a permission prompt: the footer is idle, the ❯ row
	// is the selector's, never a human's draft.
	permissionFrame = "⏺ Bash(rm -rf build)\n Do you want to proceed?\n ❯ 1. Yes\n   2. No\n"
	dialogFrame     = "   Background work is running\n" +
		"   The following will stop when you exit:\n" +
		"   shell · sleep 25 && echo done\n" +
		"   ❯ 1. Exit and stop tasks\n" +
		"     2. Stay\n" +
		"   Enter to confirm · Esc to cancel\n"
)

// TestFinisherTypesExitOnlyAfterTheTurnAndConfirmsTheExitDialog is the
// finisher's graceful half, reload's worker's two rules: /exit typed into a
// running turn is conversation text, so it waits for the footer to go idle;
// and /exit over background work opens a confirmation whose preselected
// "Exit and stop tasks" it presses Enter on. /exit goes only into a composer
// proven empty: a turn still running at the bound, a draft (Enter would send
// it as a prompt) on the marker row or a row below it, a Claude box with no
// closing rule, a composer not drawn, an engine whose composer pfm cannot
// read (OpenCode), a composer whose styling cannot be read, or a pane already
// gone gets no keystroke at all — the fallback kill-pane ends it. chat_exit
// and chat_kill --exit share this finisher.
func TestFinisherTypesExitOnlyAfterTheTurnAndConfirmsTheExitDialog(t *testing.T) {
	tests := []struct {
		name       string
		engine     pfmengine.ID
		tmux       *screenTmux
		wantEvents []string
		wantKilled bool
		// wantReason is in the one log line a run that typed no exit command
		// writes; "" means the log stays empty.
		wantReason string
	}{
		{
			name: "busy turn, then the dialog",
			tmux: &screenTmux{
				fakeTmux: &fakeTmux{existsFor: 1 << 20}, keyGone: "Enter",
				screen: []string{busyFrame, busyFrame, idleFrame, idleFrame, dialogFrame},
			},
			wantEvents: []string{
				"capture", "capture", "capture", "capture", "line /exit", "capture", "key Enter",
			},
		},
		{
			name:       "idle chat with no background work",
			tmux:       &screenTmux{fakeTmux: &fakeTmux{existsFor: 1}, screen: []string{idleFrame}},
			wantEvents: []string{"capture", "capture", "line /exit", "capture"},
		},
		{
			name:       "turn still running at the bound",
			tmux:       &screenTmux{fakeTmux: &fakeTmux{existsFor: 1 << 20}, screen: []string{busyFrame}},
			wantEvents: []string{"capture", "capture", "capture", "capture", "capture", "capture"},
			wantKilled: true,
			wantReason: "the turn was still running",
		},
		{
			name: "a draft in the composer when the turn ends",
			tmux: &screenTmux{
				fakeTmux: &fakeTmux{existsFor: 1 << 20}, screen: []string{draftFrame}, styled: draftFrame,
			},
			wantEvents: []string{"capture", "capture", "styled"},
			wantKilled: true,
			wantReason: `holds the draft "fix the"`,
		},
		{
			name: "a draft ending in a marker",
			tmux: &screenTmux{
				fakeTmux: &fakeTmux{existsFor: 1 << 20},
				screen:   []string{"⏺ done\n────────\n❯ explain this symbol ›\n────────\n"},
				styled:   "⏺ done\n────────\n❯ explain this symbol ›\n────────\n",
			},
			wantEvents: []string{"capture", "capture", "styled"},
			wantKilled: true,
			wantReason: `holds the draft "explain this symbol ›"`,
		},
		{
			name: "a marker on a continuation draft row",
			tmux: &screenTmux{
				fakeTmux: &fakeTmux{existsFor: 1 << 20},
				screen:   []string{"⏺ done\n────────\n❯ unfinished draft\n  ›\n────────\n"},
			},
			wantEvents: []string{"capture", "capture", "styled"},
			wantKilled: true,
			wantReason: `holds the draft "unfinished draft"`,
		},
		{
			name: "a box glyph on a draft row",
			tmux: &screenTmux{
				fakeTmux: &fakeTmux{existsFor: 1 << 20},
				screen:   []string{"⏺ done\n────────\n❯ \n  ─\n  unfinished draft\n────────\n"},
			},
			wantEvents: []string{"capture", "capture"},
			wantKilled: true,
			wantReason: "holds text below its marker row",
		},
		{
			name:       "a composer not drawn",
			tmux:       &screenTmux{fakeTmux: &fakeTmux{existsFor: 1 << 20}, screen: []string{transcriptFrame}},
			wantEvents: []string{"capture", "capture"},
			wantKilled: true,
			wantReason: "drew no composer",
		},
		{
			name: "a permission prompt open when the turn ends",
			tmux: &screenTmux{
				fakeTmux: &fakeTmux{existsFor: 1 << 20}, screen: []string{permissionFrame}, styled: permissionFrame,
			},
			wantEvents: []string{"capture", "capture"},
			wantKilled: true,
			wantReason: `shows the open selector "❯ 1. Yes"`,
		},
		{
			// pfm reads no OpenCode composer, so it can never prove that box
			// empty: an OpenCode pane closes by the fallback kill alone.
			name:       "an engine whose composer pfm cannot read",
			engine:     pfmengine.OpenCode,
			tmux:       &screenTmux{fakeTmux: &fakeTmux{existsFor: 1 << 20}, screen: []string{idleFrame}},
			wantEvents: []string{"capture", "capture"},
			wantKilled: true,
			wantReason: "cannot prove the OpenCode composer empty",
		},
		{
			// Codex's composer row is readable but its box has no closing
			// rule, so a draft below the row is unbounded: Codex closes by
			// the fallback kill alone, never a typed /quit.
			name:       "an engine whose composer has no provable end",
			engine:     pfmengine.Codex,
			tmux:       &screenTmux{fakeTmux: &fakeTmux{existsFor: 1 << 20}, screen: []string{codexIdleFrame}},
			wantEvents: []string{"capture", "capture"},
			wantKilled: true,
			wantReason: "cannot prove the Codex composer empty",
		},
		{
			name:       "a draft on the composer's second row",
			tmux:       &screenTmux{fakeTmux: &fakeTmux{existsFor: 1 << 20}, screen: []string{secondRowDraftFrame}},
			wantEvents: []string{"capture", "capture"},
			wantKilled: true,
			wantReason: "holds text below its marker row",
		},
		{
			name:       "one character on the composer's second row",
			tmux:       &screenTmux{fakeTmux: &fakeTmux{existsFor: 1 << 20}, screen: []string{secondRowDashFrame}},
			wantEvents: []string{"capture", "capture"},
			wantKilled: true,
			wantReason: "holds text below its marker row",
		},
		{
			name:       "a composer box with no closing rule",
			tmux:       &screenTmux{fakeTmux: &fakeTmux{existsFor: 1 << 20}, screen: []string{openBoxFrame}},
			wantEvents: []string{"capture", "capture"},
			wantKilled: true,
			wantReason: "has no closing rule",
		},
		{
			name: "a dim suggestion is an empty composer",
			tmux: &screenTmux{
				fakeTmux: &fakeTmux{existsFor: 1}, screen: []string{hintFrame}, styled: hintStyled,
			},
			wantEvents: []string{"capture", "capture", "styled", "line /exit", "capture"},
		},
		{
			name: "a composer whose styling cannot be read",
			tmux: &screenTmux{
				fakeTmux: &fakeTmux{existsFor: 1 << 20}, screen: []string{draftFrame},
				styledErr: errors.New("capture-pane -e failed"),
			},
			wantEvents: []string{"capture", "capture", "styled"},
			wantKilled: true,
			wantReason: "capture-pane -e failed",
		},
		{
			name: "the pane is already gone when the turn ends",
			tmux: &screenTmux{
				fakeTmux:   &fakeTmux{killPaneErr: errors.New("can't find pane: %4")},
				captureErr: errors.New("can't find pane: %4"),
			},
			wantEvents: []string{"capture"},
			wantKilled: true,
			wantReason: "already gone",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			jail := newKillJail(t)
			database := jail.open(t)
			var log bytes.Buffer
			finisher, err := NewFinisher(database, Dependencies{
				Log:          &log,
				Tmux:         test.tmux,
				Refresher:    refreshFunc(func(context.Context) error { return nil }),
				Delay:        time.Millisecond,
				PollEvery:    time.Millisecond,
				PollAttempts: 5,
				IdleAttempts: 6,
			})
			if err != nil {
				t.Fatal(err)
			}
			engine := test.engine
			if engine == "" {
				engine = pfmengine.Claude
			}
			runErr := finisher.Run(context.Background(), ExitArgs{
				Engine:     engine,
				ID:         "77777777-7777-4777-8777-777777777777",
				SocketPath: filepath.Join(jail.tmuxDir, "cc-700-1-1"),
				SocketName: "cc-700-1-1",
				PaneID:     "%4",
			})
			if runErr != nil {
				t.Fatalf("Run: %v", runErr)
			}
			if !reflect.DeepEqual(test.tmux.events, test.wantEvents) {
				t.Fatalf("pane events = %q, want %q", test.tmux.events, test.wantEvents)
			}
			if test.wantKilled != (len(test.tmux.sent) == 0) || len(test.tmux.killedPanes) != 1 {
				t.Fatalf(
					"sent = %q, killed %q, with fallback-only %v",
					test.tmux.sent, test.tmux.killedPanes, test.wantKilled,
				)
			}
			lines := strings.Split(strings.TrimSuffix(log.String(), "\n"), "\n")
			if test.wantReason == "" && log.Len() != 0 ||
				test.wantReason != "" && (len(lines) != 1 || !strings.Contains(lines[0], "no exit command typed") ||
					!strings.Contains(lines[0], test.wantReason)) {
				t.Fatalf("log = %q, want one no-exit-command line naming %q", log.String(), test.wantReason)
			}
		})
	}
}
