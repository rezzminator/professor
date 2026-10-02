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
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// The door's clocks. A lock older than lockStaleAfter belongs to a crashed
// peer. A failure other than a 429 backs off failureBackoff — just long enough
// that two callers opened together do not both pay for the same dead endpoint
// — and a 429 at least rateLimitFloor, longer when the server's Retry-After
// says so. A 401 or 403 backs off refusalBackoff: a refusal changes only when
// the account's credential or standing does, and a credential change lifts the
// backoff at once (cacheView.answer), so waiting longer costs nothing a repair
// would not undo.
const (
	lockStaleAfter = 30 * time.Second
	failureBackoff = time.Minute
	rateLimitFloor = 10 * time.Minute
	refusalBackoff = 30 * time.Minute
)

// StaleHorizon is how long a last-good payload stays showable through a
// failure: the one horizon the door, the prompt hook and the Limits tab
// (internal/stats) all apply.
const StaleHorizon = time.Hour

// ErrRefreshHeld reports that a peer process holds the account's refresh lock
// while nothing usable is cached to answer with meanwhile.
var ErrRefreshHeld = errors.New("another pfm process is refreshing this account's usage and nothing is cached yet")

// StatusError is a non-2xx usage response other than 429. ErrorType and
// Message are the provider's own reading of it, from the body's
// `{"error":{"type","message"}}`, empty when the body carried none; the json
// tags let a backoff record replay it typed (CacheBackoff.Refusal).
type StatusError struct {
	Code      int    `json:"code"`
	Status    string `json:"status"`
	ErrorType string `json:"error_type,omitempty"`
	Message   string `json:"message,omitempty"`
}

func (err *StatusError) Error() string {
	text := "usage endpoint returned " + err.Status
	switch {
	case err.ErrorType != "" && err.Message != "":
		return text + ": " + err.ErrorType + ": " + err.Message
	case err.ErrorType != "" || err.Message != "":
		return text + ": " + err.ErrorType + err.Message
	}
	return text
}

// CredentialInvalid is the one rule for a refusal a token refresh repairs: a
// 401, or a 403 whose body says the token itself is invalid, expired or
// revoked. Every other 403 — a scope the token lacks, a disabled subscription
// or organization — is the account's state, which no refresh changes.
func (err *StatusError) CredentialInvalid() bool {
	switch err.Code {
	case http.StatusUnauthorized:
		return true
	case http.StatusForbidden:
		return err.ErrorType == "authentication_error" || NamesDeadCredential(err.Message)
	}
	return false
}

// AccountRefused reports a 403 that is the account's state, not its
// credential's: the card shows it as a status, and no credential probe runs.
func (err *StatusError) AccountRefused() bool {
	return err.Code == http.StatusForbidden && !err.CredentialInvalid()
}

