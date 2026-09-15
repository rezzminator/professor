// Package usagehook implements the fail-open UserPromptSubmit usage warning.
package usagehook

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"hostops/pfm/internal/atomicfile"
	"hostops/pfm/internal/paths"
)

const defaultEndpoint = "https://api.anthropic.com/api/oauth/usage"

// Options is the complete, jail-replaceable hook environment.
type Options struct {
	Now         func() time.Time
	Home        string
	ConfigDir   string
	AccountDirs map[string]int
	CacheDir    string
	Warn        int
	Critical    int
	TTL         time.Duration
	Client      *http.Client
	Endpoint    string
	Log         io.Writer
}

// Window is one model usage window returned by Anthropic's OAuth endpoint.
type Window struct {
	Utilization *float64 `json:"utilization"`
	ResetsAt    string   `json:"resets_at"`
}

// ScopedLimit is one model-specific quota carried in the usage response's
// limits array.
type ScopedLimit struct {
	Kind  string `json:"kind"`
	Scope struct {
		Model struct {
			DisplayName string `json:"display_name"`
		} `json:"model"`
	} `json:"scope"`
	Percent  *float64 `json:"percent"`
	ResetsAt string   `json:"resets_at"`
	IsActive bool     `json:"is_active"`
}

// Usage is the typed quota-bearing core of the OAuth usage response. Unknown
// top-level provider metadata is intentionally ignored rather than presented
// as a rate-limit window.
type Usage struct {
	FiveHour  Window        `json:"five_hour"`
	SevenDay  Window        `json:"seven_day"`
	SevenOpus Window        `json:"seven_day_opus"`
	Limits    []ScopedLimit `json:"limits"`
}

// WindowDescriptor is the canonical public name and ordering metadata for one
// recognized API window key.
type WindowDescriptor struct {
	Key   string
	Label string
	Known bool
}

// NamedWindow couples canonical name metadata with the window carried by a
// usage response.
type NamedWindow struct {
	WindowDescriptor
	Window Window
}

var knownWindowDescriptors = []WindowDescriptor{
	{Key: "five_hour", Label: "5h", Known: true},
	{Key: "seven_day", Label: "7d", Known: true},
	{Key: "seven_day_fable", Label: "7d-fable", Known: true},
}

// AllWindows returns every window this build knows how to render, in canonical
// display order, whether or not the current response carries it. A renderer
// that iterates only the PRESENT keys cannot distinguish "this window is at 0%"
// from "this window's data never arrived" — the two render identically as
// nothing at all. Callers that must keep those apart start here.
func AllWindows() []WindowDescriptor {
	return append([]WindowDescriptor(nil), knownWindowDescriptors...)
}

// DescribeWindows returns recognized present keys in canonical display order.
// Provider metadata and unrecognized top-level keys are never windows.
func DescribeWindows(keys []string) []WindowDescriptor {
	present := make(map[string]bool, len(keys))
	for _, key := range keys {
		present[key] = true
	}
	described := make([]WindowDescriptor, 0, len(knownWindowDescriptors))
	for _, descriptor := range knownWindowDescriptors {
		if present[descriptor.Key] {
			described = append(described, descriptor)
		}
	}
	return described
}

// NamedWindows returns only windows actually carried by the response. A zero
// utilization pointer is still present and therefore remains visible.
func (usage Usage) NamedWindows() []NamedWindow {
	return usage.NamedWindowsAt(time.Now())
}

// NamedWindowsAt is the deterministic form used by samplers and renderers that
// already own a clock.
func (usage Usage) NamedWindowsAt(now time.Time) []NamedWindow {
	windows := map[string]Window{
		"five_hour": usage.FiveHour,
		"seven_day": usage.SevenDay,
	}
	if fable, ok := usage.fableWindow(now); ok {
		windows["seven_day_fable"] = fable
	}
	keys := make([]string, 0, len(windows))
	for key, window := range windows {
		if window.Utilization != nil || window.ResetsAt != "" {
			keys = append(keys, key)
		}
	}
	descriptors := DescribeWindows(keys)
	named := make([]NamedWindow, 0, len(descriptors))
	for _, descriptor := range descriptors {
		named = append(named, NamedWindow{WindowDescriptor: descriptor, Window: windows[descriptor.Key]})
	}
	return named
}

