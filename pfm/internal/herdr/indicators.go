package herdr

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/clock"
)

// The sidebar state indicators pfm paints into Herdr. Herdr's own state_icon
// cannot blink and cannot hide its idle glyph, so pfm reports one custom
// token per pane and per workspace, the one its Herdr agent_status maps to:
// idle carries none, blocked carries its token only in the on-phase of the
// blink. The user's sidebar rows name these tokens as `$pfm_working` and the
// rest; themesync keeps each one's fg equal to the Claude theme's hue.
const (
	IndicatorWorking = "pfm_working"
	IndicatorBlocked = "pfm_blocked"
	IndicatorDone    = "pfm_done"
	IndicatorUnknown = "pfm_unknown"
)

// The glyph each indicator token carries.
const (
	GlyphWorking = "●"
	GlyphBlocked = "●"
	GlyphDone    = "✓"
	GlyphUnknown = "○"
)

// IndicatorTokens lists every indicator token, in sidebar order.
func IndicatorTokens() []string {
	return []string{IndicatorWorking, IndicatorBlocked, IndicatorDone, IndicatorUnknown}
}

// The Herdr plugin that runs the painter: its id in Herdr's plugin registry,
// the report source its tokens carry, and the `pfm internal` verb its hooks
// exec.
const (
	IndicatorPluginID = "professor.indicators"
	IndicatorSource   = "pfm-indicators"
	IndicatorVerb     = "herdr-indicators"
)

// IndicatorEvents lists the Herdr events whose hooks repaint the indicators;
// the plugin's startup hook paints once more when Herdr's server starts.
func IndicatorEvents() []string {
	return []string{
		"pane.agent_status_changed",
		"pane.agent_detected",
		"pane.created",
		"tab.created",
		"pane.focused",
		"pane.closed",
		"pane.exited",
		"pane.moved",
		"tab.focused",
		"tab.moved",
		"tab.closed",
		"workspace.created",
		"workspace.focused",
	}
}

// The blink: one half-second per phase, so a blocked token is on for 500 ms and
// off for 500 ms (1 Hz). The phase is a function of the wall clock alone, so
// concurrent painters agree without talking to each other. A blinker that
// meets maxPaintFailures failed repaints in a row gives up.
const (
	blinkHalfPeriod  = 500 * time.Millisecond
	maxPaintFailures = 3
	// maxSettleRounds bounds the extra list-and-paint rounds a run makes
	// after paints that wrote with nothing blocked.
	maxSettleRounds = 3
	// unprovedReprove is how long a paint skips a pane whose ownership proof
	// failed while its agent_status is unchanged, so a foreign pane costs one
	// proof per interval, never one per half-second tick.
	unprovedReprove = 5 * time.Second
)

// indicatorPainter is one PaintIndicators run: the Herdr socket it paints, the
// lock directory that keeps one blinker per socket, and the seams the socket
// exchange and the time sit behind.
// PaneProof proves a durable owner immediately before a pane-addressed write.
type PaneProof func(context.Context, string, string) error

var errIndicatorOwnership = errors.New("pane ownership unproved")

type indicatorPainter struct {
	provePane PaneProof
	socket    string
	lockDir   string
	stderr    io.Writer
	call      func(ctx context.Context, socket string, lines ...[]byte) ([]json.RawMessage, error)
	clock     clock.Clock
	// seq numbers the request ids; reported holds the unexpected agent_status
	// strings already named on stderr, so a blinking pane names one once.
	seq      uint64
	reported map[string]bool
	// hasDone keeps the blinker reconciling unseen completions: marking an
	// already-focused tab seen need not emit any focus or agent-state event.
	hasDone         bool
	tabs            TabProvider
	hasTabs         bool
	tabObservations []TabDecoration
	// unproved holds each pane whose ownership proof failed: the status it
	// failed under and when. Such a pane is named once while it stays
	// unproved, and paint re-proves it only once its status changes or
	// unprovedReprove passed; a proof that succeeds forgets it.
	unproved map[string]unprovedPane
}

type unprovedPane struct {
	status string
	at     time.Time
}

