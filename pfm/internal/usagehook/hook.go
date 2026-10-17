// Package usagehook implements the fail-open UserPromptSubmit usage warning.
package usagehook

import (
	"context"
	"encoding/json"
	"errors"
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

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

const (
	defaultEndpoint  = "https://api.anthropic.com/api/oauth/usage"
	fiveHourKey      = "five_hour"
	sevenDayKey      = "seven_day"
	sevenDayFableKey = "seven_day_fable"
)

// Options is the complete, jail-replaceable hook environment.
type Options struct {
	Clock       clock.Clock
	Env         paths.Env
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
	// Version is the running pfm's version, named in a usage request's
	// User-Agent as `pfm/{Version}` only when the account's Claude Code
	// version is unknown (Options.userAgent).
	Version string
	// ClaudeBinary is the account's Claude Code binary, asked for its version
	// (claudeCodeVersion) when a request is about to go out; empty means `claude`.
	ClaudeBinary string
	// BypassBackoff, when set, is a caller's one authorized live retry: a
	// fresh cache no longer answers, and neither does an active backoff whose
	// replayed error it accepts (a credential failure the caller just repaired).
	BypassBackoff func(error) bool
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
	{Key: fiveHourKey, Label: "5h", Known: true},
	{Key: sevenDayKey, Label: "7d", Known: true},
	{Key: sevenDayFableKey, Label: "7d-fable", Known: true},
}

// AllWindows returns canonical windows, including absent ones, so renderers
// can distinguish a measured zero from a missing reading.
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
	return usage.NamedWindowsAt(clock.Real.Now())
}

