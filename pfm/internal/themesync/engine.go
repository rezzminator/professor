package themesync

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// surfaceClaude is the Claude Code theme surface, named after its engine.
var surfaceClaude = pfmengine.MustLookup(pfmengine.Claude).LongName

const (
	surfaceTmux     = "tmux"
	surfaceHerdr    = "herdr"
	surfaceGhostty  = "ghostty"
	surfaceVscode   = "vscode"
	surfaceFollower = "follower"
)

// session is one locked run over the state: the environment, the state being
// edited, the palette in force and the tmux seam.
type session struct {
	env     Env
	store   stateStore
	state   themeState
	palette Palette
	servers TmuxServers
}

// surfaceOps is one surface's three verbs, in the matrix's row order.
type surfaceOps struct {
	name                 string
	apply, read, restore func(context.Context) SurfaceResult
}

func (s *session) surfaces() []surfaceOps {
	return []surfaceOps{
		{surfaceClaude, s.applyClaude, s.readClaude, s.restoreClaude},
		{surfaceTmux, s.applyTmux, s.readTmux, s.restoreTmux},
		{surfaceHerdr, s.applyHerdr, s.readHerdr, s.restoreHerdr},
		{surfaceGhostty, s.applyGhostty, s.readGhostty, s.restoreGhostty},
		{surfaceVscode, s.applyVscode, s.readVscode, s.restoreVscode},
		{surfaceFollower, s.applyFollower, s.readFollower, s.restoreFollower},
	}
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
	}
	return out
}

func (s *session) applyOne(ctx context.Context, ops surfaceOps) SurfaceResult {
	applied := ops.apply(ctx)
	if applied.Verdict != VerdictApplied {
		return applied
	}
	read := s.readOne(ctx, ops)
	if read.Verdict == VerdictInSync && applied.Detail != "" {
		read.Verdict, read.Detail = VerdictApplied, applied.Detail
	}
	return read
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
		Enabled: s.state.Enabled, Palette: s.state.Palette, Mode: s.state.Mode,
		Appearance: s.state.Appearance, Generation: s.state.Generation, Surfaces: rows,
	}
}

// errInert marks a run with no state file: nothing was ever set, so nothing is touched.
var errInert = errors.New("themesync: no theme set")

// beginActive is begin for a verb that needs a prior Set; with no state file
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
	dir := env.StateDir
	if dir == "" {
		dir = DefaultStateDir(env.Home)
	}
	store := stateStore{dir: dir}
	release, err := store.lock()
	return store, release, err
}

func (env Env) withStateDir() Env {
	if env.StateDir == "" {
		env.StateDir = DefaultStateDir(env.Home)
	}
	return env
}

func newSession(env Env, store stateStore, state themeState, palette Palette) *session {
	return &session{env: env.withStateDir(), store: store, state: state, palette: palette, servers: env.Tmux}
}

func resolveMode(mode Mode) (Mode, error) {
	switch mode {
	case "", ModeAuto:
		return ModeAuto, nil
	case ModeDark, ModeLight:
		return mode, nil
	}
	return "", fmt.Errorf("themesync: unknown mode %q (known: auto, dark, light)", mode)
}

func resolveAppearance(ctx context.Context, env Env, mode Mode) (Appearance, error) {
	switch mode {
	case ModeDark:
		return Dark, nil
	case ModeLight:
		return Light, nil
	}
	return DetectAppearance(ctx, env.Runner)
}

// Set renders the palette into every present surface, reloads each live and
// reads each back. The first Set snapshots every user file it edits.
func Set(ctx context.Context, env Env, request Request) (report Report, err error) {
	palette, err := PaletteByName(request.Palette)
	if err != nil {
		return Report{}, err
	}
	mode, err := resolveMode(request.Mode)
	if err != nil {
		return Report{}, err
	}
	for _, appearance := range []Appearance{Dark, Light} {
		if err := palette.Variant(appearance).validate(); err != nil {
			return Report{}, fmt.Errorf("themesync: palette %s %s: %w", palette.Name, appearance, err)
		}
	}
	if env.Runner == nil {
		return Report{}, errors.New("themesync: Env.Runner is nil")
	}
	appearance, err := resolveAppearance(ctx, env, mode)
	if err != nil {
		return Report{}, err
	}
	store, release, err := begin(env)
	if err != nil {
		return Report{}, err
	}
	defer func() { err = errors.Join(err, release()) }()
	state, found, err := store.load()
	if err != nil {
		return Report{}, err
	}
	if !found || !state.Enabled || state.Palette != palette.Name || state.Mode != mode ||
		state.Appearance != appearance {
		state.Generation++
	}
	state.Enabled, state.Palette, state.Mode, state.Appearance = true, palette.Name, mode, appearance
	sess := newSession(env, store, state, palette)
	if err := sess.persist(); err != nil {
		return Report{}, err
	}
	rows := sess.applyAll(ctx)
	return sess.report(rows), sess.persist()
}

