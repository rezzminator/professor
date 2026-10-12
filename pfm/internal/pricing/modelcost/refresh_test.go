package modelcost

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
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

// stampByThisBinary records a check at t0 of the clone file, by this binary.
func stampByThisBinary(t *testing.T, home, file string) {
	t.Helper()
	binary, err := ThisBinary()
	if err != nil {
		t.Fatalf("ThisBinary: %v", err)
	}
	if err := pricing.WriteStamp(home, t0, []byte(readFile(t, file)), binary); err != nil {
		t.Fatal(err)
	}
}

func TestRefreshWithinTheWindowFetchesNothing(t *testing.T) {
	home, file := refreshHome(t, fixtureTable(t, t0, 99))
	stampByThisBinary(t, home, file)
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
	_, result := refreshAt(t, home, t0.Add(time.Hour), &fakeFetch{}, false, true)
	if result.Status != StatusOffline || result.StampErr == nil ||
		!strings.Contains(result.StampErr.Error(), paths.PricesStampPath(home)) {
		t.Fatalf("result = %+v, want offline with the stamp error named", result)
	}
	_, result = refreshAt(t, home, t0.Add(time.Hour), &fakeFetch{}, false, false)
	if result.Status != StatusRefreshed || result.StampErr != nil {
		t.Fatalf("result = %+v, want a malformed stamp refetched and rewritten", result)
	}
	if stamp, err := pricing.ReadStamp(home); err != nil || stamp.Binary == "" {
		t.Fatalf("rewritten stamp = %+v, %v; want this binary's", stamp, err)
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
	home, file := refreshHome(t, fixtureTable(t, t0, 4))
	stampByThisBinary(t, home, file)
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
		binary, err := ThisBinary()
		if err != nil {
			t.Fatal(err)
		}
		if err := pricing.WriteStamp(home, t0, []byte(readFile(t, file)), binary); err != nil {
			t.Fatal(err)
		}
		fetch := &fakeFetch{}
		if _, result := refreshAt(t, home, now, fetch, false, false); result.Status != StatusRefreshed ||
			fetch.calls != 1 {
			t.Fatalf("status %q after %d fetches, want refreshed after 1", result.Status, fetch.calls)
		}
	})
}

func TestDocumentStatesTheServedTableAndItsRefresh(t *testing.T) {
	checked := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	prices := pricing.Prices{
		Table: pricing.Table{
			Version:   pricing.Version,
			FetchedAt: "2026-01-01T00:00:00Z",
			Sources:   []pricing.Source{{Provider: "openai", URL: "https://example.invalid"}},
		},
		Origin:   pricing.Origin{From: pricing.FromFile, File: "/clone/prices.json", FileError: errors.New("stale")},
		Override: &pricing.Override{Path: "/cfg/pfm.prices.json", Rows: 2},
	}
	refreshed := PriceDocument(
		prices,
		Result{Status: StatusRefreshed, Changed: true, PersistErr: errors.New("read-only"), CheckedAt: checked},
		"",
		nil,
	)
	if refreshed.ServedFrom != pricing.FromFile || refreshed.File != "/clone/prices.json" ||
		refreshed.FileError != "stale" ||
		refreshed.CheckedAt != "2026-01-02T03:04:05Z" ||
		refreshed.Override == nil ||
		refreshed.Override.Rows != 2 {
		t.Fatalf("Document header = %+v", refreshed)
	}
	if r := refreshed.Refresh; r.Status != StatusRefreshed || r.Changed == nil || !*r.Changed || r.Persisted == nil ||
		*r.Persisted ||
		r.PersistError != "read-only" {
		t.Fatalf("refreshed report = %+v", r)
	}
	failed := PriceDocument(prices, Result{Status: StatusFailed, Err: errors.New("HTTP 503")}, "claude-opus-5-5", nil)
	if r := failed.Refresh; r.Status != StatusFailed || r.Error != "HTTP 503" || r.Changed != nil ||
		r.Persisted != nil ||
		failed.Model != "claude-opus-5-5" {
		t.Fatalf("failed report = %+v, model %q", r, failed.Model)
	}
}

func TestMachineOptionsReadTheRuntime(t *testing.T) {
	var runtime config.Runtime
	runtime.Paths.Home, runtime.Config.Path = "/h", "/h/pfm.config.json"
	options := MachineOptions(runtime)
	if options.Home != "/h" || options.ConfigPath != "/h/pfm.config.json" || options.Force ||
		options.Offline != paths.PricesOffline() || options.Clock == nil || options.Fetch == nil {
		t.Fatalf("MachineOptions = %+v", options)
	}
}