func (usage Usage) fableWindow(now time.Time) (Window, bool) {
	var fallback *ScopedLimit
	for index := range usage.Limits {
		limit := &usage.Limits[index]
		if strings.TrimSpace(limit.Kind) != "weekly_scoped" ||
			!strings.EqualFold(strings.TrimSpace(limit.Scope.Model.DisplayName), "Fable") ||
			limit.Percent == nil {
			continue
		}
		resetAt, err := time.Parse(time.RFC3339, strings.TrimSpace(limit.ResetsAt))
		if err != nil || !resetAt.After(now) {
			continue
		}
		if limit.IsActive {
			return Window{Utilization: limit.Percent, ResetsAt: limit.ResetsAt}, true
		}
		if fallback == nil {
			fallback = limit
		}
	}
	if fallback == nil {
		return Window{}, false
	}
	return Window{Utilization: fallback.Percent, ResetsAt: fallback.ResetsAt}, true
}

type (
	usage       = Usage
	usageWindow = Window
)

type credentials struct {
	OAuth struct {
		AccessToken string `json:"accessToken"`
	} `json:"claudeAiOauth"`
}

// Evaluate returns only model-facing hook text. Callers deliberately swallow
// its error and exit zero: hook infrastructure must never block a prompt.
func Evaluate(ctx context.Context, options Options) (string, error) {
	options = normalize(options)
	// A config dir with no usable credential in EITHER source is simply not a
	// Claude account seat, so the hook stays silent — but only for that
	// reason. A keychain we failed to read is a different thing entirely and
	// must surface rather than masquerade as "no account here".
	if err := CredentialAvailable(ctx, options.ConfigDir); err != nil {
		if IsCredentialUnavailable(err) {
			return "", nil
		}
		return "", err
	}
	account := accountNumber(options.Home, options.ConfigDir, options.AccountDirs)
	if err := EnsurePrivateDirectory(options.CacheDir); err != nil {
		return "", err
	}
	now := options.Now()
	cachePath := CachePath(options.CacheDir, account)
	record, readErr := ReadCacheRecord(cachePath)
	matches := readErr == nil && record.MatchesConfigDir(options.ConfigDir)
	// Only this account's record can defer a refresh, including through a
	// peer's backoff. Numeric account IDs can be reassigned to another seat.
	backoff := matches && record.Backoff != nil && now.Before(record.Backoff.RetryAfter)
	if !matches || (cacheAge(record, cachePath, now) >= options.TTL && !backoff) {
		if err := refresh(ctx, options, cachePath); err != nil {
			fmt.Fprintf(options.Log, "pfm usage-hook: refresh failed; trying stale cache: %v\n", err)
		}
	}
	record, err := ReadCacheRecord(cachePath)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !record.MatchesConfigDir(options.ConfigDir) || cacheAge(record, cachePath, now) > time.Hour {
		return "", nil
	}
	cached := record.Usage
	five := currentUtilization(cached.FiveHour, now, 0)
	seven := currentUtilization(cached.SevenDay, now, 0)
	opus := currentUtilization(cached.SevenOpus, now, -1)
	fable := -1
	for _, named := range cached.NamedWindowsAt(now) {
		if named.Key == "seven_day_fable" {
			fable = utilization(named.Window, -1)
			break
		}
	}
	maximum := max(five, seven, opus, fable)
	flagPath := filepath.Join(options.CacheDir, fmt.Sprintf("warned-%d", account))
	if maximum < options.Warn {
		flag, err := os.ReadFile(flagPath)
		if err != nil && !os.IsNotExist(err) {
			return "", err
		}
		if err == nil && (CacheRecord{ConfigDir: string(flag)}).MatchesConfigDir(options.ConfigDir) {
			if err := os.Remove(flagPath); err != nil {
				return "", err
			}
			return fmt.Sprintf(
				"✅ usage recovered — account %d is back to %s · %s. Any earlier limit warning in this conversation is STALE — ignore it and work normally.\n",
				account,
				recoveredWindowPhrase("5h", cached.FiveHour, five, now),
				recoveredWindowPhrase("7d", cached.SevenDay, seven, now),
			), nil
		}
		return "", nil
	}
	if err := atomicfile.Write(flagPath, []byte(options.ConfigDir), 0o600); err != nil {
		return "", err
	}
	line := windowPhrase("5h", cached.FiveHour, five, now, "15:04") +
		" · " + windowPhrase("7d", cached.SevenDay, seven, now, "Mon 15:04")
	if opus >= 0 {
		line += fmt.Sprintf(" · 7d-opus %d%%", opus)
	}
	if fable >= 0 {
		line += fmt.Sprintf(" · 7d-fable %d%%", fable)
	}
	if maximum >= options.Critical {
		return fmt.Sprintf(
			"🔴 USAGE LIMIT IMMINENT — account %d: %s. %s\n",
			account,
			line,
			criticalGuidance(five, seven, opus, fable, options.Critical),
		), nil
	}
	return fmt.Sprintf(
		"⚠ usage limit approaching — account %d: %s. Plan around it: wrap up soon or /reload to another account before the cap hits.\n",
		account,
		line,
	), nil
}

