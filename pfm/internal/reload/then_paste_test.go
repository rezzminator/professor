package reload

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/gather"
)

// codexPasteTmux plays a reborn Codex pane: an empty composer until the
// follow-up is pasted, then the composer drawing it (as draftView renders
// it) until an Enter submits it — when submits is true.
type codexPasteTmux struct {
	fakeReloadTmux
	pastes    []string
	enters    int
	draftView func(text string) string
	submits   bool
	submitted bool
}

func (tmux *codexPasteTmux) paste(_ context.Context, _, _, text string) error {
	tmux.pastes = append(tmux.pastes, text)
	return nil
}

func (tmux *codexPasteTmux) Capture(context.Context, string, string) (string, error) {
	if tmux.submitted {
		return "OpenAI Codex\n• working on the follow-up\n› ", nil
	}
	if len(tmux.pastes) == 0 || tmux.draftView == nil {
		return "OpenAI Codex\n› ", nil
	}
	return "OpenAI Codex\n" + tmux.draftView(tmux.pastes[len(tmux.pastes)-1]), nil
}

func (tmux *codexPasteTmux) SendKey(ctx context.Context, socket, pane, key string) error {
	if key == "Enter" {
		tmux.enters++
		if tmux.submits && len(tmux.pastes) > 0 {
			tmux.submitted = true
		}
	}
	return tmux.fakeReloadTmux.SendKey(ctx, socket, pane, key)
}

func runCodexThen(t *testing.T, tmux *codexPasteTmux, then string) error {
	t.Helper()
	proc := fakeReloadProc{
		pids: []int{801},
		argv: map[int][]string{801: {"codex"}},
		stat: map[int]gather.ProcStat{801: {ParentPID: 700}},
	}
	fakeClock := clock.NewFake(time.Unix(0, 0))
	var err error
	driveFakeClock(t, fakeClock, func() {
		err = deliverThen(
			context.Background(),
			Request{Engine: pfmengine.Codex, SocketPath: "/tmp/tmux-1000/cx-then-paste", Pane: "%7", Then: then},
			Options{ThenTries: 2, Clock: fakeClock, Paste: tmux.paste},
			tmux,
			proc,
			io.Discard,
		)
	})
	return err
}

const multiLineThen = "finish the migration\nthen run the package tests\nand report the verdict"

func TestDeliverThenPastesAMultiLineCodexFollowUpAndSubmitsItOnce(t *testing.T) {
	t.Parallel()
	views := map[string]func(string) string{
		"collapsed placeholder": func(string) string { return "› [Pasted Content 72 chars]" },
		"inline draft": func(text string) string {
			return "› " + strings.ReplaceAll(text, "\n", "\n  ")
		},
	}
	for name, view := range views {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tmux := &codexPasteTmux{draftView: view, submits: true}
			if err := runCodexThen(t, tmux, multiLineThen); err != nil {
				t.Fatalf("deliverThen() error = %v, want the pasted follow-up submitted", err)
			}
			if len(tmux.pastes) != 1 || tmux.pastes[0] != multiLineThen {
				t.Fatalf("pastes = %q, want the whole follow-up as exactly one paste", tmux.pastes)
			}
			if tmux.literal != "" {
				t.Fatalf("typed %q with send-keys -l, want nothing typed — every newline would be a key", tmux.literal)
			}
			if tmux.enters != 1 {
				t.Fatalf("Enter pressed %d times, want one submit once the composer released it", tmux.enters)
			}
		})
	}
}

func TestDeliverThenRefusesACodexFollowUpTheComposerNeverReleases(t *testing.T) {
	t.Parallel()
	tmux := &codexPasteTmux{draftView: func(string) string { return "› [Pasted Content 72 chars]" }}
	err := runCodexThen(t, tmux, multiLineThen)
	if !errors.Is(err, errThenNotSubmitted) {
		t.Fatalf("deliverThen() error = %v, want errThenNotSubmitted", err)
	}
	if tmux.enters != thenSubmitPresses {
		t.Fatalf("Enter pressed %d times, want %d before giving up", tmux.enters, thenSubmitPresses)
	}
	if len(tmux.displays) != 1 {
		t.Fatalf("displays = %q, want the unconfirmed submit shown on the pane", tmux.displays)
	}
}

func TestDeliverThenPressesNoEnterWhenTheCodexPasteNeverShows(t *testing.T) {
	t.Parallel()
	tmux := &codexPasteTmux{submits: true}
	err := runCodexThen(t, tmux, multiLineThen)
	if !errors.Is(err, errThenNotPasted) {
		t.Fatalf("deliverThen() error = %v, want errThenNotPasted", err)
	}
	if tmux.enters != 0 {
		t.Fatalf("Enter pressed %d times, want none — a blind Enter on an empty composer proves nothing", tmux.enters)
	}
}
