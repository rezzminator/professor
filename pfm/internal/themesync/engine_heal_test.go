package themesync

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

// The sync's healing and its fault doors: files that come and go after set,
// an apply racing off, surfaces that cannot be looked at, a failed prior read,
// and the follower's every-minute sync re-applying only what drifted.

func TestClaudeFollowsThemeFilesAddedOrDeletedAfterSet(t *testing.T) {
	h := newHarness(t)
	h.set("tokyo-night", ModeAuto)
	plain := strings.Replace(fixtureGold, "Professor Gold", "Professor Teal", 1)
	h.write(".claude/themes/professor-teal.json", plain)
	status, err := ReadBack(context.Background(), h.env)
	if err != nil {
		t.Fatal(err)
	}
	wantVerdict(t, status, "claude", VerdictDrift, "professor-teal.json is not themed")
	if _, err := Sync(context.Background(), h.env, false); err != nil {
		t.Fatal(err)
	}
	if h.read(".claude/themes/professor-teal.json") == plain {
		t.Fatal("a professor-* file installed after set must be themed by the next sync")
	}

	if err := os.Remove(h.path(".claude/themes/professor-gold.json")); err != nil {
		t.Fatal(err)
	}
	report, err := Sync(context.Background(), h.env, false)
	if err != nil {
		t.Fatal(err)
	}
	wantVerdict(t, report, "claude", VerdictInSync, "")
	if _, err := Off(context.Background(), h.env); err != nil {
		t.Fatal(err)
	}
	if h.exists(".claude/themes/professor-gold.json") {
		t.Fatal("off must not bring back a theme file the user deleted")
	}
	if h.read(".claude/themes/professor-teal.json") != plain {
		t.Fatal("off must restore the late file's pre-theme bytes")
	}
}

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
	h.set("tokyo-night", ModeAuto)
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
	for surface, rel := range map[string]string{
		"ghostty": "Library/Application Support/com.mitchellh.ghostty/config.ghostty",
		"vscode":  "Library/Application Support/Code/User/settings.json",
	} {
		t.Run(surface, func(t *testing.T) {
			h := newHarness(t)
			h.set("tokyo-night", ModeAuto)
			if err := os.Remove(h.path(rel)); err != nil {
				t.Fatal(err)
			}
			// A link to itself fails every stat with ELOOP, root or not.
			if err := os.Symlink(h.path(rel), h.path(rel)); err != nil {
				t.Fatal(err)
			}
			report, err := ReadBack(context.Background(), h.env)
			if err != nil {
				t.Fatal(err)
			}
			wantVerdict(t, report, surface, VerdictError, filepath.Base(rel))
		})
	}
}

