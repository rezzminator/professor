// ─────────────────────────────────────────────────────────────────────────────
// Configs — THE single source of truth for the whole engine. ALL configuration
// lives here: tunable knobs, caps, char budgets, the per-agent model TIER + reasoning
// EFFORT maps, and the run-derived prompt fragments. Nothing is hardcoded elsewhere —
// engine.ts / utils / store / the agent modules READ every number and policy off the
// CONFIG singleton. Change a value here and it changes everywhere; never re-introduce a
// literal in another module.
//
// Configs also validates the injected JSON args (which can be ANYTHING) and fills safe
// defaults in the constructor. One immutable CONFIG singleton holds the run. (Each agent's
// schema + prompt-builder still live in src/agents/<agent>/; its tier/effort value lives
// ONLY in the TIER/EFFORT maps below — the agent module reads it from CONFIG.)
// ─────────────────────────────────────────────────────────────────────────────
import type { Effort, Mode, PhaseMap, RawArgs, Tier } from './types/index.js';

export class Configs {
  // run config (validated + defaulted)
  query: string;
  mode: Mode;
  maxWave: number | 'auto';
  HARD_CAP: number;
  maxParallelBrainers: number; // max LIVE brainers in the brainer tree (1 = today's single-brainer behavior; clamps to a hard ceiling of 5)
  MAX_BRAINER_DEPTH: number; // safety cap on spawn-chain depth (a child of a child of … )
  parallelLaneResearchAgentsPerWave: number | 'auto';
  parallelSourcesPerLaneResearchAgent: number | 'auto';
  PHASE: PhaseMap;
  MAX_JUDGE_PASSES: number;
  MAX_LANE_REFAILS: number;
  VALIDATOR_THIN: number;
  VALIDATOR_INTRO_CHARS: number;
  VALIDATOR_MISSING_CHARS: number;
  QUERY_PLATEAU: number;
  PLATEAU_MIN_WAVES: number;
  PLATEAU_WINDOW: number;
  AGENT_RETRIES: number;
  INJECT_SCORE: number;
  AUTO_CAP: number;
  AUTO_SOURCE_DEFAULT: number;
  NEAR_DUP: number;
  FINALIZE_TOP_OPEN: number;
  RESEARCHER_TOKEN_BUDGET: number;
  BRAINER_LANE_CAP: number;
  CHUNK_OVERLAP_CHARS: number;
  CHARS_PER_TOKEN: number;
  CONTEXT: Record<Tier, number>;
  MAX_SLICES_PER_READER: number;
  MAX_SOURCES_PER_LANE: number;
  HANDOFF_CHARS: number;
  MAX_STARVED_WAVES: number;
  TREE_LOG_WIDTH: number;
  QUOTE_MAX_CHARS: number;
  CLAIM_DIGEST_CAP: number;
  CLAIM_DIGEST_CLIP: number;
  CALIB_DEFAULT_SCORE: number;
  AUDIT_BATCH: number;
  LINEAGE_BATCH: number;
  SETTLED_MIN_CLUSTERS: number;
  VOI_SENS_THRESHOLD: number;
  CALIB_CLAMP_LO: number;
  CALIB_CLAMP_HI: number;
  CALIB_NORM: number;
  CALIB_ALPHA: number;
  CALIB_LEAD_WEIGHT: number;
  CALIB_REALIZED_MAX: number;
  CHAO_COVERAGE_STOP: number;
  BRAINER_LEDGER_CAP: number;
  CLAIM_LINE_CLIP: number;
  SENSITIVITY_CLIP: number;
  TREE_ANSWER_CLIP: number;
  MANDATE_CLIP: number;
  SCHED_VOCAB_CAP: number;
  VENUE_WARN_MIN: number;
  VENUE_UNROUTED_MIN_WAVE: number;
  SCOUT_PROBES: number;
  SCOUT_PROBE_SOURCES: number;
  SCOUT_PAGES_CAP: number;
  GENERAL_PURPOSE: string;
  CHECKPOINT_MARK: string;
  TIER: Record<string, Tier>;
  EFFORT: Record<string, Effort>;
  compute: boolean;
  checkpoint: boolean;
  computeNote: string;
  thinkerNote: string;
  researcherNote: string;
  debug: boolean;
  debugPrompt: string;
  tag: string;
  slug: string;
  DIR: string;
  rawArgs: RawArgs; // the COMPLETE set of arguments the run was launched with, captured verbatim (persisted into the output)
  // derived prompt fragments woven into the agent builders
  FOOTER: string;
  NET: string;
  COMPUTE_NOTE: string;
  THINKER_NOTE: string;
  RESEARCHER_NOTE: string;
  RUBRIC: string;
  STOP: string;

