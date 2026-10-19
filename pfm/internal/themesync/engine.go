package themesync

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/clock"
)

const (
	surfaceTmux     = "tmux"
	surfaceHerdr    = "herdr"
	surfaceVscode   = "vscode"
	surfaceFollower = "follower"
)

// flipClock is the time seam of the latency measurement: read once after each
// surface's apply.
var flipClock clock.Clock = clock.Real

// flipOrigin is where a flip is measured from: the file whose mtime the user's
// change left, and when.
type flipOrigin struct {
	path string
	at   time.Time
}

// session is one locked run over the state: the environment, the state being
// edited, the source row, the tmux seam and the latency being measured.
type session struct {
	env     Env
	store   stateStore
	state   themeState
	servers TmuxServers
	// source is the claude row: the governing account and its theme, or the
	// error that stopped the run before any surface was written.
	source      SurfaceResult
	serverCount int
	origin      *flipOrigin
	offsets     map[string]float64
	applied     bool
}

// surfaceOps is one surface's three verbs, in the matrix's row order.
type surfaceOps struct {
	name                 string
	apply, read, restore func(context.Context) SurfaceResult
}

func (s *session) surfaces() []surfaceOps {
	return []surfaceOps{
		{surfaceClaude, s.sourceRow, s.sourceRow, s.restoreSource},
		{surfaceTmux, s.applyTmux, s.readTmux, s.restoreTmux},
		{surfaceHerdr, s.applyHerdr, s.readHerdr, s.restoreHerdr},
		{surfaceVscode, s.applyVscode, s.readVscode, s.restoreVscode},
		{surfaceFollower, s.applyFollower, s.readFollower, s.restoreFollower},
	}
}

// theme is the derived theme in force; the zero Applied before any apply.
func (s *session) theme() Applied {
	if s.state.Applied == nil {
		return Applied{}
	}
	return *s.state.Applied
}

func (s *session) sourceRow(context.Context) SurfaceResult { return s.source }

// restoreSource is the claude row of an off: Claude's files are only read.
func (s *session) restoreSource(context.Context) SurfaceResult {
	return s.result(surfaceClaude, VerdictSource, "", "never written")
}

func (s *session) in(surface string, verdict Verdict, path, detail string, generation int) SurfaceResult {
	return SurfaceResult{Surface: surface, Verdict: verdict, Generation: generation, Path: path, Detail: detail}
}

func (s *session) result(surface string, verdict Verdict, path, detail string) SurfaceResult {
	return s.in(surface, verdict, path, detail, 0)
}

func (s *session) absent(surface, path, detail string) SurfaceResult {
	return s.result(surface, VerdictAbsent, path, detail)
}

func (s *session) failed(surface, path string, err error) SurfaceResult {
	return s.result(surface, VerdictError, path, err.Error())
}

func (s *session) persist() error { return s.store.save(s.state) }

func (s *session) setDue(surface string, due bool) {
	if !due {
		delete(s.state.Due, surface)
		return
	}
	if s.state.Due == nil {
		s.state.Due = map[string]bool{}
	}
	s.state.Due[surface] = true
}

// reloadDue says whether a surface needs its live reload now: it changed, or
// an earlier reload of it failed.
func (s *session) reloadDue(surface string, changed bool) bool {
	return changed || s.state.Due[surface]
}

// settle records the outcome of a live reload: a failure stays due until a
// later run's reload succeeds.
func (s *session) settle(surface, path, note string, err error) SurfaceResult {
	s.setDue(surface, err != nil)
	if err != nil {
		return s.failed(surface, path, err)
	}
	return s.result(surface, VerdictApplied, path, note)
}

// danglingTarget follows a chain of symlinks whose last target does not exist
// yet to that target; a path that is no link comes back unchanged.
func danglingTarget(path string) (string, error) {
	for hops := 0; hops < 40; hops++ {
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			return path, nil
		}
		if err != nil {
			return "", fmt.Errorf("themesync: resolve %s: %w", path, err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			return path, nil
		}
		target, err := os.Readlink(path)
		if err != nil {
			return "", fmt.Errorf("themesync: resolve %s: %w", path, err)
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(path), target)
		}
		path = target
	}
	return "", fmt.Errorf("themesync: resolve %s: too many links", path)
}

