package modelcost

import (
	"context"
	"fmt"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
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
// paths.EnvPricesOffline set; a nil Fetch fetches the publishers' pages.
type Options struct {
	Home, ConfigPath string
	Force, Offline   bool
	Clock            clock.Clock
	Fetch            func(context.Context) ([]Catalog, error)
}

// Result is what one refresh did. Err is a failed fetch or derivation;
// Changed and Persisted, PersistErr and a stamp write's StampErr describe a
// refreshed table; StampErr also carries an unreadable stamp; CheckedAt is the
// check that confirmed the clone file, zero when none applies.
type Result struct {
	Status               string
	Err                  error
	Changed, Persisted   bool
	PersistErr, StampErr error
	CheckedAt            time.Time
}

// Refresh brings the clone's prices.json up to date at most once per Window
// and serves the result with the override merged. Offline fetches nothing,
// --force included; a table fetched, or a clone file confirmed, less than a
// Window ago is current; otherwise both pages are fetched and the clone file
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
	now := ticker.Now().UTC()
	served, origin := published.Table, published.Origin
	switch {
	case options.Offline:
		result.Status = StatusOffline
	case !options.Force && (fetchedWithin(published.FetchedAt, now) || (confirmed && within(stamp.At, now))):
		result.Status = StatusCurrent
	default:
		served, origin = refreshNow(ctx, options, published, now, &result)
	}
	prices, err := pricing.ServeTable(served, origin, options.ConfigPath)
	if err != nil {
		return pricing.Prices{}, result, err
	}
	return prices, result, nil
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
	if err := pricing.WriteStamp(options.Home, now, content); err != nil {
		result.StampErr = err
	} else {
		result.StampErr, result.CheckedAt = nil, now
	}
	return fresh, origin
}