func TestGhosttyStaleThemeThatCannotBeRemovedIsAnError(t *testing.T) {
	h := newHarness(t)
	h.set("tokyo-night", ModeAuto)
	// A non-empty directory refuses os.Remove, root or not.
	h.write("Library/Application Support/com.mitchellh.ghostty/pfm-theme/old-palette-dark/keep", "x")
	report, err := Sync(context.Background(), h.env, true)
	if err != nil {
		t.Fatal(err)
	}
	wantVerdict(t, report, "ghostty", VerdictError, "old-palette-dark")
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
	h.set("tokyo-night", ModeAuto)
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
	h.set("tokyo-night", ModeAuto)
	current := h.path(".local/state/pfm/theme/current.json")
	minuteAgo := time.Now().Add(-time.Minute)
	if err := os.Chtimes(current, minuteAgo, minuteAgo); err != nil {
		t.Fatal(err)
	}
	h.psVscode = "/Applications/Visual Studio Code.app/Contents/MacOS/Electron\n"
	h.rearm()
	stateBefore, err := os.Stat(filepath.Join(h.stateDir, StateFileName))
	if err != nil {
		t.Fatal(err)
	}
	applyCalls := h.tmux.applyCalls
	// A VS Code without the extension never acks: the follower's every-minute
	// sync re-applies that one surface, never every chat server's options.
	report, err := Sync(context.Background(), h.env, false)
	if err != nil {
		t.Fatal(err)
	}
	wantVerdict(t, report, "vscode", VerdictDrift, "no ack")
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

// A config.ghostty kept as a symlink into another directory: the include line
// names the absolute pfm-theme.ghostty pfm wrote beside the link, so Ghostty's
// resolution of a relative path against the link's target never matters; off
// removes it and the legacy relative line an earlier pfm wrote.
func TestGhosttyIncludeIsAbsoluteThroughASymlinkedConfig(t *testing.T) {
	h := newHarness(t)
	link := "Library/Application Support/com.mitchellh.ghostty/config.ghostty"
	target := "dotfiles/ghostty/config"
	original := h.read(link)
	h.write(target, original)
	if err := os.Remove(h.path(link)); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(h.path(target), h.path(link)); err != nil {
		t.Fatal(err)
	}
	report := h.set("tokyo-night", ModeAuto)
	wantVerdict(t, report, "ghostty", VerdictInSync, "")
	include := filepath.Join(filepath.Dir(h.path(link)), "pfm-theme.ghostty")
	if _, err := os.Stat(include); err != nil {
		t.Fatalf("pfm-theme.ghostty not written beside the link: %v", err)
	}
	got := h.read(target)
	absolute := "config-file = ?" + include + "\n"
	if !strings.Contains(got, absolute) || strings.Contains(got, "config-file = ?pfm-theme.ghostty") {
		t.Fatalf("include line is not the absolute %s:\n%s", include, got)
	}
	h.write(target, got+"config-file = ?pfm-theme.ghostty\n")
	if _, err := Off(context.Background(), h.env); err != nil {
		t.Fatal(err)
	}
	if after := h.read(target); after != original {
		t.Fatalf("off left %q, want the original %q", after, original)
	}
}

// No running Ghostty and no binary at /Applications, ~/Applications or on
// PATH: the row's verdict is the file read-back alone, never ERROR.
func TestGhosttyWithoutABinaryIsJudgedByReadBackAlone(t *testing.T) {
	h := newHarness(t)
	h.psGhostty = "  777 /bin/zsh -l\n"
	h.rearm()
	report := h.set("tokyo-night", ModeAuto)
	wantVerdict(t, report, "ghostty", VerdictApplied, "not validated: Ghostty binary not found (tried ")
	status, err := ReadBack(context.Background(), h.env)
	if err != nil {
		t.Fatal(err)
	}
	wantVerdict(t, status, "ghostty", VerdictInSync, "not validated: Ghostty binary not found")
}

// A binary found at ~/Applications or on PATH that fails validation stays ERROR.
func TestGhosttyBinaryFoundOffTheProcessListStillValidates(t *testing.T) {
	for _, where := range []string{"home", "path"} {
		t.Run(where, func(t *testing.T) {
			h := newHarness(t)
			h.psGhostty = "  777 /bin/zsh -l\n"
			h.rearm()
			binary := h.path("Applications/Ghostty.app/Contents/MacOS/ghostty")
			if where == "home" {
				h.write("Applications/Ghostty.app/Contents/MacOS/ghostty", "#!/bin/sh\n")
			} else {
				binary = "/opt/ghostty/bin/ghostty"
				h.runner.ScriptLookPath("ghostty", binary, nil)
			}
			h.runner.Script(
				[]string{binary, "+validate-config"},
				deps.RunResult{ExitCode: 1, Stderr: []byte("bad key")},
				nil,
			)
			report, err := Set(context.Background(), h.env, Request{Palette: "tokyo-night", Mode: ModeAuto})
			if err != nil {
				t.Fatal(err)
			}
			wantVerdict(t, report, "ghostty", VerdictError, "bad key")
		})
	}
}