// removeFile deletes a user file the sync created; a symlinked one (a dotfiles
// link that dangled before set wrote its target) is deleted at the file it
// resolves to, so the link survives as the user had it.
func (s *session) removeFile(path string) error {
	resolved, err := filepath.EvalSymlinks(path)
	switch {
	case err == nil:
		path = resolved
	case !errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("themesync: resolve %s: %w", path, err)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("themesync: remove %s: %w", path, err)
	}
	return nil
}

// replaceFile writes a user file atomically, keeping its mode; a symlinked
// file (a dotfiles checkout) is written at the file it resolves to, so the
// link survives the rename; a dangling link is written at its target.
func (s *session) replaceFile(path string, content []byte) error {
	resolved, err := filepath.EvalSymlinks(path)
	switch {
	case err == nil:
		path = resolved
	case !errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("themesync: resolve %s: %w", path, err)
	default:
		if path, err = danglingTarget(path); err != nil {
			return err
		}
	}
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	if err := atomicfile.Write(path, content, mode); err != nil {
		return fmt.Errorf("themesync: write %s: %w", path, err)
	}
	return nil
}

// backupBeside copies a user file next to itself before the sync first edits it.
func (s *session) backupBeside(path string, content []byte) (string, error) {
	backup := path + ".pfm-theme-" + wallClock.Now().Format(backupStampLayout)
	if err := atomicfile.Write(backup, content, 0o600); err != nil {
		return "", fmt.Errorf("themesync: back up %s: %w", path, err)
	}
	return backup, nil
}

// readOne reads one surface back; a live reload that failed earlier keeps an
// otherwise matching surface from reading in sync.
func (s *session) readOne(ctx context.Context, ops surfaceOps) SurfaceResult {
	read := ops.read(ctx)
	if read.Verdict == VerdictInSync && s.state.Due[ops.name] {
		read.Verdict = VerdictError
		read.Detail = strings.TrimSpace("live reload failed earlier; the next sync retries it. " + read.Detail)
	}
	return read
}

func (s *session) readAll(ctx context.Context) []SurfaceResult {
	var rows []SurfaceResult
	for _, ops := range s.surfaces() {
		rows = append(rows, s.readOne(ctx, ops))
	}
	return rows
}

// applyAll applies every surface then reads it back; a surface whose live
// reload is deferred reads APPLIED with the note, never a bare IN-SYNC.
func (s *session) applyAll(ctx context.Context) []SurfaceResult {
	var rows []SurfaceResult
	for _, ops := range s.surfaces() {
		rows = append(rows, s.applyOne(ctx, ops))
	}
	s.applied = true
	return rows
}

// applyDrifted re-applies only the surfaces whose read-back row is DRIFT or
// ERROR, keeping every other row as read: a surface out of sync costs its own
// apply, never every surface's (each chat server's options) once a minute.
func (s *session) applyDrifted(ctx context.Context, rows []SurfaceResult) []SurfaceResult {
	out := make([]SurfaceResult, 0, len(rows))
	for index, ops := range s.surfaces() {
		if row := rows[index]; row.Verdict != VerdictDrift && row.Verdict != VerdictError {
			out = append(out, row)
			continue
		}
		out = append(out, s.applyOne(ctx, ops))
		s.applied = true
	}
	return out
}

func (s *session) applyOne(ctx context.Context, ops surfaceOps) SurfaceResult {
	applied := ops.apply(ctx)
	if applied.Verdict != VerdictApplied {
		return applied
	}
	s.stamp(ops.name)
	read := s.readOne(ctx, ops)
	if read.Verdict == VerdictInSync && applied.Detail != "" {
		read.Verdict, read.Detail = VerdictApplied, applied.Detail
	}
	return read
}

// stamp records how long after the flip's origin a surface finished applying.
func (s *session) stamp(surface string) {
	if s.origin == nil || (surface != surfaceTmux && surface != surfaceHerdr && surface != surfaceVscode) {
		return
	}
	if s.offsets == nil {
		s.offsets = map[string]float64{}
	}
	s.offsets[surface] = math.Round(flipClock.Now().Sub(s.origin.at).Seconds()*100) / 100
}

// recordFlip keeps the offsets this run measured as the last flip.
func (s *session) recordFlip() {
	if s.origin == nil || len(s.offsets) == 0 {
		return
	}
	s.state.LastFlip = &Flip{
		At: s.origin.at.UTC().Format(time.RFC3339Nano), Source: s.origin.path,
		Tmux: s.offsets[surfaceTmux], Herdr: s.offsets[surfaceHerdr], VSCode: s.offsets[surfaceVscode],
	}
}

