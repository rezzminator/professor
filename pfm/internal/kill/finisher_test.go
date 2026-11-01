package kill

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
)

// CaptureScreen, CaptureStyled, CaptureTarget, SendKey, SendLiteral,
// SendPaste and LeaveCopyMode make fakeTmux the finisher's pane door (call.Screen) for the tests that never
// read the screen: an idle, empty composer, and every typed text kept in sent.
func (tmux *fakeTmux) CaptureScreen(context.Context, pfmcall.Target) (string, error) {
	return idleFrame, nil
}

func (tmux *fakeTmux) CaptureStyled(context.Context, pfmcall.Target) (string, error) {
	return idleFrame, nil
}

func (tmux *fakeTmux) SendKey(context.Context, pfmcall.Target, string) error { return nil }

func (tmux *fakeTmux) SendLiteral(_ context.Context, _ pfmcall.Target, text string) error {
	tmux.mutex.Lock()
	defer tmux.mutex.Unlock()
	tmux.sent = append(tmux.sent, text)
	return nil
}

func (tmux *fakeTmux) SendPaste(context.Context, pfmcall.Target, string) error {
	return errors.New("the finisher never pastes")
}

func (tmux *fakeTmux) CaptureTarget(context.Context, pfmcall.Target, int) (string, error) {
	return "", errors.New("the finisher never reads history")
}

func (tmux *fakeTmux) LeaveCopyMode(context.Context, pfmcall.Target) (bool, error) {
	return false, errors.New("the finisher never leaves copy mode")
}

// screenTmux is fakeTmux with a screen: the pane frames a capture answers in
// order, the last one repeating, and a key whose press ends the pane (the
// exit dialog's Enter). styled answers the styled capture ("" is the current
// frame, unstyled); a captureErr or
// styledErr fails that read. events is every pane-facing call in order — a
// typed line and the Enter that submits it are one "line" event — and
// targets every address the door was handed.
type screenTmux struct {
	*fakeTmux
	screen                []string
	styled                string
	captureErr, styledErr error
	keyGone               string
	gone                  bool
	typing                string
	events                []string
	targets               []pfmcall.Target
}

func (tmux *screenTmux) CaptureScreen(_ context.Context, target pfmcall.Target) (string, error) {
	tmux.mutex.Lock()
	defer tmux.mutex.Unlock()
	tmux.events = append(tmux.events, "capture")
	tmux.targets = append(tmux.targets, target)
	if tmux.captureErr != nil {
		return "", tmux.captureErr
	}
	frame := tmux.screen[0]
	if len(tmux.screen) > 1 {
		tmux.screen = tmux.screen[1:]
	}
	return frame, nil
}

func (tmux *screenTmux) CaptureStyled(_ context.Context, target pfmcall.Target) (string, error) {
	tmux.mutex.Lock()
	defer tmux.mutex.Unlock()
	tmux.events = append(tmux.events, "styled")
	tmux.targets = append(tmux.targets, target)
	if tmux.styled == "" && tmux.styledErr == nil {
		return tmux.screen[0], nil
	}
	return tmux.styled, tmux.styledErr
}

func (tmux *screenTmux) SendKey(_ context.Context, target pfmcall.Target, key string) error {
	tmux.mutex.Lock()
	defer tmux.mutex.Unlock()
	tmux.targets = append(tmux.targets, target)
	if tmux.typing != "" && key == "Enter" {
		tmux.events = append(tmux.events, "line "+tmux.typing)
		tmux.typing = ""
		return nil
	}
	tmux.events = append(tmux.events, "key "+key)
	tmux.gone = tmux.gone || key == tmux.keyGone
	return nil
}

func (tmux *screenTmux) SendLiteral(ctx context.Context, target pfmcall.Target, text string) error {
	tmux.mutex.Lock()
	tmux.typing = text
	tmux.targets = append(tmux.targets, target)
	tmux.mutex.Unlock()
	return tmux.fakeTmux.SendLiteral(ctx, target, text)
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
			finisher, err := NewFinisher(database, Dependencies{Tmux: tmux, Screen: tmux})
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

// An unreadable children table never hides a chat's flat-file teammates: they
// are reaped, their registrations kept for the retry the table's failure
// forces, and that failure returned beside them.
func TestReapTeammatesReadsTheFlatFileWhenTheTableIsUnreadable(t *testing.T) {
	jail := newKillJail(t)
	database := jail.open(t)
	ctx := context.Background()
	tmux := &fakeTmux{}
	finisher, err := NewFinisher(database, Dependencies{Tmux: tmux, Screen: tmux})
	if err != nil {
		t.Fatal(err)
	}
	const id = "parent"
	registrations := map[string]string{fleetdb.KindNew: "cc-legacy", fleetdb.KindPane: "shared\t%7"}
	for kind, value := range registrations {
		flat := filepath.Join(jail.home, ".claude", ".cc-"+kind+"-children", id)
		if err := os.MkdirAll(filepath.Dir(flat), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(flat, []byte(value+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	err = finisher.reapTeammates(ctx, id)
	const closed = "sql: database is closed"
	want := "read new children of parent: query shared children: " + closed + "\n" +
		`reaped new child "cc-legacy" of parent; its registration stays: clear shared new child "cc-legacy" of parent: ` +
		closed + "\n" +
		"read pane children of parent: query shared children: " + closed + "\n" +
		`reaped pane child "shared\t%7" of parent; its registration stays: clear shared pane child "shared\t%7" of parent: ` +
		closed
	if got := fmt.Sprint(err); got != want {
		t.Errorf("reap error = %q, want %q", got, want)
	}
	if got, want := fmt.Sprint(tmux.killedServers, tmux.killedPanes), "[cc-legacy] [shared\t%7]"; got != want {
		t.Errorf("reaped servers and panes = %q, want %q", got, want)
	}
	for kind, value := range registrations {
		flat := filepath.Join(jail.home, ".claude", ".cc-"+kind+"-children", id)
		if contents, err := os.ReadFile(flat); err != nil || string(contents) != value+"\n" {
			t.Errorf("%s flat registrations = %q, %v; want %q kept", kind, contents, err, value+"\n")
		}
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
// gone gets no keystroke at all — the fallback kill-pane ends it. exit
// and kill --exit share this finisher.
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
				Screen:       test.tmux,
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
			socketPath := filepath.Join(jail.tmuxDir, "cc-700-1-1")
			runErr := finisher.Run(context.Background(), ExitArgs{
				Engine:     engine,
				ID:         "77777777-7777-4777-8777-777777777777",
				SocketPath: socketPath,
				SocketName: "cc-700-1-1",
				PaneID:     "%4",
			})
			if runErr != nil {
				t.Fatalf("Run: %v", runErr)
			}
			// Every read and keystroke went through the one pane door, addressed
			// to this chat's own tmux pane.
			want := pfmcall.Target{SocketPath: socketPath, Pane: "%4", Engine: string(engine)}
			if len(test.tmux.targets) == 0 {
				t.Fatal("the finisher never reached the pane door")
			}
			for _, target := range test.tmux.targets {
				if target != want {
					t.Fatalf("pane door addressed %+v, want %+v", target, want)
				}
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
