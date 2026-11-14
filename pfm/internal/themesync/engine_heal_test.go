package themesync

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// The sync's healing and its fault doors: files that come and go after set,
// an apply racing off, surfaces that cannot be looked at, a failed prior read,
// and the follower's every-minute sync re-applying only what drifted.

// gatedTmux holds the first Show on one socket until released, so a test can
// run another verb while that apply is in flight.
type gatedTmux struct {
	*fakeTmux
	socket  string
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (gated *gatedTmux) Show(ctx context.Context, socket, name string) (string, error) {
	if socket == gated.socket {
		gated.once.Do(func() {
			close(gated.started)
			<-gated.release
		})
	}
	return gated.fakeTmux.Show(ctx, socket, name)
}

func TestApplyTmuxServerNeverFinishesAfterAnOffThatRestoredTheServer(t *testing.T) {
	h := newHarness(t)
	h.on()
	h.tmux.addServer("cc-born")
	gated := &gatedTmux{fakeTmux: h.tmux, socket: "cc-born", started: make(chan struct{}), release: make(chan struct{})}
	applied := make(chan error, 1)
	go func() { applied <- ApplyTmuxServer(context.Background(), gated, h.stateDir, "cc-born") }()
	select {
	case <-gated.started:
	case <-time.After(10 * time.Second):
		t.Fatal("ApplyTmuxServer never reached the server")
	}
	turnedOff := make(chan error, 1)
	go func() {
		_, err := Off(context.Background(), h.env)
		turnedOff <- err
	}()
	var offErr error
	select {
	case offErr = <-turnedOff: // the apply took no lock: off ran to the end mid-apply
		close(gated.release)
	case <-time.After(time.Second): // off waits for the in-flight apply
		close(gated.release)
		offErr = <-turnedOff
	}
	if err := <-applied; err != nil {
		t.Fatal(err)
	}
	if offErr != nil {
		t.Fatal(offErr)
	}
	if theme, ok := h.tmux.servers["cc-born"]["@pfm-theme"]; ok {
		t.Fatalf("an apply in flight finished after off and left cc-born themed (@pfm-theme=%s)", theme)
	}
}

func TestUnstatableSurfaceFileReadsBackAsError(t *testing.T) {
	rel := "Library/Application Support/Code/User/settings.json"
	h := newHarness(t)
	h.on()
	if err := os.Remove(h.path(rel)); err != nil {
		t.Fatal(err)
	}
	// A link to itself fails every stat with ELOOP, root or not.
	if err := os.Symlink(h.path(rel), h.path(rel)); err != nil {
		t.Fatal(err)
	}
	wantVerdict(
		t,
		h.status(),
		"vscode",
		VerdictError,
		"themesync: stat {tmp}/Library/Application Support/Code/User/settings.json: stat {tmp}/Library/Application Support/Code/User/settings.json: too many levels of symbolic links",
	)
}

// flakyPriorTmux fails every read of one captured prior, as a tmux call that
// times out would.
type flakyPriorTmux struct {
	*fakeTmux
	option string
}

func (flaky flakyPriorTmux) Show(ctx context.Context, socket, name string) (string, error) {
	if name == flaky.option {
		return "", errors.New("tmux: server timed out")
	}
	return flaky.fakeTmux.Show(ctx, socket, name)
}

func TestReapplyNeverRecapturesThePriorOfAThemedServer(t *testing.T) {
	h := newHarness(t)
	h.on()
	flaky := h.env
	flaky.Tmux = flakyPriorTmux{fakeTmux: h.tmux, option: "@pfm-theme-prior-mode-style"}
	if _, err := Sync(context.Background(), flaky, true); err != nil {
		t.Fatal(err)
	}
	if _, err := Off(context.Background(), h.env); err != nil {
		t.Fatal(err)
	}
	if got := h.tmux.servers["cc-a"]["mode-style"]; got != "bg=yellow,fg=black" {
		t.Fatalf("off restored mode-style %q, want the user's bg=yellow,fg=black", got)
	}
}

func TestOffPreservesTmuxPriorsWhenReadsFail(t *testing.T) {
	for _, option := range []string{"mode-style", "@host_accent", tmuxGenerationKey, tmuxChipsKey} {
		t.Run(option, func(t *testing.T) {
			h := newHarness(t)
			h.tmux.servers["cc-a"]["@host_accent"] = "#123456"
			h.on()
			read := option
			if option != tmuxGenerationKey && option != tmuxChipsKey {
				read = tmuxPriorName(option)
			}
			before := h.tmux.servers["cc-a"]["mode-style"]
			prior := h.tmux.servers["cc-a"][tmuxPriorName("mode-style")]
			flaky := h.env
			flaky.Tmux = flakyPriorTmux{fakeTmux: h.tmux, option: read}
			report, err := Off(context.Background(), flaky)
			if err != nil {
				t.Fatal(err)
			}
			failed := ": tmux: server timed out"
			wantVerdict(t, report, surfaceTmux, VerdictError,
				"tmux cc-a: read "+read+failed+"; tmux cc-b: read "+read+failed)
			if got := h.tmux.servers["cc-a"]; got["mode-style"] != before ||
				got[tmuxPriorName("mode-style")] != prior || got[tmuxPriorName("@host_accent")] != "v:#123456" {
				t.Errorf("failed observation changed settings or priors: %v", got)
			}
			if !RestorePending(h.stateDir) {
				t.Error("failed prior read left no restore retry obligation")
			}
			if _, err := Off(context.Background(), h.env); err != nil {
				t.Fatal(err)
			}
			if got := h.tmux.servers["cc-a"]; got["mode-style"] != "bg=yellow,fg=black" ||
				got["@host_accent"] != "#123456" {
				t.Errorf("retry did not restore the original values: %v", got)
			}
		})
	}
}

// A real tmux Apply can fail after a prefix, leaving saved priors and themed styles but no marker.
func TestApplyPreservesTmuxPriorsAfterPartialApply(t *testing.T) {
	// Only cc-a holds a prior to re-read; both servers read the generation and the chips.
	for read, want := range map[string]string{
		tmuxPriorName("mode-style"): "1 of 2 servers failed: tmux cc-a: read @pfm-theme-prior-mode-style: tmux: server timed out",
		tmuxGenerationKey: "2 of 2 servers failed: tmux cc-a: read @pfm-theme-generation: tmux: server timed out;" +
			" tmux cc-b: read @pfm-theme-generation: tmux: server timed out",
		tmuxChipsKey: "2 of 2 servers failed: tmux cc-a: read @pfm-theme-chips: tmux: server timed out;" +
			" tmux cc-b: read @pfm-theme-chips: tmux: server timed out",
	} {
		t.Run(read, func(t *testing.T) {
			h := newHarness(t)
			h.on()
			delete(h.tmux.servers["cc-a"], tmuxGenerationKey)
			flaky := h.env
			flaky.Tmux = flakyPriorTmux{fakeTmux: h.tmux, option: read}
			report, err := Sync(context.Background(), flaky, true)
			if err != nil {
				t.Fatal(err)
			}
			wantVerdict(t, report, surfaceTmux, VerdictError, want)
			if got := h.tmux.servers["cc-a"][tmuxPriorName("mode-style")]; got != "v:bg=yellow,fg=black" {
				t.Errorf("retry overwrote original prior with %q", got)
			}
			if _, err := Off(context.Background(), h.env); err != nil {
				t.Fatal(err)
			}
			if got := h.tmux.servers["cc-a"]["mode-style"]; got != "bg=yellow,fg=black" {
				t.Errorf("off restored themed value %q instead of user value", got)
			}
		})
	}
}

func TestSyncReappliesOnlyTheDriftedSurfaceAndRewritesNoState(t *testing.T) {
	h := newHarness(t)
	h.on()
	minuteAgo := time.Now().Add(-time.Minute)
	if err := os.Chtimes(h.path(".local/state/pfm/theme/current.json"), minuteAgo, minuteAgo); err != nil {
		t.Fatal(err)
	}
	h.processes = [][]string{{"/Applications/Visual Studio Code.app/Contents/MacOS/Electron"}}
	h.rearm()
	// An ack left at another generation while VS Code runs is the one drift.
	h.ackVscode(9)
	stateBefore, err := os.Stat(filepath.Join(h.stateDir, StateFileName))
	if err != nil {
		t.Fatal(err)
	}
	applyCalls := h.tmux.applyCalls
	report := h.sync(false)
	wantVerdict(
		t,
		report,
		"vscode",
		VerdictDrift,
		"extension ack is at generation 9, want 1: no VS Code window running Professor on this machine applied it; a Remote-SSH window runs Professor here only when the client's Professor is 0.6.0 or later: update it there and reload the window",
	)
	wantVerdict(t, report, "tmux", VerdictInSync, "2 servers")
	if h.tmux.applyCalls != applyCalls {
		t.Errorf("a vscode-only drift re-applied tmux: %d apply calls, want %d", h.tmux.applyCalls, applyCalls)
	}
	stateAfter, err := os.Stat(filepath.Join(h.stateDir, StateFileName))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(stateBefore, stateAfter) {
		t.Error("a sync that changed nothing rewrote state.json")
	}
}

func TestFlipPersistsOnlyMeasuredSurfacesIncludingZero(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(map[bool]string{false: "absent", true: "error"}[failure], func(t *testing.T) {
			h := newHarness(t)
			h.on()
			if failure {
				for socket := range h.tmux.servers {
					h.tmux.failApply[socket] = errors.New("apply refused")
				}
			} else {
				h.tmux.servers = map[string]map[string]string{}
			}
			h.write(settingsRel, `{"theme":"light"}`)
			origin := time.Now().Add(time.Hour)
			if err := os.Chtimes(h.path(settingsRel), origin, origin); err != nil {
				t.Fatal(err)
			}
			previous := flipClock
			flipClock = &scriptedClock{times: []time.Time{origin}}
			t.Cleanup(func() { flipClock = previous })
			h.sync(false)
			// state() reloads JSON; marshal once more to prove presence survives a round trip.
			encoded, err := json.Marshal(h.state().LastFlip)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(encoded, &fields); err != nil {
				t.Fatal(err)
			}
			if _, exists := fields["tmux"]; exists {
				t.Errorf("unmeasured tmux offset persisted: %s", encoded)
			}
			for _, surface := range []string{"herdr", "vscode"} {
				if string(fields[surface]) != "0" {
					t.Errorf("measured zero %s offset lost: %s", surface, encoded)
				}
			}
		})
	}
}

// An off that could not restore a surface keeping no file snapshot (a tmux
// server, the follower) leaves the off pending, so the uninstall rerun runs
// it again; the off that restores it clears the mark.
func TestOffThatFailsASurfaceLeavesTheOffPending(t *testing.T) {
	h := newHarness(t)
	h.on()
	h.tmux.failApply["cc-b"] = errors.New("lost connection")
	off, err := Off(context.Background(), h.env)
	if err != nil {
		t.Fatal(err)
	}
	wantVerdict(t, off, "tmux", VerdictError, "tmux cc-b: restore: lost connection")
	if !RestorePending(h.stateDir) {
		t.Fatal("RestorePending = false after an off left tmux server cc-b themed, want true")
	}
	delete(h.tmux.failApply, "cc-b")
	if off, err = Off(context.Background(), h.env); err != nil || !off.OK() {
		t.Fatalf("retried Off = %+v, %v, want every surface restored", off.Surfaces, err)
	}
	if RestorePending(h.stateDir) {
		t.Fatal("RestorePending = true after the retried off restored every surface, want false")
	}
}
