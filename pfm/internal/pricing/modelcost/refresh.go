package modelcost

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/pricing"
)

// Window is how long a fetched or confirmed table stays current.
const Window = 24 * time.Hour

// The refresh statuses.
const (
	StatusCurrent   = "current"
	StatusRefreshed = "refreshed"
	StatusFailed    = "failed"
	StatusOffline   = "offline"
)

// Options configure one refresh. ConfigPath is the pfm.config.json whose
// sibling pfm.prices.json is merged ("" for none); Offline is
// paths.EnvPricesOffline set; a nil Fetch fetches the publishers' pages;
// Binary identifies the binary that parses (nil for this executable,
// ThisBinary), asked only when a stamp is judged or written.
type Options struct {
	Home, ConfigPath string
	Force, Offline   bool
	Clock            clock.Clock
	Fetch            func(context.Context) ([]Catalog, error)
	Binary           func() (string, error)
}

// executable is the file a process was started from, read at startup: its
// path, its file info, or why neither could be read.
type executable struct {
	path string
	info os.FileInfo
	err  error
}

// startedAs is this process's executable as it was at startup, so a binary
// renamed over its path later (a host install) is never taken for this one.
var startedAs = func() executable {
	path, err := os.Executable()
	if err != nil {
		return executable{err: fmt.Errorf("this binary's identity: %w", err)}
	}
	info, err := os.Stat(path)
	if err != nil {
		return executable{path: path, err: fmt.Errorf("this binary's identity: %w", err)}
	}
	return executable{path: path, info: info}
}()

var thisBinary = sync.OnceValues(func() (string, error) { return identify(startedAs) })

