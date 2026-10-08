package modelcost

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/pricing"
)

// t0 is the clone file's fetch time, later than any embedded table.
var t0 = time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)

// fakeFetch serves the two fixture pages, or fails with err, counting calls.
type fakeFetch struct {
	calls  int
	err    error
	claude string
	openai string
}

func (f *fakeFetch) fetch(t *testing.T) func(context.Context) ([]Catalog, error) {
	return func(context.Context) ([]Catalog, error) {
		f.calls++
		if f.err != nil {
			return nil, f.err
		}
		claude, openai := f.claude, f.openai
		if claude == "" {
			claude = claudePage
		}
		if openai == "" {
			openai = openAIPage
		}
		return catalogsOf(t, claude, openai), nil
	}
}

// fixtureTable is the fixture pages' table fetched at at, with opus-5-5's
// input at opusIn.
func fixtureTable(t *testing.T, at time.Time, opusIn float64) pricing.Table {
	t.Helper()
	table := derived(t, claudePage, openAIPage)
	table.FetchedAt = at.Format(time.RFC3339)
	for i := range table.Rows {
		if table.Rows[i].Key == "claude-opus-5-5" {
			table.Rows[i].In = rate(opusIn)
		}
	}
	return table
}

// refreshHome is a home whose clone holds table (written canonically); it
// returns the home and the clone file.
func refreshHome(t *testing.T, table pricing.Table) (string, string) {
	t.Helper()
	home, clone := t.TempDir(), t.TempDir()
	file := filepath.Join(clone, filepath.FromSlash(pricing.FileRel))
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := pricing.WriteFile(file, table); err != nil {
		t.Fatal(err)
	}
	if err := paths.WriteSourceRepoMarker(home, clone); err != nil {
		t.Fatal(err)
	}
	resolved, err := pricing.CloneFile(home)
	if err != nil {
		t.Fatal(err)
	}
	return home, resolved
}

func refreshAt(
	t *testing.T,
	home string,
	at time.Time,
	fetch *fakeFetch,
	force, offline bool,
) (pricing.Prices, Result) {
	t.Helper()
	prices, result, err := Refresh(context.Background(), Options{
		Home: home, Force: force, Offline: offline, Clock: clock.NewFake(at), Fetch: fetch.fetch(t),
	})
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	return prices, result
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func TestRefreshWithinTheWindowFetchesNothing(t *testing.T) {
	home, file := refreshHome(t, fixtureTable(t, t0, 99))
	before := readFile(t, file)
	fetch := &fakeFetch{}
	prices, result := refreshAt(t, home, t0.Add(23*time.Hour), fetch, false, false)
	if result.Status != StatusCurrent || fetch.calls != 0 || prices.From != pricing.FromFile ||
		readFile(t, file) != before {
		t.Fatalf("status %q, %d fetches, from %q, file changed %v; want current, 0, file, unchanged",
			result.Status, fetch.calls, prices.From, readFile(t, file) != before)
	}
}

func TestRefreshPastTheWindowWritesChangedRows(t *testing.T) {
	home, file := refreshHome(t, fixtureTable(t, t0, 99))
	now := t0.Add(25 * time.Hour)
	fetch := &fakeFetch{}
	prices, result := refreshAt(t, home, now, fetch, false, false)
	want, err := pricing.Encode(fixtureTable(t, now, 4))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusRefreshed || !result.Changed || !result.Persisted || result.PersistErr != nil ||
		fetch.calls != 1 {
		t.Fatalf("result = %+v after %d fetches, want refreshed, changed, persisted after 1", result, fetch.calls)
	}
	if readFile(t, file) != string(want) || prices.From != pricing.FromFile ||
		prices.FetchedAt != now.Format(time.RFC3339) {
		t.Fatalf(
			"file holds the derived table %v, from %q fetched %q",
			readFile(t, file) == string(want),
			prices.From,
			prices.FetchedAt,
		)
	}
	info, err := os.Stat(file)
	if err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("file mode = %v, %v; want 0644", info.Mode().Perm(), err)
	}
	stamp, err := pricing.ReadStamp(home)
	if err != nil || !stamp.At.Equal(now) || !stamp.Matches(want) || !result.CheckedAt.Equal(now) {
		t.Fatalf(
			"stamp = %+v, %v; checked %v; want the check at %v over the new file",
			stamp,
			err,
			result.CheckedAt,
			now,
		)
	}
}

