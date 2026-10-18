package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// Pfm's Herdr theme off always wins. A run that meets the theme off's stop
// marker in record leaves it, however the marker came to be written before the
// record: between the run's start and its record, or by a theme off that ran to
// its end meanwhile. Only the theme on (RetireIndicatorStops) removes it.
func TestARunThatBeganBeforeThemeOffLeavesTheStopMarker(t *testing.T) {
	server := blockedHerdr()
	painter, _, stderr := newTestPainter(t, server, phaseOn)
	// Theme off, between the run's start and its record.
	if err := painter.writeStop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := painter.record(context.Background()); !errors.Is(err, errThemeOff) {
		t.Fatalf("record = %v, want errThemeOff", err)
	}
	if _, err := os.Stat(painter.stopPath()); err != nil {
		t.Fatalf("the theme off's stop marker was removed: %v", err)
	}
	if _, err := os.Stat(painter.recordPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a run that stood down recorded its socket: %v", err)
	}
	if len(server.writes) != 0 || stderr.Len() != 0 {
		t.Fatalf("writes %v, stderr %q, want none", server.writes, stderr)
	}

	// A theme off that ran to its end between the run's start and its record
	// wins as well: its marker outlives it, so the run neither re-creates the
	// record nor paints on while the plugin is unlinked ...
	finished := blockedHerdr()
	earlier, _, _ := newTestPainter(t, finished, phaseOn)
	if err := earlier.record(context.Background()); err != nil {
		t.Fatal(err)
	}
	late, _, _ := newTestPainter(t, finished, phaseOn)
	late.lockDir = earlier.lockDir
	if err := clearRecordedIndicators(
		context.Background(), earlier.lockDir, io.Discard, finished.call, allowPaneProof,
	); err != nil {
		t.Fatalf("theme off = %v", err)
	}
	if err := late.record(context.Background()); !errors.Is(err, errThemeOff) {
		t.Fatalf("record after a finished theme off = %v, want errThemeOff", err)
	}
	if _, err := os.Stat(late.recordPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a run that began before a finished theme off re-created the record: %v", err)
	}
	// ... until the theme on retires the marker: the next run records again.
	if err := RetireIndicatorStops(earlier.lockDir); err != nil {
		t.Fatalf("theme on = %v", err)
	}
	next, _, _ := newTestPainter(t, finished, phaseOn)
	next.lockDir = earlier.lockDir
	if err := next.record(context.Background()); err != nil {
		t.Fatalf("record of the next theme-on run = %v", err)
	}
	if _, err := os.Stat(next.stopPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the theme on kept the theme off's marker: %v", err)
	}
}

// A paint that read no stop marker and is about to send its write is not
// undone by a theme off that begins in between: the theme off waits for the
// write (they share the stop lock), then clears what it wrote. The injected
// step is the theme off itself, started inside the paint's first write.
func TestThemeOffAfterAPaintReadsNoMarkerStillClearsItsWrite(t *testing.T) {
	server := blockedHerdr()
	painter, _, _ := newTestPainter(t, server, phaseOn)
	if err := painter.record(context.Background()); err != nil {
		t.Fatal(err)
	}
	var serverMu sync.Mutex
	serve := func(ctx context.Context, socket string, lines ...[]byte) ([]json.RawMessage, error) {
		serverMu.Lock()
		defer serverMu.Unlock()
		return server.call(ctx, socket, lines...)
	}
	themeOff := make(chan error, 1)
	started := false
	painter.call = func(ctx context.Context, socket string, lines ...[]byte) ([]json.RawMessage, error) {
		if !started && strings.Contains(string(lines[0]), "report_metadata") {
			started = true
			go func() {
				themeOff <- clearRecordedIndicators(context.Background(), painter.lockDir, io.Discard, serve, allowPaneProof)
			}()
			// Time for the theme off to write its marker and clear, if nothing orders it after this write.
			deadline := time.Now().Add(300 * time.Millisecond)
			for time.Now().Before(deadline) {
				if _, err := os.Stat(painter.stopPath()); err == nil {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
		}
		return serve(ctx, socket, lines...)
	}
	if _, _, err := painter.paint(context.Background()); err != nil {
		t.Fatalf("paint = %v", err)
	}
	if err := <-themeOff; err != nil {
		t.Fatalf("theme off = %v", err)
	}
	if len(server.panes[0].Tokens) != 0 || len(server.spaces[0].Tokens) != 0 {
		t.Fatalf("tokens after theme off: pane %v, workspace %v, want none",
			server.panes[0].Tokens, server.spaces[0].Tokens)
	}
	if _, err := os.Stat(painter.recordPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the theme off left the socket's record: %v", err)
	}
}

// A paint that meets the stop marker, or the loss of its record, before a
// write sends nothing: it ends clean and leaves the clear to the theme off. A
// theme off that lands after the paint's token writes, while it reads its
// tabs, stops its tab status labels the same way.
func TestAPaintAfterThemeOffWritesNothing(t *testing.T) {
	for _, tc := range []struct {
		name string
		off  func(*indicatorPainter) error
	}{
		{"the stop marker", func(p *indicatorPainter) error { return p.writeStop(context.Background()) }},
		{"the removed record", func(p *indicatorPainter) error { return os.Remove(p.recordPath()) }},
	} {
		for _, duringTabs := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s, during the tab paint %v", tc.name, duringTabs), func(t *testing.T) {
				server := blockedHerdr()
				painter, _, _ := newTestPainter(t, server, phaseOn)
				if err := painter.record(context.Background()); err != nil {
					t.Fatal(err)
				}
				painter.tabs = func(context.Context, string) ([]TabDecoration, error) {
					if duringTabs {
						if err := tc.off(painter); err != nil {
							return nil, err
						}
					}
					return []TabDecoration{tabObservation("blocked")}, nil
				}
				tab, serve := newTabStatusFixture(), painter.call
				painter.call = func(ctx context.Context, socket string, lines ...[]byte) ([]json.RawMessage, error) {
					line := string(lines[0])
					if strings.Contains(line, `"tab.`) || strings.Contains(line, `"pane.get"`) {
						return tab.call(ctx, socket, lines...)
					}
					return serve(ctx, socket, lines...)
				}
				if !duringTabs {
					if err := tc.off(painter); err != nil {
						t.Fatal(err)
					}
				}
				blocked, wrote, err := painter.paint(context.Background())
				if err != nil || len(tab.renames) != 0 || !blocked {
					t.Fatalf("paint = blocked %v err %v, tab renames %q; want no rename and no error",
						blocked, err, tab.renames)
				}
				if !duringTabs && (wrote || len(server.writes) != 0) {
					t.Fatalf("paint wrote %v, writes %v; want no write", wrote, server.writes)
				}
			})
		}
	}
}

