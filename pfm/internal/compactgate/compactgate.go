// Package compactgate owns `pfm internal compact-gate`, the Claude Code
// PreCompact hook that holds each compacting party — the main chat or one of
// its sub-agents — to its own auto-compact threshold
// (config.ClaudePrefs.AutoCompactMain / AutoCompactSubagent). Claude Code has
// one compaction window for a chat and every sub-agent it spawns, fires
// automatic compaction well below that window, and names no agent in the hook
// payload, so the party is inferred from which transcript was written last and
// its size estimated from the transcript itself. Exit 2 blocks the compaction
// with one line on stderr; every other outcome — a manual compaction, unset
// thresholds, any read failure — exits 0 and lets it run: the gate never
// blocks on an error.
package compactgate

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

const (
	// activeWindow is how recently a sub-agent transcript must have been
	// written for its sub-agent to count as running.
	activeWindow = 60 * time.Second
	// bytesPerToken converts the transcript bytes written after the last
	// recorded usage (tool results the model has not been billed for yet)
	// into an estimated token count. Measured live on Claude Code 2.1.281
	// (2026-09-24): an unbilled transcript tail runs 8.27 and 8.44 bytes per
	// token (a main chat and a sub-agent, each against Claude Code's own
	// preTokens at compaction) — a transcript line carries the tool result
	// plus its JSON envelope, so 4 overestimated the context by 10–15%.
	bytesPerToken = 8
	// blockExit is Claude Code's blocking hook exit code.
	blockExit = 2
	hookName  = "compact-gate"
	// settleQuiet, settleStep and settleCap bound the wait for Claude Code to
	// finish writing the compacting party's transcript: a PreCompact hook can
	// run while the tool results that pushed the party over its trigger are
	// still being flushed (observed live: ~79K on disk at hook time, 173K six
	// seconds later), so the estimate waits for settleQuiet of no writes,
	// checking every settleStep, for at most settleCap.
	settleQuiet = time.Second
	settleStep  = 250 * time.Millisecond
	settleCap   = 3 * time.Second
)

// gatePayload is the part of Claude Code's PreCompact hook input the gate
// reads; transcript_path is always the MAIN chat's transcript.
type gatePayload struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	Trigger        string `json:"trigger"`
}

// party is who is compacting: its name for the block reason, the transcript
// its size is estimated from, and the threshold it is held to.
type party struct {
	name       string
	transcript string
	threshold  int
}

// GateCompaction reads one PreCompact payload from stdin and returns the hook's
// exit code: blockExit, with a one-line reason on stderr, when an automatic
// compaction's party is below its threshold; 0 otherwise. now supplies the
// current time the sub-agent activity window is measured from.
func GateCompaction(
	ctx context.Context,
	stdin io.Reader,
	stderr io.Writer,
	prefs config.ClaudePrefs,
	now clock.Clock,
) int {
	logger := obs.Logger(obs.Component(ctx, "hooks"))
	allow := func(reason string, err error) int {
		attrs := []any{"hook", hookName, "decision", "allow", "reason", reason}
		if err != nil {
			logger.Warn("compact-gate: allowed on a read failure", append(attrs, obs.FieldErr, err.Error())...)
			return 0
		}
		logger.Info("compact-gate: allowed", attrs...)
		return 0
	}
	var payload gatePayload
	if err := json.NewDecoder(stdin).Decode(&payload); err != nil {
		return allow("the PreCompact payload did not decode", err)
	}
	if payload.Trigger != "auto" {
		return allow(fmt.Sprintf("trigger %q is not an automatic compaction", payload.Trigger), nil)
	}
	mainLimit, subLimit, ok := prefs.CompactThresholds()
	if !ok {
		return allow("claude.autoCompactMain and claude.autoCompactSubagent are not both set", nil)
	}
	if strings.TrimSpace(payload.TranscriptPath) == "" {
		return allow("the PreCompact payload names no transcript", errors.New("empty transcript_path"))
	}
	who, err := compactingParty(payload.TranscriptPath, now.Now(), mainLimit, subLimit)
	if err != nil {
		return allow("could not tell who is compacting", err)
	}
	settleTranscript(ctx, now, who.transcript)
	tokens, err := estimateTranscriptTokens(who.transcript)
	if err != nil {
		return allow("could not estimate the "+who.name+"'s tokens", err)
	}
	if tokens >= who.threshold {
		reached := fmt.Sprintf("%s at ~%d tokens reached its %d-token threshold", who.name, tokens, who.threshold)
		return allow(reached, nil)
	}
	reason := fmt.Sprintf(
		"pfm compact-gate: blocked auto-compaction of the %s at ~%d tokens, below its %d-token threshold",
		who.name, tokens, who.threshold,
	)
	logger.Info(
		"compact-gate: blocked",
		"hook", hookName, "decision", "block", "reason", reason, "session", payload.SessionID,
	)
	if _, err := fmt.Fprintln(stderr, reason); err != nil {
		logger.Error("compact-gate: write the block reason", "hook", hookName, obs.FieldErr, err.Error())
	}
	return blockExit
}