// loadActive loads the state of a running sync: a missing or disabled state is
// (zero, false, nil) so every caller stays inert.
func loadActive(store stateStore) (themeState, Palette, bool, error) {
	state, found, err := store.load()
	if err != nil || !found || !state.Enabled {
		return state, Palette{}, false, err
	}
	palette, err := PaletteByName(state.Palette)
	return state, palette, err == nil, err
}

// Sync re-resolves the appearance and re-applies when it (or force) says so;
// with nothing changed it writes nothing and reports the read-back.
func Sync(ctx context.Context, env Env, force bool) (report Report, err error) {
	store, release, err := beginActive(env)
	if errors.Is(err, errInert) {
		return Report{}, nil
	}
	if err != nil {
		return Report{}, err
	}
	defer func() { err = errors.Join(err, release()) }()
	state, palette, active, err := loadActive(store)
	if err != nil || !active {
		return Report{Generation: state.Generation}, err
	}
	appearance, err := resolveAppearance(ctx, env, state.Mode)
	if err != nil {
		return Report{}, err
	}
	sess := newSession(env, store, state, palette)
	if appearance != state.Appearance {
		sess.state.Appearance = appearance
		sess.state.Generation++
		if err := sess.persist(); err != nil {
			return Report{}, err
		}
		return sess.report(sess.applyAll(ctx)), sess.persist()
	}
	rows := sess.readAll(ctx)
	if force {
		return sess.report(sess.applyAll(ctx)), sess.persist()
	}
	if (Report{Surfaces: rows}).OK() {
		return sess.report(rows), nil // nothing changed and nothing drifted: write nothing
	}
	return sess.report(sess.applyDrifted(ctx, rows)), sess.persist()
}

// ReadBack reads every surface back against the state's generation.
func ReadBack(ctx context.Context, env Env) (Report, error) {
	if env.Runner == nil {
		return Report{}, errors.New("themesync: Env.Runner is nil")
	}
	store := stateStore{dir: env.withStateDir().StateDir}
	state, palette, active, err := loadActive(store)
	if err != nil || !active {
		return Report{Generation: state.Generation}, err
	}
	sess := newSession(env, store, state, palette)
	return sess.report(sess.readAll(ctx)), nil
}

// Off restores every surface from its snapshot and disables the sync.
func Off(ctx context.Context, env Env) (report Report, err error) {
	store, release, err := beginActive(env)
	if errors.Is(err, errInert) {
		return Report{}, nil
	}
	if err != nil {
		return Report{}, err
	}
	defer func() { err = errors.Join(err, release()) }()
	state, found, err := store.load()
	if err != nil || !found {
		return Report{}, err
	}
	palette, paletteErr := PaletteByName(state.Palette)
	if paletteErr != nil {
		palette = embeddedPalettes[0] // restoring needs only option names, not colours
	}
	if state.Enabled {
		state.Generation++
	}
	state.Enabled = false
	sess := newSession(env, store, state, palette)
	if err := sess.persist(); err != nil {
		return Report{}, err
	}
	rows := sess.restoreAll(ctx)
	sess.state.Due = nil
	sess.state.OffPending = false
	for _, row := range rows {
		sess.state.OffPending = sess.state.OffPending || row.Verdict == VerdictError
	}
	return sess.report(rows), sess.persist()
}

// ApplyTmuxServer puts the current theme on one chat server (capturing each
// option's prior value on that server first); a no-op when the sync is off.
// The state is read and the server themed under the theme lock, so an apply
// never lands after an Off that already restored the same server.
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
	state, palette, active, err := loadActive(store)
	if err != nil || !active {
		return err
	}
	sess := &session{store: store, state: state, palette: palette, servers: servers}
	return sess.tmuxApplySocket(ctx, socket)
}

// HoldsClaudeTheme reports whether the sync currently owns a Claude Code
// theme file, so pfm install leaves it alone until `pfm theme off`.
func HoldsClaudeTheme(stateDir, target string) bool {
	if !SyncOn(stateDir) {
		return false
	}
	state, found, err := stateStore{dir: stateDir}.load()
	if err != nil {
		return true // an unreadable state errs toward not clobbering a held file
	}
	if !found {
		return false
	}
	resolved, resolveErr := filepath.EvalSymlinks(target)
	for held := range state.Claude {
		if filepath.Clean(held) == filepath.Clean(target) {
			return true
		}
		if heldResolved, err := filepath.EvalSymlinks(
			held,
		); resolveErr == nil && err == nil &&
			heldResolved == resolved {
			return true
		}
	}
	return false
}