// rawStamp writes the check stamp as the line "{t0} {sha256 of the clone
// file} {binary}", the binary field left out when binary is empty (the stamp
// format before a check named its binary).
func rawStamp(t *testing.T, home, file, binary string) {
	t.Helper()
	line := fmt.Sprintf("%s %x", t0.Format(time.RFC3339Nano), sha256.Sum256([]byte(readFile(t, file))))
	if binary != "" {
		line += " " + binary
	}
	if err := os.MkdirAll(filepath.Dir(paths.PricesStampPath(home)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.PricesStampPath(home), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestRefreshTiesTheWindowToTheBinary: a table is current only by a stamp
// this binary wrote within the Window. A table fetched an hour ago with no
// stamp, a stamp in the binary-less format, and a stamp another binary wrote
// (a refresh just before a host install) are all refetched; the refresh then
// stamps this binary, so its next check within the Window is current.
func TestRefreshTiesTheWindowToTheBinary(t *testing.T) {
	for _, tc := range []struct{ name, binary string }{
		{"no stamp", ""},
		{"a stamp without a binary", "-"},
		{"a stamp from another binary", "another-pfm-build"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, file := refreshHome(t, fixtureTable(t, t0, 4))
			switch tc.binary {
			case "":
			case "-":
				rawStamp(t, home, file, "")
			default:
				rawStamp(t, home, file, tc.binary)
			}
			fetch := &fakeFetch{}
			_, result := refreshAt(t, home, t0.Add(time.Hour), fetch, false, false)
			if result.Status != StatusRefreshed || fetch.calls != 1 {
				t.Fatalf("status %q after %d fetches, want refreshed after 1", result.Status, fetch.calls)
			}
			_, result = refreshAt(t, home, t0.Add(2*time.Hour), fetch, false, false)
			if result.Status != StatusCurrent || fetch.calls != 1 {
				t.Fatalf("this binary's next check: status %q after %d fetches, want current after 1",
					result.Status, fetch.calls)
			}
		})
	}
}

// TestIdentifyIsTheExecutableThisProcessStartedFrom: the identity is the
// sha256 of the file the process started from; a binary renamed over its path
// afterwards (a host install while an old pfm runs) is no identity, so the old
// process never stamps its parse as the new binary's.
func TestIdentifyIsTheExecutableThisProcessStartedFrom(t *testing.T) {
	started := func(t *testing.T) executable {
		t.Helper()
		path := filepath.Join(t.TempDir(), "pfm")
		if err := os.WriteFile(path, []byte("old build"), 0o600); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		return executable{path: path, info: info}
	}
	t.Run("the file it started from", func(t *testing.T) {
		got, err := identify(started(t))
		if want := fmt.Sprintf("exe-sha256:%x", sha256.Sum256([]byte("old build"))); err != nil || got != want {
			t.Fatalf("identify = %q, %v; want %q", got, err, want)
		}
	})
	t.Run("a binary renamed over its path", func(t *testing.T) {
		exe := started(t)
		replacement := exe.path + ".new"
		if err := os.WriteFile(replacement, []byte("new build"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(replacement, exe.path); err != nil {
			t.Fatal(err)
		}
		if got, err := identify(exe); err == nil || !strings.Contains(err.Error(), "was replaced") {
			t.Fatalf("identify = %q, %v; want the replacement refused", got, err)
		}
	})
	t.Run("an executable unreadable at startup", func(t *testing.T) {
		cause := errors.New("this binary's identity: fixture")
		if got, err := identify(executable{err: cause}); !errors.Is(err, cause) || got != "" {
			t.Fatalf("identify = %q, %v; want the startup error", got, err)
		}
	})
}

// TestRefreshReportsAnUnreadableIdentityOnlyWhereAStampIsJudged: a machine
// with no clone file serves its embedded table without a stamp, so an
// identity that cannot be read is no warning there; where the stamp write
// fails too, the identity's cause is named beside the write error, never
// dropped.
func TestRefreshReportsAnUnreadableIdentityOnlyWhereAStampIsJudged(t *testing.T) {
	unreadable := func() (string, error) { return "", errors.New("this binary's identity: fixture") }
	t.Run("no clone file, embedded table current", func(t *testing.T) {
		home := t.TempDir()
		published, err := pricing.LoadPublished(home)
		if err != nil {
			t.Fatal(err)
		}
		fetchedAt, err := time.Parse(time.RFC3339, published.FetchedAt)
		if err != nil {
			t.Fatal(err)
		}
		fetch := &fakeFetch{}
		_, result, err := Refresh(context.Background(), Options{
			Home: home, Clock: clock.NewFake(fetchedAt.Add(time.Hour)), Fetch: fetch.fetch(t), Binary: unreadable,
		})
		if err != nil || result.Status != StatusCurrent || result.StampErr != nil || fetch.calls != 0 {
			t.Fatalf("result = %+v, %v after %d fetches; want current with no stamp warning", result, err, fetch.calls)
		}
	})
	t.Run("a stamp that cannot be written", func(t *testing.T) {
		home, _ := refreshHome(t, fixtureTable(t, t0, 4))
		stateDir := filepath.Dir(paths.PricesStampPath(home))
		if err := os.MkdirAll(filepath.Dir(stateDir), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(stateDir, []byte("a file where the state directory goes\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, result, err := Refresh(context.Background(), Options{
			Home:   home,
			Clock:  clock.NewFake(t0.Add(25 * time.Hour)),
			Fetch:  (&fakeFetch{}).fetch(t),
			Binary: unreadable,
		})
		if err != nil || result.Status != StatusRefreshed || result.StampErr == nil ||
			!strings.Contains(result.StampErr.Error(), "write price check stamp") ||
			!strings.Contains(result.StampErr.Error(), "identity: fixture") {
			t.Fatalf(
				"result = %+v, %v; want refreshed, the write error and the identity's cause both named",
				result,
				err,
			)
		}
	})
}

// A pfm update older than this build sets no PFM_PRICES_OFFLINE for its
// children, so the lock it holds is the only sign the clone is mid-update: a
// refresh then must not rewrite the clone's tracked prices.json, which would
// refuse the update's fast-forward and its rollback's reset --keep.
func TestRefreshDuringAnUpdateWritesNothingAndSaysWhy(t *testing.T) {
	home, file := refreshHome(t, fixtureTable(t, t0, 99))
	before := readFile(t, file)
	lockPath := UpdateLockPath(home)
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		t.Fatal(err)
	}
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Close() }()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}

	fetch := &fakeFetch{}
	_, result := refreshAt(t, home, t0.Add(48*time.Hour), fetch, true, false)
	if result.Status != StatusOffline || !strings.Contains(result.Reason, "pfm update") ||
		readFile(t, file) != before {
		t.Fatalf(
			"during an update: status %q reason %q, file changed %v; want offline, a reason naming pfm update, unchanged",
			result.Status,
			result.Reason,
			readFile(t, file) != before,
		)
	}

	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	_, result = refreshAt(t, home, t0.Add(48*time.Hour), fetch, true, false)
	if result.Status != StatusRefreshed || result.Reason != "" || fetch.calls != 1 || !result.Persisted {
		t.Fatalf("after the update: status %q reason %q, %d fetches, persisted %v; want refreshed, no reason, 1, true",
			result.Status, result.Reason, fetch.calls, result.Persisted)
	}
}

// The updater must not enter between a refresh's fetch and persistence, even
// on a home whose persistent ownership inode has not been created yet.
func TestRefreshDuringAnUpdateHoldsOwnershipThroughPersistence(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprintf("existing lock %v", existing), func(t *testing.T) {
			home, _ := refreshHome(t, fixtureTable(t, t0, 99))
			lockPath := UpdateLockPath(home)
			if existing {
				if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(lockPath, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			fetch := (&fakeFetch{}).fetch(t)
			_, result, err := Refresh(context.Background(), Options{
				Home:   home,
				Force:  true,
				Clock:  clock.NewFake(t0.Add(48 * time.Hour)),
				Binary: func() (string, error) { return "fixture", nil },
				Fetch: func(ctx context.Context) ([]Catalog, error) {
					if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
						t.Fatal(err)
					}
					updater, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
					if err != nil {
						t.Fatal(err)
					}
					defer func() { _ = updater.Close() }()
					if err := syscall.Flock(
						int(updater.Fd()),
						syscall.LOCK_EX|syscall.LOCK_NB,
					); !errors.Is(
						err,
						syscall.EWOULDBLOCK,
					) {
						t.Errorf("updater entered during price fetch: flock = %v; want ownership contention", err)
					}
					return fetch(ctx)
				},
			})
			if err != nil || result.Status != StatusRefreshed || !result.Persisted {
				t.Fatalf("refresh = %+v, %v; want persisted", result, err)
			}
			updater, err := os.OpenFile(lockPath, os.O_RDWR, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = updater.Close() }()
			if err := syscall.Flock(int(updater.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
				t.Fatalf("refresh retained ownership after persistence: %v", err)
			}
		})
	}
}

// A lock file that cannot be looked up (anything but absent) is no proof that
// no update is running: the refresh writes nothing and says so by name.
func TestRefreshWithAnUnreadableUpdateLockFailsAndWritesNothing(t *testing.T) {
	home, file := refreshHome(t, fixtureTable(t, t0, 99))
	before := readFile(t, file)
	lockPath := UpdateLockPath(home)
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(lockPath, lockPath); err != nil {
		t.Fatal(err)
	}

	fetch := &fakeFetch{}
	_, result := refreshAt(t, home, t0.Add(48*time.Hour), fetch, true, false)

	if result.Status != StatusFailed || result.Err == nil || !strings.Contains(result.Err.Error(), lockPath) ||
		readFile(t, file) != before {
		t.Fatalf(
			"unreadable lock: status %q err %v, file changed %v; want failed, an error naming %s, unchanged",
			result.Status,
			result.Err,
			readFile(t, file) != before,
			lockPath,
		)
	}
}
