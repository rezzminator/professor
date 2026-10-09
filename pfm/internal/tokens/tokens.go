// Package tokens is `pfm tokens`, the read-only audit of agent transcripts:
// where did the tokens go? It replays Claude Code JSONL transcripts and Codex
// CLI rollouts, counts OpenCode sessions from their store's per-message token
// records, prices every call from the one table `pfm model-cost` serves,
// and traces each dollar to the context that caused it. A RUN is one
// transcript (a main chat loop, a sub-agent, a Codex thread); a FAMILY is a
// main chat plus every sub-agent it spawned. Dollars are list-price estimates:
// they rank and compare, they are not a bill, and a model with no priced key
// or available rate renders "n/a", never $0, while its tokens still count.
package tokens

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/pricing/modelcost"
)

// The literals more than one section spells: a run's kind, the unpriced
// marker, JavaScript's renderings, the content and tool names the replay
// classifies by, and Codex's sub-agent thread source.
const (
	kindMain          = "main"
	kindAgent         = "agent"
	notPriced         = "n/a"
	noModel           = "(none)"
	jsUndefined       = "undefined"
	jsNull            = "null"
	blockText         = "text"
	triggerToolResult = "tool result"
	toolRead          = "Read"
	toolBash          = "Bash"
	threadSubagent    = "subagent"
	errPrefix         = "pfm tokens: "
)

// Options are the run's seams: its arguments and streams, the clock that
// fixes "now", the environment that names the host, the zone a stamp without
// an offset reads in, pfm's resolved transcript roots (Claude, Codex and
// OpenCode), and how the served price table loads when no --prices file is
// given.
type Options struct {
	Args           []string
	Stdout, Stderr io.Writer
	Clock          clock.Clock
	Env            paths.Env
	Location       *time.Location
	ClaudeRoots    []string
	CodexHomes     []string
	OpenCodeRoots  []string
	Served         modelcost.Options
	// LedgerDir is the cost ledger directory; empty means no ledger can be found.
	LedgerDir string
}

// exitError is a refusal or failure the run reports on one stderr line:
// code 2 for a usage error, 1 for a runtime failure.
type exitError struct {
	code    int
	message string
}

func (e *exitError) Error() string { return e.message }

func usageError(format string, args ...any) error {
	return &exitError{code: 2, message: fmt.Sprintf(format, args...)}
}

func failure(format string, args ...any) error {
	return &exitError{code: 1, message: fmt.Sprintf(format, args...)}
}

// globals are the totals every run adds to.
type globals struct {
	usd, tok                   tokenCounts
	advisor                    advisorSpend
	think, calls               float64
	cats                       [categoryCount]float64
	bands, modelEffort, hourly *object
	rewrites, rewriteTool      *object
	small, poll, callIdx, tier *object
	tools, bash, attach        *object
	repeats, rereads           *object
	landings                   []landingRow
}

func newGlobals() *globals {
	return &globals{
		bands: newObject(), modelEffort: newObject(), hourly: newObject(), rewrites: newObject(),
		rewriteTool: newObject(), small: newObject(), poll: newObject(), callIdx: newObject(), tier: newObject(),
		tools: newObject(), bash: newObject(), attach: newObject(), repeats: newObject(), rereads: newObject(),
		landings: []landingRow{},
	}
}

// auditor is one run of the audit: its options, the window, the price table
// and every accumulator the modes fill.
type auditor struct {
	opts         flags
	stdout       io.Writer
	stderr       io.Writer
	loc          *time.Location
	host         string
	now          float64
	since        float64
	hours        float64
	timelineMode bool
	claudeRoots  []string
	codexHomes   []string
	openCodeRoot []string
	prices       *priceBook
	scan         *scanState
	global       *globals
	runs         []*run
	flight       *flightPlan
	// ledgerDir holds the cost ledgers; transcripts is every transcript the ownership walk found, whatever
	// the window or --session replays, because a ledger row is held by any transcript of its session
	ledgerDir   string
	transcripts []string
	ledgers     *ledgerReport
}