func TestRefreshForceFetchesInsideTheWindow(t *testing.T) {
	home, _ := refreshHome(t, fixtureTable(t, t0, 99))
	fetch := &fakeFetch{}
	_, result := refreshAt(t, home, t0.Add(time.Hour), fetch, true, false)
	if result.Status != StatusRefreshed || fetch.calls != 1 {
		t.Fatalf("status %q after %d fetches, want refreshed after 1", result.Status, fetch.calls)
	}
}

func TestRefreshOfUnchangedRowsKeepsTheFileAndStampsTheCheck(t *testing.T) {
	home, file := refreshHome(t, fixtureTable(t, t0, 4))
	before := readFile(t, file)
	fetch := &fakeFetch{}
	now := t0.Add(25 * time.Hour)
	prices, result := refreshAt(t, home, now, fetch, false, false)
	if result.Status != StatusRefreshed || result.Changed || !result.Persisted || readFile(t, file) != before {
		t.Fatalf("result = %+v, file changed %v; want refreshed, unchanged, persisted, the same bytes",
			result, readFile(t, file) != before)
	}
	if prices.From != pricing.FromFile || prices.FetchedAt != t0.Format(time.RFC3339) || !result.CheckedAt.Equal(now) {
		t.Fatalf(
			"served from %q fetched %q checked %v; want the file as fetched, checked now",
			prices.From,
			prices.FetchedAt,
			result.CheckedAt,
		)
	}
	_, later := refreshAt(t, home, now.Add(time.Hour), fetch, false, false)
	if later.Status != StatusCurrent || fetch.calls != 1 || !later.CheckedAt.Equal(now) {
		t.Fatalf(
			"an hour after a confirming check: %+v after %d fetches; want current via the stamp",
			later,
			fetch.calls,
		)
	}
	restored := fixtureTable(t, t0.Add(-time.Hour), 4)
	if _, err := pricing.WriteFile(file, restored); err != nil {
		t.Fatal(err)
	}
	_, replaced := refreshAt(t, home, now.Add(time.Hour), fetch, false, false)
	if replaced.Status != StatusRefreshed || fetch.calls != 2 {
		t.Fatalf(
			"a replaced clone file: %+v after %d fetches; the stamp no longer matches, want a refresh",
			replaced,
			fetch.calls,
		)
	}
}