  constructor(rawArgs: unknown) {
    // args: { query, mode?, compute?, maxWave?, chaoCoverageStop?, parallelLaneResearchAgentsPerWave?, parallelSourcesPerLaneResearchAgent?, debug?, debugPrompt?, agents? }
    let parsed: unknown;
    try {
      parsed = typeof rawArgs === 'string' ? JSON.parse(rawArgs) : rawArgs;
    } catch (e) {
      throw new Error('RR: args is not valid JSON — ' + ((e && e.message) || e));
    }
    if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
      throw new Error('RR: args must be a JSON object { query, mode?, maxWave? }');
    }
    const arg = parsed as RawArgs;
    if (typeof arg.query !== 'string' || arg.query.trim() === '') {
      throw new Error('RR requires args { query: non-empty string, mode?, maxWave? }');
    }
    this.rawArgs = arg; // capture the COMPLETE launch args verbatim — persisted into the output files
    // typed readers — keep the supplied value only when it is the right type, else fall back to the default
    const str = (v: unknown, d: string): string => (typeof v === 'string' && v.length ? v : d);
    const bool = (v: unknown, d: boolean): boolean => (typeof v === 'boolean' ? v : d);
    // coerceBool — a STRICT boolean reader (B8): a real boolean passes; the common string/number truthy/falsy
    // spellings coerce explicitly; absent ⇒ the default; ANYTHING else throws LOUDLY rather than silently
    // defaulting. (A bare `bool()` would default "false"/0/"no" back to true — a foot-gun for `compute:false`.)
    const coerceBool = (v: unknown, d: boolean): boolean => {
      if (v === undefined || v === null) return d;
      if (typeof v === 'boolean') return v;
      if (typeof v === 'number') {
        if (v === 1) return true;
        if (v === 0) return false;
        throw new Error('RR: expected a boolean, got number ' + v);
      }
      if (typeof v === 'string') {
        const s = v.trim().toLowerCase();
        if (s === 'true' || s === '1' || s === 'yes' || s === 'on') return true;
        if (s === 'false' || s === '0' || s === 'no' || s === 'off') return false;
        throw new Error('RR: expected a boolean-ish value, got string "' + v + '"');
      }
      throw new Error('RR: expected a boolean, got ' + typeof v);
    };
    const autoInt = (v: unknown, lo: number, hi: number, d: number | 'auto'): number | 'auto' =>
      v === 'auto'
        ? 'auto'
        : Number.isInteger(v) && (v as number) > 0
          ? Math.min(hi, Math.max(lo, v as number))
          : d;