// criticalGuidance names the window that actually crossed the critical threshold, and tells
// the reader only what is true of THAT window.
//
// The severity is max() across every window, which is right — any one of them hitting its cap
// stops work. The old copy was not: it asserted "This window is nearly exhausted: finish the
// in-flight step, then /reload" no matter which window tripped. A model-scoped 7-day cap
// (opus, fable) at 95% therefore printed a session-wide stop order while the live 5-hour
// window sat at 1% used — an alarm that is false about the thing it is read as describing, on
// every single prompt. Read literally, it truncates work that had ~99% of its budget left; read
// often enough, it trains the reader to ignore the banner that will one day be true.
//
// So: say which window, and say what is spendable. A model-scoped cap names the model and the
// still-open 5-hour headroom; the 5-hour window keeps the original stop-and-reload advice.
func criticalGuidance(five, seven, opus, fable, critical int) string {
	if five >= critical {
		return "The 5-hour window is nearly exhausted: finish the in-flight step, then /reload to another account (or pause until the reset)."
	}
	if seven >= critical {
		return fmt.Sprintf(
			"The 7-day account cap is nearly reached (the 5-hour window is only %d%% used, but the weekly cap binds first): /reload to another account.",
			five,
		)
	}
	scoped := make([]string, 0, 2)
	if opus >= critical {
		scoped = append(scoped, "opus")
	}
	if fable >= critical {
		scoped = append(scoped, "fable")
	}
	if len(scoped) > 0 {
		return fmt.Sprintf(
			"Only the 7-day %s cap is nearly reached — the 5-hour window is %d%% used and every other model on this account is unaffected. Keep working; route %s-tier spawns elsewhere or /reload for those.",
			strings.Join(scoped, " and "),
			five,
			strings.Join(scoped, "/"),
		)
	}
	return "One window is nearly exhausted: finish the in-flight step, then /reload to another account (or pause until the reset)."
}

func normalize(options Options) Options {
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Home == "" {
		options.Home = os.Getenv(paths.EnvHome)
		if options.Home == "" {
			options.Home, _ = os.UserHomeDir()
		}
	}
	if options.ConfigDir == "" {
		options.ConfigDir = os.Getenv("CLAUDE_CONFIG_DIR")
		if options.ConfigDir == "" {
			options.ConfigDir = filepath.Join(options.Home, ".claude")
		}
	}
	if options.CacheDir == "" {
		options.CacheDir = DefaultCacheDir()
	}
	if options.Warn <= 0 {
		options.Warn = envInt("CC_USAGE_WARN", 80)
	}
	if options.Critical <= 0 {
		options.Critical = envInt("CC_USAGE_CRIT", 95)
	}
	if options.TTL <= 0 {
		options.TTL = time.Duration(envInt("CC_USAGE_TTL", 180)) * time.Second
	}
	if options.Client == nil {
		options.Client = &http.Client{Timeout: 6 * time.Second}
	}
	if options.Endpoint == "" {
		options.Endpoint = defaultEndpoint
	}
	if options.Log == nil {
		options.Log = io.Discard
	}
	return options
}

func accountNumber(home, configDir string, accountDirs ...map[string]int) int {
	physical, err := filepath.EvalSymlinks(configDir)
	if err != nil {
		physical = configDir
	}
	if len(accountDirs) != 0 {
		for directory, account := range accountDirs[0] {
			candidate := directory
			if resolved, resolveErr := filepath.EvalSymlinks(directory); resolveErr == nil {
				candidate = resolved
			}
			if filepath.Clean(physical) == filepath.Clean(candidate) {
				return account
			}
		}
	}
	switch filepath.Clean(physical) {
	case filepath.Join(home, ".claude"):
		return 1
	case filepath.Join(home, ".claude3"):
		return 2
	case filepath.Join(home, ".cc", "3"):
		return 3
	}
	value, err := strconv.Atoi(filepath.Base(physical))
	if err == nil && value > 0 {
		return value
	}
	return 1
}