// compactingParty applies the party rule: sub-agent transcripts live under
// <main transcript minus .jsonl>/subagents/agent-*.jsonl and are active when
// written within activeWindow of now. With no active sub-agent the main chat
// is compacting; with active ones and a main transcript last written before
// the newest active sub-agent's, that sub-agent is; otherwise the main chat.
func compactingParty(mainPath string, now time.Time, mainLimit, subLimit int) (party, error) {
	mainParty := party{name: "main chat", transcript: mainPath, threshold: mainLimit}
	dir := filepath.Join(strings.TrimSuffix(mainPath, ".jsonl"), "subagents")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return mainParty, nil
	}
	if err != nil {
		return party{}, fmt.Errorf("list sub-agent transcripts in %s: %w", dir, err)
	}
	var newest party
	var newestAt time.Time
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, "agent-") || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		info, err := entry.Info()
		if errors.Is(err, fs.ErrNotExist) {
			continue // removed between the listing and the stat
		}
		if err != nil {
			return party{}, fmt.Errorf("stat sub-agent transcript %s: %w", filepath.Join(dir, name), err)
		}
		written := info.ModTime()
		if now.Sub(written) >= activeWindow || (newest.transcript != "" && !written.After(newestAt)) {
			continue
		}
		newest = party{
			name:       "sub-agent " + strings.TrimSuffix(name, ".jsonl"),
			transcript: filepath.Join(dir, name),
			threshold:  subLimit,
		}
		newestAt = written
	}
	if newest.transcript == "" {
		return mainParty, nil
	}
	mainInfo, err := os.Stat(mainPath)
	if err != nil {
		return party{}, fmt.Errorf("stat main transcript: %w", err)
	}
	if mainInfo.ModTime().Before(newestAt) {
		return newest, nil
	}
	return mainParty, nil
}

// settleTranscript waits, through the clock seam, until the transcript has
// had no write for settleQuiet or settleCap has passed; a stat or wait error
// ends the wait early and the estimate reads what is on disk.
func settleTranscript(ctx context.Context, now clock.Clock, path string) {
	for waited := time.Duration(0); waited < settleCap; waited += settleStep {
		info, err := os.Stat(path)
		if err != nil || now.Now().Sub(info.ModTime()) >= settleQuiet {
			return
		}
		if now.Sleep(ctx, settleStep) != nil {
			return
		}
	}
}

// estimateTranscriptTokens is the last assistant line's recorded usage
// (input + cache_read + cache_creation) plus the bytes written after that
// line divided by bytesPerToken — usage lags the tool results that land
// before the next model call records it. A transcript with no usage yet is
// all unbilled bytes.
func estimateTranscriptTokens(path string) (int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("open transcript: %w", err)
	}
	defer func() { _ = file.Close() }() // read-only: a close failure loses nothing
	reader := bufio.NewReader(file)
	var offset, afterUsage int64
	usage := 0
	for {
		line, readErr := reader.ReadBytes('\n')
		offset += int64(len(line))
		if bytes.Contains(line, []byte(`"usage"`)) || bytes.Contains(line, []byte(`"compact_boundary"`)) {
			if tokens, ok := recordedContext(line); ok {
				usage, afterUsage = tokens, offset
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return 0, fmt.Errorf("read transcript %s: %w", path, readErr)
		}
	}
	return usage + int((offset-afterUsage)/bytesPerToken), nil
}

// recordedContext reads the context size a line records: an assistant turn's
// prompt-side usage, or a compact_boundary's postTokens — after a compaction
// the older usage lines describe a context that no longer exists. A line that
// does not decode (a write still in flight) or records neither reports false.
func recordedContext(line []byte) (int, bool) {
	var entry struct {
		Type            string `json:"type"`
		Subtype         string `json:"subtype"`
		CompactMetadata *struct {
			PostTokens *int `json:"postTokens"`
		} `json:"compactMetadata"`
		Message *struct {
			Usage *struct {
				Input         int `json:"input_tokens"`
				CacheRead     int `json:"cache_read_input_tokens"`
				CacheCreation int `json:"cache_creation_input_tokens"`
			} `json:"usage"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &entry) != nil {
		return 0, false
	}
	if entry.Type == "system" && entry.Subtype == "compact_boundary" && entry.CompactMetadata != nil &&
		entry.CompactMetadata.PostTokens != nil {
		return *entry.CompactMetadata.PostTokens, true
	}
	if entry.Type != "assistant" || entry.Message == nil || entry.Message.Usage == nil {
		return 0, false
	}
	usage := entry.Message.Usage
	return usage.Input + usage.CacheRead + usage.CacheCreation, true
}