func (s *session) restoreAll(ctx context.Context) []SurfaceResult {
	var rows []SurfaceResult
	for _, ops := range s.surfaces() {
		rows = append(rows, ops.restore(ctx))
	}
	return rows
}

func (s *session) report(rows []SurfaceResult) Report {
	return Report{
		Enabled: s.state.Enabled, Source: s.state.Source, Appearance: s.state.Appearance,
		Generation: s.state.Generation, LastFlip: s.state.LastFlip, Changed: s.applied,
		Servers: s.serverCount, Surfaces: rows,
	}
}

// errInert marks a run with no state file: nothing was ever turned on, so nothing is touched.
var errInert = errors.New("themesync: no theme sync on")

// beginActive is begin for a verb that needs a prior On; with no state file
// it returns errInert before the state directory is created or locked.
func beginActive(env Env) (stateStore, func() error, error) {
	dir := env.withStateDir().StateDir
	if _, err := os.Stat(filepath.Join(dir, StateFileName)); errors.Is(err, os.ErrNotExist) {
		return stateStore{}, nil, errInert
	}
	return begin(env)
}

// begin validates the environment, takes the lock and loads the state.
func begin(env Env) (stateStore, func() error, error) {
	if env.Runner == nil {
		return stateStore{}, nil, errors.New("themesync: Env.Runner is nil")
	}
	store := stateStore{dir: env.withStateDir().StateDir}
	release, err := store.lock()
	return store, release, err
}

func (env Env) withStateDir() Env {
	if env.StateDir == "" {
		env.StateDir = DefaultStateDir(env.Home)
	}
	return env
}

func newSession(env Env, store stateStore, state themeState) *session {
	return &session{env: env.withStateDir(), store: store, state: state, servers: env.Tmux}
}

// sourceState is the read-only source resolved for one run.
type sourceState struct {
	info    SourceInfo
	applied Applied
	memory  map[string]string
	origin  *flipOrigin
}

// shortHome writes a path under home as ~/…
func shortHome(home, path string) string {
	if rel, err := filepath.Rel(home, path); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.Join("~", rel)
	}
	return path
}

// resolveSource reads the accounts, picks the governing one (D9), derives its
// theme and lays the pins of its profile over it. Any failure names the file;
// the run then writes no surface.
func resolveSource(env Env, state themeState) (sourceState, error) {
	accounts, err := readAccounts(env.Home)
	if err != nil {
		return sourceState{}, err
	}
	previous := ""
	if state.Source != nil {
		previous = state.Source.Path
	}
	governing, memory := govern(state.Accounts, previous, accounts)
	theme, err := resolveValue(governing.Value, governing.dir())
	if err != nil {
		return sourceState{}, fmt.Errorf("%s: %w", governing.Path, err)
	}
	profile, err := profilePins(env.Home, governing.Value)
	if err != nil {
		return sourceState{}, fmt.Errorf("%s: %w", governing.Path, err)
	}
	theme.Applied.Pins = profile.pins
	origin := &flipOrigin{path: governing.Path, at: governing.ModTime}
	if theme.ThemeFile != "" && theme.ThemeTime.After(origin.at) {
		origin = &flipOrigin{path: theme.ThemeFile, at: theme.ThemeTime}
	}
	// A pin change is a source change as much as a palette change: the newest
	// of the three files is the one whose edit the surfaces are waiting on.
	if profile.file != "" && profile.at.After(origin.at) {
		origin = &flipOrigin{path: profile.file, at: profile.at}
	}
	return sourceState{
		info: SourceInfo{
			Account: shortHome(env.Home, governing.dir()), Path: governing.Path,
			Value: theme.Applied.Value, Title: theme.Applied.Title, Side: theme.Applied.Side,
		},
		applied: theme.Applied, memory: memory, origin: origin,
	}, nil
}

// sourceResult is the claude row of a resolved source, or of its failure.
func sourceResult(info *SourceInfo, err error) SurfaceResult {
	if err != nil {
		return SurfaceResult{Surface: surfaceClaude, Verdict: VerdictError, Detail: err.Error()}
	}
	return SurfaceResult{
		Surface: surfaceClaude, Verdict: VerdictSource, Path: info.Path,
		Detail: info.Account + " · " + info.Value + " · " + info.Title,
	}
}