// PaintIndicators repaints every pane's and workspace's indicator token on the
// Herdr server at socket from its agent_status, and keeps reconciling while
// any is blocked or done; lockDir holds the one-blinker-per-socket lock and the socket's
// record, whose removal (ClearIndicators) stops the blinker; errors are
// written to stderr with their context and returned.
func PaintIndicators(ctx context.Context, socket, lockDir string, stderr io.Writer, proof PaneProof) error {
	if stderr == nil {
		stderr = io.Discard
	}
	painter := &indicatorPainter{
		socket: socket, lockDir: lockDir, stderr: stderr, call: Call, clock: clock.Real,
		reported: map[string]bool{}, provePane: proof,
	}
	return painter.run(ctx)
}

func (painter *indicatorPainter) logf(format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	_, _ = fmt.Fprintln(painter.stderr, message)
	if painter.lockDir != "" {
		if err := AppendError(painter.lockDir, "indicators", painter.clock.Now(), message); err != nil {
			_, _ = fmt.Fprintf(painter.stderr, "persist indicator diagnostic: %v\n", err)
		}
	}
}

func (painter *indicatorPainter) nextSeq() uint64 {
	painter.seq++
	return painter.seq
}

// run records the socket, then paints; a paint that wrote is followed by
// another list and paint, up to maxSettleRounds, so a write that crossed a
// newer state change is repainted from it. When anything is blocked or done
// it takes the socket's blinker lock and repaints on the half-second until
// neither remains, or returns at once when another process holds the lock. After
// the lock is released it lists once more and takes it again if a blocked
// or done agent appeared meanwhile.
func (painter *indicatorPainter) run(ctx context.Context) error {
	if painter.socket == "" {
		err := fmt.Errorf("herdr indicators: no Herdr socket: %s is not set", EnvSocket)
		painter.logf("%v", err)
		return err
	}
	if painter.lockDir == "" {
		err := fmt.Errorf("herdr indicators: no lock directory for the blinker of socket %s", painter.socket)
		painter.logf("%v", err)
		return err
	}
	if err := painter.record(); err != nil {
		painter.logf("%v", err)
		return err
	}
	if painter.tabs != nil {
		release, held, err := painter.takeBlinkerLock()
		if err != nil {
			painter.logf("%v", err)
			return err
		}
		if !held {
			return nil
		}
		defer release()
		blocked, _, err := painter.paint(ctx)
		if err != nil {
			painter.logf("%v", err)
			return err
		}
		if !blocked && !painter.hasDone && !painter.hasTabs {
			return nil
		}
		_, err = painter.blink(ctx)
		if ctx.Err() != nil {
			if restoreErr := painter.restoreTabs(context.WithoutCancel(ctx)); restoreErr != nil {
				painter.logf("%v", restoreErr)
				return errors.Join(err, restoreErr)
			}
		}
		return err
	}
	settles := 0
	for {
		blocked, wrote, err := painter.paint(ctx)
		if err != nil {
			painter.logf("%v", err)
			return err
		}
		if !blocked && !painter.hasDone && !painter.hasTabs {
			if !wrote || settles >= maxSettleRounds {
				return nil
			}
			settles++
			continue
		}
		release, held, err := painter.takeBlinkerLock()
		if err != nil {
			painter.logf("%v", err)
			return err
		}
		if !held {
			return nil
		}
		stopped, err := painter.blink(ctx)
		release()
		if err != nil {
			return err
		}
		if stopped || ctx.Err() != nil {
			return nil
		}
	}
}

// blink repaints on every half-second boundary until nothing is blocked or done. A
// cancelled context ends it cleanly; maxPaintFailures failed repaints in a row
// end it with the last error. A tick that finds the socket's record gone
// (ClearIndicators removed it: pfm's Herdr theme is off) clears the
// indicators and stops the blinker, stopped true. Every failure is written to
// stderr as it happens.
func (painter *indicatorPainter) blink(ctx context.Context) (stopped bool, err error) {
	failures := 0
	for {
		wait := blinkHalfPeriod - time.Duration(painter.clock.Now().UnixNano())%blinkHalfPeriod
		if err := painter.clock.Sleep(ctx, wait); err != nil {
			return false, nil // Sleep ends early only when ctx is done
		}
		if !painter.recorded() {
			if err := painter.clear(ctx); err != nil {
				painter.logf("%v", err)
				return true, err
			}
			return true, nil
		}
		blocked, _, err := painter.paint(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return false, nil
			}
			painter.logf("%v", err)
			failures++
			if failures >= maxPaintFailures {
				painter.logf("herdr indicators: blinker on socket %s gave up after %d failed repaints in a row",
					painter.socket, failures)
				return false, fmt.Errorf(
					"herdr indicators: blinker on socket %s gave up after %d failed repaints in a row: %w",
					painter.socket,
					failures,
					err,
				)
			}
			continue
		}
		failures = 0
		if !blocked && !painter.hasDone && !painter.hasTabs {
			return false, nil
		}
	}
}