    // ---- centralized constants (the single source of truth — no literal of these lives anywhere else) ----
    this.AUTO_CAP = 5; // auto-mode hard cap on lanes/wave AND sources/lane (the brainer is never told the number); read by the lane+source autoInt bounds, utils laneCount, and the engine srcCount
    this.AUTO_SOURCE_DEFAULT = 2; // auto-mode sources/lane when the brainer assigns none to a lane
    this.NEAR_DUP = 0.85; // store dedup: Jaccard token-set overlap ≥ this counts as "the same lead, reworded"; kept high so distinct leads are never merged
    this.FINALIZE_TOP_OPEN = 6; // finalize: how many top open rabbit-holes feed the initiator + synthesiser (Open questions)
    this.VALIDATOR_INTRO_CHARS = 240; // crawl: char budget for each finding's intro handed to the validator gate
    this.VALIDATOR_MISSING_CHARS = 300; // crawl: char budget for the validator's `missing` gaps threaded into the next brainer
    this.PLATEAU_MIN_WAVES = 3; // collect DRY: minimum waves before the novelty-plateau stop can fire
    this.PLATEAU_WINDOW = 2; // collect DRY: how many trailing top-scores must all sit ≤ QUERY_PLATEAU×peak to call it dry
    // scheduler/reader knobs (B4/B5) — read by utils.packReaders + the engine lane threads
    this.RESEARCHER_TOKEN_BUDGET = 130000; // one reader-unit budget: the calibrated safe ceiling a single reader may carry (the bin-pack unit)
    this.BRAINER_LANE_CAP = 5; // lanes/wave — the wave bound (≥ this many lanes never run in one wave)
    this.CHUNK_OVERLAP_CHARS = 2000; // overlap re-read at each split boundary when one source is packed across multiple reader-units
    // CHARS_PER_TOKEN — inverts Harvester's size_only token heuristic (tokens ≈ chars/2 for prose, A6) so the
    // engine's CHAR windows agree with the scheduler's TOKEN sizes: budget(tokens) × CHARS_PER_TOKEN = the char
    // ceiling a reader-unit may span. The heuristic deliberately OVER-counts tokens (real prose is ~chars/4), so
    // 130k heuristic-tokens ≈ ~65k real tokens — comfortably inside the worker context window with headroom.
    this.CHARS_PER_TOKEN = 2;
    // CONTEXT — each tier's real context window (Claude models, tokens). The reader budget is ANCHORED against the
    // researcher tier's window below (a reader-unit can never be asked to ingest more than the model can hold).
    this.CONTEXT = { haiku: 200000, sonnet: 200000, opus: 200000 };
    this.MAX_SLICES_PER_READER = 8; // B7: max whole sources combined into ONE reader-unit (a lane of 40 tiny files never packs 40 into one turn)
    // MAX_SOURCES_PER_LANE (B7 governor) — set below, once parallelSourcesPerLaneResearchAgent is validated: 12
    // by default ('auto'), or the caller's clamped override when a positive integer is passed.
    this.HANDOFF_CHARS = 16000; // B7: cap the running-answer handoff carried between sequential readers (keeps read + handoff inside context)
    this.MAX_STARVED_WAVES = 2; // B6: consecutive all-null / empty-schedule waves before the crawl breaks with stopReason scheduler-starved
    this.MAX_BRAINER_DEPTH = 3; // safety cap on the spawn-chain depth (root=0); spawning past this depth is refused
    this.TREE_LOG_WIDTH = 120; // crawl-tree render: per-line clip width for the LIVE TERMINAL log ONLY — the persisted _tree.md + returned tree keep full, unclipped lines
    // claim-ledger knobs (v3) — read by utils claimStatus/chao1/updateCalib/calibFactor + the prompt builders
    this.QUOTE_MAX_CHARS = 300; // max chars of the VERBATIM quote a claim pins (the FOOTER + reader schema ceiling)
    this.CLAIM_DIGEST_CAP = 30; // max existing key-claim one-liners woven into a reader prompt (the stance targets)
    this.CLAIM_DIGEST_CLIP = 90; // max chars of a claim's text on ONE claimDigestOf line (distinct from CLAIM_DIGEST_CAP, which caps the line COUNT)
    this.CALIB_DEFAULT_SCORE = 50; // ingestWave: predicted-yield default when a pursued lead carries no score history yet (the neutral midpoint)
    this.AUDIT_BATCH = 50; // claimAuditor: max claims per batched quote-audit call (chunks beyond this dispatch as separate concurrent calls)
    this.LINEAGE_BATCH = 80; // lineageClerk: max claims per batched entity-canonicalization call (chunks beyond this dispatch as separate concurrent calls)
    this.SETTLED_MIN_CLUSTERS = 2; // claimStatus: independent lineage clusters required before a claim can settle
    this.VOI_SENS_THRESHOLD = 0.15; // VOI stop assist: a derivation input below this sensitivity share is not worth another lane
    this.CALIB_CLAMP_LO = 0.5; // yieldCalib: floor on the per-kind selection multiplier (a cold kind is never zeroed out)
    this.CALIB_CLAMP_HI = 1.5; // yieldCalib: ceiling on the per-kind selection multiplier (a hot kind never dominates)
    this.CALIB_NORM = 4; // yieldCalib: realized-yield divisor — (auditedPassClaims + CALIB_LEAD_WEIGHT×freshLeads)/CALIB_NORM ≈ 1 on a good lane
    this.CALIB_ALPHA = 0.3; // yieldCalib: EMA weight of the newest realized/predicted observation
    this.CALIB_LEAD_WEIGHT = 0.3; // yieldCalib: a lane's fresh (pre-dedup) leads count for this much of one audited-pass claim in the realized-yield formula
    this.CALIB_REALIZED_MAX = 2; // yieldCalib: ceiling on one wave's realized-yield observation before it feeds the EMA (an outlier lane never swings the ratio unboundedly)
    this.CHAO_COVERAGE_STOP =
      typeof arg.chaoCoverageStop === 'number' &&
      arg.chaoCoverageStop > 0 &&
      arg.chaoCoverageStop <= 1
        ? arg.chaoCoverageStop
        : 0.9; // collect DRY assist: plateau AND chao1 coverage ≥ this → dry; optional arg (0,1], default 0.9
    // v3 STEERING knobs (batch 3) — the brainer's ledger/calibration/sensitivity-aware prompt sections + the scheduler's vocabulary clause.
    this.BRAINER_LEDGER_CAP = 120; // max ledger lines rendered into the CLAIM LEDGER section (utils ledgerLines); beyond this, a "(+N more)" tail
    this.CLAIM_LINE_CLIP = 120; // max chars of a claim's text on ONE ledger line (distinct from QUOTE_MAX_CHARS, which caps the underlying quote)
    this.SENSITIVITY_CLIP = 60; // max chars of a backing claim's text on ONE sensitivityRanking line (utils sensitivityRanking)
    this.TREE_ANSWER_CLIP = 400; // max chars of a brainer's resultSoFar.answer rendered into _brainers.json (the crawl-tree artifact)
    this.MANDATE_CLIP = 60; // max chars of a trail label / spawn mandate on ONE log or tree line (utils trailOf; engine.ts spawn log + tree render)
    this.SCHED_VOCAB_CAP = 20; // max community-vocabulary terms (by uses) rendered into the scheduler's COMMUNITY VOCABULARY clause
    this.VENUE_WARN_MIN = 2; // venuesWithYieldWarn: min lane-assignments a venue must carry before a persistent 0-yield earns the ⚠ warning suffix
    this.VENUE_UNROUTED_MIN_WAVE = 2; // venuesWithYieldWarn: first wave at which a prospector venue with NO lane assignment yet earns the '⚠ never assigned' suffix (waves 0-1 are legitimately still routing)
    // scout SWARM knobs (v3 batch 2s) — the wave-0 seed is now a planner→probes→merger swarm, not one broad sweep.
    this.SCOUT_PROBES = 5; // max search angles the planner may propose (≥3); each angle spawns exactly one probe
    this.SCOUT_PROBE_SOURCES = 3; // max sources ONE probe fetches for its own angle (mirrors v2's single-scout ≤5, now split across probes)
    this.SCOUT_PAGES_CAP = 10; // max pages the merger (or its JS fallback) keeps in the final ScoutOut — the union of every probe's pages, strongest kept
    this.GENERAL_PURPOSE = 'general-purpose'; // the harness agentType handed to code-capable / tool-using sub-agents
    this.CHECKPOINT_MARK = '⏺CKPT'; // per-wave crash-safety checkpoint log-line prefix: engine.ts emits `${CHECKPOINT_MARK} w<n> <json>` at wave end (zero-cost — no agent); runtime.ts's debug LOG_BUFFER filter skips lines starting with it so _debug.md never bloats
    // Per-agent model TIER + reasoning EFFORT — keyed by agent name; each src/agents/<agent>/ module reads its value
    // here (the tiering rationale travels in each agent's own comment). Brainer = ALWAYS Opus + xhigh (the global
    // brain/reducer); scout probes + researcher = Haiku (bounded summarize + extract — the page reading is the fixed
    // Haiku reader's job); escalate only on measured failure. scoutPlanner/scoutMerger = Sonnet + high: landscape
    // sensing (grounded search-angle judgment) and cross-angle synthesis are reasoning jobs, not bounded extraction.
    this.TIER = {
      scout: 'haiku',
      scoutPlanner: 'sonnet',
      scoutMerger: 'sonnet',
      prospector: 'opus',
      brainer: 'opus',
      validator: 'sonnet',
      researcher: 'haiku',
      researchScheduler: 'sonnet',
      initiator: 'opus',
      refiner: 'sonnet',
      judge: 'opus',
      synthesiser: 'opus',
      debugAnalyst: 'opus',
      // v3 ledger clerks — mostly Haiku: each is a bounded, mechanical, batched-per-wave job (grep a
      // quote, re-execute a stored script). lineageClerk alone is promoted to Sonnet — fuzzy entity
      // resolution against a growing canon (same-as spellings, merges) is judgment, not grep.
      claimAuditor: 'haiku',
      lineageClerk: 'sonnet',
      rerunner: 'haiku',
    };
    this.EFFORT = {
      scout: 'medium',
      scoutPlanner: 'high',
      scoutMerger: 'high',
      prospector: 'high',
      brainer: 'xhigh',
      validator: 'medium',
      researcher: 'medium',
      researchScheduler: 'high',
      initiator: 'xhigh',
      refiner: 'high',
      judge: 'xhigh',
      synthesiser: 'xhigh',
      debugAnalyst: 'high',
      claimAuditor: 'medium',
      lineageClerk: 'medium',
      rerunner: 'low',
    };
    // PER-SEAT OVERRIDE (`agents` arg) — the caller may retune ANY seat's model/effort without touching source.
    // Applied AFTER the defaults above (overlays them) and BEFORE the reader-budget anchor below (so an
    // overridden researcher tier is anchored against ITS actual context window). Unknown seat / bad model / bad
    // effort all throw loudly — no silent coercion, matching every other validation in this constructor.
    const VALID_TIERS: Tier[] = ['haiku', 'sonnet', 'opus'];
    const VALID_EFFORTS: Effort[] = ['low', 'medium', 'high', 'xhigh'];
    const seats = Object.keys(this.TIER); // the canonical 16 seat names — read off the default map itself, never a second hand-kept list
    if (arg.agents !== undefined && arg.agents !== null) {
      if (typeof arg.agents !== 'object' || Array.isArray(arg.agents))
        throw new Error(
          'RR: agents must be an object keyed by seat name, e.g. { researcher: { model: "sonnet" } }',
        );
      const overrides = arg.agents as Record<string, unknown>;
      for (const seat of Object.keys(overrides)) {
        if (!seats.includes(seat))
          throw new Error(
            'RR: unknown agent seat "' + seat + '" in `agents` — valid seats: ' + seats.join(', '),
          );
        const o = overrides[seat];
        if (typeof o !== 'object' || o === null || Array.isArray(o))
          throw new Error('RR: agents.' + seat + ' must be an object { model?, effort? }');
        const { model, effort } = o as { model?: unknown; effort?: unknown };
        if (model !== undefined) {
          if (!VALID_TIERS.includes(model as Tier))
            throw new Error(
              'RR: agents.' +
                seat +
                '.model must be one of ' +
                VALID_TIERS.join(', ') +
                ', got ' +
                JSON.stringify(model),
            );
          this.TIER[seat] = model as Tier;
          // brainer = ALWAYS opus by default (the global brain/reducer) — a caller MAY downgrade it, but never
          // silently: loud warning at construction, guarded like the mode warning above (log may be unavailable
          // in unit tests).
          if (seat === 'brainer' && model !== 'opus') {
            try {
              if (typeof log === 'function')
                log(
                  '⚠ RR: agents.brainer.model overridden to "' +
                    model +
                    '" (below opus) — measured: a Haiku brainer scored erratically + drifted off-goal',
                );
            } catch (e) {
              /* log not available at construction (unit test) → skip the warning */
            }
          }
        }
        if (effort !== undefined) {
          if (!VALID_EFFORTS.includes(effort as Effort))
            throw new Error(
              'RR: agents.' +
                seat +
                '.effort must be one of ' +
                VALID_EFFORTS.join(', ') +
                ', got ' +
                JSON.stringify(effort),
            );
          this.EFFORT[seat] = effort as Effort;
        }
      }
    }
    // ANCHOR the reader budget (B10) — now that TIER is set: a reader-unit must FIT the researcher tier's context
    // window, and (in chars) EXCEED the overlap re-read so every split makes forward progress. Fail loudly otherwise.
    if (this.RESEARCHER_TOKEN_BUDGET > this.CONTEXT[this.TIER.researcher])
      throw new Error(
        'RR config: RESEARCHER_TOKEN_BUDGET exceeds the researcher tier context window',
      );
    if (this.RESEARCHER_TOKEN_BUDGET * this.CHARS_PER_TOKEN <= this.CHUNK_OVERLAP_CHARS)
      throw new Error(
        'RR config: RESEARCHER_TOKEN_BUDGET (in chars) must exceed CHUNK_OVERLAP_CHARS',
      );