// The painter never removes the theme-off stop marker: a painter that starts
// after a theme off finished meets the marker, leaves it and paints nothing.
// Only a theme on (RetireIndicatorStops) removes it.
func TestAPainterStartedAfterThemeOffLeavesTheMarker(t *testing.T) {
	server := blockedHerdr()
	painter, _, _ := newTestPainter(t, server, phaseOn)
	if err := painter.record(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := clearRecordedIndicators(
		context.Background(), painter.lockDir, io.Discard, server.call, allowPaneProof,
	); err != nil {
		t.Fatalf("theme off = %v", err)
	}
	writes := len(server.writes)
	late, stepping, stderr := newTestPainter(t, server, phaseOn)
	late.lockDir = painter.lockDir
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stepping.onSleep = func(ctx context.Context) error {
		cancel() // a painter that went on to blink ends here
		return ctx.Err()
	}
	if err := late.run(ctx); err != nil {
		t.Fatalf("run after theme off = %v; stderr %q", err, stderr)
	}
	if _, err := os.Stat(late.stopPath()); err != nil {
		t.Fatalf("a painter started after theme off removed the stop marker: %v", err)
	}
	if _, err := os.Stat(late.recordPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a painter started after theme off recorded its socket: %v", err)
	}
	if got := server.writes[writes:]; len(got) != 0 {
		t.Fatalf("a painter started after theme off painted %v", got)
	}
}

// A FIFO or a directory put at the stop marker or the stop lock is a named
// error of the run, never a hang.
func TestStopPathsRefuseAFIFOOrADirectory(t *testing.T) {
	for _, tc := range []struct {
		name, what string
		path       func(*indicatorPainter) string
		make       func(string) error
	}{
		{"a FIFO at the stop marker", "stop marker", (*indicatorPainter).stopPath, fifo},
		{"a directory at the stop marker", "stop marker", (*indicatorPainter).stopPath, directory},
		{"a FIFO at the stop lock", "stop lock", (*indicatorPainter).stopLockPath, fifo},
		{"a directory at the stop lock", "stop lock", (*indicatorPainter).stopLockPath, directory},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := blockedHerdr()
			painter, _, _ := newTestPainter(t, server, phaseOn)
			if err := os.MkdirAll(painter.lockDir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := tc.make(tc.path(painter)); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- painter.run(context.Background()) }()
			select {
			case err := <-done:
				if err == nil || !strings.Contains(err.Error(), tc.what) {
					t.Fatalf("run = %v, want an error naming the %s", err, tc.what)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("run hung on %s", tc.name)
			}
			if len(server.writes) != 0 {
				t.Fatalf("a run refused at its %s painted %v", tc.what, server.writes)
			}
		})
	}
}

func fifo(path string) error      { return syscall.Mkfifo(path, 0o600) }
func directory(path string) error { return os.Mkdir(path, 0o700) }