// paint lists every pane and workspace once and writes the indicator tokens
// that differ from what the blink phase of the clock wants, one request per
// changed target. blocked says whether anything listed is blocked, wrote
// whether Herdr accepted any write. hasDone tracks unseen completions in the
// latest list. A target that vanished between list and report is logged and
// skipped; every other refusal or failure ends the paint with its error.
func (painter *indicatorPainter) paint(ctx context.Context) (blocked, wrote bool, err error) {
	targets, err := painter.list(ctx)
	if err != nil {
		return false, false, err
	}
	blinkOn := painter.clock.Now().UnixMilli()/blinkHalfPeriod.Milliseconds()%2 == 0
	painter.hasDone = false
	for _, target := range targets {
		blocked = blocked || target.status == string(StateBlocked)
		painter.hasDone = painter.hasDone || target.status == "done"
		want, known := wantedIndicators(target.status, blinkOn)
		if !known && !painter.reported[target.status] {
			painter.reported[target.status] = true
			painter.logf("herdr indicators: socket %s: %s %s has agent_status %q, painted as unknown",
				painter.socket, target.kind, target.id, target.status)
		}
		patch := indicatorPatch(target.tokens, want)
		if patch != nil && painter.skipUnproved(target) {
			continue
		}
		accepted, err := painter.report(ctx, target, patch)
		if errors.Is(err, errIndicatorOwnership) {
			continue
		}
		if err != nil {
			return blocked, wrote, err
		}
		wrote = wrote || accepted
	}
	if painter.tabs != nil {
		observations, err := painter.tabs(ctx, painter.socket)
		if err != nil {
			return blocked, wrote, fmt.Errorf("herdr tab status: %w", err)
		}
		accepted, err := painter.paintTabs(ctx, observations, blinkOn)
		wrote = wrote || accepted
		if err != nil {
			return blocked, wrote, err
		}
	}
	return blocked, wrote, nil
}

// paintTabs reconciles every observed tab's status label. One tab's failure
// never stops the others: an unproved owner or a foreign label leaves that tab
// untouched, named once while it lasts; any other failure is named and joined
// into the returned error after every other tab is painted.
func (painter *indicatorPainter) paintTabs(
	ctx context.Context,
	observations []TabDecoration,
	blinkOn bool,
) (wrote bool, err error) {
	painter.tabObservations = observations
	painter.hasTabs = false
	var failures []error
	for i := range observations {
		observation := observations[i]
		painter.hasTabs = painter.hasTabs || observation.Mode != TabStatusOff
		accepted, err := reconcileTab(ctx, painter.socket, painter.lockDir, observation, blinkOn, painter.call)
		wrote = wrote || accepted
		key := "tab\x00" + observation.Tab + "\x00" + observation.Identity
		switch {
		case errors.Is(err, errTabOwnership) || errors.Is(err, errTabLabelForeign):
			if !painter.reported[key] {
				painter.reported[key] = true
				painter.logf("herdr tab status: socket %s: %v (left untouched)", painter.socket, err)
			}
		case err != nil:
			painter.logf("herdr tab status: socket %s: %v (painting the other tabs)", painter.socket, err)
			failures = append(failures, err)
		default:
			delete(painter.reported, key)
		}
	}
	return wrote, errors.Join(failures...)
}

