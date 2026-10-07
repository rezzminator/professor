package statusline

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// cacheWindowSegment renders the prompt cache's time left and the share of
// the last call's prompt read from it: 💾1h✓59m:28s 94%. The window's length
// comes first from Claude Code's own prompt_cache (its request clock and TTL);
// when the payload carries no TTL the launch record decides it, never the
// statusline's own environment; a session pfm never launched falls to the
// transcript's newest cache write (usage.cache_creation), else no marker. The
// countdown runs to the payload's expiry, else from the transcript's newest
// request. A failed launch read renders 💾⚠ with its cause on stderr, and a
// machine config that failed to load 💾⚠config: the launch record lives where
// the config's state.db says. hit < 0 means the harness reported no usage yet,
// and drops the tail; a lapsed window renders the hit as history, was 99%.
func cacheWindowSegment(
	runtime Runtime,
	now time.Time,
	transcriptPath string,
	hit int,
	harness *promptCache,
	sessionID string,
) string {
	label := harness.ttlLabel()
	var text string
	var lapsed bool
	if harness.expires() && label != "" {
		text, lapsed = countdownText(label, time.Unix(*harness.ExpiresAt, 0), now)
		return withHit(text, hit, lapsed)
	}
	launch, found, failure := launchCache(runtime, sessionID)
	if failure != "" {
		return failure
	}
	switch {
	case found && harness.expires():
		text, lapsed = countdownText(launchLabel(launch), time.Unix(*harness.ExpiresAt, 0), now)
	case found:
		text, lapsed = launchWindowText(runtime, now, transcriptPath, launch.Cache1H)
	default:
		var ok bool
		text, lapsed, ok = unlaunchedWindowText(runtime, now, transcriptPath, harness)
		if !ok {
			return ""
		}
	}
	return withHit(text, hit, lapsed)
}

// withHit joins the window and the last call's hit share into the segment.
func withHit(text string, hit int, lapsed bool) string {
	if hit < 0 {
		return sep + text
	}
	return sep + text + " " + cacheHitText(hit, lapsed)
}

// launchCache reads the session's launch record from the state database the
// config resolved (PFM_STATE_DB still overrides it). found is false for a
// session pfm never launched, or none named; a non-empty failure is the
// rendered warning for a read that failed, its cause already on stderr.
func launchCache(runtime Runtime, sessionID string) (launch fleetdb.Launch, found bool, failure string) {
	if sessionID == "" {
		return fleetdb.Launch{}, false, ""
	}
	if runtime.ConfigError != nil {
		return fleetdb.Launch{}, false, sep + cBad + "💾⚠config" + reset
	}
	stateDB := runtime.getenv(paths.EnvStateDB)
	if stateDB == "" {
		stateDB = runtime.StateDB
	}
	if stateDB == "" {
		stateDB = paths.DefaultStateDB(runtime.Home)
	}
	launches, err := fleetdb.OpenLaunches(context.Background(), paths.Values{StateDB: stateDB})
	if err == nil {
		defer func() {
			if closeErr := launches.Close(); closeErr != nil {
				fmt.Fprintf(os.Stderr, "statusline: close launch record: %v\n", closeErr)
			}
		}()
		launch, err = launches.LaunchFor(context.Background(), sessionID)
	}
	if errors.Is(err, fleetdb.ErrNoLaunch) {
		return fleetdb.Launch{}, false, ""
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "statusline: launch record %s: %v\n", sessionID, err)
		return fleetdb.Launch{}, false, sep + cBad + "💾⚠" + reset
	}
	return launch, true, ""
}

func launchLabel(launch fleetdb.Launch) string {
	if launch.Cache1H {
		return "1h"
	}
	return "5m"
}

// promptCache is the statusline payload's prompt_cache object: Claude Code
// measures the cache from its own requests, so its expiry needs no guessing.
type promptCache struct {
	TTL       string `json:"ttl"`
	ExpiresAt *int64 `json:"expires_at"`
}

// expires reports whether the payload carries an expiry; it does not before
// the first cached request, or on a build without the field.
func (cache *promptCache) expires() bool {
	return cache != nil && cache.ExpiresAt != nil
}

// ttlLabel is the payload's own window length, "" when it names none.
func (cache *promptCache) ttlLabel() string {
	if cache == nil {
		return ""
	}
	return strings.TrimSpace(cache.TTL)
}

