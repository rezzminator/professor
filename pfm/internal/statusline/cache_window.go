package statusline

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
)

// cacheWindowSegment renders the prompt cache's time left and what the last
// call wrote to it: 💾1h✓59m:28s +4.2K. The window's length is the one the
// newest cache write actually used (usage.cache_creation), not an assumption;
// the environment decides it only for a transcript that records none.
// written < 0 means the harness reported no usage yet, and drops the tail.
func cacheWindowSegment(runtime Runtime, now time.Time, transcriptPath string, written int64) string {
	window := sep + cacheWindowText(runtime, now, transcriptPath)
	if written < 0 {
		return window
	}
	return window + " " + cacheWriteText(written)
}

func cacheWindowText(runtime Runtime, now time.Time, transcriptPath string) string {
	ttl := time.Hour
	label := "1h"
	if runtime.getenv("FORCE_PROMPT_CACHING_5M") == "1" {
		ttl = 5 * time.Minute
		label = "5m"
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
	if transcriptPath == "" {
		return cBad + "💾" + label + "!" + reset
	}
	info, err := os.Stat(transcriptPath)
	if err != nil || info.IsDir() {
		return cBad + "💾" + label + "!" + reset
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
	if window.ttl > 0 {
		ttl = window.ttl
		label = "1h"
		if ttl == 5*time.Minute {
			label = "5m"
		}
	}
	if window.anchor.IsZero() {
		if label == "1h" {
			return cWarn + "💾1h∞" + reset
		}
		return cWarn + "💾" + label + "?" + reset
	}
	remaining := ttl - now.Sub(window.anchor)
	if remaining > 0 {
		return cGood + "💾" + label + "✓" + formatCacheTime(remaining, false) + reset
	}
	return cBad + "💾" + label + "✗" + formatCacheTime(-remaining, true) + reset
}

// cacheWriteText renders the tokens one call wrote to the prompt cache — the
// part of its prompt that was new since the call before, paid at the write
// price: +4.2K. Green under 20K (an ordinary step), yellow under 100K (a big
// tool result or a first call), red above (the context re-written: the window
// lapsed or its opening changed).
func cacheWriteText(tokens int64) string {
	color := cGood
	switch {
	case tokens >= 100_000:
		color = cBad
	case tokens >= 20_000:
		color = cWarn
	}
	return color + "+" + formatContextTokens(tokens) + reset
}

// cacheWindow is what the transcript says about the main chat's prompt cache:
// the moment it was last used (anchor) and the lifetime of its newest write
// (ttl; 0 when no record carries the breakdown).
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
			if json.Unmarshal(scanner.Bytes(), &record) != nil || record.Sidechain {
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