// UsageCacheDir returns the usage cache directory nested under base using the
// same "cc-usage-<uid>" naming DefaultCacheDir applies to its own base (a
// jail home's "tmp" directory, or os.TempDir()). A caller that already holds
// a jail-scoped base directory computed the identical way — e.g.
// statusline's Runtime.CacheDir, which DefaultRuntime derives from the exact
// same paths.EnvHome rule — uses this instead of re-deriving the directory
// independently by reading the environment a second time, so the
// "cc-usage-" naming stays owned in exactly one place.
func UsageCacheDir(base string, uid int) string {
	return filepath.Join(base, "cc-usage-"+strconv.Itoa(uid))
}

// DefaultCacheDir returns the shared usage cache directory every caller gets
// when it doesn't override Options.CacheDir: PFM_HOME-anchored exactly like
// every other jail override in this package, uid-scoped otherwise. The
// Limits tab (`stats.LimitsSampler`) resolves the SAME directory so it reads
// and writes the one file this hook already owns instead of keeping a
// second, per-process cache.
func DefaultCacheDir() string {
	if jailHome := os.Getenv(paths.EnvHome); jailHome != "" {
		return UsageCacheDir(filepath.Join(jailHome, "tmp"), os.Getuid())
	}
	return UsageCacheDir(os.TempDir(), os.Getuid())
}

// CredentialPath is the one filesystem rule for an account's OAuth credential
// file. Callers that must tell "this account has no credentials on disk" apart
// from a provider failure resolve it here rather than rebuilding the join, so
// the rule cannot drift between the prompt hook, the fetch, and the Limits
// tab's own absent-credentials handling.
func CredentialPath(configDir string) string {
	return filepath.Join(configDir, ".credentials.json")
}

// CachePath returns the shared cache file for one Claude account number
// under cacheDir — the exact file this hook's own refresh() reads and
// writes, and the one `stats.LimitsSampler` reads and writes too.
func CachePath(cacheDir string, account int) string {
	return filepath.Join(cacheDir, fmt.Sprintf("acct-%d.json", account))
}

// CachedFableWindow reads pfm's own usage cache for account — the cache this
// hook's own OAuth refresh (and the Limits tab's LimitsSampler) already
// maintain — and runs the exact same fableWindow selector a live fetch uses.
// It is the statusline render path's second source for the Fable window,
// consulted only when a harness payload carried no scoped `limits` array at
// all (see statusline.rateLimits.windowsAt).
//
// It never returns an error: a missing cache, an unreadable or malformed
// file, an active backoff (another picker's already-recorded 429 or other
// failure — treated as "no fresh data", not stale-but-usable), a cache older
// than one hour, or a cache with no qualifying Fable entry all report
// ok=false. One hour matches Evaluate's own "a stale cache remains usable for
// about an hour" bound above, rather than inventing a second number for the
// same file. A statusline read degrades to "window absent" on any of these —
// it must never crash the render or fabricate a reading from a bad file.
func CachedFableWindow(base string, uid, account int, configDir string, now time.Time) (Window, bool) {
	path := CachePath(UsageCacheDir(base, uid), account)
	record, err := ReadCacheRecord(path)
	if err != nil || !record.MatchesConfigDir(configDir) {
		return Window{}, false
	}
	if record.Backoff != nil && now.Before(record.Backoff.RetryAfter) {
		return Window{}, false
	}
	if cacheAge(record, path, now) > time.Hour {
		return Window{}, false
	}
	return record.Usage.fableWindow(now)
}

// CacheBackoff records a rate-limited or otherwise failed fetch so every
// reader of the shared cache — this hook, and every `pfm ls` Limits tab —
// skips its own request until RetryAfter instead of each picker process
// rediscovering the same failure independently.
type CacheBackoff struct {
	Message    string    `json:"message"`
	RetryAfter time.Time `json:"retry_after"`
	RecordedAt time.Time `json:"recorded_at"`
}

// CacheRecord binds shared usage and backoff to a config directory. Legacy
// bare Usage JSON still decodes, but cannot be reused without an identity.
type CacheRecord struct {
	Usage
	ConfigDir string        `json:"config_dir,omitempty"`
	FetchedAt *time.Time    `json:"fetched_at,omitempty"`
	Backoff   *CacheBackoff `json:"backoff,omitempty"`
}