// NamedWindowsAt is the deterministic form used by samplers and renderers that
// already own a clock.
func (usage Usage) NamedWindowsAt(now time.Time) []NamedWindow {
	windows := map[string]Window{
		fiveHourKey: usage.FiveHour,
		sevenDayKey: usage.SevenDay,
	}
	if fable, ok := usage.fableWindow(now); ok {
		windows[sevenDayFableKey] = fable
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
	window, ok, _ := usage.selectFableWindow(now, false)
	return window, ok
}

// FableWindowAt keeps unknown resets and reports malformed ones to renderers.
func (usage Usage) FableWindowAt(now time.Time) (Window, bool, error) {
	return usage.selectFableWindow(now, true)
}

func (usage Usage) selectFableWindow(now time.Time, allowUnknown bool) (Window, bool, error) {
	var fallback *ScopedLimit
	var invalidReset error
	for index := range usage.Limits {
		limit := &usage.Limits[index]
		if strings.TrimSpace(limit.Kind) != "weekly_scoped" ||
			!strings.EqualFold(strings.TrimSpace(limit.Scope.Model.DisplayName), "Fable") ||
			limit.Percent == nil {
			continue
		}
		stamp := strings.TrimSpace(limit.ResetsAt)
		if stamp == "" && !allowUnknown {
			continue
		}
		if stamp != "" {
			resetAt, err := time.Parse(time.RFC3339, stamp)
			if err != nil {
				invalidReset = fmt.Errorf("decode cached Fable reset %q: %w", limit.ResetsAt, err)
				continue
			}
			if !resetAt.After(now) {
				continue
			}
		}
		if limit.IsActive {
			return Window{Utilization: limit.Percent, ResetsAt: limit.ResetsAt}, true, nil
		}
		if fallback == nil {
			fallback = limit
		}
	}
	if fallback == nil {
		return Window{}, false, invalidReset
	}
	return Window{Utilization: fallback.Percent, ResetsAt: fallback.ResetsAt}, true, nil
}

type usageWindow = Window

type credentials struct {
	OAuth struct {
		AccessToken string `json:"accessToken"`
		// ExpiresAt is the access token's expiry, Unix milliseconds; 0 when
		// the credential carries none.
		ExpiresAt int64 `json:"expiresAt"`
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
	if err := EnsurePrivateDir(options.CacheDir); err != nil {
		return "", err
	}
	now := options.Now()
	cached, confirmedAt, err := Fetch(ctx, options, account)
	if err != nil {
		fmt.Fprintf(options.Log, "pfm usage-hook: refresh failed; trying stale cache: %v\n", err)
	}
	if confirmedAt.IsZero() || now.Sub(confirmedAt) > StaleHorizon {
		return "", nil
	}
	five := currentUtilization(cached.FiveHour, now, 0)
	seven := currentUtilization(cached.SevenDay, now, 0)
	opus := currentUtilization(cached.SevenOpus, now, -1)
	fable := -1
	for _, named := range cached.NamedWindowsAt(now) {
		if named.Key == sevenDayFableKey {
			fable = utilization(named.Window, -1)
			break
		}
	}
	maximum := maximumInt(five, seven, opus, fable)
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
	if options.Clock == nil {
		options.Clock = clock.Real
	}
	if options.Env == nil {
		options.Env = paths.OSEnv{}
	}
	if options.Now == nil {
		options.Now = options.Clock.Now
	}
	if options.Home == "" {
		options.Home = options.Env.Get(paths.EnvHome)
		if options.Home == "" {
			options.Home, _ = options.Env.Home()
		}
	}
	if options.ConfigDir == "" {
		options.ConfigDir = options.Env.Get("CLAUDE_CONFIG_DIR")
		if options.ConfigDir == "" {
			options.ConfigDir = filepath.Join(options.Home, ".claude")
		}
	}
	if options.CacheDir == "" {
		options.CacheDir = cacheDirForEnv(options.Env)
	}
	if options.Warn <= 0 {
		options.Warn = envInt(options.Env, "CC_USAGE_WARN", 80)
	}
	if options.Critical <= 0 {
		options.Critical = envInt(options.Env, "CC_USAGE_CRIT", 95)
	}
	if options.TTL <= 0 {
		options.TTL = time.Duration(envInt(options.Env, "CC_USAGE_TTL", 180)) * time.Second
	}
	if options.Client == nil {
		options.Client = &http.Client{Timeout: 6 * time.Second}
	}
	options.Client = obs.WrapClient(options.Client)
	if options.Endpoint == "" {
		options.Endpoint = defaultEndpoint
	}
	if options.Log == nil {
		options.Log = io.Discard
	}
	if options.Version == "" {
		options.Version = pfmconfig.DevelopmentVersion
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
	return cacheDirForEnv(paths.OSEnv{})
}

// CacheDirFor is DefaultCacheDir under env, a caller's host-environment seam
// (nil is the real process environment).
func CacheDirFor(env paths.Env) string {
	if env == nil {
		env = paths.OSEnv{}
	}
	return cacheDirForEnv(env)
}

func cacheDirForEnv(env paths.Env) string {
	return UsageCacheDir(tempBase(env.Get(paths.EnvHome)), os.Getuid())
}

// tempBase is the host temp directory, or a PFM_HOME jail's own tmp.
func tempBase(jailHome string) string {
	if jailHome != "" {
		return filepath.Join(jailHome, "tmp")
	}
	return os.TempDir()
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

// RefreshLockPath is the cross-process refresh lock beside CachePath: the process
// that creates it with O_EXCL is the one allowed to request this account.
func RefreshLockPath(cacheDir string, account int) string {
	return filepath.Join(cacheDir, fmt.Sprintf("acct-%d.lock", account))
}

// CacheBackoff shares failed-fetch cooldowns across readers until RetryAfter.
// Credential fingerprints let changed credentials lift the cooldown; Refusal
// preserves typed 401/403 failures when replayed.
type CacheBackoff struct {
	Message    string       `json:"message"`
	RetryAfter time.Time    `json:"retry_after"`
	RecordedAt time.Time    `json:"recorded_at"`
	Credential string       `json:"credential,omitempty"`
	Refusal    *StatusError `json:"refusal,omitempty"`
}

// replay is the failure the backoff recorded: the typed refusal when it kept
// one, the message otherwise.
func (backoff *CacheBackoff) replay() error {
	if backoff.Refusal != nil {
		refusal := *backoff.Refusal
		return &refusal
	}
	return errors.New(backoff.Message)
}

// CacheRecord binds shared usage and backoff to a config directory. Legacy
// bare Usage JSON still decodes, but cannot be reused without an identity.
type CacheRecord struct {
	Usage
	ConfigDir string        `json:"config_dir,omitempty"`
	FetchedAt *time.Time    `json:"fetched_at,omitempty"`
	Backoff   *CacheBackoff `json:"backoff,omitempty"`
	// Unchanged counts the fetches in a row that read exactly what the one
	// before them read; it stretches the record's freshness (FreshFor).
	Unchanged int `json:"unchanged,omitempty"`
}

// adaptiveCeiling bounds how far an idle account's freshness stretches: the
// careful pollers keep 90-300 s between requests and slow down when idle
// (RR claude-oauth-usage-429-pollers § 5.5), so an account whose readings stop
// moving is asked at most every five minutes, and the first changed reading
// brings the caller's own interval back.
const adaptiveCeiling = 5 * time.Minute

// FreshFor is how long the record answers without a request for a caller
// whose interval is ttl: ttl doubled per unchanged fetch in a row, up to
// adaptiveCeiling, never below ttl.
func (record CacheRecord) FreshFor(ttl time.Duration) time.Duration {
	fresh := ttl
	for range min(record.Unchanged, 8) {
		if fresh >= adaptiveCeiling {
			break
		}
		fresh *= 2
	}
	return max(ttl, min(fresh, adaptiveCeiling))
}

// MatchesConfigDir rejects legacy unbound records and reused account numbers.
func (record CacheRecord) MatchesConfigDir(configDir string) bool {
	return record.ConfigDir != "" && configDir != "" &&
		filepath.Clean(record.ConfigDir) == filepath.Clean(configDir)
}

// ReadCacheRecord decodes a bounded, owned cache file; read failures are errors.
func ReadCacheRecord(path string) (CacheRecord, error) {
	record, _, err := ReadCacheRecordWithMtime(path)
	return record, err
}

// ReadCacheRecordWithMtime binds legacy confirmation age to the bytes read.
// Only bounded regular files in an owned, non-writable cache directory qualify.
func ReadCacheRecordWithMtime(path string) (CacheRecord, time.Time, error) {
	dir := filepath.Dir(path)
	info, err := os.Lstat(dir)
	if err != nil {
		return CacheRecord{}, time.Time{}, err
	}
	stat, owned := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || !owned || int(stat.Uid) != os.Getuid() || info.Mode().Perm()&0o022 != 0 {
		return CacheRecord{}, time.Time{}, fmt.Errorf("usage cache directory %s is not a private owned directory", dir)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return CacheRecord{}, time.Time{}, err
	}
	defer func() { _ = root.Close() }()
	opened, err := root.Stat(".")
	if err != nil {
		return CacheRecord{}, time.Time{}, fmt.Errorf("stat opened usage cache directory %s: %w", dir, err)
	}
	if !os.SameFile(info, opened) {
		return CacheRecord{}, time.Time{}, fmt.Errorf("usage cache directory %s changed while opening", dir)
	}
	leaf, err := root.Lstat(filepath.Base(path))
	if err != nil {
		return CacheRecord{}, time.Time{}, err
	}
	if !leaf.Mode().IsRegular() {
		return CacheRecord{}, time.Time{}, fmt.Errorf("usage cache %s is not a regular leaf", path)
	}
	file, err := root.OpenFile(filepath.Base(path), os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return CacheRecord{}, time.Time{}, err
	}
	defer func() { _ = file.Close() }()
	openedLeaf, err := file.Stat()
	if err != nil {
		return CacheRecord{}, time.Time{}, err
	}
	if !os.SameFile(leaf, openedLeaf) {
		return CacheRecord{}, time.Time{}, fmt.Errorf("usage cache %s changed while opening", path)
	}
	return readCacheRecordFile(file)
}

func readCacheRecordFile(file *os.File) (CacheRecord, time.Time, error) {
	info, err := file.Stat()
	if err != nil {
		return CacheRecord{}, time.Time{}, fmt.Errorf("stat usage cache %s: %w", file.Name(), err)
	}
	stat, owned := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || !owned || int(stat.Uid) != os.Getuid() || info.Mode().Perm()&0o022 != 0 {
		return CacheRecord{}, time.Time{}, fmt.Errorf("usage cache %s is not an owned regular file", file.Name())
	}
	const maxCacheBytes = 1 << 20
	if info.Size() > maxCacheBytes {
		return CacheRecord{}, time.Time{}, fmt.Errorf("usage cache %s exceeds %d bytes", file.Name(), maxCacheBytes)
	}
	body, err := io.ReadAll(io.LimitReader(file, maxCacheBytes+1))
	if err != nil {
		return CacheRecord{}, time.Time{}, fmt.Errorf("read usage cache %s: %w", file.Name(), err)
	}
	if len(body) > maxCacheBytes {
		return CacheRecord{}, time.Time{}, fmt.Errorf("usage cache %s exceeds %d bytes", file.Name(), maxCacheBytes)
	}
	var record CacheRecord
	if err := json.Unmarshal(body, &record); err != nil {
		return CacheRecord{}, time.Time{}, fmt.Errorf("decode usage cache %s: %w", file.Name(), err)
	}
	return record, info.ModTime(), nil
}

// WriteCacheRecord atomically publishes a private cache record.
func WriteCacheRecord(path string, record CacheRecord) error {
	if err := EnsurePrivateDir(filepath.Dir(path)); err != nil {
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
	// ErrorType is the body's error type ("rate_limit_error"), empty when
	// the body named none.
	ErrorType string
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

func EnsurePrivateDir(path string) error {
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
		fiveHourKey: true, sevenDayKey: true, "seven_day_opus": true, "limits": true,
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

// currentUtilization excludes windows whose known reset has passed.
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

// cacheAge measures payload age, not a backoff writer's newer file mtime.
// Legacy or future-dated records fall back to mtime, matching existing hook
// behavior; the statusline rejects explicit future stamps instead.
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

// recoveredWindowPhrase distinguishes an expired reading from measured zero.
func recoveredWindowPhrase(label string, window usageWindow, percent int, now time.Time) string {
	if resetPassed(window.ResetsAt, now) {
		return label + " — (reset passed · awaiting refetch)"
	}
	return fmt.Sprintf("%s %d%%", label, percent)
}

// windowPhrase renders a reading or marks its known reset as passed.
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

func envInt(env paths.Env, name string, fallback int) int {
	value, err := strconv.Atoi(env.Get(name))
	if err != nil {
		return fallback
	}
	return value
}

func maximumInt(values ...int) int {
	maximum := values[0]
	for _, value := range values[1:] {
		if value > maximum {
			maximum = value
		}
	}
	return maximum
}