// Audit audits per opts.Args and returns the exit code.
func Audit(ctx context.Context, opts Options) int {
	code, err := run0(ctx, opts)
	if errors.Is(err, errHelp) {
		fmt.Fprintln(opts.Stdout, usage)
		return 0
	}
	if err != nil {
		var exit *exitError
		if errors.As(err, &exit) {
			fmt.Fprintln(opts.Stderr, errPrefix+exit.message)
			return exit.code
		}
		fmt.Fprintln(opts.Stderr, errPrefix+err.Error())
		return 1
	}
	return code
}

// newAuditor is a run over parsed flags at opts' clock, its prices not yet loaded.
func newAuditor(opts Options, parsed flags, host string) *auditor {
	now := float64(opts.Clock.Now().UnixMilli())
	a := &auditor{
		opts: parsed, stdout: opts.Stdout, stderr: opts.Stderr, loc: opts.Location, host: host, now: now,
		since: now - float64(parsed.hours*3600e3), hours: parsed.hours, timelineMode: len(parsed.timeline) != 0,
		claudeRoots: opts.ClaudeRoots, codexHomes: opts.CodexHomes, openCodeRoot: opts.OpenCodeRoots,
		ledgerDir: opts.LedgerDir,
		scan:      newScanState(), global: newGlobals(),
	}
	if a.loc == nil {
		a.loc = time.Local
	}
	if parsed.codexHome != "" {
		a.codexHomes = []string{parsed.codexHome}
	}
	if parsed.opencodeRoot != "" {
		a.openCodeRoot = []string{parsed.opencodeRoot}
	}
	if a.timelineMode {
		a.since = 0
	}
	return a
}

func run0(ctx context.Context, opts Options) (int, error) {
	host, err := opts.Env.Hostname()
	if err != nil {
		return 1, failure("read the host name: %v", err)
	}
	parsed, err := readFlags(opts.Args, host)
	if err != nil {
		return 2, err
	}
	a := newAuditor(opts, parsed, host)
	// the price table loads once, before any transcript is read
	if parsed.prices != "" {
		a.prices, err = loadSaved(parsed.prices)
	} else {
		a.prices, err = loadServed(ctx, opts.Served)
	}
	if err != nil {
		return 1, failure("%v", err)
	}
	if parsed.flight != "" {
		if err := a.loadFlightPlan(); err != nil {
			return 1, err
		}
	}
	if parsed.codex {
		return a.runCodex(ctx)
	}
	if parsed.opencode {
		return a.runOpenCode(ctx)
	}
	if a.timelineMode {
		return a.runTimeline()
	}
	if err := a.collectRuns(); err != nil {
		return 1, err
	}
	totals := a.aggregate()
	if parsed.flight != "" {
		return a.runFlight(ctx)
	}
	return a.runReport(totals)
}

// print writes one stdout line.
func (a *auditor) print(line string) { fmt.Fprintln(a.stdout, line) }

// Main runs `pfm tokens` for the machine: pfm's resolved transcript roots,
// the real clock and host, and the served price table.
func Main(args []string, stdout, stderr io.Writer, runtime config.Runtime) int {
	return Audit(context.Background(), Options{
		Args: args, Stdout: stdout, Stderr: stderr, Clock: clock.Real, Env: paths.OSEnv{}, Location: time.Local,
		ClaudeRoots: runtime.Paths.Roots[pfmengine.Claude], CodexHomes: runtime.Paths.Roots[pfmengine.Codex],
		OpenCodeRoots: runtime.Paths.Roots[pfmengine.OpenCode], Served: modelcost.MachineOptions(runtime),
		LedgerDir: paths.CostLedgerDir(paths.OSEnv{}, runtime.Paths.Home),
	})
}