// MatchesConfigDir rejects legacy unbound records and reused account numbers.
func (record CacheRecord) MatchesConfigDir(configDir string) bool {
	return record.ConfigDir != "" && configDir != "" &&
		filepath.Clean(record.ConfigDir) == filepath.Clean(configDir)
}

// ReadCacheRecord decodes the shared cache file at path. A missing or
// unreadable file is reported through the error return — callers treat it as
// "nothing usable cached yet," never as a record with an empty Backoff.
func ReadCacheRecord(path string) (CacheRecord, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return CacheRecord{}, err
	}
	var record CacheRecord
	if err := json.Unmarshal(body, &record); err != nil {
		return CacheRecord{}, fmt.Errorf("decode usage cache %s: %w", path, err)
	}
	return record, nil
}

// WriteCacheRecord atomically writes record to path with the same private
// directory and atomic-rename discipline this hook's own refresh() has
// always used.
func WriteCacheRecord(path string, record CacheRecord) error {
	if err := EnsurePrivateDirectory(filepath.Dir(path)); err != nil {
		return err
	}
	body, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode usage cache %s: %w", path, err)
	}
	return atomicfile.Write(path, body, 0o600)
}

// RateLimitError reports an HTTP 429 from a usage endpoint. RetryAfter is
// whatever the server's own Retry-After header parsed to, or zero if the
// server sent none or an unparseable value — callers apply their own floor.
type RateLimitError struct {
	RetryAfter time.Duration
}

func (err *RateLimitError) Error() string {
	return "usage endpoint returned 429 Too Many Requests"
}

// ParseRetryAfter decodes an HTTP Retry-After header value — either a delay
// in seconds or an HTTP-date — into a duration measured from now. An empty
// or unparseable value returns zero, which callers treat as "the server
// didn't say."
func ParseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil {
		if wait := when.Sub(now); wait > 0 {
			return wait
		}
	}
	return 0
}

func EnsurePrivateDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("usage cache is not a real directory")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Getuid() {
		return fmt.Errorf("usage cache is not owned by this uid")
	}
	return os.Chmod(path, 0o700)
}

func refresh(ctx context.Context, options Options, cachePath string) error {
	credential, err := loadCredential(ctx, options.ConfigDir)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, options.Endpoint, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+credential.OAuth.AccessToken)
	request.Header.Set("anthropic-beta", "oauth-2025-04-20")
	response, err := options.Client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("usage endpoint returned %s", response.Status)
	}
	fresh, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return err
	}
	var decoded usage
	if err := json.Unmarshal(fresh, &decoded); err != nil {
		return err
	}
	logUnknownUsageKeys(fresh, options.Log)
	if decoded.FiveHour.Utilization == nil {
		return fmt.Errorf("usage response omitted five_hour utilization")
	}
	fetchedAt := options.Now()
	return WriteCacheRecord(cachePath, CacheRecord{
		Usage: decoded, ConfigDir: options.ConfigDir, FetchedAt: &fetchedAt,
	})
}

