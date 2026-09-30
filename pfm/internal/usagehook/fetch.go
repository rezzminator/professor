package usagehook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"time"

	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// The door's clocks. A lock older than lockStaleAfter belongs to a crashed
// peer. A failure other than a 429 backs off failureBackoff — just long enough
// that two callers opened together do not both pay for the same dead endpoint
// — and a 429 at least rateLimitFloor, longer when the server's Retry-After
// says so.
const (
	lockStaleAfter = 30 * time.Second
	failureBackoff = time.Minute
	rateLimitFloor = 10 * time.Minute
)

// StaleHorizon is how long a last-good payload stays showable through a
// failure: the one horizon the door, the prompt hook and the Limits tab
// (internal/stats) all apply.
const StaleHorizon = time.Hour

// ErrRefreshHeld reports that a peer process holds the account's refresh lock
// while nothing usable is cached to answer with meanwhile.
var ErrRefreshHeld = errors.New("another pfm process is refreshing this account's usage and nothing is cached yet")

// StatusError is a non-2xx usage response other than 429.
type StatusError struct {
	Code   int
	Status string
}

func (err *StatusError) Error() string {
	return "usage endpoint returned " + err.Status
}

// Fetch is the one door to the usage endpoint: the prompt hook (Evaluate) and
// the Limits tab (stats.LimitsSampler) both come through it, so the processes
// on a host spend at most one request per account per TTL between them. In
// order:
//
//  1. a statusline snapshot for this seat younger than options.TTL, its
//     five-hour reset still ahead, answers without a request;
//  2. this seat's cache record fresh within options.TTL answers;
//  3. an active shared backoff answers with the cached usage and its error;
//  4. the O_EXCL refresh lock (RefreshLockPath) is taken — while a peer holds it no
//     request is sent and the cache answers; holding it, the cache is read
//     again and answers if a peer refreshed or backed off meanwhile;
//  5. the request: success writes the record, a 429 writes the shared backoff
//     (BackoffFor), any other failure a one-minute backoff.
//
// With an error the returned usage is the last-good payload still inside the
// one-hour stale horizon, or none (a zero confirmation time): callers decide
// whether to show it.
func Fetch(ctx context.Context, options Options, account int) (usage Usage, confirmedAt time.Time, err error) {
	options = normalize(options)
	now := options.Now()
	var notes []error
	finish := func(answer doorAnswer) (Usage, time.Time, error) {
		if answer.err != nil {
			return answer.usage, answer.confirmedAt, errors.Join(append([]error{answer.err}, notes...)...)
		}
		for _, note := range notes {
			fmt.Fprintf(options.Log, "pfm usage-hook: %v\n", note)
		}
		return answer.usage, answer.confirmedAt, nil
	}
	snapshot, snapshotAt, found, err := ReadStatuslineSnapshot(
		ClaudeRateLimitDir(options.Env.Get(paths.EnvHome)), account, options.ConfigDir, now, options.TTL,
	)
	if err != nil {
		notes = append(notes, fmt.Errorf("statusline snapshot not used: %w", err))
	} else if found && snapshot.FiveHour.Utilization != nil {
		return snapshot, snapshotAt, nil
	}
	cachePath := CachePath(options.CacheDir, account)
	view := readCacheView(cachePath, options.ConfigDir, now)
	if view.readErr != nil {
		notes = append(notes, view.readErr)
	}
	if answer, ok := view.answer(ctx, options, now); ok {
		return finish(answer)
	}
	lockPath := RefreshLockPath(options.CacheDir, account)
	held, lockErr := false, EnsurePrivateDir(options.CacheDir)
	if lockErr != nil {
		lockErr = fmt.Errorf("usage cache directory %s: %w", options.CacheDir, lockErr)
	} else {
		held, lockErr = takeRefreshLock(lockPath, options.Clock.Now())
	}
	if held {
		// Released on every way out, a panic unwinding through the request
		// included; a failed release travels in the returned error.
		defer func() {
			if releaseErr := os.Remove(lockPath); releaseErr != nil {
				err = errors.Join(err, fmt.Errorf("release usage refresh lock %s: %w", lockPath, releaseErr))
			}
		}()
	}
	if lockErr == nil && !held {
		answer := view.staleAnswer(now, nil)
		if answer.confirmedAt.IsZero() {
			answer.err = ErrRefreshHeld
		}
		return finish(answer)
	}
	// A lock that could not be created at all (an unusable cache directory)
	// coordinates nobody and caches nothing: the request still goes out, and
	// the lock failure travels in the error beside whatever it returned.
	answer := refreshHeld(ctx, options, cachePath, now)
	if lockErr != nil {
		answer.err = errors.Join(answer.err, lockErr)
	}
	return finish(answer)
}

// doorAnswer is one way the door settled.
type doorAnswer struct {
	usage       Usage
	confirmedAt time.Time
	err         error
}