// appearanceFor is the side the single-variant surfaces draw: the system's for
// an auto theme, the theme's own otherwise.
func appearanceFor(ctx context.Context, env Env, applied Applied) (Appearance, error) {
	if !applied.Auto {
		return applied.Side, nil
	}
	return DetectAppearance(ctx, env.Runner)
}

type convergeMode int

const (
	convergeSync convergeMode = iota
	convergeForce
	convergeOn
)

// globalPreferences is the macOS preferences file whose rewrite flips the system appearance.
func globalPreferences(home string) string {
	return filepath.Join(home, "Library", "Preferences", ".GlobalPreferences.plist")
}

// converge resolves the source and brings every surface to it: it applies all
// of them when the theme or the appearance changed (a new generation), only
// the drifted ones otherwise, and writes nothing when everything reads back.
func converge(ctx context.Context, env Env, store stateStore, state themeState, mode convergeMode) (Report, error) {
	env = env.withStateDir()
	source, srcErr := resolveSource(env, state)
	sess := newSession(env, store, state)
	if srcErr != nil {
		sess.source = sourceResult(nil, srcErr)
		return sess.report([]SurfaceResult{sess.source}), nil
	}
	appearance, err := appearanceFor(ctx, env, source.applied)
	if err != nil {
		return Report{}, err
	}
	known := state.Enabled && state.Applied != nil
	sourceMoved := !known || !reflect.DeepEqual(*state.Applied, source.applied)
	changed := sourceMoved || state.Appearance != appearance
	sess.state.Enabled, sess.state.Source, sess.state.Accounts = true, &source.info, source.memory
	sess.source = sourceResult(&source.info, nil)
	if changed {
		sess.state.Generation++
		sess.state.Appearance, sess.state.Applied = appearance, &source.applied
		sess.origin = source.origin
		switch {
		case !known:
			// A first apply (or the first over an old-shaped state) replaces
			// nothing the surfaces showed: it is no flip, and the source's
			// mtime, however old, is no latency.
			sess.origin = nil
		case !sourceMoved:
			sess.origin = nil
			if info, err := os.Stat(globalPreferences(env.Home)); err == nil {
				sess.origin = &flipOrigin{path: globalPreferences(env.Home), at: info.ModTime()}
			}
		}
	}
	if changed || mode == convergeOn || mode == convergeForce {
		if err := sess.persist(); err != nil {
			return Report{}, err
		}
		rows := sess.applyAll(ctx)
		sess.recordFlip()
		return sess.report(rows), sess.persist()
	}
	rows := sess.readAll(ctx)
	if (Report{Surfaces: rows}).OK() {
		return sess.report(rows), sess.persist() // nothing changed and nothing drifted: persist only what moved
	}
	rows = sess.applyDrifted(ctx, rows)
	return sess.report(rows), sess.persist()
}

// On turns the sync on: it reads the source, writes the state, installs the
// follower and applies every present surface. The first On snapshots every
// user file it edits.
func On(ctx context.Context, env Env) (report Report, err error) {
	store, release, err := begin(env)
	if err != nil {
		return Report{}, err
	}
	defer func() { err = errors.Join(err, release()) }()
	state, _, err := store.load()
	if err != nil {
		return Report{}, err
	}
	return converge(ctx, env, store, state, convergeOn)
}

// Sync follows the source now: a changed theme or appearance (or force)
// re-applies every surface; with nothing changed it writes nothing and
// reports the read-back. A sync that was never turned on is inert.
func Sync(ctx context.Context, env Env, force bool) (report Report, err error) {
	store, release, err := beginActive(env)
	if errors.Is(err, errInert) {
		return Report{}, nil
	}
	if err != nil {
		return Report{}, err
	}
	defer func() { err = errors.Join(err, release()) }()
	state, _, err := store.load()
	if err != nil || !state.Enabled {
		return Report{Generation: state.Generation}, err
	}
	mode := convergeSync
	if force {
		mode = convergeForce
	}
	return converge(ctx, env, store, state, mode)
}