// Fetch reads one account's current OAuth usage without touching the warning
// cache. It is the shared fetch seam for the Limits tab and the prompt hook.
func Fetch(ctx context.Context, options Options) (Usage, error) {
	options = normalize(options)
	credential, err := loadCredential(ctx, options.ConfigDir)
	if err != nil {
		return Usage{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, options.Endpoint, nil)
	if err != nil {
		return Usage{}, fmt.Errorf("build usage request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+credential.OAuth.AccessToken)
	request.Header.Set("anthropic-beta", "oauth-2025-04-20")
	response, err := options.Client.Do(request)
	if err != nil {
		return Usage{}, fmt.Errorf("fetch usage endpoint: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusTooManyRequests {
		return Usage{}, &RateLimitError{RetryAfter: ParseRetryAfter(response.Header.Get("Retry-After"), options.Now())}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Usage{}, fmt.Errorf("usage endpoint returned %s", response.Status)
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
		return Usage{}, fmt.Errorf("usage response omitted five_hour utilization")
	}
	return decoded, nil
}

func logUnknownUsageKeys(body []byte, logger io.Writer) {
	if logger == nil {
		return
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		fmt.Fprintf(logger, "pfm usage-hook: debug: enumerate ignored usage keys: %v\n", err)
		return
	}
	known := map[string]bool{
		"five_hour": true, "seven_day": true, "seven_day_opus": true, "limits": true,
	}
	unknown := make([]string, 0, len(fields))
	for key := range fields {
		if !known[key] {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) == 0 {
		return
	}
	sort.Strings(unknown)
	fmt.Fprintf(logger, "pfm usage-hook: debug: ignored usage keys: %s\n", strings.Join(unknown, ","))
}

func utilization(window usageWindow, fallback int) int {
	if window.Utilization == nil {
		return fallback
	}
	return int(*window.Utilization)
}

// currentUtilization is utilization for a window that still exists. A window
// whose resets_at has already passed describes quota that has since rolled
// over — the same rule fableWindow applies — so it reads as absent and can
// never raise the warning on its own.
func currentUtilization(window usageWindow, now time.Time, fallback int) int {
	if resetPassed(window.ResetsAt, now) {
		return fallback
	}
	return utilization(window, fallback)
}

// resetPassed reports whether this window's resets_at names a moment that has
// already arrived. An empty or unparsable stamp is UNKNOWN, never expired.
func resetPassed(raw string, now time.Time) bool {
	resetAt, err := time.Parse(time.RFC3339, strings.TrimSpace(raw))
	return err == nil && !resetAt.After(now)
}

// cacheAge is how long ago this record's PAYLOAD was fetched, not how long ago
// the file was touched. Any writer that records only a backoff carries the
// previous fetched_at forward while bumping mtime (internal/stats: a 429
// recorded against a last-good payload), and an mtime-only reading would
// revive hours-old usage as fresh. The file's mtime is the fallback for
// identity-bound records that predate fetched_at, and for a missing file.
//
// A fetched_at stamped AFTER now is unverifiable — corruption or clock skew —
// and trusting it would make the cache permanently fresh: never refreshed,
// never aged out of the warning. It is refused the same way internal/stats'
// cacheFresh refuses a confirmedAt that is After(now), and the record falls
// back to the file's mtime.
func cacheAge(record CacheRecord, path string, now time.Time) time.Duration {
	if record.FetchedAt != nil && !record.FetchedAt.After(now) {
		return now.Sub(*record.FetchedAt)
	}
	return fileAge(path, now)
}

func fileAge(path string, now time.Time) time.Duration {
	info, err := os.Stat(path)
	if err != nil {
		return 100 * 365 * 24 * time.Hour
	}
	return now.Sub(info.ModTime())
}

// recoveredWindowPhrase states one window's standing in the recovery banner.
// currentUtilization collapses a window whose resets_at has already passed to
// the caller's fallback, and this banner's fallback is 0 — so a bare "5h 0%%"
// here would assert a MEASURED zero when the truth is "the window rolled over
// and we have not re-asked yet". That is the same fabrication windowPhrase
// refuses on the warn line, and an expired window is precisely what drives the
// recovery branch (it drags `maximum` under the warn threshold), so this is
// the banner's likely reading, not a corner of it.
func recoveredWindowPhrase(label string, window usageWindow, percent int, now time.Time) string {
	if resetPassed(window.ResetsAt, now) {
		return label + " — (reset passed · awaiting refetch)"
	}
	return fmt.Sprintf("%s %d%%", label, percent)
}

// windowPhrase is one window's clause in the hook's warn line. A window whose
// resets_at has already passed describes quota that has since rolled over —
// currentUtilization refuses to count it toward the warning, so the clause must
// not advertise its stale percentage or a reset moment already in the past
// either. It reads "5h — (reset passed)" until a refetch lands.
func windowPhrase(label string, window usageWindow, percent int, now time.Time, layout string) string {
	if resetPassed(window.ResetsAt, now) {
		return label + " — (reset passed)"
	}
	return fmt.Sprintf("%s %d%% (resets %s)", label, percent, formatReset(window.ResetsAt, now, layout))
}

func formatReset(raw string, now time.Time, layout string) string {
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return "?"
	}
	return parsed.In(now.Location()).Format(layout)
}

func envInt(name string, fallback int) int {
	value, err := strconv.Atoi(os.Getenv(name))
	if err != nil {
		return fallback
	}
	return value
}

func max(values ...int) int {
	maximum := values[0]
	for _, value := range values[1:] {
		if value > maximum {
			maximum = value
		}
	}
	return maximum
}
