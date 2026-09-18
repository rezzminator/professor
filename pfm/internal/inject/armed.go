package inject

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	pfmengine "hostops/pfm/internal/engine"
)

// armedRecord is the ONE post-command steer armed on a pane, kept beside the
// steer log (armedPathFor) from the moment the waiter is spawned until the
// chain's last hop finishes. Without it a second chat_self_compact while one
// was armed passed every guard, spawned a second waiter whose O_TRUNC wiped
// the first one's log, and left two waiters racing to type into one pane.
//
// PID is 0 while nobody owns the record yet: the spawner writes it before the
// detached waiter has a pid (setsid -f forks, so the spawner never learns it),
// and a chain hop hands it back to 0 for the hop it just armed. The waiter
// claims it with its own pid on start (DeliverThen).
type armedRecord struct {
	PID    int    `json:"pid"`
	Socket string `json:"socket"`
	Pane   string `json:"pane"`
	Steer  string `json:"steer"`
	Stamp  int64  `json:"stamp"`
}

// armedLaunchGrace bounds how long an unclaimed record (PID 0) counts as
// armed: a waiter that has not claimed its record this long after the spawn
// or the hand-off never started, and the record is stale.
const armedLaunchGrace = 30 * time.Second

// armedSteerMax caps the steer excerpt the record carries — one line, enough
// to name the arming in a refusal, never a transcript.
const armedSteerMax = 120

// armedPathFor names the armed record beside its steer log: the same
// sanitised <socket>.<pane> component, ".armed" for ".log".
func armedPathFor(logPath string) string {
	return strings.TrimSuffix(logPath, ".log") + ".armed"
}

// steerLogPath mirrors chat.sh:940 — ${TMPDIR:-/tmp}/chat-then-<target>.log —
// but scoped by SOCKET as well as pane. Every chat's own live pane is %0 on
// its own dedicated socket, so a bare pane-derived name collided across
// EVERY chat on the machine: a fresh chain on one chat truncated the exact
// log file another chat's forensics depended on
// (the 2026-09-03 self-compact that ate an operator's live draft). The path is now
// ${TMPDIR:-/tmp}/chat-then-<sanitized base(SocketPath)>.<sanitized Pane>.log:
// each component is sanitized SEPARATELY, every non-alphanumeric byte in it
// (including a literal '-' inside the socket name itself) folded to '_',
// BEFORE the two are joined with a '.' — a byte the sanitizer never emits.
// Joining the raw components first (with '-') and sanitizing afterward let a
// hyphen inside one component alias with the join delimiter: two distinct
// (socket, pane) pairs whose hyphen boundary fell in different places could
// sanitize to the identical path (this repo's own socket names are
// hyphen-joined numeric triples — spawn.FreshSocket,
// "%s%d-%d-%d"). Sanitizing first and joining on a delimiter the sanitizer
// never produces makes that collision structurally impossible.
func (engine *Engine) steerLogPath(target Target) string {
	sanitize := func(component string) string {
		return strings.Map(func(character rune) rune {
			switch {
			case character >= 'a' && character <= 'z',
				character >= 'A' && character <= 'Z',
				character >= '0' && character <= '9':
				return character
			default:
				return '_'
			}
		}, component)
	}
	name := sanitize(filepath.Base(target.SocketPath)) + "." + sanitize(target.Pane)
	return filepath.Join(engine.options.ThenLogRoot, "chat-then-"+name+".log")
}

// steerExcerpt is the one-line, bounded spelling of a steer the record keeps.
func steerExcerpt(steer string) string {
	line := strings.Join(strings.Fields(steer), " ")
	if len(line) > armedSteerMax {
		line = line[:armedSteerMax]
	}
	return line
}

func writeArmedRecord(path string, record armedRecord) error {
	raw, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode armed steer record: %w", err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		return fmt.Errorf("write armed steer record: %w", err)
	}
	return nil
}

// readArmedRecord distinguishes the three states a caller must never blur:
// no record (nothing armed), a record, and a record it could not read — the
// last is an error, never absence, because arming over it could be exactly
// the second waiter the record exists to prevent.
func readArmedRecord(path string) (record armedRecord, exists bool, err error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return armedRecord{}, false, nil
	}
	if err != nil {
		return armedRecord{}, false, err
	}
	if decodeErr := json.Unmarshal(raw, &record); decodeErr != nil {
		return armedRecord{}, false, fmt.Errorf("decode %s: %w", path, decodeErr)
	}
	return record, true, nil
}

// alive reports whether the arming this record names is still in force: a
// claimed record while its waiter's process exists, an unclaimed one while
// the launch grace has not run out.
func (record armedRecord) alive(now time.Time) bool {
	if record.PID > 0 {
		err := syscall.Kill(record.PID, 0)
		return err == nil || errors.Is(err, syscall.EPERM)
	}
	return now.Sub(time.Unix(record.Stamp, 0)) < armedLaunchGrace
}

// waiter names the record's owner for a message: its pid, or the fact that
// none has claimed it yet.
func (record armedRecord) waiter() string {
	if record.PID > 0 {
		return fmt.Sprintf("pid %d", record.PID)
	}
	return "still launching"
}

func (record armedRecord) since() string {
	return time.Unix(record.Stamp, 0).UTC().Format(time.RFC3339)
}

