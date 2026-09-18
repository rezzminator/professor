// Package nudge reminds the main Claude chat, at context milestones, that a
// self-compact is due — a reminder, never an order.
//
// Two files per session live in the fleet's SID directory. The statusline
// writes the context sample (nudge-ctx-<session>) on every render, straight
// from the used-percentage Claude Code hands it; the UserPromptSubmit hook
// reads that sample and keeps the last band it spoke at (nudge-band-<session>)
// so each band is announced once per climb and re-armed when a compaction
// drops the context back below it.
package nudge

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"hostops/pfm/internal/atomicfile"
	"hostops/pfm/internal/obs"
)

func samplePath(sidDir, sessionID string) string {
	return filepath.Join(sidDir, "nudge-ctx-"+sessionID)
}

func bandPath(sidDir, sessionID string) string {
	return filepath.Join(sidDir, "nudge-band-"+sessionID)
}

// RecordContext persists the used-percentage Claude Code reported for one
// session; the statusline is its only writer.
func RecordContext(sidDir, sessionID string, percent int) error {
	if err := os.MkdirAll(sidDir, 0o700); err != nil {
		return fmt.Errorf("create sample directory %s: %w", sidDir, err)
	}
	return writeInt(samplePath(sidDir, sessionID), percent)
}

// ReadContext returns the last recorded percentage. found=false is a session
// the statusline has not rendered yet — distinct from a sample that exists but
// cannot be read, which is an error.
func ReadContext(sidDir, sessionID string) (int, bool, error) {
	return readInt(samplePath(sidDir, sessionID))
}

// Band maps a percentage onto the reminder ladder start, start+step, … — 0
// below the first rung. The ladder stops at the last rung that fits under
// 100%, so an over-full context still lands on a real band.
func Band(percent, start, step int) int {
	if start <= 0 || step <= 0 || percent < start {
		return 0
	}
	band := start + step*((percent-start)/step)
	if top := start + step*((100-start)/step); band > top {
		band = top
	}
	return band
}

// Decide reports the band percent falls in and whether it is time to speak:
// once when a band is first reached on the way up, never again inside it, and
// again after the context fell (a compaction) and climbed back. The last band
// spoken at is the only state.
func Decide(sidDir, sessionID string, percent, start, step int) (threshold int, fire bool, err error) {
	defer func() {
		next := "held"
		if fire {
			next = "nudged"
		}
		obs.Transition(
			context.Background(),
			"nudge",
			"watching",
			next,
			fmt.Sprintf("%d%% vs threshold %d", percent, threshold),
		)(
			err,
		)
	}()
	band := Band(percent, start, step)
	last, _, err := readInt(bandPath(sidDir, sessionID))
	if err != nil {
		return band, false, err
	}
	if band == last {
		return band, false, nil
	}
	if err := os.MkdirAll(sidDir, 0o700); err != nil {
		return band, false, fmt.Errorf("create band directory %s: %w", sidDir, err)
	}
	if err := writeInt(bandPath(sidDir, sessionID), band); err != nil {
		return band, false, err
	}
	return band, band > last, nil
}

// Text is the reminder itself. It names the milestone and the tool, says in
// so many words that it is not an order, and never names another command —
// which slash commands exist, and who may fire them, is their own business.
func Text(percent, band, step int) string {
	next := "this is the last reminder"
	if band+step <= 100 {
		next = fmt.Sprintf("next reminder at %d%%", band+step)
	}
	return fmt.Sprintf(
		"CONTEXT NUDGE — this chat's context is at %d%%. Not an order: if you are at a milestone "+
			"— a phase closed, a plan checkpointed, a gate green — this is the moment to write what must "+
			"survive into its durable file, then call chat_self_compact with a one-line focus and ONE steer "+
			"and end the turn. If you are mid-task, keep going and compact at the next milestone (%s).",
		percent, next,
	)
}

func writeInt(path string, value int) error {
	if err := atomicfile.Write(path, []byte(strconv.Itoa(value)+"\n"), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func readInt(path string) (int, bool, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("read %s: %w", path, err)
	}
	value, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		return 0, false, fmt.Errorf("%s holds %q, not a percentage: %w", path, strings.TrimSpace(string(raw)), err)
	}
	return value, true, nil
}