// ReadBack reads every surface back against the state's generation; a source
// that moved since the last sync is drawn as drift without being applied.
func ReadBack(ctx context.Context, env Env) (Report, error) {
	if env.Runner == nil {
		return Report{}, errors.New("themesync: Env.Runner is nil")
	}
	env = env.withStateDir()
	store := stateStore{dir: env.StateDir}
	state, found, err := store.load()
	if err != nil || !found || !state.Enabled {
		return Report{Generation: state.Generation}, err
	}
	sess := newSession(env, store, state)
	source, srcErr := resolveSource(env, state)
	if srcErr != nil {
		sess.source = sourceResult(nil, srcErr)
	} else {
		sess.source = sourceResult(&source.info, nil)
		sess.state.Source = &source.info
		appearance, err := appearanceFor(ctx, env, source.applied)
		if err != nil {
			return Report{}, err
		}
		if state.Applied == nil || !reflect.DeepEqual(*state.Applied, source.applied) ||
			state.Appearance != appearance {
			sess.state.Generation++
			sess.state.Appearance, sess.state.Applied = appearance, &source.applied
		}
	}
	report := sess.report(sess.readAll(ctx))
	report.LastFlip = sess.flipWithAck()
	return report, nil
}

// flipWithAck is the stored last flip plus the extension's ack offset when it
// has acknowledged the generation in force.
func (s *session) flipWithAck() *Flip {
	if s.state.LastFlip == nil {
		return nil
	}
	flip := *s.state.LastFlip
	ack, haveAck, err := s.readAck()
	if err != nil || !haveAck || ack.Error != "" || ack.Generation != s.state.Generation {
		return &flip
	}
	appliedAt, appliedErr := time.Parse(time.RFC3339Nano, ack.AppliedAt)
	origin, originErr := time.Parse(time.RFC3339Nano, flip.At)
	if appliedErr == nil && originErr == nil {
		offset := math.Round(appliedAt.Sub(origin).Seconds()*100) / 100
		flip.Ack = &offset
	}
	return &flip
}

// Off restores every surface from its snapshot and disables the sync.
func Off(ctx context.Context, env Env) (report Report, err error) {
	store, release, err := beginActive(env)
	if errors.Is(err, errInert) {
		pending, readErr := VSCodePendingRestores(env.withStateDir().StateDir)
		if readErr != nil {
			return Report{}, readErr
		}
		if len(pending) == 0 {
			return Report{}, nil
		}
		store, release, err = begin(env)
	}
	if err != nil {
		return Report{}, err
	}
	defer func() { err = errors.Join(err, release()) }()
	state, _, err := store.load()
	if err != nil {
		return Report{}, err
	}
	if state.Enabled {
		state.Generation++
	}
	state.Enabled = false
	sess := newSession(env, store, state)
	if err := sess.persist(); err != nil {
		return Report{}, err
	}
	rows := sess.restoreAll(ctx)
	sess.state.Due, sess.state.OffPending = nil, false
	sess.state.Source, sess.state.Accounts, sess.state.Applied, sess.state.LastFlip = nil, nil, nil, nil
	for _, row := range rows {
		sess.state.OffPending = sess.state.OffPending || row.Verdict == VerdictError
	}
	return sess.report(rows), sess.persist()
}

// LogOnce records the line a follower run printed so the same failure on the
// next run stays silent; "" clears it (a clean run). It reports whether the
// line is new, so the caller prints only then.
func LogOnce(env Env, line string) (fresh bool, err error) {
	store, release, err := beginActive(env)
	if errors.Is(err, errInert) {
		return true, nil
	}
	if err != nil {
		return true, err
	}
	defer func() { err = errors.Join(err, release()) }()
	state, found, err := store.load()
	if err != nil || !found {
		return true, err
	}
	if state.LastLog == line {
		return false, nil
	}
	state.LastLog = line
	return true, store.save(state)
}

// ApplyTmuxServer puts the current theme on one chat server (capturing each
// option's prior value on that server first); a no-op when the sync is off or
// has not applied a theme yet. The state is read and the server themed under
// the theme lock, so an apply never lands after an Off that already restored
// the same server. It reads the stored Applied, never the Claude source.
func ApplyTmuxServer(ctx context.Context, servers TmuxServers, stateDir, socket string) (err error) {
	if !SyncOn(stateDir) {
		return nil
	}
	store := stateStore{dir: stateDir}
	release, err := store.lock()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, release()) }()
	state, found, err := store.load()
	if err != nil || !found || !state.Enabled || state.Applied == nil {
		return err
	}
	sess := &session{store: store, state: state, servers: servers}
	return sess.tmuxApplySocket(ctx, socket)
}