// NamesDeadCredential reports whether a provider's refusal text says the token
// or credential itself is invalid, expired or revoked. A refusal naming a
// token for any other reason ("does not meet scope requirement") is not one.
func NamesDeadCredential(text string) bool {
	lower := strings.ToLower(text)
	if !strings.Contains(lower, "token") && !strings.Contains(lower, "credential") &&
		!strings.Contains(lower, "bearer") && !strings.Contains(lower, "api key") {
		return false
	}
	for _, marker := range []string{"invalid", "expired", "revoked"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
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
	ctx = obs.With(ctx, "acct", account)
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
	fetched, credentialID, err := request(ctx, options)
	if err != nil {
		if ctx.Err() != nil {
			return doorAnswer{err: err}
		}
		// No request was spent: there was no credential to send.
		if IsCredentialUnavailable(err) {
			return view.staleAnswer(now, err)
		}
		// A refusal backs off like any failure, keyed to the credential it
		// refused: a token refresh or a re-login changes the credential and
		// lifts the backoff at once (cacheView.answer), and the one live retry
		// a credential probe authorizes bypasses it (Options.BypassBackoff).
		refusal := refusalOf(err)
		if refusal != nil {
			kind := "account-state"
			if refusal.CredentialInvalid() {
				kind = "credential"
			}
			obs.Logger(ctx).Warn("usage.refused", "config_dir", options.ConfigDir, "status", refusal.Code,
				"error_type", refusal.ErrorType, "error_message", refusal.Message, "kind", kind)
		}
		message, retryAfter := BackoffFor(err, now)
		cacheErr := WriteCacheRecord(cachePath, CacheRecord{
			Usage: previousUsage, ConfigDir: options.ConfigDir, FetchedAt: previousFetchedAt,
			Backoff: &CacheBackoff{
				Message: message, RetryAfter: retryAfter, RecordedAt: now, Credential: credentialID, Refusal: refusal,
			},
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
	unchanged := 0
	if previousFetchedAt != nil && sameReadings(previousUsage, fetched) {
		unchanged = view.record.Unchanged + 1
	}
	record := CacheRecord{Usage: fetched, ConfigDir: options.ConfigDir, FetchedAt: &fetchedAt, Unchanged: unchanged}
	obs.Logger(ctx).Info("usage.fetched", "config_dir", options.ConfigDir, "unchanged", unchanged,
		"fresh_for", record.FreshFor(options.TTL).String())
	if err := WriteCacheRecord(cachePath, record); err != nil {
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
	fresh := view.record.FreshFor(options.TTL)
	if options.BypassBackoff == nil && view.confirmed && now.Sub(view.confirmedAt) < fresh &&
		HasCurrentWindow(view.record.Usage, now) {
		return doorAnswer{usage: view.record.Usage, confirmedAt: view.confirmedAt}, true
	}
	backoff := view.record.Backoff
	if backoff == nil || !now.Before(backoff.RetryAfter) {
		return doorAnswer{}, false
	}
	// Limits and refusals belong to an access token: a backoff recorded
	// against another credential than the one the account holds now no longer
	// speaks for it. A credential that cannot be read keeps the backoff.
	if backoff.Credential != "" {
		current, err := CredentialFingerprint(ctx, options.ConfigDir)
		switch {
		case err != nil:
			obs.Logger(ctx).Warn("usage.backoff.credential", "config_dir", options.ConfigDir, "err", err.Error())
		case current != backoff.Credential:
			obs.Logger(ctx).Info("usage.backoff.lifted",
				"config_dir", options.ConfigDir, "reason", "credential changed")
			return doorAnswer{}, false
		}
	}
	// The failure is replayed as recorded so a caller recognizes a replayed
	// failure exactly like a live one.
	err := backoff.replay()
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
	if refusalOf(err) != nil {
		return err.Error(), now.Add(refusalBackoff)
	}
	return err.Error(), now.Add(failureBackoff)
}

// refusalOf is the 401 or 403 inside err, or nil: the provider refused the
// request for the credential's or the account's sake.
func refusalOf(err error) *StatusError {
	var status *StatusError
	if errors.As(err, &status) && (status.Code == http.StatusUnauthorized || status.Code == http.StatusForbidden) {
		return status
	}
	return nil
}

// request builds and sends one usage request. Only Fetch calls it, holding the
// refresh lock. credentialID is the fingerprint of the credential it sent
// (CredentialFingerprint), empty when none was loaded.
func request(ctx context.Context, options Options) (usageResult Usage, credentialID string, returnErr error) {
	credential, err := loadCredential(ctx, options.ConfigDir)
	if err != nil {
		return Usage{}, "", err
	}
	credentialID = tokenFingerprint(credential.OAuth.AccessToken)
	outbound, err := http.NewRequestWithContext(obs.Presence(ctx), http.MethodGet, options.Endpoint, http.NoBody)
	if err != nil {
		return Usage{}, credentialID, fmt.Errorf("build usage request: %w", err)
	}
	userAgent, source := options.userAgent(ctx)
	outbound.Header.Set("Authorization", "Bearer "+credential.OAuth.AccessToken)
	outbound.Header.Set("anthropic-beta", "oauth-2025-04-20")
	outbound.Header.Set("User-Agent", userAgent)
	obs.Logger(ctx).Info("usage.request", "config_dir", options.ConfigDir, "user_agent", userAgent, "ua_source", source)
	response, err := options.Client.Do(outbound)
	if err != nil {
		return Usage{}, credentialID, fmt.Errorf("fetch usage endpoint: %w", err)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close usage response: %w", err))
		}
	}()
	if response.StatusCode == http.StatusTooManyRequests {
		errorType, _, bodyErr := providerError(response.Body)
		limited := &RateLimitError{
			RetryAfter: ParseRetryAfter(response.Header.Get("Retry-After"), options.Now()), ErrorType: errorType,
		}
		return Usage{}, credentialID, joinBodyErr(limited, bodyErr)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		errorType, message, bodyErr := providerError(response.Body)
		status := &StatusError{
			Code: response.StatusCode, Status: response.Status, ErrorType: errorType, Message: message,
		}
		return Usage{}, credentialID, joinBodyErr(status, bodyErr)
	}
	fresh, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return Usage{}, credentialID, fmt.Errorf("read usage response: %w", err)
	}
	var decoded Usage
	if err := json.Unmarshal(fresh, &decoded); err != nil {
		return Usage{}, credentialID, fmt.Errorf("decode usage response: %w", err)
	}
	logUnknownUsageKeys(fresh, options.Log)
	if decoded.FiveHour.Utilization == nil {
		return Usage{}, credentialID, fmt.Errorf("usage response omitted %s utilization", fiveHourKey)
	}
	return decoded, credentialID, nil
}

// sameReadings reports whether two payloads read exactly the same, the signal
// that stretches an idle account's freshness (CacheRecord.FreshFor). A payload
// that cannot be encoded never counts as unchanged.
func sameReadings(previous, fetched Usage) bool {
	before, errBefore := json.Marshal(previous)
	after, errAfter := json.Marshal(fetched)
	return errBefore == nil && errAfter == nil && string(before) == string(after)
}

// providerError reads the provider's `{"error":{"type","message"}}` from a
// refused request's body. A body that is not that shape yields its first 200
// characters as the message, so the card still says what the server said.
func providerError(body io.Reader) (errorType, message string, err error) {
	raw, err := io.ReadAll(io.LimitReader(body, 64<<10))
	if err != nil {
		return "", "", fmt.Errorf("read usage refusal body: %w", err)
	}
	var decoded struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &decoded) == nil && (decoded.Error.Type != "" || decoded.Error.Message != "") {
		return decoded.Error.Type, decoded.Error.Message, nil
	}
	text := strings.Join(strings.Fields(string(raw)), " ")
	if len(text) > 200 {
		text = text[:200]
	}
	return "", text, nil
}

// joinBodyErr keeps the status typed (errors.As still finds it) while an
// unreadable body travels beside it.
func joinBodyErr(status, bodyErr error) error {
	if bodyErr == nil {
		return status
	}
	return errors.Join(status, bodyErr)
}

// userAgent names the request the way Claude Code names its own when the
// account's installed Claude Code version is known: the endpoint buckets its
// rate limit by User-Agent, and a client that is not Claude Code is limited
// after a handful of calls (RR claude-oauth-usage-429-pollers § 5.1). With
// the version unknown it names pfm, as every request did before; a bare
// `claude-code` without a version is never sent.
func (options Options) userAgent(ctx context.Context) (agent, source string) {
	version, err := ClaudeVersion(ctx, options.ClaudeBinary)
	if err != nil {
		obs.Logger(ctx).Warn("usage.claude_version", "binary", options.ClaudeBinary, "err", err.Error())
		return "pfm/" + options.Version, "fallback: Claude Code version unknown"
	}
	return "claude-code/" + version, "claude --version"
}

// claudeVersions caches each Claude Code binary's version for as long as the
// binary file is unchanged, so a long-lived sampler execs it once per update.
var claudeVersions = struct {
	sync.Mutex
	byPath map[string]claudeVersion
}{byPath: make(map[string]claudeVersion)}

type claudeVersion struct {
	modTime time.Time
	size    int64
	version string
}

var semanticVersion = regexp.MustCompile(`\b\d+\.\d+\.\d+\b`)

// ClaudeVersion is the semantic version the Claude Code binary reports
// (binary empty means `claude` on PATH). It execs the binary only when a
// request is about to go out and the binary changed since it last asked.
func ClaudeVersion(ctx context.Context, binary string) (string, error) {
	if binary == "" {
		binary = "claude"
	}
	path := deps.Executable(binary)
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("stat Claude Code binary %s: %w", path, err)
	}
	claudeVersions.Lock()
	cached, found := claudeVersions.byPath[path]
	claudeVersions.Unlock()
	if found && cached.modTime.Equal(info.ModTime()) && cached.size == info.Size() {
		return cached.version, nil
	}
	ctx, cancel := context.WithTimeout(ctx, deps.ProbeTimeout)
	defer cancel()
	result, err := obs.Runner(deps.RealRunner{}).Run(ctx, []string{path, "--version"}, deps.RunOptions{
		WaitDelay: time.Second,
	})
	if err != nil {
		return "", fmt.Errorf("%s --version: %w", path, err)
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf("%s --version exited %d: %s", path, result.ExitCode,
			strings.TrimSpace(string(result.Stderr)))
	}
	version := semanticVersion.FindString(string(result.Stdout))
	if version == "" {
		return "", fmt.Errorf("%s --version printed no version: %q", path, strings.TrimSpace(string(result.Stdout)))
	}
	claudeVersions.Lock()
	claudeVersions.byPath[path] = claudeVersion{modTime: info.ModTime(), size: info.Size(), version: version}
	claudeVersions.Unlock()
	obs.Logger(ctx).Info("usage.claude_version", "binary", path, "version", version)
	return version, nil
}