// identify is the sha256 of exe's bytes, refused when the file at its path is
// no longer the one the process started from.
func identify(exe executable) (string, error) {
	if exe.err != nil {
		return "", exe.err
	}
	file, err := os.Open(exe.path)
	if err != nil {
		return "", fmt.Errorf("this binary's identity: %w", err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("this binary's identity: stat %s: %w", exe.path, err)
	}
	if !os.SameFile(exe.info, info) || info.Size() != exe.info.Size() || !info.ModTime().Equal(exe.info.ModTime()) {
		return "", fmt.Errorf("this binary's identity: %s was replaced after this process started", exe.path)
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("this binary's identity: read %s: %w", exe.path, err)
	}
	return fmt.Sprintf("exe-sha256:%x", hash.Sum(nil)), nil
}

// ThisBinary is the identity of the running executable, the sha256 of its
// bytes: two builds of one version differ, and a rebuilt binary is a new
// parser. Its error is an executable that could not be read, or one replaced
// on disk after this process started.
func ThisBinary() (string, error) {
	return thisBinary()
}

// MachineOptions are the options a command refreshes the machine's table
// with: the runtime's home and config, the offline switch, the real clock and
// the HTTP fetcher. pfm model-cost and pfm tokens both load through them.
func MachineOptions(runtime config.Runtime) Options {
	return Options{
		Home:       runtime.Paths.Home,
		ConfigPath: runtime.Config.Path,
		Offline:    paths.PricesOffline(),
		Clock:      clock.Real,
		Fetch:      NewFetcher(nil, nil).Fetch,
	}
}

// Result is what one refresh did. Reason says why an offline status was not
// the switch's doing (a running pfm update); Err is a failed fetch or derivation;
// Changed and Persisted, PersistErr and a stamp write's StampErr describe a
// refreshed table; StampErr also carries an unreadable stamp; CheckedAt is the
// check that confirmed the clone file, zero when none applies.
type Result struct {
	Status               string
	Reason               string
	Err                  error
	Changed, Persisted   bool
	PersistErr, StampErr error
	CheckedAt            time.Time
}

// Refresh brings the clone's prices.json up to date at most once per Window
// and serves the result with the override merged. Offline fetches nothing,
// --force included; a clone file this binary confirmed less than a Window ago
// is current, and so is an embedded table fetched less than a Window ago on a
// machine with no clone file (the table is this binary's own). A stamp
// another binary wrote, or one naming no binary, is stale: a refresh just
// before a host install never keeps the old parse. Otherwise both pages are
// fetched and the clone file
// rewritten only when its rows or sources changed, so an unchanged refresh
// leaves the tracked file alone and records the check in the stamp. A failed
// fetch serves the loaded table. The error is a table that cannot be served
// (an invalid override); the Result is meaningful either way.
func Refresh(ctx context.Context, options Options) (pricing.Prices, Result, error) {
	ticker := options.Clock
	if ticker == nil {
		ticker = clock.Real
	}
	published, err := pricing.LoadPublished(options.Home)
	if err != nil {
		return pricing.Prices{}, Result{}, err
	}
	stamp, stampErr := pricing.ReadStamp(options.Home)
	result := Result{StampErr: stampErr}
	confirmed := stampErr == nil && stamp.Matches(published.FileBytes)
	if confirmed {
		result.CheckedAt = stamp.At
	}
	identity := options.Binary
	if identity == nil {
		identity = ThisBinary
	}
	var binaryErr error
	binary := func() string {
		id, err := identity()
		if err != nil {
			binaryErr = err
		}
		return id
	}
	now := ticker.Now().UTC()
	served, origin := published.Table, published.Origin
	var running bool
	var lockErr error
	var refreshLock *os.File
	if !options.Offline && published.File != "" {
		refreshLock, running, lockErr = acquireRefreshOwnership(options.Home)
	}
	switch {
	case options.Offline:
		result.Status = StatusOffline
	case lockErr != nil:
		// No proof either way that an update is running: write nothing.
		result.Status, result.Err = StatusFailed, lockErr
	case running:
		// A pfm update older than this build sets no offline switch for its
		// children, and a rewrite of the clone's tracked prices.json now would
		// refuse the update's fast-forward and its rollback's reset --keep.
		result.Status = StatusOffline
		result.Reason = fmt.Sprintf(
			"a pfm update is running (it holds %s); the clone's %s is left alone",
			UpdateLockPath(options.Home), pricing.FileRel,
		)
	case !options.Force && published.File == "" && fetchedWithin(published.FetchedAt, now):
		result.Status = StatusCurrent
	case !options.Force && confirmed && within(stamp.At, now) && stamp.By(binary()):
		result.Status = StatusCurrent
	default:
		served, origin = refreshNow(ctx, options, published, binary, now, &result)
	}
	if refreshLock != nil {
		if err := refreshLock.Close(); err != nil {
			result.Status = StatusFailed
			result.Err = errors.Join(
				result.Err,
				fmt.Errorf("release price refresh ownership %s: %w", refreshLock.Name(), err),
			)
		}
	}
	switch {
	case binaryErr == nil:
	case result.StampErr == nil:
		result.StampErr = binaryErr
	default:
		result.StampErr = fmt.Errorf("%w; %w", result.StampErr, binaryErr)
	}
	prices, err := pricing.ServeTable(served, origin, options.ConfigPath)
	if err != nil {
		return pricing.Prices{}, result, err
	}
	return prices, result, nil
}

// PriceDocument is the --json document of a served table and the refresh that
// served it; model names a single-model lookup ("" for every row).
func PriceDocument(prices pricing.Prices, result Result, model string, rows []pricing.Row) pricing.Document {
	doc := pricing.Document{
		Version: prices.Version, FetchedAt: prices.FetchedAt, Sources: prices.Sources, ServedFrom: prices.From,
		File: prices.File, FileError: errorText(prices.FileError), Override: prices.Override, Model: model, Rows: rows,
		Refresh: pricing.RefreshReport{
			Status: result.Status, Reason: result.Reason, Error: errorText(result.Err),
			StampError: errorText(result.StampErr),
		},
	}
	if !result.CheckedAt.IsZero() {
		doc.CheckedAt = result.CheckedAt.UTC().Format(time.RFC3339Nano)
	}
	if result.Status == StatusRefreshed {
		changed, persisted := result.Changed, result.Persisted
		doc.Refresh.Changed, doc.Refresh.Persisted = &changed, &persisted
		doc.Refresh.PersistError = errorText(result.PersistErr)
	}
	return doc
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func fetchedWithin(fetchedAt string, now time.Time) bool {
	at, err := time.Parse(time.RFC3339, fetchedAt)
	return err == nil && within(at, now)
}

// within reports whether at is less than a Window before now; a time after
// now (a clock behind the one that wrote it) is no check.
func within(at, now time.Time) bool {
	age := now.Sub(at)
	return age >= 0 && age < Window
}

// refreshNow fetches, derives and persists; it returns the table to serve.
func refreshNow(
	ctx context.Context,
	options Options,
	published pricing.Published,
	binary func() string,
	now time.Time,
	result *Result,
) (pricing.Table, pricing.Origin) {
	fetch := options.Fetch
	if fetch == nil {
		fetch = NewFetcher(nil, options.Clock).Fetch
	}
	catalogs, err := fetch(ctx)
	var fresh pricing.Table
	if err == nil {
		fresh, err = Derive(catalogs, now.Format(time.RFC3339))
	}
	if err != nil {
		result.Status, result.Err = StatusFailed, err
		return published.Table, published.Origin
	}
	result.Status = StatusRefreshed
	compared := published.Table
	if published.FileTable != nil {
		compared = *published.FileTable
	}
	result.Changed = !pricing.SameRates(compared, fresh)
	origin := pricing.Origin{From: pricing.FromFetch, File: published.File, FileError: published.FileError}
	if published.File == "" {
		result.PersistErr = published.FileError
		if result.PersistErr == nil {
			result.PersistErr = fmt.Errorf(
				"source repository marker %s: %w",
				paths.SourceRepoPath(options.Home),
				paths.ErrNoSourceRepoMarker,
			)
		}
		return fresh, origin
	}
	content := published.FileBytes
	if published.FileTable == nil || result.Changed {
		written, err := pricing.WriteFile(published.File, fresh)
		if err != nil {
			result.PersistErr = err
			return fresh, origin
		}
		content = written
	} else {
		fresh = *published.FileTable
	}
	result.Persisted = true
	origin.From, origin.FileError = pricing.FromFile, nil
	if err := pricing.WriteStamp(options.Home, now, content, binary()); err != nil {
		result.StampErr = err
	} else {
		result.StampErr, result.CheckedAt = nil, now
	}
	return fresh, origin
}

// UpdateLockPath is the file a running `pfm update` holds an exclusive lock on
// for its whole run; internal/update pins that its host lock is this path.
func UpdateLockPath(home string) string {
	return filepath.Join(home, ".local", "share", "pfm", "install", "update.lock")
}

// acquireRefreshOwnership holds the updater's persistent inode shared through
// fetch and persistence. Creating it when absent closes the first-update race.
// Contention is offline; operational failures are failed refreshes, never absence.
func acquireRefreshOwnership(home string) (*os.File, bool, error) {
	path := UpdateLockPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, false, fmt.Errorf("prepare update lock %s: %w", path, err)
	}
	lock, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NONBLOCK, 0o600)
	if err != nil {
		return nil, false, fmt.Errorf("update lock %s: %w", path, err)
	}
	info, err := lock.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = errors.New("ownership inode is not a regular file")
	}
	if err != nil {
		return nil, false, errors.Join(fmt.Errorf("inspect update lock %s: %w", path, err), lock.Close())
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		closeErr := lock.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) && closeErr == nil {
			return nil, true, nil
		}
		return nil, false, errors.Join(fmt.Errorf("update lock %s: %w", path, err), closeErr)
	}
	return lock, false, nil
}
