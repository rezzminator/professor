package themesync

import (
	"context"
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
	wantVerdict(t, h.status(), "vscode", VerdictError, filepath.Base(rel))
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

func TestSyncReappliesOnlyTheDriftedSurfaceAndRewritesNoState(t *testing.T) {
	h := newHarness(t)
	h.on()
	minuteAgo := time.Now().Add(-time.Minute)
	if err := os.Chtimes(h.path(".local/state/pfm/theme/current.json"), minuteAgo, minuteAgo); err != nil {
		t.Fatal(err)
	}
	h.psVscode = "/Applications/Visual Studio Code.app/Contents/MacOS/Electron\n"
	h.rearm()
	// An ack left at another generation while VS Code runs is the one drift.
	h.ackVscode(9)
	stateBefore, err := os.Stat(filepath.Join(h.stateDir, StateFileName))
	if err != nil {
		t.Fatal(err)
	}
	applyCalls := h.tmux.applyCalls
	report := h.sync(false)
	wantVerdict(t, report, "vscode", VerdictDrift, "extension ack is at generation 9, want 1")
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
	wantVerdict(t, off, "tmux", VerdictError, "lost connection")
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