// launchWindowText renders a launched chat's window: the launch record's
// length, counted from the transcript's newest request, and whether it has
// lapsed; an unmeasurable window never counts as lapsed.
func launchWindowText(runtime Runtime, now time.Time, transcriptPath string, cache1h bool) (string, bool) {
	ttl := 5 * time.Minute
	label := "5m"
	if cache1h {
		ttl = time.Hour
		label = "1h"
	}
	// A transcript we could not read is NOT a chat without a cache window, and
	// the two must never share a rendering. Returning "" here made the segment
	// disappear, which is indistinguishable from a statusline that has no cache
	// timer at all — so the one state worth shouting about, a chat running with
	// transcript saving off, arrived as silence. That chat cannot be resumed and
	// its window cannot be measured; the statusline is where the user finds out.
	//
	// "!" is deliberately not "?": "?" means the transcript WAS read and simply
	// carries no user turn to anchor on, which is a fact about the chat. "!" is
	// a fact about us — we could not look.
	window, readable := transcriptWindow(runtime, transcriptPath)
	if !readable {
		return cBad + "💾" + label + "!" + reset, false
	}
	if window.anchor.IsZero() {
		if label == "1h" {
			return cWarn + "💾1h∞" + reset, false
		}
		return cWarn + "💾" + label + "?" + reset, false
	}
	return countdownText(label, window.anchor.Add(ttl), now)
}

// unlaunchedWindowText renders the window of a session pfm never launched:
// the length its transcript's newest cache write used, counted to the
// payload's expiry or from the newest request. ok is false when nothing names
// a length — no marker. A named transcript that cannot be read is our failure
// to look, 💾!, never an absent segment; a payload without one names nothing.
func unlaunchedWindowText(
	runtime Runtime,
	now time.Time,
	transcriptPath string,
	harness *promptCache,
) (string, bool, bool) {
	if transcriptPath == "" {
		return "", false, false
	}
	window, readable := transcriptWindow(runtime, transcriptPath)
	if !readable {
		return cBad + "💾!" + reset, false, true
	}
	var label string
	switch window.ttl {
	case time.Hour:
		label = "1h"
	case 5 * time.Minute:
		label = "5m"
	default:
		return "", false, false
	}
	if harness.expires() {
		text, lapsed := countdownText(label, time.Unix(*harness.ExpiresAt, 0), now)
		return text, lapsed, true
	}
	if window.anchor.IsZero() {
		return cWarn + "💾" + label + "?" + reset, false, true
	}
	text, lapsed := countdownText(label, window.anchor.Add(window.ttl), now)
	return text, lapsed, true
}

// transcriptWindow reads the main chat's cache window from its transcript,
// through the per-transcript anchor cache keyed on its size and mtime.
// readable is false when the transcript is unnamed, missing or a directory.
func transcriptWindow(runtime Runtime, transcriptPath string) (cacheWindow, bool) {
	if transcriptPath == "" {
		return cacheWindow{}, false
	}
	info, err := os.Stat(transcriptPath)
	if err != nil || info.IsDir() {
		return cacheWindow{}, false
	}
	cachePath := filepath.Join(
		runtime.CacheDir,
		"cc-sl-anchor-"+strings.TrimSuffix(filepath.Base(transcriptPath), ".jsonl"),
	)
	key := fmt.Sprintf("v2:%d:%d", info.ModTime().Unix(), info.Size())
	cachedKey, window := readAnchorCache(cachePath)
	if cachedKey != key {
		window = cacheAnchor(transcriptPath)
		_ = atomicfile.Write(cachePath, []byte(key+" "+window.encode()), 0o600)
	}
	return window, true
}

// countdownText renders a live or lapsed cache window, 💾1h✓59m:28s or
// 💾5m✗2m:0s, and reports whether it has lapsed.
func countdownText(label string, expires, now time.Time) (string, bool) {
	remaining := expires.Sub(now)
	if remaining > 0 {
		return cGood + "💾" + label + "✓" + formatCacheTime(remaining, false) + reset, false
	}
	return cBad + "💾" + label + "✗" + formatCacheTime(-remaining, true) + reset, true
}

// agentCacheText is a sub-agent row's cache segment in the main line's shape —
// 💾5m✓3m:8s 94% — measured from the agent's own transcript: the length its
// newest cache write used, counted from its newest request. The row payload
// carries no prompt_cache per agent. hit < 0 (no reply yet) renders 💾–.
func agentCacheText(transcript string, hit int, now time.Time) string {
	if hit < 0 {
		return cMuted + "💾–" + reset
	}
	window := cacheAnchorIn(transcript, true)
	label := "?"
	ttl := 5 * time.Minute
	switch window.ttl {
	case time.Hour:
		label, ttl = "1h", time.Hour
	case 5 * time.Minute:
		label = "5m"
	}
	text, lapsed := cWarn+"💾"+label+"?"+reset, false
	if !window.anchor.IsZero() {
		text, lapsed = countdownText(label, window.anchor.Add(ttl), now)
	}
	return text + " " + cacheHitText(hit, lapsed)
}

// cacheHitText renders the share of one call's prompt read from the cache:
// 94%. Green from 80, yellow from 50, red below — the context re-written.
// The figure only changes when a call completes, so once the window has
// lapsed it describes a warm cache that is gone: it renders muted as
// was 94%, never as live health beside the expiry.
func cacheHitText(percent int, lapsed bool) string {
	if lapsed {
		return cMuted + fmt.Sprintf("was %d%%", percent) + reset
	}
	color := cBad
	switch {
	case percent >= 80:
		color = cGood
	case percent >= 50:
		color = cWarn
	}
	return color + fmt.Sprintf("%d%%", percent) + reset
}