// refreshHeld runs under the refresh lock. A peer may have refreshed or backed
// off since this caller looked, so the cache is read again before a request.
func refreshHeld(ctx context.Context, options Options, cachePath string, now time.Time) doorAnswer {
	view := readCacheView(cachePath, options.ConfigDir, now)
	if answer, ok := view.answer(ctx, options, now); ok {
		return answer
	}
	previousUsage, previousFetchedAt := Usage{}, (*time.Time)(nil)
	if view.matches && view.confirmed {
		stamp := view.confirmedAt
		previousUsage, previousFetchedAt = view.record.Usage, &stamp
	}
	fetched, err := request(ctx, options)
	if err != nil {
		if ctx.Err() != nil {
			return doorAnswer{err: err}
		}
		// No request was spent (no credential to send), or the credential was
		// refused — a token refresh repairs that, waiting does not, and a
		// backoff would block the one live retry the repair authorizes.
		if IsCredentialUnavailable(err) || credentialRefused(err) {
			return view.staleAnswer(now, err)
		}
		message, retryAfter := BackoffFor(err, now)
		cacheErr := WriteCacheRecord(cachePath, CacheRecord{
			Usage: previousUsage, ConfigDir: options.ConfigDir, FetchedAt: previousFetchedAt,
			Backoff: &CacheBackoff{Message: message, RetryAfter: retryAfter, RecordedAt: now},
		})
		var rateLimit *RateLimitError
		if errors.As(err, &rateLimit) {
			err = errors.New(message)
		}
		if cacheErr != nil {
			err = errors.Join(err, fmt.Errorf("write Claude limits cache: %w", cacheErr))
		}
		return view.staleAnswer(now, err)
	}
	fetchedAt := options.Now()
	if ctx.Err() != nil {
		return doorAnswer{err: ctx.Err()}
	}
	if err := WriteCacheRecord(cachePath, CacheRecord{
		Usage: fetched, ConfigDir: options.ConfigDir, FetchedAt: &fetchedAt,
	}); err != nil {
		return doorAnswer{usage: fetched, confirmedAt: fetchedAt, err: fmt.Errorf("write Claude limits cache: %w", err)}
	}
	return doorAnswer{usage: fetched, confirmedAt: fetchedAt}
}

// cacheView is one read of the shared cache file, judged for one seat.
type cacheView struct {
	record      CacheRecord
	matches     bool      // the record is bound to this config directory
	confirmed   bool      // its payload carries a trustworthy confirmation time
	confirmedAt time.Time // fetched_at, or the file mtime for a record that predates it
	readErr     error     // a present file that could not be read or decoded
}

func readCacheView(path, configDir string, now time.Time) cacheView {
	record, err := ReadCacheRecord(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cacheView{}
		}
		return cacheView{readErr: fmt.Errorf("usage cache treated as empty: %w", err)}
	}
	view := cacheView{record: record, matches: record.MatchesConfigDir(configDir)}
	// A fetched_at stamped after now is unverifiable — corruption or clock
	// skew — and trusting it would make the cache permanently fresh.
	if record.FetchedAt != nil {
		view.confirmedAt = *record.FetchedAt
	} else if info, statErr := os.Stat(path); statErr == nil {
		view.confirmedAt = info.ModTime()
	} else {
		view.readErr = fmt.Errorf("usage cache %s unconfirmed: %w", path, statErr)
		return view
	}
	view.confirmed = !view.confirmedAt.After(now)
	return view
}

// answer is steps 2 and 3 of the door: a fresh record, then an active backoff.
func (view cacheView) answer(ctx context.Context, options Options, now time.Time) (doorAnswer, bool) {
	if !view.matches {
		return doorAnswer{}, false
	}
	if options.BypassBackoff == nil && view.confirmed && now.Sub(view.confirmedAt) < options.TTL &&
		HasCurrentWindow(view.record.Usage, now) {
		return doorAnswer{usage: view.record.Usage, confirmedAt: view.confirmedAt}, true
	}
	backoff := view.record.Backoff
	if backoff == nil || !now.Before(backoff.RetryAfter) {
		return doorAnswer{}, false
	}
	// The message is replayed verbatim so a caller recognizes a replayed
	// failure exactly like a live one.
	err := errors.New(backoff.Message)
	if options.BypassBackoff != nil && options.BypassBackoff(err) {
		return doorAnswer{}, false
	}
	answer := view.staleAnswer(now, err)
	// An empty replay blanks the caller's card. With no credential to spend
	// the request path costs a local check and returns the credential
	// sentinel a caller can act on, where a replay is only a flat string.
	if !HasCurrentWindow(answer.usage, now) && IsCredentialUnavailable(CredentialAvailable(ctx, options.ConfigDir)) {
		return doorAnswer{}, false
	}
	return answer, true
}

// staleAnswer pairs err with the last-good payload a failure may still show:
// this seat's, confirmed, inside the stale horizon — or none.
func (view cacheView) staleAnswer(now time.Time, err error) doorAnswer {
	if !view.matches || !view.confirmed || now.Sub(view.confirmedAt) > StaleHorizon {
		return doorAnswer{err: err}
	}
	return doorAnswer{usage: view.record.Usage, confirmedAt: view.confirmedAt, err: err}
}

