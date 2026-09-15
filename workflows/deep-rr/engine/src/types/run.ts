// Run infra types — the engine/config plumbing: untrusted args, agent-call options, the IO/metrics logs,
// and the final returned result shape.
import type { Schema } from './schema.js';
import type {
  Confidence,
  Mode,
  RabbitHole,
  ResultSoFar,
  ScoreEntry,
  Tier,
  Effort,
  Venue,
  WaveLogEntry,
} from './domain.js';
import type { ChaoStats } from './claims.js';

// the injected JSON args — anything until validated, so every field is `unknown`.
export interface RawArgs {
  query?: unknown;
  mode?: unknown;
  compute?: unknown;
  computeNote?: unknown;
  checkpoint?: unknown;
  thinkerNote?: unknown;
  researcherNote?: unknown;
  maxWave?: unknown;
  chaoCoverageStop?: unknown;
  maxParallelBrainers?: unknown;
  parallelLaneResearchAgentsPerWave?: unknown;
  parallelSourcesPerLaneResearchAgent?: unknown;
  debug?: unknown;
  debugPrompt?: unknown;
  tag?: unknown;
  agents?: unknown;
}

// the canonical phase-name map CONFIG.PHASE carries.
export interface PhaseMap {
  scout: string;
  crawl: string;
  finalize: string;
  debug: string;
}

// per-call options handed to agent()/retryAgent alongside the prompt.
export interface AgentOpts {
  label: string;
  phase?: string;
  model?: Tier;
  effort?: Effort;
  schema?: Schema;
  agentType?: string;
}

// one captured agent call (debug mode): exact prompt in, exact output out (or the error on degrade).
export interface IoLogEntry {
  label: string;
  model: string;
  phase: string;
  prompt: string;
  output: unknown;
  error?: string;
}

// why the crawl stopped (engine classification).
export type StopReason =
  | 'brainer-done'
  | 'scheduler-starved'
  | 'collect-dry-plateau'
  | 'wave-cap'
  | 'rabbithole-dry'
  | 'rabbithole-empty'
  | 'brainer-dead'; // the wave-0 brainer died (all retries exhausted) — the crawl never ran; finalize proceeds on scout material only, loudly labelled

// the run's diagnostic metrics (logged + fed to the debug analyst + written to _rabbitHoles-adjacent files).
export interface Metrics {
  mode: Mode;
  dir: string;
  wavesRun: number;
  stopReason: StopReason | null;
  scoutRabbitHoles: number;
  prospectorVenues: number;
  pursuedTotal: number;
  rabbitHolesFinal: number;
  bestOpenScore: number;
  topScores: number[];
  done: boolean;
  reportWritten: boolean;
  confidence: Confidence | null;
  claimsTotal: number; // size of the claim ledger at finalize
  nullAttacksTotal: number; // completed counter-searches that found nothing
  chao: ChaoStats | null; // collect-mode coverage estimate; null outside collect mode / before the first estimate
  citationsBogus: number; // synthesiser citation lint: [cN] markers stripped because the id was unknown/retracted
  citationsAuditFailed: number; // synthesiser citation lint: [cN] markers stripped because the claim's quote-pin audit failed
  auditCounts: { pass: number; fail: number; repinned: number; unpinned: number; pending: number }; // quote-pin audit outcome over the final ledger (repinned = the auditor located and replaced a broken quote — those claims read audit 'pass')
  quotesRepinned: number; // claims whose broken quote the auditor replaced with a verified contiguous span
  cachePathsRejected: number; // claims whose cachePath was untrusted (never scheduled + outside the harvester cache) and was stripped to unpinned
  venuesUnrouted: number; // prospector venues never assigned to any lane across the whole run
  goalMet: boolean | null; // the FINAL judge verdict's goalMet (null when no judge ran)
  judgePasses: number; // how many judge passes ran in finalize (including the gate on the multi-brainer path)
  reopenedLanes: number; // finalize judge-reopen lanes — included in pursuedTotal, split out so crawl-vs-finalize counts reconcile
}

// a rabbit-hole flattened for the finalize report / _rabbitHoles.json.
export interface RabbitHoleOut {
  id: number;
  keyword: string;
  why: string;
  path: string[];
  score: number | null;
  scoreHistory: ScoreEntry[];
  note?: string; // the brainer's per-lane directive (B8 observability) — present when the lane carried one
}

// the file bundle the workflow returns (the caller writes them).
export type Files = Record<string, string>;

// the value ResearchReport.run() resolves to.
export interface RunResult {
  query: string;
  mode: Mode;
  dir: string;
  stopReason: StopReason | null;
  done: boolean;
  tree: string[];
  verdict: string | null;
  confidence: Confidence | null;
  plan: string[];
  openQuestions: string[];
  pursued: string[];
  pursuedArchive: RabbitHole[];
  highValueSources: Venue[];
  rabbitHoles: RabbitHoleOut[];
  resultSoFar: ResultSoFar | null;
  waveLog: WaveLogEntry[];
  metrics: Metrics;
  files: Files;
}