// clear lists every pane and workspace once and removes the indicator tokens
// each holds, leaving every other source's tokens alone.
func (painter *indicatorPainter) clear(ctx context.Context) error {
	var failures []error
	if err := painter.restoreTabs(ctx); err != nil {
		failures = append(failures, err)
	}
	targets, err := painter.list(ctx)
	if err != nil {
		return errors.Join(append(failures, err)...)
	}
	for _, target := range targets {
		if _, err := painter.report(ctx, target, indicatorPatch(target.tokens, nil)); err != nil {
			if !errors.Is(err, errIndicatorOwnership) {
				return errors.Join(append(failures, err)...)
			}
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// list asks the socket for its panes and workspaces in one exchange.
func (painter *indicatorPainter) list(ctx context.Context) ([]indicatorTarget, error) {
	paneLine, err := PaneList{Seq: painter.nextSeq()}.Line()
	if err != nil {
		return nil, fmt.Errorf("herdr indicators: render pane.list: %w", err)
	}
	workspaceLine, err := WorkspaceList{Seq: painter.nextSeq()}.Line()
	if err != nil {
		return nil, fmt.Errorf("herdr indicators: render workspace.list: %w", err)
	}
	results, err := painter.call(ctx, painter.socket, paneLine, workspaceLine)
	if err != nil {
		return nil, fmt.Errorf("herdr indicators: list panes and workspaces: %w", err)
	}
	if len(results) != 2 {
		return nil, fmt.Errorf(
			"herdr indicators: socket %s answered %d results to 2 list requests",
			painter.socket,
			len(results),
		)
	}
	targets, err := decodeIndicatorTargets(results[0], results[1])
	if err != nil {
		return nil, fmt.Errorf("herdr indicators: socket %s: %w", painter.socket, err)
	}
	return targets, nil
}

// report writes patch to target; a nil patch writes nothing. accepted says
// whether Herdr took it: only the matching resource-not-found refusal (the
// target closed since the list) is logged and skipped. Other refusals fail.
func (painter *indicatorPainter) report(
	ctx context.Context,
	target indicatorTarget,
	patch map[string]*string,
) (accepted bool, err error) {
	if patch == nil {
		return false, nil
	}
	if target.kind == targetPane {
		var err error
		if painter.provePane == nil {
			err = fmt.Errorf("ownership unproved: no pane proof configured")
		} else {
			err = painter.provePane(ctx, painter.socket, target.id)
		}
		if err != nil {
			if _, named := painter.unproved[target.id]; !named {
				painter.logf(
					"herdr indicators: socket %s: pane %s ownership unproved: %v",
					painter.socket,
					target.id,
					err,
				)
			}
			if painter.unproved == nil {
				painter.unproved = map[string]unprovedPane{}
			}
			painter.unproved[target.id] = unprovedPane{status: target.status, at: painter.clock.Now()}
			return false, fmt.Errorf("%w: pane %s: %w", errIndicatorOwnership, target.id, err)
		}
		delete(painter.unproved, target.id)
	}
	line, err := tokenReport{target: target, tokens: patch, seq: painter.nextSeq()}.Line()
	if err != nil {
		return false, fmt.Errorf("herdr indicators: render the report for %s %s: %w", target.kind, target.id, err)
	}
	if _, err := painter.call(ctx, painter.socket, line); err != nil {
		var refusal *Refusal
		if errors.As(err, &refusal) && (target.kind == targetPane && refusal.Code == "pane_not_found" ||
			target.kind == targetWorkspace && refusal.Code == "workspace_not_found") {
			painter.logf("herdr indicators: socket %s: %s %s: %v", painter.socket, target.kind, target.id, err)
			return false, nil
		}
		return false, fmt.Errorf("herdr indicators: paint %s %s: %w", target.kind, target.id, err)
	}
	return true, nil
}

// skipUnproved says whether paint leaves a pane alone without proving it
// again: its last proof failed under the same agent_status less than
// unprovedReprove ago. Skipping writes nothing, so it can only fail closed.
func (painter *indicatorPainter) skipUnproved(target indicatorTarget) bool {
	if target.kind != targetPane {
		return false
	}
	last, ok := painter.unproved[target.id]
	return ok && last.status == target.status && painter.clock.Now().Sub(last.at) < unprovedReprove
}

// socketKey names a socket's files in the lock directory: the first 8 hex of
// its path's sha256.
func socketKey(socket string) string {
	sum := sha256.Sum256([]byte(socket))
	return hex.EncodeToString(sum[:])[:8]
}

// recordPath is the socket's record: a file in the lock directory holding the
// socket path, through which ClearIndicators finds every Herdr server pfm
// painted.
func (painter *indicatorPainter) recordPath() string {
	return filepath.Join(painter.lockDir, "indicators-"+socketKey(painter.socket)+".socket")
}

// record writes the socket's record unless it already holds the socket,
// atomically, so a reader never sees it half-written.
func (painter *indicatorPainter) record() error {
	path := painter.recordPath()
	held, err := os.ReadFile(path)
	switch {
	case err == nil && string(held) == painter.socket:
		return nil
	case err != nil && !errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("herdr indicators: read socket record %s: %w", path, err)
	}
	if err := atomicfile.Write(path, []byte(painter.socket), 0o600); err != nil {
		return fmt.Errorf("herdr indicators: write socket record %s: %w", path, err)
	}
	return nil
}

// recorded says whether the socket's record is still there. A stat failure
// other than absence is written to stderr and counts as recorded, so a
// transient error never stops a blinker.
func (painter *indicatorPainter) recorded() bool {
	path := painter.recordPath()
	_, err := os.Stat(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		painter.logf("herdr indicators: stat socket record %s: %v", path, err)
		return true
	}
	return err == nil
}

// ClearIndicators removes the indicator tokens from every pane and workspace
// of every Herdr server a painter recorded in lockDir: the step pfm theme off
// takes once the plugin is unlinked, since a token carries no ttl and would
// stay until its pane closes. A socket cleared, and a socket nobody listens
// on (its server is gone, its tokens with it), has its record removed, which
// also stops that socket's running blinker at its next tick. A failure on one
// socket leaves its record, is written to stderr, and the rest are still
// cleared; the failures come back joined.
func ClearIndicators(ctx context.Context, lockDir string, stderr io.Writer, proof PaneProof) error {
	return clearRecordedIndicators(ctx, lockDir, stderr, Call, proof)
}

func clearRecordedIndicators(
	ctx context.Context,
	lockDir string,
	stderr io.Writer,
	call func(ctx context.Context, socket string, lines ...[]byte) ([]json.RawMessage, error),
	proof PaneProof,
) error {
	if stderr == nil {
		stderr = io.Discard
	}
	entries, err := os.ReadDir(lockDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		err = fmt.Errorf("herdr indicators: read lock directory %s: %w", lockDir, err)
		_, _ = fmt.Fprintln(stderr, err)
		return err
	}
	var failures []error
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "indicators-") || !strings.HasSuffix(name, ".socket") {
			continue
		}
		path := filepath.Join(lockDir, name)
		socket, err := os.ReadFile(path)
		if err != nil {
			failures = append(failures, fmt.Errorf("herdr indicators: read socket record %s: %w", path, err))
			continue
		}
		painter := &indicatorPainter{
			socket: string(socket), lockDir: lockDir, stderr: stderr, call: call, clock: clock.Real,
			reported: map[string]bool{}, provePane: proof,
		}
		if err := painter.clear(ctx); err != nil && (errors.Is(err, errIndicatorOwnership) ||
			!errors.Is(err, syscall.ENOENT) && !errors.Is(err, syscall.ECONNREFUSED)) {
			failures = append(failures, fmt.Errorf("herdr indicators: clear socket %s: %w", socket, err))
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			failures = append(failures, fmt.Errorf("herdr indicators: remove socket record %s: %w", path, err))
		}
	}
	for _, failure := range failures {
		_, _ = fmt.Fprintln(stderr, failure)
	}
	return errors.Join(failures...)
}

// takeBlinkerLock takes the non-blocking flock that makes one process the
// blinker of the painter's socket: held is false when another process holds
// it. release unlocks it.
func (painter *indicatorPainter) takeBlinkerLock() (release func(), held bool, err error) {
	if err := os.MkdirAll(painter.lockDir, 0o700); err != nil {
		return nil, false, fmt.Errorf("herdr indicators: create lock directory %s: %w", painter.lockDir, err)
	}
	path := filepath.Join(painter.lockDir, "indicators-"+socketKey(painter.socket)+".lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, fmt.Errorf("herdr indicators: open blinker lock %s: %w", path, err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		closeErr := file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, false, nil
		}
		return nil, false, errors.Join(fmt.Errorf("herdr indicators: lock %s: %w", path, err), closeErr)
	}
	return func() {
		if err := syscall.Flock(int(file.Fd()), syscall.LOCK_UN); err != nil {
			painter.logf("herdr indicators: unlock %s: %v", path, err)
		}
		if err := file.Close(); err != nil {
			painter.logf("herdr indicators: close %s: %v", path, err)
		}
	}, true, nil
}