// HasCurrentWindow is the one rule for whether a usage payload still says
// anything current: some window carries a utilization and its reset is still
// ahead (or unknown). A payload whose readings all rolled over, or whose
// windows name a reset but no reading, answers nothing current — the door does
// not serve it as fresh, and internal/stats does not show it as a stale card.
func HasCurrentWindow(usage Usage, now time.Time) bool {
	for _, named := range usage.NamedWindowsAt(now) {
		if named.Window.Utilization != nil && !resetPassed(named.Window.ResetsAt, now) {
			return true
		}
	}
	return false
}

// takeRefreshLock creates lockPath with O_EXCL. held=false with a nil error
// means a live peer owns it. A lock older than lockStaleAfter is a crashed
// peer's: it is removed and the create tried once more.
func takeRefreshLock(lockPath string, now time.Time) (bool, error) {
	for range 2 {
		file, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			if closeErr := file.Close(); closeErr != nil {
				closeErr = fmt.Errorf("close usage refresh lock %s: %w", lockPath, closeErr)
				if removeErr := os.Remove(lockPath); removeErr != nil {
					return false, errors.Join(closeErr,
						fmt.Errorf("remove usage refresh lock %s after the failed close: %w", lockPath, removeErr))
				}
				return false, closeErr
			}
			return true, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return false, fmt.Errorf("take usage refresh lock %s: %w", lockPath, err)
		}
		info, err := os.Stat(lockPath)
		if errors.Is(err, fs.ErrNotExist) {
			continue // the peer released it between the create and the stat
		}
		if err != nil {
			return false, fmt.Errorf("inspect usage refresh lock %s: %w", lockPath, err)
		}
		if age := now.Sub(info.ModTime()); age < lockStaleAfter && age > -lockStaleAfter {
			return false, nil
		}
		if err := os.Remove(lockPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return false, fmt.Errorf("remove stale usage refresh lock %s: %w", lockPath, err)
		}
	}
	return false, nil
}

// BackoffFor turns a failed request into the shared cache's backoff record: a
// 429 backs off for at least rateLimitFloor — honoring whatever Retry-After
// the server sent — and reports the SAME "rate-limited — retry HH:MM (429 Too
// Many Requests)" message whether a caller hit the 429 directly or is
// replaying the record. The retry time leads so a narrow pane keeps it, and
// the message never says "limits unavailable": the caller that renders it
// owns that phrase. Every other failure backs off failureBackoff and keeps its
// original message, so a replayed failure reads exactly like a live one.
func BackoffFor(err error, now time.Time) (message string, retryAfter time.Time) {
	var rateLimit *RateLimitError
	if errors.As(err, &rateLimit) {
		wait := max(rateLimit.RetryAfter, rateLimitFloor)
		retryAfter = now.Add(wait)
		return fmt.Sprintf(
			"rate-limited — retry %s (429 Too Many Requests)",
			retryAfter.Format("15:04"),
		), retryAfter
	}
	return err.Error(), now.Add(failureBackoff)
}

// credentialRefused reports a 401 or 403: the provider refused the token.
func credentialRefused(err error) bool {
	var status *StatusError
	return errors.As(err, &status) &&
		(status.Code == http.StatusUnauthorized || status.Code == http.StatusForbidden)
}

// request builds and sends one usage request. Only Fetch calls it, holding the
// refresh lock.
func request(ctx context.Context, options Options) (usageResult Usage, returnErr error) {
	credential, err := loadCredential(ctx, options.ConfigDir)
	if err != nil {
		return Usage{}, err
	}
	outbound, err := http.NewRequestWithContext(obs.Presence(ctx), http.MethodGet, options.Endpoint, http.NoBody)
	if err != nil {
		return Usage{}, fmt.Errorf("build usage request: %w", err)
	}
	outbound.Header.Set("Authorization", "Bearer "+credential.OAuth.AccessToken)
	outbound.Header.Set("anthropic-beta", "oauth-2025-04-20")
	outbound.Header.Set("User-Agent", "pfm/"+options.Version)
	response, err := options.Client.Do(outbound)
	if err != nil {
		return Usage{}, fmt.Errorf("fetch usage endpoint: %w", err)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close usage response: %w", err))
		}
	}()
	if response.StatusCode == http.StatusTooManyRequests {
		return Usage{}, &RateLimitError{RetryAfter: ParseRetryAfter(response.Header.Get("Retry-After"), options.Now())}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Usage{}, &StatusError{Code: response.StatusCode, Status: response.Status}
	}
	fresh, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return Usage{}, fmt.Errorf("read usage response: %w", err)
	}
	var decoded Usage
	if err := json.Unmarshal(fresh, &decoded); err != nil {
		return Usage{}, fmt.Errorf("decode usage response: %w", err)
	}
	logUnknownUsageKeys(fresh, options.Log)
	if decoded.FiveHour.Utilization == nil {
		return Usage{}, fmt.Errorf("usage response omitted %s utilization", fiveHourKey)
	}
	return decoded, nil
}