// cacheHitPercent is cache reads over the whole prompt; -1 for no prompt.
func cacheHitPercent(read, created, uncached int64) int {
	total := read + created + uncached
	if total <= 0 {
		return -1
	}
	return int(read * 100 / total)
}

// cacheWindow carries the transcript's newest request anchor and the length
// its newest cache write used: a sub-agent row and a session pfm never
// launched take their window from it; a launched chat uses its launch record.
type cacheWindow struct {
	anchor time.Time
	ttl    time.Duration
}

func (window cacheWindow) encode() string {
	anchor := "-"
	if !window.anchor.IsZero() {
		anchor = strconv.FormatInt(window.anchor.Unix(), 10)
	}
	return anchor + " " + strconv.FormatInt(int64(window.ttl/time.Second), 10)
}

func readAnchorCache(path string) (string, cacheWindow) {
	body, err := os.ReadFile(path)
	if err != nil {
		return "", cacheWindow{}
	}
	fields := strings.Fields(string(body))
	if len(fields) != 3 {
		return "", cacheWindow{}
	}
	ttl, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil {
		return "", cacheWindow{}
	}
	window := cacheWindow{ttl: time.Duration(ttl) * time.Second}
	if fields[1] == "-" {
		return fields[0], window
	}
	epoch, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return "", cacheWindow{}
	}
	window.anchor = time.Unix(epoch, 0)
	return fields[0], window
}

// cacheAnchor reads the main chat's cache window from its transcript tail.
// The anchor is the newest record that sent an API request: a user record — a
// prompt or a tool result — other than a local slash command's transcript
// echo. The request is sent the moment that record is written; the assistant
// record that answers it is stamped only after the reply streamed, so
// anchoring on it overstated the time left by the reply's length. Local
// commands (/rc, /cost, the /compact receipt …) write user-typed records
// without making a request, and anchoring on them showed a cache twelve hours
// cold as "expired 9m ago". Sidechains refresh their own cache, never the main
// chat's. A transcript with no such record (a Codex rollout) anchors on its
// newest reply instead. The ttl is the lifetime the newest cache write used,
// from its usage.cache_creation breakdown.
func cacheAnchor(path string) cacheWindow {
	return cacheAnchorIn(path, false)
}

// cacheAnchorIn reads a cache window from a transcript; sidechain admits the
// records a sub-agent's own transcript is made of.
func cacheAnchorIn(path string, sidechain bool) cacheWindow {
	var replied cacheWindow
	for _, size := range []int64{65_536, 1_048_576} {
		body, err := readTail(path, size)
		if err != nil {
			continue
		}
		var window cacheWindow
		scanner := bufio.NewScanner(strings.NewReader(string(body)))
		scanner.Buffer(make([]byte, 64*1024), int(size)+1)
		for scanner.Scan() {
			var record struct {
				Type      string `json:"type"`
				Sidechain bool   `json:"isSidechain"`
				Timestamp string `json:"timestamp"`
				Message   struct {
					Content json.RawMessage `json:"content"`
					Usage   struct {
						CacheCreation struct {
							FiveMinutes int64 `json:"ephemeral_5m_input_tokens"`
							OneHour     int64 `json:"ephemeral_1h_input_tokens"`
						} `json:"cache_creation"`
					} `json:"usage"`
				} `json:"message"`
			}
			if json.Unmarshal(scanner.Bytes(), &record) != nil || record.Sidechain && !sidechain {
				continue
			}
			switch record.Type {
			case "assistant":
				switch created := record.Message.Usage.CacheCreation; {
				case created.OneHour > 0:
					window.ttl = time.Hour
				case created.FiveMinutes > 0:
					window.ttl = 5 * time.Minute
				}
				if parsed, parseErr := time.Parse(time.RFC3339Nano, record.Timestamp); parseErr == nil &&
					parsed.After(replied.anchor) {
					replied.anchor = parsed
				}
				continue
			case "user":
				if record.Timestamp == "" || localCommandRecord(record.Message.Content) {
					continue
				}
			default:
				continue
			}
			parsed, parseErr := time.Parse(time.RFC3339Nano, record.Timestamp)
			if parseErr == nil && parsed.After(window.anchor) {
				window.anchor = parsed
			}
		}
		if !window.anchor.IsZero() {
			return window
		}
		replied.ttl = window.ttl
	}
	return replied
}

// localCommandRecord recognises the transcript echo of a local slash command:
// string content opening with one of Claude Code's local-command tags. Array
// content is a real turn (tool results, content blocks) and always counts.
func localCommandRecord(content json.RawMessage) bool {
	var text string
	if json.Unmarshal(content, &text) != nil {
		return false
	}
	trimmed := strings.TrimSpace(text)
	return strings.HasPrefix(trimmed, "<local-command-") || strings.HasPrefix(trimmed, "<command-name>")
}