    // ---- run config (validated + defaulted) ----
    this.query = arg.query;
    // normalize mode (B8): trim + lowercase BEFORE the 'collect' test (so 'Collect'/' COLLECT ' canonicalize),
    // and warn LOUDLY when a non-empty mode fails to match either canonical value instead of silently → goal.
    const rawMode = arg.mode == null ? '' : String(arg.mode).trim().toLowerCase();
    this.mode = rawMode === 'collect' ? 'collect' : 'goal'; // canonical mode; anything not 'collect' → 'goal'
    if (rawMode && rawMode !== 'collect' && rawMode !== 'goal') {
      try {
        if (typeof log === 'function')
          log('⚠ RR: unrecognized mode "' + String(arg.mode) + '" → defaulting to goal');
      } catch (e) {
        /* log not available at construction (unit test) → skip the warning */
      }
    }
    this.maxWave = autoInt(arg.maxWave, 5, 15, 'auto'); // 'auto' (brainer-stopped, capped at HARD_CAP) or a clamped [5,15] override
    this.HARD_CAP = 15; // absolute ceiling on waves — no run ever exceeds this
    // maxParallelBrainers: max LIVE brainers in the brainer tree. A positive integer clamps to [1,5];
    // anything else (absent / 'auto' / junk) defaults to 1 — today's single-brainer behavior, so the tree is strictly opt-in.
    this.maxParallelBrainers =
      Number.isInteger(arg.maxParallelBrainers) && (arg.maxParallelBrainers as number) > 0
        ? Math.min(5, Math.max(1, arg.maxParallelBrainers as number))
        : 1;
    this.parallelLaneResearchAgentsPerWave = autoInt(
      arg.parallelLaneResearchAgentsPerWave,
      1,
      this.AUTO_CAP,
      'auto',
    ); // lanes/wave: 'auto' (brainer-assigned, hidden cap AUTO_CAP) or clamped [1,AUTO_CAP]
    this.parallelSourcesPerLaneResearchAgent = autoInt(
      arg.parallelSourcesPerLaneResearchAgent,
      1,
      this.AUTO_CAP,
      'auto',
    ); // sources/lane: 'auto' (brainer-assigned, hidden cap AUTO_CAP) or clamped [1,AUTO_CAP]
    // MAX_SOURCES_PER_LANE (B7 governor, consumed by researcher/run.ts) — 'auto' keeps today's fixed cap of 12;
    // a positive override governs it directly (already clamped to [1,AUTO_CAP] above).
    this.MAX_SOURCES_PER_LANE =
      this.parallelSourcesPerLaneResearchAgent === 'auto'
        ? 12
        : this.parallelSourcesPerLaneResearchAgent;
    this.PHASE = { scout: 'Scout', crawl: 'Research', finalize: 'Finalize', debug: 'Debug' };
    this.MAX_JUDGE_PASSES = 2; // finalize: max remediation passes the judge may drive (brain-compute / re-refine / crawl-reopen) before the report is written — the judge runs at most MAX+1 times
    this.MAX_LANE_REFAILS = 2; // crawl: max times the per-wave validator re-opens one lane after a null/thin return; after that it surfaces as a known gap (no infinite loop)
    this.VALIDATOR_THIN = 120; // crawl: a finding shorter than this (chars) is "thin" → it (or any null lane) gates the validator to run that wave
    this.QUERY_PLATEAU = 0.7; // collect-mode DRY: stop when top novelty-score stays ≤ this × the run's PEAK for 2 waves (no magic absolute floor)
    // L7 robustness: RETRY a failed agent() call up to AGENT_RETRIES times (a fresh spawn). A single transient failure (e.g. StructuredOutput
    // retry-cap exceeded on a JS-rendered page) often clears on a clean re-run; only after the retries are exhausted does it degrade to null
    // (handled by the existing null-guards) instead of throwing and crashing the WHOLE workflow.
    this.AGENT_RETRIES = 2;
    this.INJECT_SCORE = 90; // score for judge-reopened gap rabbit-holes (finalize crawl-reopen) — high, so they top the store
    this.compute = coerceBool(arg.compute, true); // master switch for ALL derivation: false → the brainer runs as a plain subagent (no code) + no finalize derivation (the brainer/judge are told it is off). STRICT: "false"/0/"no" coerce to false, never silently default to true
    this.checkpoint = coerceBool(arg.checkpoint, true); // per-wave crash-safety checkpoint: gates a single CHECKPOINT_MARK log line (compactCheckpoint) emitted at the end of each wave — zero agent cost. false → the line never logs. STRICT coercion, same as compute
    this.computeNote = str(arg.computeNote, ''); // optional run-specific compute guidance; appended after the baked stack note (COMPUTE_NOTE) the compute-aware agents receive
    this.thinkerNote = str(arg.thinkerNote, ''); // optional operator run-steering (priorities/framing/constraints/audience); reaches the Opus reasoning tier ONLY (THINKER_NOTE), pure passthrough
    this.researcherNote = str(arg.researcherNote, ''); // optional operator note to the web-research/probe agents (RESEARCHER_NOTE); terse passthrough, reaches scout/prospector/researcher/brainer
    this.debug = bool(arg.debug, true); // last-phase Debug & Analysis agent → one _debug.md (raw agent I/O + run log + metrics); ON by default, pass debug:false to turn it off
    this.debugPrompt = str(arg.debugPrompt, ''); // optional run-specific analysis question handed to the debug agent
    this.tag = str(arg.tag, ''); // optional slug suffix so parallel variants of one query write to distinct dirs
    const baseSlug = this.query
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, '-')
      .replace(/^-|-$/g, '')
      .slice(0, 40);
    this.slug = baseSlug + (this.tag ? '-' + this.tag : '');
    this.DIR = 'RR/' + this.slug;

    // ---- shared prompt fragments (woven into the builders below) ----
    // FOOTER — the 6-channel return contract every reading agent appends: gap searches (in the source's
    // own vocabulary) + attack queries, stance-tagged citations, quote-pinned claims, new terms, and a
    // surprise flag. The do-not-pad discipline holds on every channel.
    this.FOOTER = `Then append a section titled "Rabbit holes": 0-5 gap searches worth a researcher's time — the biggest things the content raises but does not explain, EACH PHRASED IN THE SOURCE'S OWN TERMINOLOGY (the community's words, not yours). Each: a concrete next web-search query and one line on why it matters. For any claim the page supports, also give the single strongest REALISTIC counter-evidence search, returned with kind:"attack". When a recurring author, venue, or dataset clearly matters to the topic, also give one search to follow their other work, returned with kind:"entity". If the page is a dead end or self-contained, give 1 or none — do not pad. Skip anything the page already explains.
Then append a section titled "Next sources": up to 5 of the page's highest-value outbound citations or links as concrete fetch targets — each the exact URL or DOI the page points to, one line on why following it matters, and whether it is expected to SUPPORT or ATTACK a specific existing claim (name which) or is neutral. Give none when the page cites nothing worth following.
Then append a section titled "Claims": each load-bearing fact the page carries — the fact in one line (with its value when it has one), a VERBATIM quote of at most ${this.QUOTE_MAX_CHARS} characters copied exactly from the page that pins it — one CONTIGUOUS unbroken span, never fragments joined with an ellipsis, and the source's entities (authors, funder, dataset, venue) when visible. Only facts the answer could rest on — do not pad.
Then append a section titled "New terms": the community's terms of art the page uses that we did not — each with a one-line gloss. Give none when the page speaks our vocabulary.
Then append a "Surprise" note ONLY when the page contradicts the current key claims: one line naming the contradiction. No section otherwise.`;
    // L3 (directive A): primary tools are WebSearch + mcp__harvester__fetch, but agents MAY reach for any other tool that genuinely helps the rabbit-hole.
    this.NET = `Primary tools: WebSearch + mcp__harvester__fetch — load WebSearch via ToolSearch "select:WebSearch" if absent (built-in WebFetch is hook-denied; fetch only through Harvester). You may also load any other tool that genuinely helps THIS rabbit-hole (e.g. context7 for library/API docs) via ToolSearch — pick the best tool for the question, not only web search. Prefer primary, recent sources; stay on-rabbit-hole.`;
    // COMPUTE_NOTE — capability fragment for the compute-aware agents (mirrors NET). Names the scientific Python stack the compute
    // environment ships so they reach for it over hand-rolled math; the optional computeNote arg appends per-run guidance after it.
    this.COMPUTE_NOTE =
      `The compute environment's python3 ships a scientific stack — prefer it over hand-rolled math: scipy (integration/ODEs, optimization, stats, linear algebra), sympy (symbolic math + dimensional/algebra checks), uncertainties or a numpy Monte-Carlo for error-bar propagation, pint for unit consistency, pandas + statsmodels + scikit-learn for data and statistics, networkx for graph/path reasoning, rdkit for molecular similarity. Import what fits the derivation instead of coding the method yourself.` +
      (this.computeNote ? '\n' + this.computeNote : '');
    // THINKER_NOTE — the operator's run-steering, labeled so the reasoning tier treats it as HOW to approach the run (priorities,
    // framing, constraints, audience) rather than WHAT to research. Pure passthrough of the thinkerNote arg; empty ⇒ nothing renders.
    this.THINKER_NOTE = this.thinkerNote
      ? 'OPERATOR STEERING — how to approach THIS run (priorities, framing, constraints, audience), not additional questions to research:\n' +
        this.thinkerNote
      : '';
    // RESEARCHER_NOTE — the operator's terse one-line note to the agents that DO web research/fetching (scout, prospector,
    // researcher, brainer). Minimal framing — a short prefix then the note. Pure passthrough; empty ⇒ nothing renders.
    this.RESEARCHER_NOTE = this.researcherNote ? 'Research note: ' + this.researcherNote : '';
    this.RUBRIC =
      this.mode === 'collect'
        ? `MODE = collect (exhaustive): score each rabbit-hole by how much NEW information it adds about the subject; favour breadth.`
        : `MODE = goal (directed): score each rabbit-hole by how much it improves or better-verifies the answer to the goal; favour rabbit-holes that close or verify it.`;
    this.STOP =
      this.mode === 'collect'
        ? `done = true when the high-value material is collected and remaining rabbit-holes are only marginally novel — the novelty trajectory has fallen well below peak and plateaued. The subject need not be exhausted (a rich one never is); call it when further waves add footnotes, not substance.`
        : `done = true only when the goal is answered AND pursuing the top remaining rabbit-holes would not materially improve or better-verify the answer.`;
  }
}
export const CONFIG = new Configs(args);