// armedEngineLabel names the target's engine for a refusal — a Codex-pane
// refusal must not read identically to a Claude one, because the two carry
// different waiter contracts (announce.go's WaitingFor says so out loud). The
// spelling comes from the engine registry, never from a local table; an
// Engine this package cannot resolve is named as itself rather than defaulted
// to Claude, because "which engine" is exactly what the reader came for.
func armedEngineLabel(engineID string) string {
	if engineID == "" {
		return "engine unknown"
	}
	id, err := pfmengine.Parse(engineID)
	if err != nil {
		return "engine " + engineID
	}
	return "engine " + strings.ToLower(pfmengine.MustLookup(id).Short)
}

// refuseIfArmed is ScheduleAfterCurrentTurn's gate before it spawns: a live
// arming on this pane refuses the request BY NAME; a stale one (waiter gone)
// is replaced with a note on the log; an unreadable one refuses with the
// read error. A chain hop (request.Chain) is the SAME arming continuing and
// is never refused by its own record.
func (engine *Engine) refuseIfArmed(target Target, request Request, logPath string) (Result, bool) {
	if request.Chain {
		return Result{}, true
	}
	path := armedPathFor(logPath)
	record, exists, err := readArmedRecord(path)
	if err != nil {
		return refused(
			CodeUndelivered,
			fmt.Sprintf(
				"could not read the armed steer record %s: %v — refusing to arm a second waiter over it",
				path,
				err,
			),
		), false
	}
	if !exists {
		return Result{}, true
	}
	if record.alive(engine.options.Clock.Now()) {
		return refused(
			CodeBusy,
			fmt.Sprintf(
				"a post-command steer is already armed on %q (%s) (waiter %s since %s, steer: %q) — wait for it or kill that waiter",
				target.Pane,
				armedEngineLabel(target.Engine),
				record.waiter(),
				record.since(),
				record.Steer,
			),
		), false
	}
	engine.warnf(
		"pfm: replacing a stale armed steer record %s (waiter %s since %s is gone, steer: %q)\n",
		path,
		record.waiter(),
		record.since(),
		record.Steer,
	)
	return Result{}, true
}

// armRecord is the spawner's half: written before the waiter is started so a
// concurrent schedule already sees the pane armed, with PID 0 until the
// waiter claims it. A record naming a LIVE arming is left alone: a plain
// `inject --then` spawned over an armed compaction (inject() is not gated by
// refuseIfArmed) must not rewrite the compaction's record as its own — the
// record keeps naming the first arming, exactly as the refusal reports it.
func armRecord(request SteerSpawn, now time.Time) error {
	if existing, exists, err := readArmedRecord(armedPathFor(request.LogPath)); err == nil && exists &&
		existing.alive(now) {
		return nil
	}
	return writeArmedRecord(armedPathFor(request.LogPath), armedRecord{
		Socket: request.SocketPath,
		Pane:   request.Target,
		Steer:  steerExcerpt(request.Steers[0]),
		Stamp:  now.Unix(),
	})
}

// claimArmed is the waiter's half on start: the record now names THIS
// process, so a second schedule can tell a live arming from a stale one. A
// record the spawner never wrote (an older spawner, a direct call) is
// created rather than assumed; one another LIVE waiter owns is theirs and
// stays so.
//
// A record that could not be READ is the one state this must never claim
// over: readArmedRecord's own contract says an unreadable record is an error
// and never absence, because stamping this waiter's pid onto it would erase
// the identity of the arming it could not read — exactly the second waiter
// the record exists to prevent, and the refusal of a third schedule would
// then name the wrong one. The error is returned, and DeliverThen stops with
// it on the visible result; a warning alone is not sufficient. A write
// failure stays a warning: the record's OWNER is unchanged by it.
func (engine *Engine) claimArmed(path, steer string) error {
	record, _, err := readArmedRecord(path)
	if err != nil {
		return err
	}
	now := engine.options.Clock.Now()
	if record.PID != 0 && record.PID != os.Getpid() && record.alive(now) {
		engine.warnf("pfm: then waiter: armed steer record %s is held by %s; leaving it\n", path, record.waiter())
		return nil
	}
	if record.Steer == "" {
		record.Steer = steerExcerpt(steer)
	}
	record.PID = os.Getpid()
	record.Stamp = now.Unix()
	if err := writeArmedRecord(path, record); err != nil {
		engine.warnf("pfm: then waiter: %v (%s)\n", err, path)
	}
	return nil
}

// releaseArmed is the waiter's half on exit. handoff (this hop delivered and
// armed the next) gives the record back unclaimed for the hop to claim;
// anything else — the chain's last delivery, a refusal, an abort — removes
// it. Only a record this process owns is touched: the next hop may already
// have claimed it, and a record naming another live pid is theirs.
func (engine *Engine) releaseArmed(path string, handoff bool) {
	record, exists, err := readArmedRecord(path)
	if err != nil {
		engine.warnf("pfm: then waiter: could not read armed steer record %s on exit: %v\n", path, err)
		return
	}
	if !exists || (record.PID != 0 && record.PID != os.Getpid()) {
		return
	}
	if handoff {
		record.PID = 0
		record.Stamp = engine.options.Clock.Now().Unix()
		if err := writeArmedRecord(path, record); err != nil {
			engine.warnf("pfm: then waiter: hand-off %v (%s)\n", err, path)
		}
		return
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		engine.warnf("pfm: then waiter: could not remove armed steer record %s: %v\n", path, err)
	}
}
