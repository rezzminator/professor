#!/usr/bin/env node
// token-audit.mjs — read-only audit of agent transcripts: where did the tokens go?
// Claude Code JSONL transcripts AND Codex CLI rollouts, one pricing table for both.
// Node builtins plus the pfm binary: prices come from `pfm model-cost --json --all`. Bounded text report; --out FILE writes JSON.
//
//   node token-audit.mjs [--since 24h|3d] [--root DIR]... [--project SUBSTR]
//                        [--family SUBSTR] [--session SID] [--top N] [--out FILE]
//   node token-audit.mjs --codex [--since 24h|3d] [--project SUBSTR] [--codex-root DIR] [--top N]
//   node token-audit.mjs --flight DIR [--metrics-out FILE] [--out FILE]
//   node token-audit.mjs --timeline FILE [--timeline FILE]...
//
// Unit of analysis: a RUN = one transcript file = one main chat loop, one sub-agent,
// or one Codex rollout thread. A FAMILY = a main chat plus every sub-agent it spawned.
// Dollars are list-price estimates: they rank and compare, they are not a bill. A model
// with no priced key or available rate renders "n/a" — never $0 — and its tokens still count.
import { parseOptions, die } from "./lib/options.mjs";
import { loadTable, useTable } from "./lib/pricing.mjs";
import { loadFlightPlan, runFlight } from "./lib/flight.mjs";
import { runCodex } from "./lib/codex.mjs";
import { runTimeline } from "./lib/timeline.mjs";
import { collectRuns } from "./lib/claude.mjs";
import { aggregate, runReport } from "./lib/report.mjs";

const opts = parseOptions();
try { useTable(loadTable(process.env)); } catch (e) { die(e.message); } // once, before any transcript is read
if (opts.flight) loadFlightPlan(opts); // the ledger, not --since, sets the window
if (opts.codex) process.exit(runCodex(opts));
if (opts.tl) process.exit(runTimeline(opts));
collectRuns(opts);
const agg = aggregate();
if (opts.flight) process.exit(runFlight(opts));
const code = runReport(opts, agg);
if (code) process.exit(code);