func TestRefreshReportsAMalformedStamp(t *testing.T) {
	home, _ := refreshHome(t, fixtureTable(t, t0, 4))
	if err := os.MkdirAll(filepath.Dir(paths.PricesStampPath(home)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.PricesStampPath(home), []byte("garbage\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, result := refreshAt(t, home, t0.Add(time.Hour), &fakeFetch{}, false, false)
	if result.Status != StatusCurrent || result.StampErr == nil ||
		!strings.Contains(result.StampErr.Error(), paths.PricesStampPath(home)) {
		t.Fatalf("result = %+v, want current with the stamp error named", result)
	}
}

func TestRefreshOfflineFetchesNothingEvenWhenForced(t *testing.T) {
	home, _ := refreshHome(t, fixtureTable(t, t0, 99))
	fetch := &fakeFetch{}
	prices, result := refreshAt(t, home, t0.Add(48*time.Hour), fetch, true, true)
	if result.Status != StatusOffline || fetch.calls != 0 || prices.From != pricing.FromFile {
		t.Fatalf("status %q after %d fetches from %q, want offline, 0, file", result.Status, fetch.calls, prices.From)
	}
}

func TestRefreshFailureServesTheLoadedTable(t *testing.T) {
	for name, fetch := range map[string]*fakeFetch{
		"fetch":  {err: errors.New("fixture network failure")},
		"derive": {openai: strings.ReplaceAll(openAIPage, "Prices per 1M tokens.", "")},
	} {
		t.Run(name, func(t *testing.T) {
			home, file := refreshHome(t, fixtureTable(t, t0, 99))
			before := readFile(t, file)
			prices, result := refreshAt(t, home, t0.Add(25*time.Hour), fetch, false, false)
			if result.Status != StatusFailed || result.Err == nil || result.Persisted || result.Changed {
				t.Fatalf("result = %+v, want failed with its error", result)
			}
			if prices.From != pricing.FromFile || prices.FetchedAt != t0.Format(time.RFC3339) ||
				readFile(t, file) != before {
				t.Fatalf(
					"served from %q fetched %q, file changed %v; want the file untouched",
					prices.From,
					prices.FetchedAt,
					readFile(t, file) != before,
				)
			}
		})
	}
}

func TestRefreshWithoutACloneRecordServesTheFetchUnpersisted(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
	prices, result := refreshAt(t, home, now, &fakeFetch{}, false, false)
	if result.Status != StatusRefreshed || result.Persisted ||
		!errors.Is(result.PersistErr, paths.ErrNoSourceRepoMarker) {
		t.Fatalf("result = %+v, want refreshed, not persisted, for want of a clone record", result)
	}
	if prices.From != pricing.FromFetch || prices.FetchedAt != now.Format(time.RFC3339) || len(prices.Rows) != 7 {
		t.Fatalf(
			"served from %q fetched %q with %d rows, want the fetch's 7",
			prices.From,
			prices.FetchedAt,
			len(prices.Rows),
		)
	}
	if _, err := os.Stat(paths.PricesStampPath(home)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stamp stat = %v; an unpersisted refresh stamps nothing", err)
	}
}

func TestRefreshThatCannotWriteTheCloneFileServesTheFetch(t *testing.T) {
	home, file := refreshHome(t, fixtureTable(t, t0, 99))
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(file, "occupied"), 0o755); err != nil {
		t.Fatal(err)
	}
	prices, result := refreshAt(t, home, time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC), &fakeFetch{}, false, false)
	if result.Status != StatusRefreshed || result.Persisted || result.PersistErr == nil ||
		prices.From != pricing.FromFetch {
		t.Fatalf("result = %+v from %q, want refreshed, not persisted, served from the fetch", result, prices.From)
	}
	if prices.FileError == nil || !strings.Contains(prices.FileError.Error(), file) {
		t.Fatalf("file error = %v, want the unreadable clone file named", prices.FileError)
	}
}

func TestRefreshInvalidOverrideIsTheError(t *testing.T) {
	home, _ := refreshHome(t, fixtureTable(t, t0, 4))
	configPath := filepath.Join(t.TempDir(), config.FileName)
	if err := os.WriteFile(config.PricesPath(configPath), []byte(`{"version":1,"rows":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, result, err := Refresh(context.Background(), Options{
		Home: home, ConfigPath: configPath, Clock: clock.NewFake(t0.Add(time.Hour)), Fetch: (&fakeFetch{}).fetch(t),
	})
	if err == nil || !strings.Contains(err.Error(), config.PricesPath(configPath)) || result.Status != StatusCurrent {
		t.Fatalf("Refresh = status %q, err %v; want current and the override named", result.Status, err)
	}
}

// A clock behind the table's or the stamp's time (a release fetched on a host
// whose clock ran ahead, a machine whose clock was reset) must not read as a
// fresh table forever: a check from the future is no check.
func TestRefreshOfAFutureDatedTableOrStampFetches(t *testing.T) {
	now := t0.Add(-48 * time.Hour)
	t.Run("table fetched in the future", func(t *testing.T) {
		home, _ := refreshHome(t, fixtureTable(t, t0, 99))
		fetch := &fakeFetch{}
		if _, result := refreshAt(t, home, now, fetch, false, false); result.Status != StatusRefreshed ||
			fetch.calls != 1 {
			t.Fatalf("status %q after %d fetches, want refreshed after 1", result.Status, fetch.calls)
		}
	})
	t.Run("stamp from the future", func(t *testing.T) {
		home, file := refreshHome(t, fixtureTable(t, now.Add(-72*time.Hour), 99))
		if err := pricing.WriteStamp(home, t0, []byte(readFile(t, file))); err != nil {
			t.Fatal(err)
		}
		fetch := &fakeFetch{}
		if _, result := refreshAt(t, home, now, fetch, false, false); result.Status != StatusRefreshed ||
			fetch.calls != 1 {
			t.Fatalf("status %q after %d fetches, want refreshed after 1", result.Status, fetch.calls)
		}
	})
}
