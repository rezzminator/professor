export const meta = {
  name: 'Research and Report',
  description:
    'Research and Report — unbounded best-first web crawl steered by a BRAINER over a persistent id-keyed rabbit-hole store, believing only what a quote-pinned CLAIM LEDGER can prove. A sonnet SCOUT PLANNER decomposes the query into 3-5 search angles (direct + a SKEPTIC angle + a RECENT angle) → parallel haiku SCOUT PROBES sweep each angle → a sonnet SCOUT MERGER folds them into one landscape, naming inter-angle tensions → opus PROSPECTOR names the high-value authoritative source venues → [the brainer looks up OR originates the rabbit-holes worth pursuing, assigning each its venue subset + a steering `note` (what to find + ranked fallbacks) → a sonnet SCHEDULER discovers + sizes the highest-value sources per lane → code bin-packs each lane into 130k-token reader-units and runs ONE sequential haiku reader thread per lane, each yielding quote-pinned CLAIMS a haiku AUDITOR mechanically verifies against the cache and a sonnet LINEAGE CLERK clusters by independent provenance (union-find; corroboration counts clusters, not sources) — claim status and confidence are COMPUTED from ledger topology, never asserted, and may only be lowered; a counter-search that lands nothing is logged as a survived challenge (nullAttack); for build-the-answer queries the brainer authors a stored seeded Python DERIVATION once, a haiku RERUNNER re-executes it whenever an input claim changes, and its variance decomposition steers which leads get read next (a value-of-information stop test) → the brainer returns delta updates (rescore / add / lookupNext / rename / drop), maintains a running resultSoFar keyed to `keyClaimIds`, decides done] until done / rabbithole-dry / wave hard-cap (15) → FINALIZE: an opus INITIATOR names the load-bearing facts + report focus → a sonnet refine pass fact-checks + hardens those facts against the sources, folding every counter-search outcome into the ledger → an opus JUDGE — the sole terminal skeptic, also handed the leftover open rabbit-holes and the power to RETRACT a discredited claim — judges the hardened answer (goal met, verification real, derivation valid) and steers a bounded remediation loop — the brain derives the answer when one is needed, refine re-checks a mis-hardened fact, or the crawl reopens on a real gap → an opus synthesiser writes the 8-section report, its citations linted against the ledger and its confidence floored by the computed value. Pursued-archive (no delete-on-pursue) + pursued memory; scoreHistory rides natively on each rabbit-hole id. Two modes: goal (satisficing) / collect (exhaustive, with a Chao1 coverage estimate gating the plateau stop). Returns per-wave markdown + the claim ledger + refinement + report + _rabbitHoles.json.',
  // ONLY the always-first phase is declared; everything after Scout is driven dynamically by phase() calls in run
  // order — each crawl wave its own phase (Research wN), then Finalize ONLY when a brainer declares done, then Debug
  // only at the very end. Declaring Finalize/Debug statically would pin them ahead of the dynamic wave phases.
  phases: [
    {
      title: 'Scout',
      detail:
        'the seed: a sonnet planner decomposes the query into search angles → parallel haiku probes sweep them (fetching sources with the rabbit-hole footer, each claim quote-pinned) → a sonnet merger folds every probe into one landscape → opus prospector names the high-value authoritative source venues → the brainer (wave 0) ledgers the scout claims, scores the scout rabbit-holes, assigns each its venue subset, and looks up the first wave',
    },
  ],
};
// ╔══ module: src/config.ts ═══════════════════════════════════════════════
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
                                                                              

class Configs {
  // run config (validated + defaulted)
  query        ;
  mode      ;
  maxWave                 ;
  HARD_CAP        ;
  maxParallelBrainers        ; // max LIVE brainers in the brainer tree (1 = today's single-brainer behavior; clamps to a hard ceiling of 5)
  MAX_BRAINER_DEPTH        ; // safety cap on spawn-chain depth (a child of a child of … )
  parallelLaneResearchAgentsPerWave                 ;
  parallelSourcesPerLaneResearchAgent                 ;
  PHASE          ;
  MAX_JUDGE_PASSES        ;
  MAX_LANE_REFAILS        ;
  VALIDATOR_THIN        ;
  VALIDATOR_INTRO_CHARS        ;
  VALIDATOR_MISSING_CHARS        ;
  QUERY_PLATEAU        ;
  PLATEAU_MIN_WAVES        ;
  PLATEAU_WINDOW        ;
  AGENT_RETRIES        ;
  INJECT_SCORE        ;
  AUTO_CAP        ;
  AUTO_SOURCE_DEFAULT        ;
  NEAR_DUP        ;
  FINALIZE_TOP_OPEN        ;
  RESEARCHER_TOKEN_BUDGET        ;
  BRAINER_LANE_CAP        ;
  CHUNK_OVERLAP_CHARS        ;
  CHARS_PER_TOKEN        ;
  CONTEXT                      ;
  MAX_SLICES_PER_READER        ;
  MAX_SOURCES_PER_LANE        ;
  HANDOFF_CHARS        ;
  MAX_STARVED_WAVES        ;
  TREE_LOG_WIDTH        ;
  QUOTE_MAX_CHARS        ;
  CLAIM_DIGEST_CAP        ;
  CLAIM_DIGEST_CLIP        ;
  CALIB_DEFAULT_SCORE        ;
  AUDIT_BATCH        ;
  LINEAGE_BATCH        ;
  SETTLED_MIN_CLUSTERS        ;
  VOI_SENS_THRESHOLD        ;
  CALIB_CLAMP_LO        ;
  CALIB_CLAMP_HI        ;
  CALIB_NORM        ;
  CALIB_ALPHA        ;
  CALIB_LEAD_WEIGHT        ;
  CALIB_REALIZED_MAX        ;
  CHAO_COVERAGE_STOP        ;
  BRAINER_LEDGER_CAP        ;
  CLAIM_LINE_CLIP        ;
  SENSITIVITY_CLIP        ;
  TREE_ANSWER_CLIP        ;
  MANDATE_CLIP        ;
  SCHED_VOCAB_CAP        ;
  VENUE_WARN_MIN        ;
  VENUE_UNROUTED_MIN_WAVE        ;
  SCOUT_PROBES        ;
  SCOUT_PROBE_SOURCES        ;
  SCOUT_PAGES_CAP        ;
  GENERAL_PURPOSE        ;
  CHECKPOINT_MARK        ;
  TIER                      ;
  EFFORT                        ;
  compute         ;
  checkpoint         ;
  computeNote        ;
  thinkerNote        ;
  researcherNote        ;
  debug         ;
  debugPrompt        ;
  tag        ;
  slug        ;
  DIR        ;
  rawArgs         ; // the COMPLETE set of arguments the run was launched with, captured verbatim (persisted into the output)
  // derived prompt fragments woven into the agent builders
  FOOTER        ;
  NET        ;
  COMPUTE_NOTE        ;
  THINKER_NOTE        ;
  RESEARCHER_NOTE        ;
  RUBRIC        ;
  STOP        ;

  constructor(rawArgs         ) {
    // args: { query, mode?, compute?, maxWave?, chaoCoverageStop?, parallelLaneResearchAgentsPerWave?, parallelSourcesPerLaneResearchAgent?, debug?, debugPrompt?, agents? }
    let parsed         ;
    try {
      parsed = typeof rawArgs === 'string' ? JSON.parse(rawArgs) : rawArgs;
    } catch (e) {
      throw new Error('RR: args is not valid JSON — ' + ((e && e.message) || e));
    }
    if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
      throw new Error('RR: args must be a JSON object { query, mode?, maxWave? }');
    }
    const arg = parsed           ;
    if (typeof arg.query !== 'string' || arg.query.trim() === '') {
      throw new Error('RR requires args { query: non-empty string, mode?, maxWave? }');
    }
    this.rawArgs = arg; // capture the COMPLETE launch args verbatim — persisted into the output files
    // typed readers — keep the supplied value only when it is the right type, else fall back to the default
    const str = (v         , d        )         => (typeof v === 'string' && v.length ? v : d);
    const bool = (v         , d         )          => (typeof v === 'boolean' ? v : d);
    // coerceBool — a STRICT boolean reader (B8): a real boolean passes; the common string/number truthy/falsy
    // spellings coerce explicitly; absent ⇒ the default; ANYTHING else throws LOUDLY rather than silently
    // defaulting. (A bare `bool()` would default "false"/0/"no" back to true — a foot-gun for `compute:false`.)
    const coerceBool = (v         , d         )          => {
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
    const autoInt = (v         , lo        , hi        , d                 )                  =>
      v === 'auto'
        ? 'auto'
        : Number.isInteger(v) && (v          ) > 0
          ? Math.min(hi, Math.max(lo, v          ))
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
    const VALID_TIERS         = ['haiku', 'sonnet', 'opus'];
    const VALID_EFFORTS           = ['low', 'medium', 'high', 'xhigh'];
    const seats = Object.keys(this.TIER); // the canonical 16 seat names — read off the default map itself, never a second hand-kept list
    if (arg.agents !== undefined && arg.agents !== null) {
      if (typeof arg.agents !== 'object' || Array.isArray(arg.agents))
        throw new Error(
          'RR: agents must be an object keyed by seat name, e.g. { researcher: { model: "sonnet" } }',
        );
      const overrides = arg.agents                           ;
      for (const seat of Object.keys(overrides)) {
        if (!seats.includes(seat))
          throw new Error(
            'RR: unknown agent seat "' + seat + '" in `agents` — valid seats: ' + seats.join(', '),
          );
        const o = overrides[seat];
        if (typeof o !== 'object' || o === null || Array.isArray(o))
          throw new Error('RR: agents.' + seat + ' must be an object { model?, effort? }');
        const { model, effort } = o                                         ;
        if (model !== undefined) {
          if (!VALID_TIERS.includes(model        ))
            throw new Error(
              'RR: agents.' +
                seat +
                '.model must be one of ' +
                VALID_TIERS.join(', ') +
                ', got ' +
                JSON.stringify(model),
            );
          this.TIER[seat] = model        ;
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
          if (!VALID_EFFORTS.includes(effort          ))
            throw new Error(
              'RR: agents.' +
                seat +
                '.effort must be one of ' +
                VALID_EFFORTS.join(', ') +
                ', got ' +
                JSON.stringify(effort),
            );
          this.EFFORT[seat] = effort          ;
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
      Number.isInteger(arg.maxParallelBrainers) && (arg.maxParallelBrainers          ) > 0
        ? Math.min(5, Math.max(1, arg.maxParallelBrainers          ))
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
const CONFIG = new Configs(args);
// ╔══ module: src/agents/shared.ts ════════════════════════════════════════
// ─────────────────────────────────────────────────────────────────────────────
// Shared cross-agent fragments — imported by the per-agent modules in this folder so a
// fragment used by more than one agent lives in EXACTLY one place (never duplicated per agent).
//
// Two kinds live here:
//   • static PROMPT fragments (FINISH, WEB_ONLY) — guard clauses appended to several agents'
//     prompts. (The run-DERIVED fragments — NET, FOOTER, RUBRIC, STOP, THINKER_NOTE,
//     RESEARCHER_NOTE, COMPUTE_NOTE — are built per run on the CONFIG singleton in ../config.js.)
//   • shared StructuredOutput SCHEMA bricks — the nested sub-schemas reused across more than one
//     agent's output contract (RABBITHOLE), plus the single-source nested bricks the top-level
//     contracts compose. Each agent's TOP-LEVEL schema is co-located in its own file; these
//     reusable bricks stay here so each nesting identity has one definition. A couple (CLAIM_ITEM's
//     quote cap) render a CONFIG knob straight into their description — the schema is data too, so
//     the same "no literal outside config.ts" rule applies to it.
// ─────────────────────────────────────────────────────────────────────────────

                                                

// ── static prompt guard clauses ──
// FINISH: the pure reducers (brainer, initiator, synthesiser) already hold the data they
// need — they MAY use a tool if it genuinely helps, but the hard rule is they FINISH: emit the
// COMPLETE StructuredOutput rather than getting lost (the wave-0 brainer once spent its whole turn
// reading this repo's own files on a self-referential query and never emitted resultSoFar/lookupNext/stop).
const FINISH = `
The data above is enough to decide. You may consult a tool if it genuinely helps, but keep it brief — the answer does not require it. Your one required action: return the complete StructuredOutput with every required field, never a partial object.`;
// WEB_ONLY: the refine pass checks claims on the web — the local repo code is never evidence.
const WEB_ONLY = `
Use the web only (WebSearch / mcp__harvester__fetch) to check sources — never read local files or this repo's own code; they are not evidence.`;
// EMIT: JSON-emission discipline for the agents whose StructuredOutput payload is large (readers, probes,
// merger, prospector, scheduler, brainer). Run forensics: emitters intermittently sent prose-/<parameter>-
// wrapped JSON and unescaped control characters in long string values — each a parse failure that burns a
// visible retry — and one probe collapsed its whole return into the first prose field, omitted every other
// required key, and re-sent that same shape through five schema-error retries. Schemas are null-tolerant
// for optional fields; this clause attacks the malformed-JSON and required-field-collapse classes.
const EMIT = `
StructuredOutput discipline: its input must be ONE valid JSON object — escape every quote, newline, and backslash inside string values; no code fences, no XML/<parameter> syntax, no prose outside the JSON. Emit EVERY required field in that one call — a required array with nothing to report is [], never omitted — and never collapse your findings into a single prose field the schema splits into structured ones. Omit optional fields you have nothing for. Keep free-text values tight (one line each unless the field says otherwise) — a compact payload parses, an essay-sized one truncates and dies. When a schema error comes back naming a field, fix exactly that field and re-emit the corrected COMPLETE object — resending the same shape fails the same way.`;

// ── shared schema bricks (declaration order respects nesting) ──
const RABBITHOLE         = {
  type: 'object',
  properties: {
    keyword: { type: 'string' },
    why: { type: 'string' },
    kind: {
      type: 'string',
      enum: ['gap', 'attack', 'entity'],
      description:
        "gap = an unexplained-gap search phrased in the SOURCE'S OWN terminology; attack = the single strongest realistic counter-evidence search for a claim this content supports; entity = follow an author/venue/dataset that keeps recurring",
    },
  },
  required: ['keyword', 'why'],
};
const SCORED         = {
  type: 'object',
  properties: {
    keyword: { type: 'string' },
    why: { type: 'string' },
    score: { type: 'number' },
    kind: {
      type: 'string',
      enum: ['gap', 'attack', 'entity', 'origin'],
      description: 'the lead channel; set "attack" for a counter-evidence lane',
    },
  },
  required: ['keyword', 'why', 'score'],
};
// CLAIM_ITEM = the load-bearing fact a reader/scout pins to a verbatim quote (pre-ledger — the engine
// assigns id/cluster/audit/status when it enters bs.claims). The base shape every claim-emitting
// agent shares; CLAIM_ITEM_STANCE adds `stance` for the researcher, which alone gets a digest of
// existing claims to bear on (the scout's wave-0 pages have no digest yet).
const CLAIM_ITEM         = {
  type: 'object',
  properties: {
    claim: { type: 'string', description: 'one load-bearing fact, in one line' },
    value: {
      type: ['string', 'null'],
      description: 'the number/quantity, when the claim is quantitative',
    },
    quote: {
      type: 'string',
      description: `a VERBATIM span, copied exactly and CONTIGUOUSLY from the source, of at most ${CONFIG.QUOTE_MAX_CHARS} characters that carries the claim — one unbroken span, NEVER separate fragments stitched with an ellipsis (a spliced quote fails the mechanical audit and the claim dies)`,
    },
    source: { type: 'string', description: 'the url or DOI this quote is from' },
    // every optional provenance field is null-tolerant (run forensics: haiku readers emit null for an
    // entity the page does not show, and a hard 'must be string' fails the whole payload) — the engine
    // null-scrubs at ingest (utils scrubEntities), so tolerance here costs nothing downstream.
    entities: {
      type: ['object', 'null'],
      properties: {
        authors: { type: ['array', 'null'], items: { type: ['string', 'null'] } },
        funder: { type: ['string', 'null'] },
        dataset: { type: ['string', 'null'] },
        venue: { type: ['string', 'null'] },
      },
      description:
        "the source's provenance — only what is visibly stated, never inferred; omit what is absent",
    },
    cachePath: {
      type: ['string', 'null'],
      description:
        'local cache file path this quote can be verified against, ONLY as reported by the fetch tool — never invented',
    },
  },
  required: ['claim', 'quote', 'source'],
};
const CLAIM_ITEM_STANCE         = {
  type: 'object',
  properties: {
    ...CLAIM_ITEM.properties,
    stance: {
      type: ['object', 'null'],
      properties: {
        target: {
          // number preferred; a string ('c12') validates and the engine's ingest coercion pulls the
          // digit-run out — a hard number-only type made every prose target a schema-mismatch retry.
          type: ['number', 'string'],
          description:
            "the NUMBER from the existing KEY CLAIM's c-id in the digest (e.g. c12 → target: 12) this claim bears on — a bare number, never prose",
        },
        kind: { type: 'string', enum: ['supports', 'attacks'] },
      },
      required: ['target', 'kind'], // a stance without a target is unlinkable — run forensics: prose targets were silently dropped and the attack graph never formed
      description:
        'ONLY when this claim directly bears on one of the KEY CLAIMS listed in the digest',
    },
  },
  required: ['claim', 'quote', 'source'],
};
// TERM_SEED = one community term of art a reader/scout surfaces (pre-ledger — `uses` is engine-owned).
const TERM_SEED         = {
  type: 'object',
  properties: { term: { type: 'string' }, gloss: { type: ['string', 'null'] } },
  required: ['term'],
};
const PAGE         = {
  type: 'object',
  properties: {
    url: { type: 'string' },
    summary: { type: 'string' },
    rabbitHoles: { type: 'array', items: RABBITHOLE },
  },
  required: ['url', 'summary', 'rabbitHoles'],
};
// LOOKUP = one item in the brainer's `lookupNext` (research NOW): EITHER {id} (an existing open rabbit-hole) OR {keyword,why,score,…}
// (originate-and-pursue-now). All fields optional so both shapes validate; `sources` are the prospector venues the brainer
// assigns to THIS lane (its researcher searches them first).
const LOOKUP         = {
  type: 'object',
  properties: {
    id: {
      type: 'number',
      description: 'id of an existing open rabbit-hole — use this OR the keyword fields, not both',
    },
    keyword: { type: 'string' },
    why: { type: 'string' },
    score: { type: 'number' },
    sources: {
      type: 'array',
      items: { type: 'string' },
      description:
        'subset of prospector venue identifiers (exact `source` strings) best suited to THIS rabbit-hole; empty if none fit',
    },
    note: {
      type: 'string',
      description:
        'WHAT to find + ranked fallbacks — steers the scheduler and the reader; distinct from `why`',
    },
    ref: {
      type: 'string',
      description:
        'a concrete URL/DOI to fetch DIRECTLY (a followed citation) instead of WebSearching',
    },
    kind: {
      type: 'string',
      enum: ['gap', 'attack', 'entity', 'origin'],
      description: 'the lead channel; set "attack" for a counter-evidence lane',
    },
    refetch: {
      type: 'boolean',
      description: 'cached copy corrupted — fetch fresh',
    },
  },
};
// resultSoFar = the run's living MEMORY, carried wave to wave. The brainer maintains it; refinement gets the FINAL one only.
const RESULT_SO_FAR         = {
  type: 'object',
  properties: {
    answer: {
      type: 'string',
      description: 'the best current answer to the goal, as it stands this wave',
    },
    keyClaimIds: {
      type: 'array',
      items: { type: 'number' },
      description: 'the ledger claim ids the answer currently rests on (load-bearing)',
    },
    assumptions: {
      type: 'array',
      items: {
        type: 'object',
        properties: {
          claim: {
            type: 'string',
            description: 'a working assumption the answer currently leans on',
          },
          basis: { type: 'string', description: 'what it rests on, and how firm that is' },
        },
        required: ['claim', 'basis'],
      },
      description:
        'working assumptions the answer leans on (each {claim, basis}); revise or retire them as evidence lands',
    },
    resolved: { type: 'array', items: { type: 'string' }, description: 'sub-questions now closed' },
    openGaps: { type: 'array', items: { type: 'string' }, description: 'what is still missing' },
    tensions: {
      type: 'array',
      items: { type: 'string' },
      description: 'conflicting sources / unresolved contradictions',
    },
    working: {
      type: 'string',
      description:
        "for build-the-answer / estimate questions, the growing derivation chain; '' for non-derivation questions",
    },
    confidence: { type: 'string' },
  },
  required: ['answer', 'keyClaimIds', 'resolved', 'openGaps', 'tensions', 'working', 'confidence'],
};
// ╔══ module: src/utils/index.ts ══════════════════════════════════════════

             
            
        
                
              
             
             
             
          
           
             
            
              
                  
             
       
       
        
             
                           

// ─────────────────────────────────────────────────────────────────────────────
// Pure helpers — stateless transforms + the file/markdown renderers.
// ─────────────────────────────────────────────────────────────────────────────
const norm = (s                           )         =>
  (s || '')
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, ' ')
    .trim();
// normalize a URL/DOI for dedup — drop scheme, www, the doi.org resolver prefix, and any trailing slash so
// "https://doi.org/10.x" and "10.X" collapse to one key (the fetch tool resolves either form).
const normRef = (s                           )         =>
  (s || '')
    .toLowerCase()
    .trim()
    .replace(/^https?:\/\//, '')
    .replace(/^www\./, '')
    .replace(/^(?:dx\.)?doi\.org\//, '')
    .replace(/\/+$/, '');
const lab = (s        )         => norm(s).replace(/ /g, '-').slice(0, 24);
const padIdx = (n        )         => String(n).padStart(2, '0');
// clip — when s exceeds n chars, keep the first n-3 and append '…'; shorter strings pass through unchanged.
const clip = (s        , n        )         => (s.length > n ? s.slice(0, n - 3) + '…' : s);
const lastScore = (r                                )                =>
  r.scoreHistory.length ? r.scoreHistory[r.scoreHistory.length - 1].score : null;
// one-line render of an open store entry for the brainer: `#id [last score or "new"] keyword — why`
// (a ` ↪ ref` suffix flags a lead that carries a concrete URL/DOI to fetch directly; a trailing kind tag
// surfaces the lead's origin channel when set, ⚔-prefixed for 'attack' so a pending attack lane stands out).
const openLine = (r   
             
                  
              
                             
               
                  
 )         =>
  '#' +
  r.id +
  ' [' +
  (r.scoreHistory.length ? r.scoreHistory[r.scoreHistory.length - 1].score : 'new') +
  '] ' +
  r.keyword +
  ' — ' +
  r.why +
  (r.ref ? ' ↪ ' + r.ref : '') +
  (r.kind ? (r.kind === 'attack' ? ' ⚔attack' : ' ·' + r.kind) : '');

// plain() — render a value as compact PLAIN TEXT for interpolation INTO an agent prompt (replaces JSON.stringify-in-prompts: less noise, no
// braces/quotes). string/number/boolean → as-is; array → one "- el" line each (recursing, nested indented two spaces); object → "key: value"
// per key, SKIPPING any key whose value is null/undefined/''/[]/{} unless opts.keep names it (those render "key: (none)"); nested indent two spaces.
const isEmpty = (v         )          =>
  v == null ||
  v === '' ||
  (Array.isArray(v) && v.length === 0) ||
  (typeof v === 'object' && !Array.isArray(v) && Object.keys(v          ).length === 0);
function plain(value         , opts                      )         {
  opts = opts || {};
  const keep = opts.keep || [];
  if (value == null) return '';
  const t = typeof value;
  if (t === 'string' || t === 'number' || t === 'boolean') return String(value);
  if (Array.isArray(value)) {
    return value
      .map((el         ) =>
        plain(el, opts)
          .split('\n')
          .map((l, i) => (i === 0 ? '- ' : '  ') + l)
          .join('\n'),
      )
      .join('\n');
  }
  const lines           = [];
  for (const k of Object.keys(value                           )) {
    const v = (value                           )[k];
    if (isEmpty(v)) {
      if (keep.includes(k)) lines.push(k + ': (none)');
      continue;
    }
    if (typeof v === 'object') {
      lines.push(k + ':');
      for (const ln of plain(v, opts).split('\n')) lines.push('  ' + ln);
    } else {
      lines.push(k + ': ' + String(v));
    }
  }
  return lines.join('\n');
}

// the HIDDEN lane cap: the brainer is never told a count — JS clamps to laneCount (AUTO_CAP in auto) here.
const laneCount         =
  CONFIG.parallelLaneResearchAgentsPerWave === 'auto'
    ? CONFIG.AUTO_CAP
    : CONFIG.parallelLaneResearchAgentsPerWave;
const trailOf = (path          , keyword         )         =>
  [clip(CONFIG.query, CONFIG.MANDATE_CLIP)]
    .concat(path || [], keyword ? [keyword] : [])
    .join('  →  ');

// chunk — split an array into groups of at most `size` items each (the v3 ledger clerks — claimAuditor/
// lineageClerk — batch a wave's claim list into bounded agent calls this way). size ≤ 0 defensively
// degrades to one whole-array chunk (never an infinite loop) — empty input still yields [].
function chunk   (items     , size        )        {
  if (!items.length) return [];
  if (size <= 0) return [items];
  const out        = [];
  for (let i = 0; i < items.length; i += size) out.push(items.slice(i, i + size));
  return out;
}

// map the brainer's assigned source-identifier strings back to the full {source, goodFor} venue objects (for the
// researcher prompt) — looked up against the prospector's high-value venue set on rr; an unknown id renders bare.
const venuesFor = (rr                               , sources           )          => {
  if (!sources || !sources.length) return [];
  return sources.map(
    (s) => rr.highValueSources.find((v) => v.source === s) || { source: s, goodFor: '' },
  );
};

// ─────────────────────────────────────────────────────────────────────────────
// Claim-ledger helpers (v3) — pure functions over the append-only claim ledger.
// Status + confidence are COMPUTED here from ledger topology, never asserted by a model.
// ─────────────────────────────────────────────────────────────────────────────

// scrubArtifacts — deterministic removal of structured-output/tool-call leakage that occasionally bleeds
// into a reader's returned text (real bug from run forensics: raw `</parameter>` tags bled into lane
// findings and propagated downstream). Strips every COMPLETE open/close tag matching the known leakage
// patterns, then a trailing UNCLOSED fragment of one (the harness cut the emission off mid-tag) — ordinary
// text is left alone: a lone '<' is only stripped when what follows it is a genuine prefix of one of these
// tag names (so "5 < 10" or "x<y" survive untouched).
const ARTIFACT_TAG = /<\/?(?:StructuredOutput|parameter|invoke|function_[a-z]+)[^>]*>/g;
const ARTIFACT_TAG_NAMES = ['StructuredOutput', 'parameter', 'invoke'];
const isArtifactTagPrefix = (frag        )          =>
  !!frag &&
  (ARTIFACT_TAG_NAMES.some((n) => n.startsWith(frag)) ||
    'function'.startsWith(frag) ||
    /^function_[a-z]*$/.test(frag));
function scrubArtifacts(s        )         {
  if (!s) return s;
  const out = s.replace(ARTIFACT_TAG, '');
  const lastLt = out.lastIndexOf('<');
  if (lastLt === -1 || out.slice(lastLt).includes('>')) return out;
  const frag = out.slice(lastLt + 1).replace(/^\//, ''); // a closing-tag fragment drops its leading '/' first
  return isArtifactTagPrefix(frag) ? out.slice(0, lastLt) : out;
}

// scrubEntities — null-scrub a claim's provenance as emitted by a worker model. The entity schema is
// null-tolerant (a hard 'must be string' on funder/dataset failed whole reader payloads into retries), so
// ingest normalizes here: keep only non-empty strings, drop null/junk, undefined when nothing survives.
function scrubEntities(e         )                            {
  if (!e || typeof e !== 'object' || Array.isArray(e)) return undefined;
  const raw = e                           ;
  const out                = {};
  const str = (v         )                     =>
    typeof v === 'string' && v.trim() ? v : undefined;
  const authors = Array.isArray(raw.authors)
    ? (raw.authors.filter((a) => typeof a === 'string' && a.trim())            )
    : [];
  if (authors.length) out.authors = authors;
  const funder = str(raw.funder);
  if (funder) out.funder = funder;
  const dataset = str(raw.dataset);
  if (dataset) out.dataset = dataset;
  const venue = str(raw.venue);
  if (venue) out.venue = venue;
  return Object.keys(out).length ? out : undefined;
}

// domainOf — the lineage signal inside a source ref: the host without www, or a DOI's registrant
// prefix ('10.x' = the publisher, per the normRef conventions). '' when nothing resolvable.
const domainOf = (url                           )         => normRef(url).split('/')[0];

// lineageKeyOf — the DETERMINISTIC lineage fallback when the lineageClerk dies: cluster a claim by
// norm(funder || venue || source-domain). '' when nothing resolvable (the caller maps '' → cluster 0).
const lineageKeyOf = (claim                                              )         => {
  const e = claim.entities || {};
  return norm(e.funder || e.venue || domainOf(claim.source));
};

// claimStatus — COMPUTED, never asserted. contested: an unretracted attacking claim targets it.
// settled: supporting clusters ≥ SETTLED_MIN_CLUSTERS AND (a survived attack OR one cluster beyond the
// minimum). Else tentative. Support = the claim's own cluster plus the clusters of unretracted,
// non-failed claims whose stance supports it; the Set counts cluster 0 (shared unknown lineage) at
// most ONCE no matter how many claims sit in it.
function claimStatus(
  claim       ,
  allClaims         ,
  nullAttacks              ,
  cfg                                  ,
)              {
  const bearsOn = (c       , kind                        )          =>
    !!c.stance && c.stance.target === claim.id && c.stance.kind === kind;
  // a set `counter` (the refiner's own counter-search landed something, or an attack-lane's finding) contests
  // the claim just as an unretracted attacking ledger claim does — same signal, no ledger row required for it.
  if (claim.counter || allClaims.some((c) => !c.retracted && bearsOn(c, 'attacks')))
    return 'contested';
  // the SUBJECT's own mechanical audit verdict: a claim the auditor actively disproved is treated like
  // retracted for THIS purpose — it can never settle, no matter how many independent clusters back it
  // (checked AFTER the contested guard above, so a still-attacked audit-fail claim reads as contested, not tentative).
  if (claim.audit === 'fail') return 'tentative';
  const clusters = new Set        ([claim.cluster]);
  for (const c of allClaims)
    if (!c.retracted && c.audit !== 'fail' && bearsOn(c, 'supports')) clusters.add(c.cluster);
  // a nullAttack naming the claim counts as a survived attack even before the attack-lane bookkeeping
  // bumps the counter; max (not sum) so the two records never double-count one challenge.
  const survived = Math.max(
    claim.attacksSurvived || 0,
    (nullAttacks || []).filter((na) => (na.claimIds || []).includes(claim.id)).length,
  );
  return clusters.size >= cfg.SETTLED_MIN_CLUSTERS &&
    (survived >= 1 || clusters.size >= cfg.SETTLED_MIN_CLUSTERS + 1)
    ? 'settled'
    : 'tentative';
}

// computedConfidence — deterministic over the key claims the answer rests on: every one settled →
// high; any contested → low; else medium. No key claims → medium; an unknown id can never ground high.
// A key claim the mechanical audit disproved (or the judge retracted) is worse than merely unsettled —
// the answer rests on a disproven pin, not just an unstressed one — so it forces 'low' outright, same as contested.
function computedConfidence(keyClaimIds          , claims         )             {
  if (!keyClaimIds || !keyClaimIds.length) return 'medium';
  const byId = new Map(claims.map((c) => [c.id, c]));
  const keys = keyClaimIds.map((id) => byId.get(id));
  if (keys.some((c) => c && (c.audit === 'fail' || c.retracted))) return 'low';
  if (keys.some((c) => c && c.status === 'contested')) return 'low';
  return keys.every((c) => c && !c.retracted && c.status === 'settled') ? 'high' : 'medium';
}

// claimDigestOf — the compact "KEY CLAIMS SO FAR" digest woven into a lane reader's prompt: non-retracted
// claims, most recent first (highest id), capped at CLAIM_DIGEST_CAP, one line each `c12 clip(claim,90)`
// (ids look like c12 — never '#12', which the ledger digest never renders and a stance target must not
// be confused with a cluster's `clu2` notation). '' when the ledger is empty (the reader's own fallback
// then renders "first wave"). Structural param (mirrors venuesFor's `rr`) so this stays a pure function
// over any claims-bearing state, not just BrainerState.
const claimDigestOf = (bs                     )         =>
  bs.claims
    .filter((c) => !c.retracted)
    .sort((a, b) => b.id - a.id)
    .slice(0, CONFIG.CLAIM_DIGEST_CAP)
    .map((c) => 'c' + c.id + ' ' + clip(c.claim, CONFIG.CLAIM_DIGEST_CLIP))
    .join('\n');

// minConfidence — the lower-only rule: models may LOWER confidence, never raise it (low < medium < high).
const CONF_RANK                             = { low: 0, medium: 1, high: 2 };
const minConfidence = (a            , b            )             =>
  CONF_RANK[a] <= CONF_RANK[b] ? a : b;

// lintCitations — v3 SYNTHESISER citation lint (pure): scan `report` for every [cN] marker the model wrote.
// One whose id is not a LIVE (non-retracted) ledger claim is stripped from the text in place (never left
// dangling, never fabricated back into a real id) and its id collected in `bogus` for the engine to log +
// count. One that IS a live claim but whose mechanical quote-pin audit came back 'fail' is ALSO stripped —
// a citation must never wear the authority of a pin the auditor actively disproved — and its id collected
// in `auditFailed` instead (a distinct count from `bogus`: this is a real claim, just a discredited one).
// No markers / empty report ⇒ passthrough, bogus: [], auditFailed: [].
function lintCitations(
  report        ,
  claims         ,
)                                                             {
  const live = new Set(claims.filter((c) => !c.retracted).map((c) => c.id));
  const auditFail = new Set(
    claims.filter((c) => !c.retracted && c.audit === 'fail').map((c) => c.id),
  );
  const bogus           = [];
  const auditFailed           = [];
  const cleaned = (report || '').replace(/\[c(\d+)\]/g, (marker, idStr        ) => {
    const id = Number(idStr);
    if (!live.has(id)) {
      bogus.push(id);
      return '';
    }
    if (auditFail.has(id)) {
      auditFailed.push(id);
      return '';
    }
    return marker;
  });
  return { report: cleaned, bogus, auditFailed };
}

// chao1 — the coverage estimator (collect mode): from claim groups and how many distinct sources saw
// each, estimate the unseen groups (n1 = singletons, n2 = doubletons; the bias-corrected form when
// n2 = 0) and the coverage share. No groups yet → coverage 0 (nothing observed ≠ complete).
function chao1(groups                       )            {
  if (!groups || !groups.length) return { unseen: 0, coverage: 0 };
  const n1 = groups.filter((g) => g.sources === 1).length;
  const n2 = groups.filter((g) => g.sources === 2).length;
  // Math.max also normalizes the n1 = 0 corner (0·(0−1)/2 = −0) to a clean +0
  const unseen = Math.max(0, n2 > 0 ? (n1 * n1) / (2 * n2) : (n1 * (n1 - 1)) / 2);
  return { unseen, coverage: groups.length / (groups.length + unseen) };
}

// updateCalib — one EMA step of a kind's realized/predicted yield ratio. PURE: returns the new entry,
// never mutates `calib`. predicted ≤ 0 → identity (an unpredicted lead teaches nothing). A kind starts
// from the neutral prior ratio 1, so early observations pull it gradually rather than whipsaw it.
function updateCalib(
  calib            ,
  kind        ,
  predicted        ,
  realized        ,
  alpha        ,
)                               {
  const prev = calib[kind] || { n: 0, ratio: 1 };
  if (!(predicted > 0)) return { n: prev.n, ratio: prev.ratio };
  return { n: prev.n + 1, ratio: prev.ratio + alpha * (realized / predicted - prev.ratio) };
}

// calibFactor — the selection-only multiplier a kind's calibration earns, clamped to [lo, hi]
// (an unseen kind is neutral: 1). Applied to sort keys only — stored scores are never touched.
const calibFactor = (calib            , kind        , lo        , hi        )         =>
  Math.min(hi, Math.max(lo, calib[kind] ? calib[kind].ratio : 1));

// ledgerLines — the brainer's CLAIM LEDGER digest (v3 STEERING): every non-retracted claim, one line
// `c12 [status·clu2·audit] claim = value — source` (claim ids look like c12, clusters like clu2 — kept
// visually distinct so a model never confuses a cluster number with a claim id, or either with a [cN]
// citation marker), ordered latest-keyClaimIds-first (the ids the LIVING answer currently rests on),
// then contested, then settled, then tentative by recency (highest id first). Capped at `cap` with a
// "(+N more)" tail; '' when the ledger is empty (the caller's clause then omits the whole CLAIM LEDGER section).
function ledgerLines(
  bs                                                      ,
  cap        ,
)         {
  const active = bs.claims.filter((c) => !c.retracted);
  const used = new Set        ();
  const ordered          = [];
  const byId = new Map(active.map((c) => [c.id, c]));
  for (const id of (bs.resultSoFar && bs.resultSoFar.keyClaimIds) || []) {
    const c = byId.get(id);
    if (c && !used.has(id)) {
      ordered.push(c);
      used.add(id);
    }
  }
  const rest = active.filter((c) => !used.has(c.id)).sort((a, b) => b.id - a.id);
  for (const status of ['contested', 'settled', 'tentative']                 )
    for (const c of rest)
      if (c.status === status && !used.has(c.id)) {
        ordered.push(c);
        used.add(c.id);
      }
  const shown = ordered.slice(0, cap);
  const line = (c       )         =>
    'c' +
    c.id +
    ' [' +
    c.status +
    '·clu' +
    c.cluster +
    '·' +
    c.audit +
    '] ' +
    clip(c.claim, CONFIG.CLAIM_LINE_CLIP) +
    (c.value ? ' = ' + c.value : '') +
    ' — ' +
    c.source;
  const remaining = ordered.length - shown.length;
  return shown.map(line).join('\n') + (remaining > 0 ? '\n(+' + remaining + ' more)' : '');
}

// topSensitivityInput — the derivation input name whose variance share is largest ('' when there are
// none). Shared by the rerunner's log line and the brainer's DERIVATION STATE clause — one definition.
const topSensitivityInput = (sensitivity                                    )         => {
  const entries = Object.entries(sensitivity || {});
  return entries.length ? entries.reduce((a, b) => (b[1] > a[1] ? b : a))[0] : '';
};

// sensitivityRanking — the initiator's SENSITIVITY RANKING body (v3 FINALIZE): derivation inputs ordered by
// variance share (highest first), each with the ledger claims backing it (`c12 clip(claim,SENSITIVITY_CLIP)`, joined —
// same c-id notation as ledgerLines/claimDigestOf) or a PRIOR flag when it is an unevidenced placeholder.
// '' when there is no completed rerun yet (the caller's clause is then omitted entirely — a freshly-authored,
// not-yet-run derivation has no sensitivity to rank).
function sensitivityRanking(
  derivation                                                                                  ,
  claims         ,
)         {
  if (!derivation) return '';
  const byId = new Map(claims.map((c) => [c.id, c]));
  const ranked = [...derivation.inputs].sort(
    (a, b) => (derivation.sensitivity[b.name] || 0) - (derivation.sensitivity[a.name] || 0),
  );
  return ranked
    .map((inp) => {
      const share = derivation.sensitivity[inp.name];
      const backing = (inp.claimIds || [])
        .map((id) => byId.get(id))
        .filter((c)             => !!c && !c.retracted)
        .map((c) => 'c' + c.id + ' ' + clip(c.claim, CONFIG.SENSITIVITY_CLIP))
        .join('; ');
      return (
        '- ' +
        inp.name +
        ' (' +
        (share != null ? share.toFixed(2) : '?') +
        ')' +
        (backing ? ' — backed by ' + backing : inp.prior ? ' — PRIOR (no backing claim yet)' : '')
      );
    })
    .join('\n');
}

// compactCheckpoint — the per-wave crash-safety recovery snapshot (replaces the old scribe-agent
// checkpoint file): LEAN by design — a human or a resuming agent needs the open frontier, the running
// answer, and the ledger's LOAD-BEARING shape, not its full evidentiary backing (the harness's own
// per-agent journal + the persisted _claims.json already hold quotes/cachePaths/entities in full, so
// none of those ride here). The caller (engine.ts) logs this JSON-stringified behind CONFIG.CHECKPOINT_MARK
// at the END of a wave, after the brainer's deltas have landed — so it reflects the wave's FINAL state.
                                     
               
                                  
                                                                                 
                    
           
               
                  
                   
                   
                        
                    
      
                      
                                                                                  
 
function compactCheckpoint(bs   
               
                                  
                                                                                              
                        
                  
                            
                                
 )                     {
  return {
    wave: bs.wave,
    resultSoFar: bs.resultSoFar,
    open: bs.rabbitHoles.map((r) => ({
      id: r.id,
      keyword: r.keyword,
      score: lastScore(r),
      kind: r.kind,
    })),
    pursued: bs.pursuedList,
    claims: bs.claims
      .filter((c) => !c.retracted)
      .map((c) => ({
        id: c.id,
        claim: clip(c.claim, CONFIG.CLAIM_LINE_CLIP),
        value: c.value,
        source: c.source,
        status: c.status,
        cluster: c.cluster,
      })),
    nullAttacks: bs.nullAttacks.length,
    derivation: bs.derivation
      ? {
          inputs: bs.derivation.inputs.map((i) => i.name),
          lastRun: bs.derivation.lastRun ? bs.derivation.lastRun.quantiles : null,
        }
      : null,
  };
}

// venuesWithYieldWarn — per-wave copy of the prospector venues for the brainer prompt, with a
// ' — ⚠ 0 yield in N lanes' suffix baked into `goodFor` for any venue assigned to ≥2 lanes that yielded
// NOTHING (no ingested claim, no fresh lead) across the run so far (assigned-vs-served reconciliation:
// venueStats.served — set by the engine's scheduleSources — is not read here; this suffix is about
// yield, not about whether the scheduler actually served the assignment). Pure: never mutates `venues`
// or the stats map; a venue with a healthy yield passes through unchanged. NEW — unrouted-venue
// surfacing: a venue with NO stats entry at all (the prospector named it but no lane was ever assigned
// it) earns its own '⚠ never assigned to any lane yet' suffix once the run is far enough along
// (`wave` given AND ≥ CONFIG.VENUE_UNROUTED_MIN_WAVE) that "not yet assigned" has become a real signal
// rather than early-run noise; `wave` omitted (single-brainer wave-0 seed call, or a caller that does
// not track wave) ⇒ the old passthrough-on-no-entry behavior.
function venuesWithYieldWarn(
  venues         ,
  venueStats                                                       ,
  wave         ,
)          {
  return venues.map((v) => {
    const s = venueStats[v.source];
    if (!s) {
      if (wave !== undefined && wave >= CONFIG.VENUE_UNROUTED_MIN_WAVE)
        return { ...v, goodFor: v.goodFor + ' — ⚠ never assigned to any lane yet' };
      return v;
    }
    if (s.assigned < CONFIG.VENUE_WARN_MIN || s.yielded !== 0) return v;
    return { ...v, goodFor: v.goodFor + ' — ⚠ 0 yield in ' + s.assigned + ' lanes' };
  });
}

// vocabSummary — the scheduler's COMMUNITY VOCABULARY clause input: the top `cap` terms by uses,
// "term (n)" comma-joined; '' when the vocabulary is empty (the caller's clause is then omitted).
const vocabSummary = (vocab        , cap        )         =>
  [...(vocab || [])]
    .sort((a, b) => b.uses - a.uses)
    .slice(0, cap)
    .map((t) => t.term + ' (' + t.uses + ')')
    .join(', ');

// packReaders — the MECHANICAL splitter (B5/B2/B7). Bin-pack a lane's sized sources into reader-units, each ≤
// `budget` tokens AND ≤ budget×charsPerToken CHARS. The UNIT is the budget, not the source: small sources that
// fit together COMBINE into one reader (its `reads` list holds several whole files); a source bigger than the
// budget SPLITS across enough readers, with `overlap` chars re-read at each split boundary.
//
// B2 — UNIT CONSISTENCY: the read window is in CHARS but the budget is in TOKENS, so `budget` is converted to a
//   char ceiling (`budgetChars = budget × charsPerToken`, the SAME over-count heuristic the scheduler sizes with)
//   and BOTH dimensions gate every pack: a source is split when size > budget OR chars > budgetChars, and the
//   split count is the MAX of both estimates — so a source the scheduler under-counts in tokens but is huge in
//   chars still splits. The scheduler's `size`/`chars` are treated as HINTS only (re-confirmed here defensively).
//   (The true byte-length / file-existence check lives in the reader — the workflow VM has no fs; see B3.)
// B7 — bounded packing: at most `maxSlices` whole sources combine into one reader (a lane of 40 tiny files never
//   packs 40 into one turn). Caller also caps sources-per-lane before this.
// A path-less / zero-char source is DROPPED (never emit a doomed reader). Returns one ReadSlice[] per reader.
function packReaders(
  sources                   ,
  budget        ,
  overlap        ,
  maxSlices         = Infinity,
  charsPerToken        ,
)                {
  const budgetChars = Math.max(1, Math.floor(budget * charsPerToken));
  const readers                = [];
  let cur              = [];
  let curTokens = 0;
  let curChars = 0;
  const flush = () => {
    if (cur.length) {
      readers.push(cur);
      cur = [];
      curTokens = 0;
      curChars = 0;
    }
  };
  for (const s of sources || []) {
    if (!s || !s.path) continue; // a path-less source is dropped (the reader has nothing on disk to read)
    // mechanical guard: derive chars + size defensively (inverse heuristic when one is missing), never trust junk.
    const chars =
      Number.isFinite(s.chars) && s.chars > 0
        ? Math.floor(s.chars)
        : Math.max(0, Math.round((Number(s.size) || 0) * charsPerToken));
    if (chars <= 0) continue;
    const size =
      Number.isFinite(s.size) && s.size > 0
        ? Math.floor(s.size)
        : Math.max(1, Math.round(chars / charsPerToken));
    // a source must split when it overflows EITHER the token budget OR the char ceiling (units kept consistent).
    if (size <= budget && chars <= budgetChars) {
      // whole source fits a reader-unit; flush first if combining it would overflow tokens/chars OR the slice cap.
      if (
        cur.length &&
        (curTokens + size > budget || curChars + chars > budgetChars || cur.length >= maxSlices)
      )
        flush();
      cur.push({ source: s.source, cachePath: s.path, offset: 0, limit: chars });
      curTokens += size;
      curChars += chars;
    } else {
      // oversized source — flush the current pack, then split it across enough readers (by BOTH dimensions) with overlap.
      flush();
      const parts = Math.max(Math.ceil(size / budget), Math.ceil(chars / budgetChars));
      const per = Math.ceil(chars / parts); // ≤ budgetChars by construction (forward progress past the overlap)
      for (let p = 0; p < parts; p++) {
        const start = Math.max(0, p * per - (p > 0 ? overlap : 0));
        const end = Math.min(chars, (p + 1) * per); // capped by the real remaining chars
        if (end > start)
          readers.push([
            { source: s.source, cachePath: s.path, offset: start, limit: end - start },
          ]);
      }
    }
  }
  flush();
  return readers;
}

// PROMPT_LOG — the exact prompt sent for EVERY agent call, keyed by label (always populated, not just in debug). The numbered phase files
// prepend their own prompt (Change E): the prompt lives in the phase file, not a separate _io.md.
const PROMPT_LOG                         = {};
// CHANGE E — prepend the exact prompt sent for `label` ahead of a numbered phase file's body. result.md stays clean (never wrapped).
const withPrompt = (label        , body        )         =>
  (PROMPT_LOG[label] ? '## Prompt sent\n\n' + PROMPT_LOG[label] + '\n\n---\n\n' : '') + body;

// render — fill a template's `{{key}}` holes from a vars object. A tiny deterministic
// `String.replace` replacer (no engine, no globals): (1) strip ONE trailing template newline
// (editors add one; the inline templates carried none); (2) strip standalone `{{! … }}` comment
// lines whole — line + newline — so they stay invisible; (3) substitute each `{{key}}` with its
// value, an absent key rendering as ''. Single-pass, so a substituted value is never re-scanned.
const render = (tpl        , vars                         )         =>
  tpl
    .replace(/\n$/, '')
    .replace(/^[ \t]*\{\{![\s\S]*?\}\}[ \t]*(?:\r?\n|$)/gm, '')
    .replace(/\{\{(\w+)\}\}/g, (_, k        ) => {
      const v = vars[k];
      return v == null ? '' : String(v);
    });

// markdown render of a wave's resultSoFar (the brainer's living memory) — this IS the kept per-wave log.
const bullets = (arr                             )         =>
  arr && arr.length ? arr.map((x) => '- ' + x).join('\n') : '_none_';
function resultSoFarMd(r                    )         {
  if (!r || typeof r !== 'object') return '_none_';
  return (
    '**Answer:** ' +
    (r.answer || '_(none)_') +
    '\n\n**Confidence:** ' +
    (r.confidence || '_(none)_') +
    (r.working ? '\n\n**Working:**\n\n' + r.working : '') +
    (r.assumptions && r.assumptions.length
      ? '\n\n**Assumptions:**\n' +
        r.assumptions.map((a) => '- **' + (a.claim || '') + '** — ' + (a.basis || '')).join('\n')
      : '') +
    '\n\n**Resolved:**\n' +
    bullets(r.resolved) +
    '\n\n**Open gaps:**\n' +
    bullets(r.openGaps) +
    '\n\n**Tensions:**\n' +
    bullets(r.tensions)
  );
}

// per-wave brainer markdown (one file per crawl wave). `store` = the open rabbit-hole store snapshot at write time.
                 
             
                  
              
                       
                 
                     
                                                                                                                  
  
                                                                                  
function waveMd(
  wave        ,
  coord                                                 ,
  picks            ,
  finds           ,
  store                  ,
)         {
  const sc = (p                          ) => (p.score != null ? p.score : 'new');
  return (
    '# Wave ' +
    wave +
    ' — Brainer\n\n**done:** ' +
    coord.stop.done +
    ' — ' +
    coord.stop.reason +
    '\n\n## Result so far\n\n' +
    resultSoFarMd(coord.resultSoFar) +
    (finds.length
      ? '\n\n## Findings pursued this wave\n\n' +
        finds
          .map(
            (f) => '### ' + f.rabbitHole + '\n\n_trail: ' + (f.trail || '') + '_\n\n' + f.summary,
          )
          .join('\n\n')
      : '') +
    '\n\n## Looking up next (' +
    picks.length +
    ')\n\n' +
    (picks
      .map(
        (p, i) =>
          i +
          1 +
          '. **[' +
          sc(p) +
          ']** #' +
          p.id +
          ' ' +
          p.keyword +
          '\n   - trail: ' +
          trailOf(p.path) +
          (p.sources && p.sources.length ? '\n   - venues: ' + p.sources.join(', ') : '') +
          (p.note ? '\n   - note: ' + p.note : '') +
          '\n   - ' +
          p.why,
      )
      .join('\n') || '_none_') +
    '\n\n## Open rabbit-holes (scored)\n\n' +
    ([...store]
      .sort((a, b) => (lastScore(b) ?? -1) - (lastScore(a) ?? -1))
      .map(
        (r) =>
          '- **[' +
          (lastScore(r) != null ? lastScore(r) : 'new') +
          ']** #' +
          r.id +
          ' ' +
          r.keyword,
      )
      .join('\n') || '_none_') +
    '\n'
  );
}

// claimsMd — the human-readable ledger artifact (_claims.md): every non-retracted claim, grouped by its
// COMPUTED status (settled → tentative → contested), one line each: `- c<id> [status·cluster·audit] claim
// = value — source (wave N, lane)`; then the nullAttacks ("Challenged and survived" — a completed
// counter-search that found nothing is first-class, distinct from "never challenged"); then the vocabulary.
function claimsMd(bs   
                  
                            
                     
 )         {
  const line = (c       )         =>
    '- c' +
    c.id +
    ' [' +
    c.status +
    '·' +
    c.cluster +
    '·' +
    c.audit +
    '] ' +
    c.claim +
    (c.value ? ' = ' + c.value : '') +
    ' — ' +
    c.source +
    ' (wave ' +
    c.wave +
    ', ' +
    c.lane +
    ')';
  const group = (status             )         =>
    bs.claims
      .filter((c) => !c.retracted && c.status === status)
      .map(line)
      .join('\n') || '_none_';
  const nullSection =
    bs.nullAttacks
      .map(
        (na) =>
          '- **' +
          na.topic +
          '** — queries: ' +
          na.queries.join(', ') +
          (na.claimIds.length ? ' → c' + na.claimIds.join(', c') : ''),
      )
      .join('\n') || '_none_';
  const vocabSection =
    bs.vocabulary
      .map((t) => '- **' + t.term + '**' + (t.gloss ? ' — ' + t.gloss : '') + ' (' + t.uses + ')')
      .join('\n') || '_none_';
  return (
    '# Claim ledger\n\n## Settled\n\n' +
    group('settled') +
    '\n\n## Tentative\n\n' +
    group('tentative') +
    '\n\n## Contested\n\n' +
    group('contested') +
    '\n\n## Challenged and survived\n\n' +
    nullSection +
    '\n\n## Vocabulary\n\n' +
    vocabSection +
    '\n'
  );
}
// ╔══ module: src/agents/brainer/prompts.ts ═══════════════════════════════
// BRAINER prompts — the brain's per-wave template + the clause-assembly function. Template strings
// are module-level consts; buildBrainer only assembles/substitutes the per-wave clauses.



                                                                            

const BRAINER_TPL = `{{! brainer — the brain: scores and steers rabbit-holes, keeps resultSoFar, decides done }}
You are the BRAINER — you make every decision in this research run and set its direction.
{{roleClause}}{{lastWaveClause}}
How the run works: a scout seeded the first rabbit-holes and a prospector named the source venues; then you drive each wave. For each lane you pick, a scheduler finds the highest-value sources and sequential readers read them in full (carrying a running answer across the sources), returning findings + new rabbit-holes; your per-lane \`note\` directs what the scheduler picks and what the readers extract. Then you update the running result, steer the next wave, and decide when to stop. On stop, a refinement stage hardens your findings, a judge stress-tests the answer, and a synthesiser writes the report.

The engine keeps the open rabbit-holes as an id-keyed store and carries each one's score history natively — you never re-emit the whole set, you return deltas against it.

Direction is two powers:
• LOOK UP rabbit-holes already in the store (by id) to research next.
• ORIGINATE — when the answer needs an angle, candidate, or sub-question no stored rabbit-hole covers, add it as a new directive {keyword, why, score} and a researcher will go collect it. Name a gap you can see rather than wait for one to surface; summon a candidate the scout missed — not padding. Put it in \`lookupNext\` to pursue now, or in \`add\` to park it for a later wave.

As you steer, hold three rules:
• Pivot on disproof — when a lead is fundamentally refuted, abandon it without sunk-cost and take a different road; a dead lead dropped is progress.
• Surfacing is not verifying — finding a result does not verify it. If the answer's headline rests on a claim you have not stress-tested, the judge will reject the stop, so stress-test load-bearing claims before declaring done.
• Promote serendipity — a surfaced, non-seeded candidate that out-evidences the seeded ones becomes first-class: deepen it like a seed rather than under-explore it for being off the seed list.

{{probeClause}}{{thinkerClause}}{{researcherClause}}

Wave {{wave}}. Query: "{{query}}". {{rubric}}
Scout landscape: {{landscape}}
RABBIT-HOLE STORE — open rabbit-holes (\`#id [last score or "new"] keyword — why\`); re-score up or down, a low one can resurrect:
{{open}}
ALREADY PURSUED — do not look up or re-originate these (research history):
{{pursuedList}}
Findings this wave (from the researchers' page-reading):
{{findings}}{{trajectory}}{{venuesClause}}{{languageClause}}{{calibrationClause}}{{sensitivityClause}}{{chaoClause}}

{{memoryClause}}{{ledgerClause}}
Update and return \`resultSoFar\` as the run's memory: refine \`answer\`; set \`keyClaimIds\` to the ledger ids the answer rests on; record the working \`assumptions\` the answer leans on (each {claim, basis}) and revise or retire them as evidence lands; move closed parts into \`resolved\`; keep \`openGaps\` current; record any \`tensions\` (conflicting sources); {{workingClause}}; set \`confidence\`.
Weight findings by evidence quality — funding independence, sample size, replication, stated limitations — not mere existence; let it drive both your scores and \`confidence\`.
For each headline / load-bearing finding, originate a lane to hunt failed replications, null trials, or refutations. Corroboration is what feeds the ledger's own settled/tentative computation — you never set status yourself; originate lanes that give the machinery independent clusters to count.{{attackClause}}{{computeField}}

Then return deltas against the store:
(1) \`rescore\`: [{id, score}] — only the rabbit-holes whose 0-100 score changes this wave (score every "new" one at least once); unlisted ones keep their last score. Score honestly per the rubric; a marginal one scores low. When you are setting stop.done=true this wave, skip scoring the new ones — a frontier you are closing needs no scores.
(2) \`add\`: [{keyword, why, score}] — new rabbit-holes to park in the store for a later wave (the engine assigns each an id).
(3) \`lookupNext\`: the rabbit-holes to research now — each either {id} (a stored one) or {keyword, why, score{{scoreFields}}} (one you originate and pursue now). None may be already pursued.{{assignClause}} For EVERY lookupNext lane author a \`note\`: the research directive — WHAT to find plus ranked fallbacks ("if not X, focus on Y; give both if available"). It steers both the scheduler's source pick and the reader's extraction; keep it distinct from \`why\` (your store/scoring rationale). A lane's method must be executable by a READ-ONLY reader over fetched pages (attack lanes alone get a bounded live search) — never assign per-item tracker probes, review-cadence checks, or interactive verification; reshape such a method into fetchable-source questions. Set refetch:true on a lane whose cached copy was reported CORRUPT so the scheduler bypasses the poisoned cache.
(4) \`rename\`: [{id, keyword, why?}] — relabel a rabbit-hole, keeping its id + history (optional).
(5) \`drop\`: [id, …] — eliminate a dead/duplicate rabbit-hole; a merge = drop the duplicate and rescore the survivor (optional).{{spawnClause}}
(6) \`stop\`: {done, reason}. {{stop}}{{goalClause}}{{voiClause}}{{validatorClause}}{{unsourcedClause}}${EMIT}{{FINISH}}
`;

const buildBrainer = ({
  wave,
  query,
  rubric,
  landscape,
  pursuedList,
  open,
  findings,
  topScores,
  resultSoFar,
  stop,
  mode,
  venues,
  languageGuidance,
  lastValidatorMissing,
  unsourced,
  compute,
  computeNote,
  thinkerNote,
  researcherNote,
  isChild,
  parentName,
  mandate,
  trail,
  canSpawn,
  lastWave,
  ledger,
  calib,
  derivation,
  chao,
}             ) => {
  // brainer-tree role: a CHILD drives ONE branch and may abandon it; the ROOT carries the whole run and never can.
  const roleClause = isChild
    ? `\nYou are a CHILD brainer: ${parentName || 'a parent'} spawned you to drive ONE branch — ${mandate || 'your mandate'} — split from the path ${trail || '(root)'}. Pursue that branch deep on the store + memory you inherited. If it proves a dead end, abandon it: set stop.lost=true with a one-line reason and you are done (no answer expected). You carry only this branch, not the whole run.`
    : `\nYou are the ROOT brainer: you carry the whole run to a real answer — stop.lost is not yours to set.`;
  const lastWaveClause = lastWave
    ? `\nLAST WAVE — the run is wrapping up: consolidate your answer into resultSoFar and set stop.done=true. Request no new lookupNext; research is closing.`
    : '';
  // spawn is offered ONLY while spawning is still permitted (caps not hit) — don't tempt a capped-out brainer.
  const spawnClause = canSpawn
    ? `\n(5b) \`spawn\` (at most ONE this wave): when the goal holds two or more INDEPENDENT investigations — separate evidence bases, sub-questions that do not inform each other — hand one to a focused child brainer THIS wave instead of carrying both in your single line: emit \`spawn\` {id (or keyword+why), mandate}. The child inherits a clean copy of your store + memory, aimed by the mandate; you drop that branch and steer the rest. Reserve a spawn for a branch substantial enough to run on its own — not a single lane — but when the run genuinely splits in two, spawn rather than interleave.`
    : `\n(spawn is unavailable this wave — the parallel-brainer cap is reached; do not emit one.)`;
  const thinkerClause = thinkerNote ? '\n\n' + thinkerNote : '';
  const validatorClause = lastValidatorMissing
    ? `\nVALIDATOR — last wave left these unfilled; re-pursue the reopened lanes or originate new ones to close them: ${lastValidatorMissing}`
    : '';
  const unsourcedClause = unsourced
    ? '\nSCHEDULER REPORT — refs/venues the last wave could NOT source, substituted, or flagged: ' + unsourced + '\nA substituted venue is NOT covered — re-route it, set refetch, or record the gap explicitly in resultSoFar.openGaps.'
    : '';
  const researcherClause = researcherNote ? '\n' + researcherNote : '';
  const trajectory = topScores.length
    ? `
TOP-PICK SCORE TRAJECTORY by wave (calibrated 0-100): ${plain(topScores)}
A steadily declining trajectory means high-value rabbit-holes are drying up — read it as convergence.`
    : '';
  const goalClause =
    mode === 'goal'
      ? `
Goal mode: if the goal is already well answered and the best remaining rabbit-hole adds only marginal value (a declining trajectory is strong evidence), set stop.done=true rather than chase diminishing returns.`
      : '';
  const venuesClause =
    venues && venues.length
      ? `
SOURCE VENUES (from the prospector) — give each lookupNext pick the subset whose source fits its lane, in its \`sources\`, so its researcher searches the right places first:
${plain(venues)}
A venue suffixed '⚠ never assigned' has had NO lane yet — either route a lane to it this wave or name the waiver in stop.reason when you declare done.`
      : '';
  const memoryClause =
    wave === 0
      ? `RESULT SO FAR — the run's living MEMORY. Start it this wave: capture the answer as it stands plus the load-bearing evidence behind it.`
      : `RESULT SO FAR — the run's living MEMORY, carried wave to wave. Prior version:
${plain(resultSoFar)}`;
  const languageClause =
    languageGuidance && languageGuidance.trim()
      ? `
Some of this topic's strongest literature is non-English. Guidance: ${languageGuidance}. Deliberately route some lanes to the non-English venues above, giving each its native venue(s) in \`sources\` — rather than defaulting every lane to English.`
      : '';
  const probeClause = `Before you decide, hunt for coverage gaps — a candidate, sub-question, or angle the goal needs that no lane has touched — and probe them yourself with WebSearch / mcp__harvester__fetch, as many as you need, to fill them; fold what you find into resultSoFar and originate the missing rabbit-holes into \`lookupNext\`. Beyond gap-filling, leave the heavy digging to the lane readers.`;
  const scoreFields = ', sources, note';
  const assignClause = venues && venues.length ? ' Assign each its `sources` venue subset.' : '';
  // workingClause — gated on compute exactly as computeField is. compute OFF ⇒ the brainer must NOT hand-roll a
  // derivation: leave `working` empty and treat unknowns as STATED UNCERTAINTY (only an EXPLICIT compute:false
  // turns it off, so the prompt-only callers — which omit compute — keep the derive-when-needed default).
  const workingClause =
    compute === false
      ? `leave \`working\` empty and treat any value you cannot source as STATED UNCERTAINTY in the answer — never hand-roll a derivation`
      : `for build-the-answer / estimate questions grow the \`working\` derivation chain (else '')`;
  // derivationClause (compute on) — replaces v2's inline COMPUTE TO STEER: redirect from deriving-inline to
  // AUTHORING the derivation once as a stored artifact the engine reruns cheaply every wave (kept in the
  // `computeField` slot/variable name — same template placeholder, same compute gate as before).
  const computeField = compute
    ? `

When the answer must be BUILT (an estimate, a synthesis with arithmetic), AUTHOR the derivation once as a stored artifact: return \`derivation\` {code, inputs} — pure seeded python3, reads one JSON arg of input values, prints {quantiles, sensitivity}. Name each input, tie it to the ledger claims backing it (claimIds), and mark unevidenced inputs prior:true with a WIDE dist — never a fake point value. The engine reruns it cheaply every wave and shows you the sensitivity; re-emit \`derivation\` only to change code or inputs. Fold the headline number into \`working\`.${computeNote ? '\n\n' + computeNote : ''}`
    : '';
  // ledgerClause — the claim ledger digest (v3 STEERING); omitted entirely before any claim is ledgered.
  const ledgerClause = ledger
    ? `\nCLAIM LEDGER — the run's evidence, quote-pinned + audited by machinery (each line: c12 [status·clu2·audit] claim = value — ids look like c12, clusters like clu2):\n${ledger}\nCorroboration counts independence CLUSTERS, not sources — two claims in one cluster are ONE voice. Reference claims by their NUMBER (the digits in its c12 id) in \`keyClaimIds\` (the ids the answer rests on). Evidence lives HERE now — do not re-emit facts into resultSoFar.evidence.`
    : '';
  // attackClause — gated on the SAME ledger presence: nothing to challenge before the first claim lands.
  const attackClause = ledger
    ? ` For every keyClaim still \`tentative\` that has never been challenged (no attack lane, no nullAttack), originate ONE kind:"attack" lane hunting its strongest realistic counter-evidence, NAMING the target's c-id in the lane's note (the machinery links the attack outcome to that claim) — a claim that has survived attack outranks one nobody questioned.`
    : '';
  // calibrationClause — only kinds with a real observation (n>0) render; an all-neutral table teaches nothing.
  const calibEntries = Object.entries(calib || {}).filter(([, v]) => v && v.n > 0);
  const calibrationClause = calibEntries.length
    ? `\nCALIBRATION — your lead kinds, predicted vs realized yield this run (ratio<1 = you overestimate this kind): ${calibEntries.map(([k, v]) => k + ': ' + v.ratio.toFixed(2) + ' (' + v.n + ')').join(', ')}. The engine already weights selection by these; let them temper your scores too.`
    : '';
  // sensitivityClause + voiClause — gated on a derivation with at least one completed rerun (lastRun);
  // a freshly-authored, not-yet-run derivation has no sensitivity to show.
  const priorNames = new Set(
    (derivation ? derivation.inputs : []).filter((i) => i.prior).map((i) => i.name),
  );
  const sensEntries = derivation ? Object.entries(derivation.sensitivity || {}) : [];
  const topInput = sensEntries.length ? sensEntries.reduce((a, b) => (b[1] > a[1] ? b : a))[0] : '';
  const staleFlag =
    derivation && derivation.stale
      ? ' (STALE — last rerun failed; consider re-emitting the derivation.)'
      : '';
  const sensitivityClause = derivation
    ? `\nDERIVATION STATE — current quantiles: ${Object.entries(derivation.quantiles || {})
        .map(([k, v]) => k + '=' + v)
        .join(', ')}. Variance shares by input: ${sensEntries
        .map(([k, v]) => k + ': ' + v.toFixed(2) + (priorNames.has(k) ? ' (PRIOR)' : ''))
        .join(
          ', ',
        )} (inputs marked PRIOR are placeholders awaiting evidence). Chase the inputs that dominate the variance: a lane that pins a ${topInput} beats any topical lead.${staleFlag}`
    : '';
  const voiClause =
    mode === 'goal' && derivation
      ? `\nSTOP TEST (value-of-information): when the goal is answered AND no open rabbit-hole targets a derivation input with variance share > ${CONFIG.VOI_SENS_THRESHOLD}, further waves cannot materially change the answer — set stop.done=true.`
      : '';
  // chaoClause — collect mode only, once the first collect-mode wave has computed a coverage estimate.
  const chaoClause =
    mode === 'collect' && chao
      ? `\nCOVERAGE — statistical estimate from the claim ledger: ~${Math.round(chao.unseen)} distinct findings remain unfound (coverage ≈${Math.round(chao.coverage * 100)}%). Read it as the inventory's completeness, not a feeling.`
      : '';
  return render(BRAINER_TPL, {
    roleClause,
    lastWaveClause,
    spawnClause,
    probeClause,
    thinkerClause,
    researcherClause,
    wave,
    query,
    rubric,
    landscape,
    open: plain(open),
    pursuedList: plain(pursuedList),
    findings: plain(findings),
    trajectory,
    venuesClause,
    languageClause,
    calibrationClause,
    sensitivityClause,
    chaoClause,
    memoryClause,
    ledgerClause,
    attackClause,
    scoreFields,
    assignClause,
    workingClause,
    stop,
    goalClause,
    voiClause,
    validatorClause,
    unsourcedClause,
    computeField,
    FINISH,
  });
};

// brain FINALIZE-COMPUTE — the brainer re-invoked (code-capable, full resultSoFar) to DERIVE the final answer
// on the hardened facts, on the judge's directive. Transplants the old compute chain's rigor: fact-check the
// input numbers, write + run a short script for the arithmetic, propagate error bars, self-check.
const BRAIN_COMPUTE_TPL = `{{! brain-compute — the brain derives the final answer on the hardened facts, with rigor + error bars }}
You are the BRAINER, now DERIVING the final answer for: "{{query}}". The judge ruled the answer still needs this derivation — build it, do not restate facts.
Judge directive: {{directive}}
Judge reasoning: {{reason}}
Hardened facts (adversarially fact-checked + source-corrected — your input numbers):
{{hardenedFacts}}
The run's accumulated RESULT (your answer + the half-built \`working\` derivation to finish):
{{resultSoFar}}
Derive with rigor:
- first fact-check your input numbers: verify each against a current primary source (WebSearch / mcp__harvester__fetch) and correct any that is stale, wrong, or imprecise before computing — a derivation is only as sound as its inputs;
- assemble the verified inputs with their units;
- write and run a short script for any non-trivial arithmetic — load Bash + Write via ToolSearch if absent, run python (or node) — compute, do not estimate;
- propagate the input uncertainties into an explicit ± error range;
- adversarially check your own work: re-derive a second way or sanity-check against an anchor, and fix any unit / formula / arithmetic slip.{{noteClause}}{{thinkerClause}}
Return the updated \`resultSoFar\`: fold the completed derivation into \`working\` (the verified inputs, the steps, the numbers, the ± result, the self-check), put the headline computed result in \`answer\`, and keep \`keyClaimIds\` / \`resolved\` / \`openGaps\` / \`tensions\` / \`confidence\` current — \`keyClaimIds\` are the ledger claim ids the answer rests on; the deprecated \`evidence\` array is never populated.{{FINISH}}
`;

const buildBrainerCompute = ({
  query,
  resultSoFar,
  hardenedFacts,
  directive,
  reason,
  computeNote,
  thinkerNote,
}                    ) => {
  const noteClause = computeNote ? '\n' + computeNote : '';
  const thinkerClause = thinkerNote ? '\n\n' + thinkerNote : '';
  return render(BRAIN_COMPUTE_TPL, {
    query,
    resultSoFar: plain(resultSoFar),
    hardenedFacts: plain(hardenedFacts),
    directive: directive || '(derive the answer the goal needs)',
    reason: reason || '',
    noteClause,
    thinkerClause,
    FINISH,
  });
};
// ╔══ module: src/agents/brainer/index.ts ═════════════════════════════════
// BRAINER — the brain / global reducer. Sees the open store + pursued set + running resultSoFar; returns
// the updated resultSoFar + DELTAS (rescore / add / lookupNext / rename / drop / stop). Looks up stored
// leads OR originates new directions; code-capable (general-purpose) when compute is on so it derives its
// own steering numbers inline — no separate compute stage.
// Tier: opus — ALWAYS Opus (the global brain/reducer — measured: a Haiku brainer scored erratically +
// drifted off-goal). Effort: xhigh — re-scores the store every wave AND sets direction AND maintains
// resultSoFar; the one role where the extra reasoning budget pays back most.



                                                                       


// COORD = the brainer's per-wave output: the updated resultSoFar + DELTAS against the engine's id-keyed open store. The engine carries
// each rabbit-hole's id + scoreHistory natively — the brainer never re-emits the whole set, it only sends what changed.
//
// Built per call by buildCoord, NOT shipped as one static shape: the platform classifier that screens
// agent spawns rejects oversized output schemas ("output schema too large to classify safely" — observed
// killing wave-0 brainers live, across models). So the optional clauses ship ONLY when the call can use
// them — `derivation` only when compute is on, `spawn` only on a wave that may spawn — and every
// description is kept terse (steering prose lives in the prompt, which already carries it). COORD (the
// full shape) stays exported as the canonical contract for types, tests, and the sandbox check.
const SPAWN         = {
  type: 'object',
  properties: {
    id: {
      type: 'number',
      description: 'an OPEN rabbit-hole id for the child (or omit and give keyword+why)',
    },
    keyword: { type: 'string' },
    why: { type: 'string' },
    mandate: {
      type: 'string',
      description: 'the directive that aims the child brainer — what this one branch must chase',
    },
  },
  required: ['mandate'],
  description:
    'OPTIONAL, at most ONE per wave: spawn a focused child brainer onto a branch worth a dedicated brain — spawns are expensive. Omit when not spawning.',
};
const DERIVATION         = {
  type: 'object',
  properties: {
    code: {
      type: 'string',
      description:
        'pure seeded python3: reads ONE json arg {inputName: value}, prints ONE json {quantiles:{p10,p50,p90}, sensitivity:{inputName: varianceShare 0-1}}',
    },
    inputs: {
      type: 'array',
      items: {
        type: 'object',
        properties: {
          name: { type: 'string' },
          dist: {
            type: 'string',
            description: 'the value/distribution to feed, e.g. "lognormal(mu=…, sigma=…)" or a point value',
          },
          claimIds: { type: 'array', items: { type: 'number' } },
          prior: { type: 'boolean', description: 'true = wide-prior placeholder, not evidence-backed' },
        },
        required: ['name', 'dist', 'claimIds', 'prior'],
      },
    },
  },
  required: ['code', 'inputs'],
  description:
    "OPTIONAL: author (or re-author) the run's stored derivation — the engine reruns it cheaply every wave. Re-emit only to change the code or the inputs.",
};
function buildCoord(opts                                         )         {
  const properties                         = {
    resultSoFar: RESULT_SO_FAR,
    rescore: {
      type: 'array',
      items: {
        type: 'object',
        properties: { id: { type: 'number' }, score: { type: 'number' } },
        required: ['id', 'score'],
      },
      description:
        'only the open rabbit-holes whose score changes this wave; unlisted ones keep their last score. Score every "new" (unscored) one at least once.',
    },
    add: {
      type: 'array',
      items: SCORED,
      description: 'new rabbit-holes to park in the store for a later wave (the engine assigns ids)',
    },
    lookupNext: {
      type: 'array',
      items: LOOKUP,
      description:
        'the rabbit-holes to research now — each either {id} (stored) or {keyword,why,score,…} (originate-and-pursue-now). None may be already pursued; assign each its `sources` venue subset.',
    },
    rename: {
      type: 'array',
      items: {
        type: 'object',
        properties: {
          id: { type: 'number' },
          keyword: { type: 'string' },
          why: { type: 'string' },
        },
        required: ['id', 'keyword'],
      },
      description: 'relabel a rabbit-hole, keeping its id + score history',
    },
    drop: {
      type: 'array',
      items: { type: 'number' },
      description:
        'ids of dead/duplicate rabbit-holes to eliminate (a MERGE = drop the duplicate, rescore the survivor)',
    },
  };
  if (opts.canSpawn) properties.spawn = SPAWN;
  if (opts.compute) properties.derivation = DERIVATION;
  properties.stop = {
    type: 'object',
    properties: {
      done: { type: 'boolean' },
      reason: { type: 'string', description: 'one line: why done, or what is still missing' },
      lost: {
        type: 'boolean',
        description:
          'CHILD brainers only: this branch is a dead end — abandon it (no answer expected). The root brainer must never set this.',
      },
    },
    required: ['done', 'reason'],
  };
  return {
    type: 'object',
    properties,
    required: ['resultSoFar', 'rescore', 'add', 'lookupNext', 'stop'],
  };
}
const COORD         = buildCoord({ compute: true, canSpawn: true });

const brainer                     = {
  tier: CONFIG.TIER.brainer,
  effort: CONFIG.EFFORT.brainer,
  schema: COORD,
  buildPrompt: buildBrainer,
};

// BRAIN_COMPUTE = the brain finalize-compute output: the updated resultSoFar with the derivation folded into `working`.
const BRAIN_COMPUTE         = {
  type: 'object',
  properties: { resultSoFar: RESULT_SO_FAR },
  required: ['resultSoFar'],
};
// ╔══ module: src/runtime.ts ══════════════════════════════════════════════


                                                              

// ─────────────────────────────────────────────────────────────────────────────
// Agent runtime — the shared sub-agent caller + the debug capture buffers. Lives
// in its own module (bundled before the per-agent run.ts modules) so every run fn
// imports retryAgent without a cycle back through engine.ts; the engine no longer
// owns the agent-call plumbing, only the orchestration that consumes it.
// ─────────────────────────────────────────────────────────────────────────────

// L7 retry indirection — wraps every agent() call; the _agent alias keeps it from rewriting itself.
const _agent = agent;
// Debug capture (opt-in via arg.debug): the raw agent I/O + the full run-log stream, consumed by the end Debug & Analysis agent.
const IO_LOG               = [];
const LOG_BUFFER           = [];
const _log = globalThis.log;
try {
  globalThis.log = (m          ) => {
    const s = typeof m === 'string' ? m : String(m);
    // per-wave checkpoint lines are a live-output/recovery mechanism, not a debug narrative — keep them
    // OUT of _debug.md's Run log so a long run's checkpoint spam never bloats it.
    if (CONFIG.debug && !s.startsWith(CONFIG.CHECKPOINT_MARK)) LOG_BUFFER.push(s);
    return _log(m);
  };
} catch (e) {
  /* log not writable → run-log just won't be buffered */
}

// run a sub-agent with AGENT_RETRIES retries, narrowing the result to its agent's typed `*Out` shape (T); degrades to null when exhausted.
const retryAgent = async    (prompt        , opts           )                    => {
  if (opts && opts.label) PROMPT_LOG[opts.label] = prompt;
  for (let attempt = 0; attempt <= CONFIG.AGENT_RETRIES; attempt++) {
    try {
      const out = (await _agent(prompt, opts))     ;
      // The harness resolves to null WITHOUT throwing when the sub-agent dies on a
      // terminal API error (e.g. a safety-classifier block) or is skipped mid-run.
      // Route null through the same retry ladder as a thrown error — otherwise the
      // ladder never engages on exactly the failure class it exists for. A borderline
      // classifier block is often probabilistic; a fresh spawn frequently passes.
      if (out == null)
        throw new Error('agent returned null (terminal API error / skip / safety block)');
      if (CONFIG.debug)
        IO_LOG.push({
          label: (opts && opts.label) || '?',
          model: (opts && opts.model) || '?',
          phase: (opts && opts.phase) || '?',
          prompt,
          output: out,
        });
      return out;
    } catch (e) {
      log(
        '  ⚠ agent error (attempt ' +
          (attempt + 1) +
          '/' +
          (CONFIG.AGENT_RETRIES + 1) +
          '): ' +
          ((e && e.message) || e),
      );
      if (attempt === CONFIG.AGENT_RETRIES) {
        log('  ⚠ agent retries exhausted → degraded to null');
        if (CONFIG.debug)
          IO_LOG.push({
            label: (opts && opts.label) || '?',
            model: (opts && opts.model) || '?',
            phase: (opts && opts.phase) || '?',
            prompt,
            output: null,
            error: (e && e.message) || String(e),
          });
        return null;
      }
    }
  }
  return null;
};
// ╔══ module: src/agents/brainer/run.ts ═══════════════════════════════════




                                                          
                                                                                         

// the single Opus BRAINER — the brain / global reducer. Sees the open store + pursued set + running resultSoFar; returns the updated
// resultSoFar + DELTAS (rescore / add / lookupNext / rename / drop / stop). Can LOOK UP stored leads OR ORIGINATE new directions; code-capable
// (general-purpose) when compute is on, so it can derive its own steering numbers inline — no separate compute stage.
async function runBrainer(
  bs              ,
  wave        ,
  findings           ,
  phaseName         = CONFIG.PHASE.crawl,
  ctx                                             ,
)                        {
  const open = bs.rabbitHoles.map(openLine);
  // v3 STEERING — the ledger digest + calibration/sensitivity/coverage state, computed here (pure reads off
  // bs) and handed to buildBrainer, which decides per-clause whether there is anything to say.
  const ledger = ledgerLines(bs, CONFIG.BRAINER_LEDGER_CAP);
  const derivation =
    bs.derivation && bs.derivation.lastRun
      ? {
          quantiles: bs.derivation.lastRun.quantiles,
          sensitivity: bs.derivation.lastRun.sensitivity,
          inputs: bs.derivation.inputs,
          stale: !!bs.derivationStale,
        }
      : undefined;
  return retryAgent       (
    brainer.buildPrompt({
      wave,
      query: CONFIG.query,
      rubric: CONFIG.RUBRIC,
      landscape: bs.scout .landscape,
      pursuedList: bs.pursuedList,
      open,
      findings,
      topScores: bs.topScores,
      resultSoFar: bs.resultSoFar,
      stop: CONFIG.STOP,
      mode: CONFIG.mode,
      venues: venuesWithYieldWarn(bs.highValueSources, bs.venueStats, wave),
      languageGuidance: bs.languageGuidance,
      lastValidatorMissing: bs.lastValidatorMissing,
      unsourced: bs.lastUnsourced,
      compute: CONFIG.compute,
      computeNote: CONFIG.COMPUTE_NOTE,
      thinkerNote: CONFIG.THINKER_NOTE,
      researcherNote: CONFIG.RESEARCHER_NOTE,
      ledger,
      calib: bs.yieldCalib,
      derivation,
      chao: bs.chao,
      // brainer-tree context — identity off the brainer, per-wave permissions off ctx (both inert in single-brainer runs)
      isChild: !bs.isRoot,
      parentName: bs.parentName || undefined,
      mandate: bs.mandate || undefined,
      trail: bs.trail || undefined,
      canSpawn: ctx ? ctx.canSpawn : false,
      lastWave: ctx ? ctx.lastWave : false,
    }),
    {
      label: 'brainer-' + (bs.isRoot ? '' : bs.name + '-') + 'w' + wave,
      phase: phaseName,
      model: brainer.tier,
      effort: brainer.effort,
      // pruned per call — optional clauses inflate the schema past the spawn classifier's size limit
      schema: buildCoord({ compute: CONFIG.compute, canSpawn: !!(ctx && ctx.canSpawn) }),
      agentType: CONFIG.compute ? CONFIG.GENERAL_PURPOSE : undefined,
    },
  );
}

// brain FINALIZE-COMPUTE — the brain (code-capable) derives the answer on the hardened facts, per the judge directive.
// Pure: returns the BrainComputeOut; the engine folds out.resultSoFar back into bs.
async function runBrainerCompute(
  bs              ,
  hardenedFacts               ,
  directive        ,
  reason        ,
  pass        ,
)                                  {
  return retryAgent                 (
    buildBrainerCompute({
      query: CONFIG.query,
      resultSoFar: bs.resultSoFar,
      hardenedFacts,
      directive,
      reason,
      computeNote: CONFIG.COMPUTE_NOTE,
      thinkerNote: CONFIG.THINKER_NOTE,
    }),
    {
      label: 'brain-compute-' + pass,
      phase: CONFIG.PHASE.finalize,
      model: brainer.tier,
      effort: brainer.effort,
      agentType: CONFIG.GENERAL_PURPOSE,
      schema: BRAIN_COMPUTE,
    },
  );
}
// ╔══ module: src/agents/claimAuditor/prompts.ts ══════════════════════════
// CLAIM AUDITOR prompts — the batched mechanical quote-audit template + its assembly function. Template
// strings are module-level consts; buildClaimAuditor only assembles/substitutes the items list. The audit
// normalizes both sides (dashes/quotes/ellipsis/markdown/whitespace) before matching so formatting noise
// never fails a real pin, does ordered ellipsis-fragment matching for spliced quotes, and — when a quote
// is broken but a verified contiguous span exists in the file — auto-REPINs it via newQuote instead of
// failing the claim outright.



                                                           

const CLAIM_AUDIT_TPL = `{{! claimAuditor — batched mechanical quote audit: does each claim's quote exist (verbatim or via normalized/ellipsis match) in its cache file, and does it carry the claim on its own? Broken-but-locatable quotes auto-repin. }}
You are the CLAIM AUDITOR. For each item below, mechanically verify its quote against the cache file on disk — you are grepping for a pin, not judging truth.
Items (\`#id claim | quote | cachePath\`):
{{items}}
For EACH item, use python3 for a robust NORMALIZED substring search — never judge by eye:
1. Read the file at cachePath with errors='replace'. If the file cannot be read for ANY reason (missing, permission, anything) NEVER fabricate a verdict — that item is 'fail' with note "file unreadable".
2. NORMALIZE the file text and the quote IDENTICALLY before comparing: lowercase; fold unicode dashes (em/en) to '-'; fold curly quotes to straight; fold the ellipsis character (…) to '...'; replace non-breaking spaces with plain spaces; strip markdown emphasis characters (*, _, \`); collapse every whitespace run to one space. Formatting noise must never fail a real pin.
3. verdict 'pass' when the normalized quote is a substring of the normalized file text AND the quoted text, on its own, actually carries the claim (not merely nearby context).
4. When the substring test fails and the quote contains '...': split on '...' and test each fragment of ≥15 chars as its own normalized substring, required to appear IN ORDER in the file. All found in order ⇒ the quote was ellipsis-spliced from real text: verdict 'repinned', and set newQuote to ONE contiguous span copied EXACTLY from the ORIGINAL (un-normalized) file text, at most {{quoteMax}} characters, that best carries the claim on its own (typically the strongest fragment's full sentence). Copy it from the file byte-for-byte — never compose or paraphrase it.
5. When the substring test fails without an ellipsis: search the file for the claim's key phrases; if ONE contiguous span of at most {{quoteMax}} chars carries the claim, verdict 'repinned' with that span as newQuote (same copy-exactly rule); otherwise verdict 'fail' with a one-line note naming precisely where it diverges (e.g. "quote not in file at all", "number differs: quote says 'over one year', file says 'over 1 year'").
Return checks: one {id, verdict, note?, newQuote?} per item — verdict is 'pass', 'fail', or 'repinned'; newQuote ONLY with 'repinned'.{{FINISH}}
`;

const buildClaimAuditor = ({ items }                ) =>
  render(CLAIM_AUDIT_TPL, { items: plain(items), quoteMax: CONFIG.QUOTE_MAX_CHARS, FINISH });
// ╔══ module: src/agents/claimAuditor/index.ts ════════════════════════════
// CLAIM AUDITOR — batched per wave: for each new claim with a cachePath, mechanically verify (Bash/python3
// grep) that its verbatim quote exists in the cache file AND carries the claim on its own, using a
// normalized match (dashes/quotes/ellipsis/markdown/whitespace folded on both sides) plus ordered
// ellipsis-fragment matching for spliced quotes. A quote that's broken but verifiably locatable in the
// file auto-REPINs — the auditor replaces it with newQuote, a verified contiguous span, and the claim
// re-enters the ledger as a verified pass rather than dying. Tier: haiku (a bounded, mechanical per-batch
// job). Effort: medium. Dies → its claims stay 'pending' (unpinned downstream).


                                                                          

const CLAIM_AUDIT         = {
  type: 'object',
  properties: {
    checks: {
      type: 'array',
      items: {
        type: 'object',
        properties: {
          id: { type: 'number', description: 'the claim id this verdict is for' },
          verdict: { type: 'string', enum: ['pass', 'fail', 'repinned'] },
          note: {
            type: 'string',
            description: 'one line, e.g. "file unreadable" or why the audit failed',
          },
          newQuote: {
            type: 'string',
            description:
              'repinned only: ONE contiguous span copied exactly from the file, ≤ the quote cap, that replaces the broken quote',
          },
        },
        required: ['id', 'verdict'],
      },
      description: 'one verdict per audited claim',
    },
  },
  required: ['checks'],
};

const claimAuditor                        = {
  tier: CONFIG.TIER.claimAuditor,
  effort: CONFIG.EFFORT.claimAuditor,
  schema: CLAIM_AUDIT,
  buildPrompt: buildClaimAuditor,
};
// ╔══ module: src/agents/claimAuditor/run.ts ══════════════════════════════
// CLAIM AUDITOR dispatch — batches new claims with a cachePath into ≤AUDIT_BATCH-item chunks, one
// retryAgent call per chunk, all chunks dispatched CONCURRENTLY via parallel(). A dead (null) chunk
// contributes nothing — its claims simply stay 'pending' (the caller treats pending as unpinned
// downstream). Hallucinated ids (not in the chunk's own input set) are dropped. Zero auditable claims →
// no agent spawned at all. Verdicts are 'pass' | 'fail' | 'repinned' — a 'repinned' verdict carries its
// newQuote (the verified contiguous span that replaces the broken quote) through to the caller.




                                                          
                                                                 

async function runClaimAuditor(
  bs              ,
  claims         ,
  tag        ,
  phaseName        ,
)                                                                                                    {
  const out = new Map                                                                                     ();
  const auditable = claims.filter((c) => !!c.cachePath); // only claims that can actually be greped
  if (!auditable.length) return out;
  const chunks = chunk(auditable, CONFIG.AUDIT_BATCH);
  // parallel() journals thunk results as JSON (a Set would come back as {}), so the thunk returns
  // the bare agent result and the id set is rebuilt per chunk on the consumer side (order-aligned).
  const results = await parallel(
    chunks.map((ch, i) => () =>
      retryAgent               (
        claimAuditor.buildPrompt({
          items: ch.map((c) => ({
            id: c.id,
            claim: c.claim,
            quote: c.quote,
            cachePath: c.cachePath ,
          })),
        }),
        {
          label: 'claim-audit-' + tag + (chunks.length > 1 ? '-b' + i : ''),
          phase: phaseName,
          model: claimAuditor.tier,
          effort: claimAuditor.effort,
          agentType: CONFIG.GENERAL_PURPOSE,
          schema: claimAuditor.schema,
        },
      ),
    ),
  );
  results.forEach((res, i) => {
    if (!res) return; // dead chunk — its claims stay 'pending'
    const ids = new Set(chunks[i].map((c) => c.id));
    for (const c of res.checks || [])
      if (c && ids.has(c.id) && (c.verdict === 'pass' || c.verdict === 'fail' || c.verdict === 'repinned'))
        out.set(c.id, { verdict: c.verdict, note: c.note, newQuote: c.newQuote });
  });
  return out;
}
// ╔══ module: src/agents/debugAnalyst/prompts.ts ══════════════════════════
// DEBUG ANALYST prompts — the diagnostics template + its assembly function. Template strings are
// module-level consts; buildDebugAnalyst only assembles/substitutes the focus clause.


                                                             

const DEBUG_TPL = `{{! debug — consolidates metrics, run log, and raw agent I/O into one debug report }}
Consolidate and analyze this RR run's diagnostics for an engineer debugging the pipeline. Goal: "{{query}}".
Walk it phase by phase — scout → prospector → each research wave → finalize (initiate → refine → judge → synthesise) — reporting what happened at each with the actual numbers, plus anomalies, degraded/failed agents, or wasted effort to fix.
Prospector→researcher utilization (run this check): the prospector named these venues:
{{highValueSources}}
Each lane in laneRecords carries the \`assignedVenues\` the brainer gave it; from that lane's summary + rabbitHoles, judge whether the researcher actually drew on those venues. Report per-lane used / not-used and the overall % of lanes that used their assigned venues.{{focusClause}}
v3 ledger machinery to sanity-check: claims are quote-pinned + audited by a claimAuditor (dead auditor ⇒ claims stuck pending), clustered by a lineageClerk (bad clustering ⇒ wrong settled/tentative), derivations rerun by a rerunner — all degrade to null. Check metrics.claimsTotal / nullAttacksTotal / citationsBogus / chao for anomalies (e.g. all claims pending, zero nullAttacks on a contested topic, bogus citations stripped). metrics.auditCounts is the quote-pin audit outcome — read the fail and repinned counts BEFORE declaring pinning healthy: citationsBogus alone masks a broken pin contract; a high fail count surviving normalization+repin means real pin rot. metrics.pursuedTotal INCLUDES finalize judge-reopen lanes (metrics.reopenedLanes) — reconcile lane counts against that split before suspecting a silent lane loss. Walk the FINALIZE phase with the same rigor as the waves: judge verdicts (metrics.goalMet, metrics.judgePasses), refine counter-findings, and any crawl reopen — when a report contradicts the crawl-era answer, the turn usually happened there.
Metrics:
{{metrics}}
Lane records (wave, keyword, assignedVenues, summary, rabbitHoles):
{{laneRecords}}
Per-wave log:
{{waveLog}}
Per-wave result-so-far log (the brainer's running memory each wave):
{{resultLog}}
Return diagnosis (markdown).{{FINISH}}
`;

const buildDebugAnalyst = ({
  query,
  focus,
  metrics,
  waveLog,
  resultLog,
  highValueSources,
  laneRecords,
}                  ) => {
  const focusClause = focus
    ? `
Then answer this run-specific question directly: ${focus}`
    : '';
  return render(DEBUG_TPL, {
    query,
    highValueSources: plain(highValueSources),
    focusClause,
    metrics: plain(metrics),
    laneRecords: plain(laneRecords),
    waveLog: plain(waveLog),
    resultLog: plain(resultLog),
    FINISH,
  });
};
// ╔══ module: src/agents/debugAnalyst/index.ts ════════════════════════════
// DEBUG ANALYST — last phase, opt-in (arg.debug). Consolidates the run's diagnostics corner by corner
// (incl. prospector→researcher venue utilization + any arg.debugPrompt question) into one _debug.md.
// Tier: opus (diagnostic synthesis). Effort: high.


                                                                            

const DIAG         = {
  type: 'object',
  properties: {
    diagnosis: {
      type: 'string',
      description: 'the full corner-by-corner debug consolidation + analysis as markdown',
    },
  },
  required: ['diagnosis'],
};

const debugAnalyst                          = {
  tier: CONFIG.TIER.debugAnalyst,
  effort: CONFIG.EFFORT.debugAnalyst,
  schema: DIAG,
  buildPrompt: buildDebugAnalyst,
};
// ╔══ module: src/agents/debugAnalyst/run.ts ══════════════════════════════




                                                      
                                                          
                                                             

// DEBUG & ANALYSIS (last phase, opt-in via arg.debug): an Opus agent consolidates the run's diagnostics — corner-by-corner,
// prospector→researcher venue utilization, and any arg.debugPrompt question — then JS appends the verbatim metrics, run log,
// and raw agent I/O (exact prompt in / exact output out) into one shippable _debug.md. Returns the _debug.md markdown (the engine writes it).
async function runDebug(
  rr                ,
  bs              ,
  metrics         ,
)                  {
  phase(CONFIG.PHASE.debug);
  log(
    '· debug & analysis · ' +
      debugAnalyst.tier +
      ' · over ' +
      IO_LOG.length +
      ' agent calls + ' +
      LOG_BUFFER.length +
      ' log lines + ' +
      rr.laneRecords.length +
      ' lane records',
  );
  const diag = await retryAgent         (
    debugAnalyst.buildPrompt({
      query: CONFIG.query,
      focus: CONFIG.debugPrompt,
      metrics,
      waveLog: bs.waveLog,
      resultLog: bs.resultLog,
      highValueSources: rr.highValueSources,
      laneRecords: rr.laneRecords,
    }),
    {
      label: 'debug-analyst',
      phase: CONFIG.PHASE.debug,
      model: debugAnalyst.tier,
      effort: debugAnalyst.effort,
      schema: debugAnalyst.schema,
    },
  );
  const narrative = (diag && diag.diagnosis) || '_(debug analyst failed — see raw sections below)_';
  const rawIO = IO_LOG.map(
    (e, i) =>
      '### ' +
      (i + 1) +
      '. `' +
      e.label +
      '` · ' +
      e.model +
      ' · ' +
      e.phase +
      '\n\n**PROMPT**\n\n' +
      (e.prompt || '') +
      '\n\n**OUTPUT**' +
      (e.error ? ' _(' + e.error + ')_' : '') +
      '\n\n' +
      (e.output == null ? '_(null)_' : JSON.stringify(e.output, null, 2)),
  ).join('\n\n');
  const artifact =
    '# RR debug & analysis — ' +
    clip(CONFIG.query, 80) +
    (CONFIG.debugPrompt ? '\n\n**Debug prompt:** ' + CONFIG.debugPrompt : '') +
    '\n\n## Analysis (debug-analyst · ' +
    debugAnalyst.tier +
    ')\n\n' +
    narrative +
    '\n\n## Metrics\n\n```json\n' +
    JSON.stringify(metrics, null, 2) +
    '\n```' +
    '\n\n## Run log (' +
    LOG_BUFFER.length +
    ' lines)\n\n```\n' +
    LOG_BUFFER.join('\n') +
    '\n```' +
    '\n\n## Raw agent I/O — exact prompt in, exact output out (' +
    IO_LOG.length +
    ' calls)\n\n' +
    (rawIO || '_(none captured)_') +
    '\n';
  log('· debug DONE · _debug.md assembled');
  return artifact;
}
// ╔══ module: src/agents/initiator/prompts.ts ═════════════════════════════
// INITIATOR prompts — the finalize-planner template + its assembly function. Template strings are
// module-level consts; buildInitiator only substitutes the operator-steering clause.


                                                          

const INITIATOR_TPL = `{{! initiator — plans the finalize pipeline, shaping the finish to this query }}
You direct the FINALIZE phase for: "{{query}}". The research is done; below is everything it gathered. Shape the finishing pipeline to fit this query, then return the plan.{{modeClause}}
The finish runs in two parts, and you set how each starts:
1. REFINEMENT — one refine agent per item adversarially fact-checks that group of load-bearing facts and returns them corrected and hardened. You decide the grouping. (A judge then evaluates the hardened answer and may trigger a derivation or a re-check; you do not plan that.)
2. SYNTHESIS — writes the final report from the hardened, judged answer. You give it a focus note.
The run's accumulated RESULT (the brainer's living memory — answer, the \`working\` derivation, keyClaimIds, gaps, tensions):
{{resultSoFar}}
Per-wave log:
{{waveLog}}
Scout landscape: {{landscape}}
Top open rabbit-holes left unpursued:
{{openRabbitHoles}}{{ledgerClause}}{{sensitivityClause}}
Return:
- refinement.facts[] — the load-bearing facts to harden, aggressively grouped: bundle facts that share sources or stand or fall together into ONE item (each {fact, why, claimId?}); prefer a few broad groups over many atomic facts. Cover every fact that would change the answer if wrong; skip soft restatements. Where a fact corresponds to a ledger claim, set its claimId — hardening then updates that claim's record.
- synthesiser.focus — one note on what the report must emphasize / the shape the answer should take.{{thinkerClause}}{{FINISH}}
`;

const buildInitiator = ({
  query,
  resultSoFar,
  waveLog,
  landscape,
  openRabbitHoles,
  mode,
  thinkerNote,
  ledger,
  sensitivity,
}               ) => {
  const thinkerClause = thinkerNote ? '\n\n' + thinkerNote : '';
  // collect mode ⇒ harden the BREADTH (coverage of the landscape), not the shape of a single answer.
  const modeClause =
    mode === 'collect'
      ? ' This run was a COLLECT inventory, not a single-answer goal — harden BREADTH: the key claims and the major sub-areas that span the landscape, and set the report focus to completeness of the catalogue rather than the shape of one answer.'
      : '';
  // ledgerClause — the ledger-fed initiator (v3 FINALIZE): names of already-pinned claims so facts can bind
  // to them via claimId instead of restating them.
  const ledgerClause = ledger
    ? `
CLAIM LEDGER — the run's evidence (ids look like c12, clusters like clu2: c12 [status·clu2·audit] claim = value):
${ledger}`
    : '';
  // sensitivityClause — SENSITIVITY RANKING (v3 FINALIZE): once a derivation has a completed rerun, prioritize
  // hardening the claims behind the inputs that dominate the variance — a lane wasted on a low-variance input
  // cannot move the answer.
  const sensitivityClause = sensitivity
    ? `
SENSITIVITY RANKING — derivation inputs by variance share (with their backing claims):
${sensitivity}
Prioritize hardening the claims behind the top-variance inputs.`
    : '';
  return render(INITIATOR_TPL, {
    query,
    resultSoFar: plain(resultSoFar),
    waveLog: plain(waveLog),
    landscape,
    openRabbitHoles: plain(openRabbitHoles),
    modeClause,
    ledgerClause,
    sensitivityClause,
    thinkerClause,
    FINISH,
  });
};
// ╔══ module: src/agents/initiator/index.ts ═══════════════════════════════
// INITIATOR — opens the Finalize phase. Reads the final resultSoFar and shapes the finish to the query:
// names the load-bearing facts to harden and sets the report focus. Tier: opus (synthesis/planning).
// Effort: xhigh.


                                                                         

// FINALIZE schemas. The INITIATOR plans the finish (which facts to harden, the report focus); a Sonnet REFINE pass adversarially
// fact-checks each load-bearing fact and returns its corrected claim; an Opus JUDGE then judges the hardened answer.
const INITIATOR         = {
  type: 'object',
  properties: {
    refinement: {
      type: 'object',
      properties: {
        facts: {
          type: 'array',
          items: {
            type: 'object',
            properties: {
              fact: {
                type: 'string',
                description:
                  'a group of related load-bearing facts the answer rests on (state the whole cluster)',
              },
              why: {
                type: 'string',
                description: 'why this group is load-bearing — what breaks if it is wrong',
              },
              claimId: {
                type: 'number',
                description:
                  'ledger claim id this fact corresponds to, when one exists — hardening then updates that claim record',
              },
            },
            required: ['fact', 'why'],
          },
          description:
            'load-bearing facts to harden, aggressively grouped into a few items — bundle facts that share sources or stand or fall together into one; cover all that would change the answer if wrong, skip soft restatements',
        },
      },
      required: ['facts'],
    },
    synthesiser: {
      type: 'object',
      properties: {
        focus: {
          type: 'string',
          description:
            'a note to the report writer on what to emphasize / the shape the answer should take',
        },
      },
      required: ['focus'],
    },
  },
  required: ['refinement', 'synthesiser'],
};

const initiator                       = {
  tier: CONFIG.TIER.initiator,
  effort: CONFIG.EFFORT.initiator,
  schema: INITIATOR,
  buildPrompt: buildInitiator,
};
// ╔══ module: src/agents/judge/prompts.ts ═════════════════════════════════
// JUDGE prompts — the finalize-phase terminal-skeptic template + its assembly function. Template
// strings are module-level consts; buildJudge only assembles/substitutes the compute-aware clauses.


                                                      

const JUDGE_TPL = `{{! judge — finalize-phase terminal skeptic: judges the hardened answer before the report is written }}
You are the JUDGE — the terminal skeptic of the FINALIZE phase for: "{{query}}". The crawl is done and its load-bearing facts were just hardened. Judge whether the answer is actually sound before the report is written.
The answer + its keyClaimIds + \`working\` derivation (the run's living memory):
{{resultSoFar}}
Hardened facts (each adversarially fact-checked + source-corrected by a refine pass):
{{cleanReports}}
What the answer must deliver: {{focus}}{{openClause}}{{modeClause}}{{ledgerClause}}{{nullAttacksClause}}{{confidenceClause}}{{stopClause}}
Before upholding, actively try to disprove the load-bearing claim as hard as you can — \`verificationSound\` holds only when it survives every angle:
- funding / conflict of interest — is the trial run or funded by the product's own seller?
- independent replication — does a separate group confirm it, or does the headline rest on a single source?
- contradicting / null results — search for failed replications and negative trials that cut against it.
- retraction status — check PubPeer, retraction notices, and expressions of concern.
- evidence quality — a weak sample size or unaddressed limitations downgrade a claim, however confidently stated.
A claim that survives this cross-examination is sound; one that does not → \`verificationSound\` false, naming the specific weakness in \`directive\`.
Judge four things, each a strict boolean:
- goalMet — the answer fully meets the goal AND delivers the spec above, not merely "close enough".
- verificationSound — the refine pass genuinely verified the facts (caught real errors, used current correct values) rather than rubber-stamping or mis-hardening one.
- needsCompute — the answer rests on a quantitative derivation it does not yet hold.{{computeClause}}
- computeSound — any derivation already present is valid (right inputs, propagated error bars, no arithmetic slip); true when none is needed.
Uphold a sound finish: when goalMet, verificationSound, and computeSound all hold, return them true with an empty directive. Otherwise name the single most load-bearing problem and the precise fix.
Return goalMet, verificationSound, needsCompute, computeSound, reasoning (the load-bearing reason for the verdict), directive (the exact fix or derivation to perform; '' when satisfied), reopenRabbitHoles (1-3 {keyword, why} ONLY when a real evidence/coverage gap needs more crawling, else []), reopenDirective (ONLY with reopenRabbitHoles: the EXTRACTION directive for the reopened lane's reader — WHAT to find in the fetched pages; keep it distinct from directive, which fixes the refine/report layer), retractClaimIds (ledger claim ids whose evidence is discredited — retraction, fabrication, or misattribution surfaced during verification; [] otherwise).{{thinkerClause}}{{FINISH}}
`;

const buildJudge = ({
  query,
  resultSoFar,
  cleanReports,
  focus,
  openRabbitHoles,
  compute,
  mode,
  computeNote,
  thinkerNote,
  ledger,
  survivedAttacks,
  neverChallenged,
  computedConfidence,
  stop,
}           ) => {
  const thinkerClause = thinkerNote ? '\n\n' + thinkerNote : '';
  // collect mode ⇒ goalMet is INVENTORY COMPLETENESS + per-item verification, not whether one answer is reached.
  const modeClause =
    mode === 'collect'
      ? `
MODE = collect — judge goalMet as INVENTORY COMPLETENESS: every major sub-area of the landscape is catalogued AND each catalogued item is individually verified, not whether a single answer is reached.`
      : '';
  const computeClause = compute
    ? ` A derivation may be written and run (Python scientific stack).${computeNote ? '\n' + computeNote : ''}`
    : ' Derivation is off for this run — you cannot run any computation. If the answer is complete without one, set needsCompute false and computeSound true; if it genuinely rests on a quantitative derivation this run cannot perform, report that honestly — set needsCompute true and name the missing derivation in `directive` (it is surfaced as a stated limitation, never fabricated). Either way set computeSound true: no derivation is present to be unsound.';
  const openClause =
    openRabbitHoles && openRabbitHoles.length
      ? `
LEFTOVER OPEN RABBIT-HOLES — leads the crawl surfaced but never pursued (it stopped first). Decide whether any names a REAL gap the answer needs; if one does, set goalMet false and return it in reopenRabbitHoles to reopen the crawl on it — otherwise ignore them:
${plain(openRabbitHoles)}`
      : '';
  // ledgerClause — the CLAIM LEDGER digest + the independence discipline (v3 FINALIZE): corroboration counts
  // CLUSTERS, not distinct-sounding source names — a claim whose supports share one cluster is single-source
  // however many names it wears.
  const ledgerClause = ledger
    ? `
CLAIM LEDGER — the run's evidence (ids look like c12, clusters like clu2: c12 [status·clu2·audit] claim = value):
${ledger}
Corroboration counts CLUSTERS: a claim whose supports share one cluster is SINGLE-SOURCE however many names it wears — flag any "independent" label the answer asserts that the clusters do not back.
The audit field is the MECHANICAL quote-pin verdict: a keyClaim reading 'fail' means its quote could not be verified against its cached source — verification is NOT sound while the answer rests on it; demand a re-pin, a retraction, or an explicit downgrade.`
    : '';
  // nullAttacksClause — challenged-and-survived vs never-challenged (v3 FINALIZE): a completed counter-search
  // that found nothing is first-class state, distinct from a key claim nobody has put to the test yet.
  const survivedLine =
    survivedAttacks && survivedAttacks.length
      ? 'CHALLENGED AND SURVIVED (counter-searched, nothing found): ' + survivedAttacks.join('; ')
      : '';
  const neverLine =
    neverChallenged && neverChallenged.length
      ? 'NEVER CHALLENGED key claims: ' + neverChallenged.join('; ')
      : '';
  const nullAttacksClause =
    survivedLine || neverLine ? '\n' + [survivedLine, neverLine].filter(Boolean).join('\n') : '';
  // confidenceClause — the computed-confidence, lower-only discipline (v3 FINALIZE).
  const confidenceClause = computedConfidence
    ? `
Machinery-computed confidence from evidence topology: ${computedConfidence} — weigh it when judging \`verificationSound\`; you do not set confidence yourself (only the synthesiser does, and it may only lower this value).`
    : '';
  // stopClause — STOP RECONCILE (v3 FINALIZE, run-forensics fix): the crawl's own final word, so a directive
  // to keep digging is never silently converted into a shipped caveat.
  const stopClause = stop
    ? `
THE CRAWL'S LAST WORD: stopped with done=${stop.done}, reason="${stop.reason}". If that reason names remaining work, either return reopenRabbitHoles for it or explicitly justify the override in your reasoning — never silently convert remaining work into a caveat.`
    : '';
  return render(JUDGE_TPL, {
    query,
    resultSoFar: plain(resultSoFar),
    cleanReports: plain(cleanReports),
    focus: focus || '(meet the goal as stated)',
    openClause,
    modeClause,
    computeClause,
    ledgerClause,
    nullAttacksClause,
    confidenceClause,
    stopClause,
    thinkerClause,
    FINISH,
  });
};
// ╔══ module: src/agents/judge/index.ts ═══════════════════════════════════
// JUDGE — the TERMINAL skeptic of the Finalize phase, the inverse of the synthesiser. Runs AFTER refine:
// sees the hardened facts + the brain's resultSoFar + the goal/deliverable, and judges whether the answer
// is sound (goal met, verification real, derivation valid). Drives a bounded remediation loop in the engine.
// Tier: opus (adversarial judgment). Effort: xhigh.



                                                                     

const JUDGE         = {
  type: 'object',
  properties: {
    goalMet: {
      type: 'boolean',
      description:
        'true = the answer fully meets the goal AND delivers the spec; false = it falls short',
    },
    verificationSound: {
      type: 'boolean',
      description:
        'true = the refine pass genuinely verified the facts; false = it rubber-stamped or mis-hardened a load-bearing fact',
    },
    needsCompute: {
      type: 'boolean',
      description: 'true = the answer rests on a quantitative derivation it does not yet hold',
    },
    computeSound: {
      type: 'boolean',
      description:
        'true = any derivation already present is valid (or none is needed); false = an existing derivation is wrong / lacks error bars',
    },
    reasoning: {
      type: 'string',
      description:
        'the load-bearing reason for the verdict — why it is sound, or the single biggest problem',
    },
    directive: {
      type: 'string',
      description: "the precise fix or derivation to perform when not satisfied; '' when satisfied",
    },
    reopenRabbitHoles: {
      type: 'array',
      items: RABBITHOLE,
      description:
        '1-3 concrete gap searches ONLY when a real evidence/coverage gap needs more crawling (NONE already pursued); empty otherwise',
    },
    reopenDirective: {
      type: 'string',
      description:
        'only with reopenRabbitHoles: the reader-facing extraction directive for the reopened lane — what to find; distinct from directive (the refine/report fix)',
    },
    retractClaimIds: {
      type: 'array',
      items: { type: 'number' },
      description:
        'ledger claim ids whose evidence is discredited (retraction/fabrication/misattribution) — the engine retracts them and recomputes everything downstream',
    },
  },
  required: ['goalMet', 'verificationSound', 'needsCompute', 'computeSound', 'reasoning'],
};

const judge                   = {
  tier: CONFIG.TIER.judge,
  effort: CONFIG.EFFORT.judge,
  schema: JUDGE,
  buildPrompt: buildJudge,
};
// ╔══ module: src/agents/lineageClerk/prompts.ts ══════════════════════════
// LINEAGE CLERK prompts — the batched entity-canonicalization template + its assembly function. Template
// strings are module-level consts; buildLineageClerk only assembles/substitutes the items + known keys.
// No FINISH here: the clerk carries no tools (a plain subagent) — there is no tool-use rabbit hole to guard against.

                                                             

const LINEAGE_TPL = `{{! lineageClerk — canonicalize provenance entities so JS can union-find independence clusters }}
You are the LINEAGE CLERK. Canonicalize each new claim's provenance entities into stable keys so independence clusters can be computed mechanically downstream — this IS the whole job: the SAME real-world entity spelled differently must map to the SAME key ("Pfizer Inc." and "Pfizer" both → funder:pfizer).
New claims (\`#id source | entities\`):
{{items}}
Known canonical keys already in use this run (reuse one of these EXACTLY whenever a claim's entity is the same real-world thing, however it is spelled):
{{knownKeys}}
For each claim return its canonical keys: lowercase, kebab-ish, prefixed by entity type — author:j-smith, funder:pfizer, dataset:gaia-dr3, venue:apj. Only emit a key for an entity actually present on that claim; skip absent/unknown entities entirely (never invent one to fill a slot).
Return links: one {id, keys} per claim.
`;

const buildLineageClerk = ({ items, knownKeys }                  ) =>
  render(LINEAGE_TPL, { items: plain(items), knownKeys: plain(knownKeys) });
// ╔══ module: src/agents/lineageClerk/index.ts ════════════════════════════
// LINEAGE CLERK — batched per wave: canonicalizes new claims' provenance entities against the known
// canonical-key list so JS can union-find independence clusters. Tier: haiku (bounded, mechanical
// canonicalization — no tools, a plain subagent). Effort: medium. Dies → deterministic JS fallback
// (utils.lineageKeyOf clusters by norm(funder || venue || source-domain); unresolvable → cluster 0).


                                                                            

const LINEAGE         = {
  type: 'object',
  properties: {
    links: {
      type: 'array',
      items: {
        type: 'object',
        properties: {
          id: { type: 'number', description: 'the claim id these canonical keys belong to' },
          keys: {
            type: 'array',
            items: { type: 'string' },
            description: 'canonical entity keys present on this claim, e.g. "funder:pfizer"',
          },
        },
        required: ['id', 'keys'],
      },
      description: 'one canonical-key set per claim',
    },
  },
  required: ['links'],
};

const lineageClerk                          = {
  tier: CONFIG.TIER.lineageClerk,
  effort: CONFIG.EFFORT.lineageClerk,
  schema: LINEAGE,
  buildPrompt: buildLineageClerk,
};
// ╔══ module: src/agents/lineageClerk/run.ts ══════════════════════════════
// LINEAGE CLERK dispatch — batches new claims into ≤LINEAGE_BATCH-item chunks, one retryAgent call per
// chunk, all chunks dispatched CONCURRENTLY via parallel(). A dead (null) chunk contributes nothing — its
// claims fall back to the deterministic lineageKeyOf clustering (the caller's job, not this module's).
// Hallucinated ids (not in the chunk's own input set) are dropped. Empty input → no agent spawned at all.




                                                          
                                                                   

async function runLineageClerk(
  bs              ,
  claims         ,
  knownKeys          ,
  tag        ,
  phaseName        ,
)                                 {
  const out = new Map                  ();
  if (!claims.length) return out;
  const chunks = chunk(claims, CONFIG.LINEAGE_BATCH);
  // parallel() journals thunk results as JSON (a Set would come back as {}), so the thunk returns
  // the bare agent result and the id set is rebuilt per chunk on the consumer side (order-aligned).
  const results = await parallel(
    chunks.map((ch, i) => () =>
      retryAgent                 (
        lineageClerk.buildPrompt({
          items: ch.map((c) => ({ id: c.id, source: c.source, entities: c.entities })),
          knownKeys,
        }),
        {
          label: 'lineage-' + tag + (chunks.length > 1 ? '-b' + i : ''),
          phase: phaseName,
          model: lineageClerk.tier,
          effort: lineageClerk.effort,
          schema: lineageClerk.schema,
        },
      ),
    ),
  );
  results.forEach((res, i) => {
    if (!res) return; // dead chunk — its claims fall back to lineageKeyOf
    const ids = new Set(chunks[i].map((c) => c.id));
    for (const l of res.links || [])
      if (l && ids.has(l.id) && Array.isArray(l.keys)) out.set(l.id, l.keys);
  });
  return out;
}
// ╔══ module: src/agents/prospector/prompts.ts ════════════════════════════
// PROSPECTOR prompts — the venue-naming template + its assembly function. Template strings are
// module-level consts; buildProspector only assembles/substitutes.


                                                           

const PROSPECTOR_TPL = `{{! prospector — names the high-value authoritative source venues for the topic }}
Goal: "{{query}}". Scout landscape: {{landscape}}
Sources the scout already opened:
{{sources}}
Name the 6-8 highest-value, authoritative source venues for this goal — where primary, expert, or rigorous information on the topic actually lives. The right set is domain-specific (GPU serving → arXiv/USENIX/MLSys/SemiAnalysis/r/LocalLLaMA; a stock → SEC EDGAR/earnings calls/Bloomberg; weather → NOAA/ECMWF).
Span what is relevant here: primary research (papers/preprints + where they live for this field), official docs, standards bodies/regulators, authoritative datasets/benchmarks, deep practitioner/industry analysis, high-signal community venues. Exclude generic SEO blogs.
Assess where this subject is most actively researched. When a non-English literature is genuinely significant for this topic — a disease studied mostly in China/Japan, a field led by Russian or Korean groups — name the high-value native venues for those languages (CNKI/Wanfang → Chinese, J-STAGE/ICHUSHI → Japanese, SciELO/LILACS → Spanish/Portuguese, eLibrary.ru → Russian, KoreaMed → Korean), each with how to query it, and set languageGuidance: one line telling the brainer which languages to cover and why. For an English-dominated topic, return only English venues and languageGuidance "".
Where the same concept is indexed under other names (older or alternate terms, regional spellings), fold those synonyms into the venues' search guidance so English-indexed work filed under a different name is still found.
For each venue: source (venue + how to reach/search it, e.g. "arXiv (site:arxiv.org)"), goodFor (the sub-questions it is best for — specific enough for the downstream brainer to match each research lane to the right venue), and lang (its language as an ISO-ish code like zh/ja/es/ru/ko — omit for English).
Run WebSearch (one or more queries) to discover and verify the actual highest-value venues — confirm each exists and is authoritative (memory alone misses recent venues). Return highValueSources (6-8, lang-tagged when non-English), languageGuidance ("" when the topic is English-dominated), and a brief reasoning naming what you searched.${EMIT}{{thinkerClause}}{{researcherClause}}{{WEB_ONLY}}
`;

const buildProspector = ({
  query,
  landscape,
  sources,
  thinkerNote,
  researcherNote,
}                ) => {
  const thinkerClause = thinkerNote ? '\n\n' + thinkerNote : '';
  const researcherClause = researcherNote ? '\n' + researcherNote : '';
  return render(PROSPECTOR_TPL, {
    query,
    landscape,
    sources: plain(sources),
    thinkerClause,
    researcherClause,
    WEB_ONLY,
  });
};
// ╔══ module: src/agents/prospector/index.ts ══════════════════════════════
// PROSPECTOR — runs after the scout, first agent of the Crawl phase. Names the high-value
// AUTHORITATIVE source venues for THIS topic (domain-specific); output rides with the brainer, which
// assigns the relevant subset to each lane. Tier: opus (cross-domain venue judgment). Effort: high.


                                                                          

// PROSPECTOR schema — names the high-value AUTHORITATIVE source venues for THIS topic (domain-specific); output rides with the brainer.
const SOURCES         = {
  type: 'object',
  properties: {
    highValueSources: {
      type: 'array',
      items: {
        type: 'object',
        properties: {
          source: {
            type: 'string',
            description:
              'the venue + how to reach/search it, e.g. "arXiv (site:arxiv.org)", "SemiAnalysis (semianalysis.com)"',
          },
          goodFor: {
            type: 'string',
            description:
              'the kinds of sub-questions/rabbit-holes this venue is BEST for — specific enough for the brainer to match a research lane to it',
          },
          lang: {
            type: ['string', 'null'],
            description:
              "the venue's language as an ISO-ish code (zh, ja, es, pt, ru, ko, …) or language name; OMIT for English venues",
          },
        },
        required: ['source', 'goodFor'],
      },
    },
    languageGuidance: {
      type: ['string', 'null'],
      description:
        'one line routing the brainer to the non-English literatures that matter for this topic and why; "" when the topic is English-dominated',
    },
    reasoning: {
      type: ['string', 'null'],
      description: '2-3 sentences max: how you chose these venues / what you searched to confirm',
    },
  },
  required: ['highValueSources'],
};

const prospector                        = {
  tier: CONFIG.TIER.prospector,
  effort: CONFIG.EFFORT.prospector,
  schema: SOURCES,
  buildPrompt: buildProspector,
};
// ╔══ module: src/agents/refiner/prompts.ts ═══════════════════════════════
// REFINER prompts — the fact-hardening template + its assembly function. Template strings are
// module-level consts; buildRefiner only assembles/substitutes.


                                                       

const REFINE_TPL = `{{! refine — adversarially fact-check ONE load-bearing fact and return its corrected, hardened version; attack-recording, not just fact-hardening }}
Fact-check and harden this load-bearing fact for the goal "{{query}}". {{net}}
Fact: {{fact}}
Why it is load-bearing: {{why}}{{pinnedClause}}
First verify it adversarially: hunt counter-evidence, newer information, and the real numbers — actively look for where it is false, outdated, or imprecise. Record every counter-search query you actually run, verbatim, in queriesTried — the record of the attack matters as much as its outcome. Do not rubber-stamp a well-supported fact; do not manufacture doubt about one you cannot actually break. Then settle every doubt against the sources and return only the clean, corrected claim(s) — the right values, current and verified, dropping anything that does not hold. Cite sources inline.{{directiveClause}}
Return report (markdown: the hardened claim(s) for this fact), queriesTried (the exact counter-search queries you ran), counterFound (true only when a real counter-example/contradiction turned up — a completed search that found nothing is false, not a lie), counterNote (what the counter-evidence was, when counterFound; '' otherwise).{{WEB_ONLY}}
`;

const buildRefiner = ({
  net,
  query,
  fact,
  why,
  directive,
  claimQuote,
  claimSource,
}            ) => {
  const directiveClause = directive
    ? `\nA judge flagged the prior verification — re-check it: ${directive}`
    : '';
  const pinnedClause = claimQuote
    ? `\nTHE CLAIM AS PINNED: "${claimQuote}" — ${claimSource || ''}`
    : '';
  return render(REFINE_TPL, { net, query, fact, why, pinnedClause, directiveClause, WEB_ONLY });
};
// ╔══ module: src/agents/refiner/index.ts ═════════════════════════════════
// REFINER — one per load-bearing fact (parallel) in the Finalize phase. Adversarially fact-checks a fact
// against the web and returns its corrected, hardened claim. Tier: sonnet (adversarial verification on the
// web — modest middle tier). Effort: high.


                                                                      

const REFINE         = {
  type: 'object',
  properties: {
    report: {
      type: 'string',
      description:
        'markdown: the clean / corrected claim(s) for this fact after adversarial fact-checking against the sources',
    },
    queriesTried: {
      type: 'array',
      items: { type: 'string' },
      description: 'the exact counter-searches you ran',
    },
    counterFound: {
      type: 'boolean',
      description: 'true only when a real counter-example/contradiction turned up',
    },
    counterNote: {
      type: 'string',
      description: 'what the counter-evidence was, when counterFound is true',
    },
  },
  required: ['report', 'queriesTried', 'counterFound'],
};

const refiner                    = {
  tier: CONFIG.TIER.refiner,
  effort: CONFIG.EFFORT.refiner,
  schema: REFINE,
  buildPrompt: buildRefiner,
};
// ╔══ module: src/agents/rerunner/prompts.ts ══════════════════════════════
// RERUNNER prompts — the derivation re-execution template + its assembly function. Template strings are
// module-level consts; buildRerunner only assembles/substitutes the code + current inputs.


                                                         

const RERUN_TPL = `{{! rerunner — re-executes the stored derivation artifact, verbatim, with the run's current inputs }}
You are the RERUNNER. A derivation was authored once as a pure, seeded Python script; re-execute it EXACTLY as given with the current inputs — the script is canonical. NEVER repair or rewrite it, even to fix an obvious bug: a broken artifact is the brainer's problem, not yours.
The script (reads ONE JSON argument, prints ONE JSON object {quantiles, sensitivity}):
\`\`\`python
{{code}}
\`\`\`
Current inputs — the single JSON argument to pass, verbatim:
{{inputsJson}}
Steps: write the script verbatim to a temp file via a Bash heredoc; run \`python3 FILE 'INPUTS'\` (FILE = the temp path, INPUTS = the JSON shown above, unescaped/verbatim); return exactly what it printed.
If it errors for any reason, return ok:false with the error message in note — do not repair, do not rewrite, do not retry with a fix of your own.
Return ok, quantiles, sensitivity, note.{{FINISH}}
`;

const buildRerunner = ({ code, inputsJson }              ) =>
  render(RERUN_TPL, { code, inputsJson, FINISH });
// ╔══ module: src/agents/rerunner/index.ts ════════════════════════════════
// RERUNNER — re-executes the stored derivation artifact (a pure, seeded Python script) with the run's
// current inputs. Tier: haiku (bounded, mechanical re-execution — never repairs the artifact). Effort: low.
// Dies or errors → lastRun stays stale; the caller keeps the last good run and tells the brainer it is stale.


                                                                        

const RERUN         = {
  type: 'object',
  properties: {
    ok: { type: 'boolean' },
    quantiles: {
      type: 'object',
      additionalProperties: { type: 'number' },
      description: "the script's printed quantiles, verbatim",
    },
    sensitivity: {
      type: 'object',
      additionalProperties: { type: 'number' },
      description: "the script's printed variance-share sensitivity, verbatim",
    },
    note: { type: 'string', description: 'the error message when ok is false' },
  },
  required: ['ok'],
};

const rerunner                      = {
  tier: CONFIG.TIER.rerunner,
  effort: CONFIG.EFFORT.rerunner,
  schema: RERUN,
  buildPrompt: buildRerunner,
};
// ╔══ module: src/agents/rerunner/run.ts ══════════════════════════════════
// RERUNNER dispatch — reads bs.derivation (null ⇒ nothing stored yet, no agent) and re-executes its code
// with the CURRENT inputs (bs.derivation.inputs, passed verbatim — the engine keeps them current across
// waves). Degrades to null on a dead agent or ok:false; the caller keeps the last lastRun and marks it stale.



                                                          
                                                        

async function runRerunner(
  bs              ,
  phaseName        ,
)                                                                                             {
  if (!bs.derivation) return null;
  const inputsJson = JSON.stringify(bs.derivation.inputs);
  const out = await retryAgent             (
    rerunner.buildPrompt({ code: bs.derivation.code, inputsJson }),
    {
      label: 'rerun-w' + bs.wave,
      phase: phaseName,
      model: rerunner.tier,
      effort: rerunner.effort,
      agentType: CONFIG.GENERAL_PURPOSE,
      schema: rerunner.schema,
    },
  );
  if (!out || !out.ok) return null;
  return { quantiles: out.quantiles || {}, sensitivity: out.sensitivity || {} };
}
// ╔══ module: src/agents/researchScheduler/prompts.ts ═════════════════════
// RESEARCH SCHEDULER prompts — the discovery template + its assembly function. Template strings are
// module-level consts; buildResearchScheduler only assembles/substitutes the per-wave clauses.


                                                                                      

const SCHEDULER_TPL = `{{! researchScheduler — discovery: per lane, find + size the highest-value sources, grouped per lane }}
You are the RESEARCH SCHEDULER — you own source discovery for this wave. For each lane below, find the HIGHEST-VALUE sources to read — as MANY as genuinely add value, no cap. The readers only read what you return; they do not search.
TOP GOAL: "{{query}}".
Tools (load any missing via ToolSearch): WebSearch; mcp__harvester__search; mcp__harvester__findWorks — resolves a work/DOI to its open-access full text; mcp__harvester__fetch — fetches + caches a url/DOI. Built-in WebFetch is denied; fetch only through Harvester.
{{venueLegend}}LANES — each carries a rabbit-hole, the brainer's directive \`note\` (WHAT to find + ranked fallbacks), and the venues to prefer:
{{lanes}}
Work in TWO batched rounds — never one-source-at-a-time round-trips:
1. DISCOVER — run ALL lanes' searches in ONE parallel batch (WebSearch / mcp__harvester__search / findWorks). Prefer each lane's assigned venues; let its \`note\` decide which results serve it. A lane carrying a concrete ref takes that ref as a source directly — no search needed for it.
2. SIZE — call mcp__harvester__fetch with size_only:true on EVERY candidate across all lanes in ONE parallel batch. With size_only it fetches + caches the full text and returns {size in tokens, path to the cache file, chars} and NO body. Drop any candidate that failed or came back walled/thin and pick another from the same lane.
SANITY — after sizing, compare the batch: two DIFFERENT urls returning identical {size, chars} is a cache-poisoning signature — treat both as failed and replace them.
For each lane, return its chosen sources as {source (the exact url or DOI), path (the cache path from size_only), size (tokens), chars}. Group them under the lane's id. A lane may return several sources; return an empty list for a lane only when every candidate failed.{{translateClause}}{{researcherClause}}{{vocabClause}}{{corruptClause}}
Return \`lanes\`: one entry per input lane id, each {id, sources:[{source, path, size, chars}], venuesServed:[...], unsourced:[{ref, reason}]}. venuesServed is the subset of THIS lane's ASSIGNED venues (the legend entries' exact source strings) its chosen sources actually come from — [] when none. unsourced lists every ref/DOI/venue the lane's directive or brief NAMED that could not be fetched, each {ref, reason} — omit the field entirely when everything named was sourced. A lane whose PRIORITY venue yielded nothing must say so in unsourced (reason e.g. "venue unfetchable") — never silently substitute a lower tier for it. Use the sizes you measured — never invent them.${EMIT}
`;

// laneLine — renders one LANES entry. `legend` maps a venue's exact source string → its VENUE LEGEND number
// (built once per prompt in buildResearchScheduler, over the deduped venue set); a lane's venues render as
// just their legend numbers ("venues: 2, 5") instead of repeating each venue's full ~700-char description.
const laneLine = (l                    , legend                     )         =>
  '#' +
  l.id +
  ' ' +
  l.keyword +
  ' — ' +
  l.why +
  '\n  directive: ' +
  (l.note && l.note.trim() ? l.note : '(none — use the rabbit-hole + goal)') +
  '\n  venues: ' +
  ((l.venues || [])
    .map((v) => legend.get(v.source))
    .filter((n)              => n !== undefined)
    .join(', ') || '(none — general search)') +
  (l.ref ? '\n  ref (fetch directly): ' + l.ref : '') +
  (l.kind === 'attack'
    ? '\n  ⚔ ATTACK lane — mandatory sources: the CURRENT product/changelog/pricing/news surface of EVERY prime suspect the directive names, not just pages about the claim.'
    : '') +
  (l.refetch
    ? "\n  REFETCH — this lane's cached copy is corrupted: fetch FRESH (cache-busting URL variant, a mirror, or archive.org) and NEVER return an already-cached path for it."
    : '');

const buildResearchScheduler = ({
  query,
  lanes,
  researcherNote,
  vocabulary,
  corruptCache,
}                       ) => {
  const anyLang = lanes.some((l) => (l.venues || []).some((v) => v.lang));
  const translateClause = anyLang
    ? `
For a lane routed to a non-English venue (tagged [zh], [ja], …), translate its query terms into that language, search the native venue, and choose the native-language sources — the readers translate the content back to English.`
    : '';
  const researcherClause = researcherNote ? '\n' + researcherNote : '';
  // vocabClause — the field's own terms of art (v3 STEERING), so venue queries speak the community's
  // language rather than the operator's; omitted entirely when the vocabulary is still empty.
  const vocabClause = vocabulary
    ? `
COMMUNITY VOCABULARY — the field's own terms of art (usage counts); phrase venue queries in THESE terms, not the operator's wording, where they fit: ${vocabulary}`
    : '';
  // corruptClause — known-poisoned cache paths (readers flagged them CORRUPT this run); omitted entirely
  // when nothing has been flagged yet.
  const corruptClause =
    corruptCache && corruptCache.length
      ? `
CORRUPTED CACHE — known-poisoned cache paths; NEVER return any of these as a source path (fetch fresh or substitute another source):
` + corruptCache.map((p) => '- ' + p).join('\n')
      : '';
  // VENUE LEGEND — dedupe the venues across every lane by v.source (first occurrence wins), then number
  // them once; laneLine looks each lane's venues up in this map instead of repeating full descriptions.
  const legendVenues                               = [];
  const seen = new Set        ();
  for (const l of lanes)
    for (const v of l.venues || [])
      if (!seen.has(v.source)) {
        seen.add(v.source);
        legendVenues.push(v);
      }
  const legend = new Map(legendVenues.map((v, i) => [v.source, i + 1]));
  const venueLegend = legendVenues.length
    ? "VENUE LEGEND — the prospector's venues, referenced by number below:\n" +
      legendVenues
        .map(
          (v, i) =>
            i +
            1 +
            '. ' +
            v.source +
            (v.lang ? ' [' + v.lang + ']' : '') +
            (v.goodFor ? ' (' + v.goodFor + ')' : ''),
        )
        .join('\n') +
      '\n\n'
    : '';
  return render(SCHEDULER_TPL, {
    query,
    lanes: lanes.map((l) => laneLine(l, legend)).join('\n') || '(no lanes)',
    venueLegend,
    translateClause,
    researcherClause,
    vocabClause,
    corruptClause,
  });
};
// ╔══ module: src/agents/researchScheduler/index.ts ═══════════════════════
// RESEARCH SCHEDULER — inserted AFTER the brainer picks the wave's lanes (resolveLookupNext), BEFORE the
// readers spawn. It owns discovery: per brainer lane (the rabbit-hole + its steering `note`), it picks the
// HIGHEST-VALUE sources — MULTIPLE per lane, no cap — by batching ALL lane searches in one parallel round,
// then sizing every candidate via mcp__harvester__fetch size_only (returns {size, path, chars}) in a second
// parallel round; it returns the chosen sources grouped per lane id. Code (engine.ts) then bin-packs each
// lane's content into RESEARCHER_TOKEN_BUDGET reader-units and spawns the sequential per-lane reader threads.
// Tier: sonnet — judging source value + driving batched tool I/O is a mid-weight job, above a worker but below
// the Opus brain. Effort: high. (Both read from the central CONFIG.TIER/EFFORT maps.)


                                                                                 

// SCHEDULE — the scheduler's output: the chosen, sized sources grouped per lane id. The engine re-confirms
// each `size` against the budget and computes the char windows itself (the Sonnet's numbers are not trusted).
const SCHEDULE         = {
  type: 'object',
  properties: {
    lanes: {
      type: 'array',
      items: {
        type: 'object',
        properties: {
          id: { type: 'number', description: 'the input lane id these sources belong to' },
          sources: {
            type: 'array',
            items: {
              type: 'object',
              properties: {
                source: {
                  type: 'string',
                  description: 'the exact url or DOI of the chosen source',
                },
                path: {
                  type: 'string',
                  description: 'the local cache file path returned by the size_only fetch',
                },
                size: {
                  type: 'number',
                  description: 'the source size in tokens (the size_only count)',
                },
                chars: { type: 'number', description: 'the source size in raw characters' },
              },
              required: ['source', 'path', 'size', 'chars'],
            },
            description:
              'the highest-value sources chosen for this lane (multiple, no cap); empty only when every candidate failed',
          },
          venuesServed: {
            type: 'array',
            items: { type: 'string' },
            description:
              "the subset of this lane's ASSIGNED venue source strings its chosen sources actually come from; [] when none",
          },
          unsourced: {
            type: 'array',
            items: {
              type: 'object',
              properties: { ref: { type: 'string' }, reason: { type: 'string' } },
              required: ['ref', 'reason'],
            },
            description:
              'directive-named refs/venues that could NOT be sourced — reported honestly, never silently substituted',
          },
        },
        required: ['id', 'sources'],
      },
    },
  },
  required: ['lanes'],
};

const researchScheduler                               = {
  tier: CONFIG.TIER.researchScheduler,
  effort: CONFIG.EFFORT.researchScheduler,
  schema: SCHEDULE,
  buildPrompt: buildResearchScheduler,
};
// ╔══ module: src/agents/researcher/prompts.ts ════════════════════════════
// RESEARCHER prompts — the lane-READER template + its assembly function. Template strings are module-level
// consts; buildResearcher only assembles/substitutes. The reader reads its assigned char window(s) from the
// cache files on disk (via code) and digests them into the running answer — it does NOT search the web
// (the scheduler owns discovery). Prompt order: task + which slice to read FIRST; the "answer so far"
// handoff LAST; "reader i of N" stated once.



                                                                      

const RESEARCHER_TPL = `{{! researcher — a lane reader: reads its assigned cache slice(s) from disk via code, then digests into the running answer }}
You are reader {{readerIndex}} of {{readerCount}} on one research lane — read your assigned slice and digest it. You do NOT search the web: the sources are already chosen and fetched to the local cache. Tools (load any via ToolSearch): Bash (read the cache files); mcp__harvester__fetch + mcp__harvester__findWorks (resolve a wall to its open-access full text); mcp__harvester__fetchImage (to view an image, request it via mcp__harvester__fetchImage for a local path, then read it).
TOP GOAL: "{{query}}".
TRAIL (top goal → … → this lane): {{trail}}.
This lane: "{{keyword}}" (why it matters: {{why}}).
DIRECTIVE — what to extract, with ranked fallbacks: {{note}}{{claimSection}}
READ NOW — your assigned slice(s), from the local cache on disk (already fetched; never re-fetch). First confirm each file exists and is non-empty (e.g. wc -c PATH); then read each char window with code, e.g. python3 -c "print(open('PATH',encoding='utf-8',errors='replace').read()[OFFSET:OFFSET+LIMIT])" — if the output is truncated, read it in sub-parts (OFFSET, OFFSET+step, … to OFFSET+LIMIT) until the whole window is consumed. Use this code path for every read — the Read tool errors out above ~25k tokens on a big cache file; if you do reach for Read, page it with offset/limit, never request the whole file:
{{reads}}
HONEST READ — if an assigned file is MISSING, empty, or unreadable, do NOT invent its content: record it in deadEnds, leave the running answer unchanged (an honest gap, never a fabricated summary), and the engine will reopen the lane. A confident wrong summary is worse than an admitted empty read. If a cached file's CONTENT is corrupted — spam, a different page than its URL promises, garbled or foreign-language filler — record it in deadEnds as "CORRUPT: <cachePath> — <one line why>"; the engine quarantines that cache path and routes a fresh fetch.
Extract everything that serves the DIRECTIVE and the top goal — facts, numbers, and for any trial or study its funding source, conflicts of interest, sample size, and key limitations. You may read images to understand the content. If the content is in another language, translate your findings to English, keeping each cited source's original-language title alongside the translation.
Extract each load-bearing fact as a claim: {claim (one sentence), value (the number/verdict if any), quote (VERBATIM from the content, ≤${CONFIG.QUOTE_MAX_CHARS} chars — ONE CONTIGUOUS unbroken span that carries the fact; NEVER stitch fragments with an ellipsis, a spliced quote fails the mechanical audit and the claim dies), source (url/DOI), cachePath (the cache file you read it in), entities (authors/funder/dataset/venue when visible)}. A claim without its verbatim quote is worthless — no quote, no claim.{{attackClause}}{{wallClause}}
Return: runningAnswer (extend the prior answer with what you found, kept a coherent whole — or begin it if you are reader 1); rabbitHoles (new gap searches the content raises, {keyword, why}); nextSources (up to 5 of the content's top outbound citations/links, each {ref: exact url or DOI, why, expect: support/attack/neutral, target: the claim id it bears on}); claims (each load-bearing fact pinned to a verbatim quote, this read's cachePath, its source, and entities when visible); newTerms (the community's terms of art this slice uses that we don't, {term, gloss}); surprise (one line, ONLY when this slice contradicts a KEY CLAIM above); deadEnds (any slice that was missing/empty/garbled/walled). <<{{footer}}>>${EMIT}{{researcherClause}}{{priorClause}}
`;

const readLine = (r           , i        )         =>
  i +
  1 +
  '. ' +
  r.source +
  ' — read ' +
  r.cachePath +
  ' chars [' +
  r.offset +
  ', ' +
  (r.offset + r.limit) +
  ') (open the file, slice [' +
  r.offset +
  ':' +
  (r.offset + r.limit) +
  '])';

const buildResearcher = ({
  query,
  trail,
  keyword,
  why,
  note,
  footer,
  reads,
  readerIndex,
  readerCount,
  priorAnswer,
  claimDigest,
  laneKind,
  researcherNote,
}                ) => {
  const wallClause = `
If your assigned content is a paywall, stub, or too thin for the directive, extract its DOI/identifier and call mcp__harvester__fetch — it resolves DOIs — or mcp__harvester__findWorks to fetch the open-access full text to the cache, then read THAT from disk; do not return an empty answer. This is scoped to resolving THIS source — do not open a general web search.`;
  const researcherClause = researcherNote ? '\n' + researcherNote : '';
  const priorClause = priorAnswer
    ? `
ANSWER SO FAR — the running answer from the earlier readers on this lane; extend and correct it, do not restate it wholesale:
${priorAnswer}`
    : '';
  // claimSection — the KEY CLAIMS digest a stance can target; omitted entirely (byte-identical to no ledger yet)
  // when the ledger is empty, rather than padding with a placeholder line.
  const claimSection = claimDigest
    ? `
KEY CLAIMS SO FAR (\`c12 claim\` — ids look like c12) — when your content SUPPORTS or ATTACKS one of these, say so via \`stance\` {target: the NUMBER from its c12 id}; a contradiction is also your Surprise note:
${claimDigest}`
    : '';
  // attackClause — an ATTACK-kind lane (RabbitHole.kind, set when the brainer originates a counter-evidence
  // search) exists to try to break a claim: its primary output is counter-evidence, never manufactured doubt.
  const attackClause =
    laneKind === 'attack'
      ? " This is an ATTACK lane: your PRIMARY output is counter-evidence — claims with stance {target, kind:'attacks'} against the target claim, or an honest empty claims list when you find none; never manufacture doubt. Attack lanes ALONE may search beyond their assigned slices: before concluding the claim holds, run up to 3 WebSearch / mcp__harvester__fetch probes against the CURRENT product/changelog/news surface of every prime suspect the DIRECTIVE names — absence from your cached slices is not absence in the world."
      : '';
  return render(RESEARCHER_TPL, {
    readerIndex,
    readerCount,
    query,
    trail,
    keyword,
    why,
    note: note || '(no directive — extract what best serves the lane + goal)',
    claimSection,
    reads: (reads || []).map(readLine).join('\n') || '(no slice assigned)',
    wallClause,
    attackClause,
    footer,
    researcherClause,
    priorClause,
  });
};
// ╔══ module: src/agents/researcher/index.ts ══════════════════════════════
// RESEARCHER — a lane READER: it reads its ASSIGNED cache slice(s) from disk (via code) and digests them
// into the lane's running answer. The scheduler owns discovery (which sources, sized to disk); code bin-packs
// each lane's content into RESEARCHER_TOKEN_BUDGET reader-units and runs ONE sequential thread per lane,
// handing the running answer forward across all its reads. Tier: haiku — the read-from-disk + digest is a
// BOUNDED worker task (the scheduler already chose + fetched the sources; a SONNET researcher crashed the
// vector-DB run). Effort: medium. (Both read from the central CONFIG.TIER/EFFORT maps.) The reader runs as a
// code-capable general-purpose agent so it can read its char window off disk + resolve a wall.



                                                                          

const RESEARCH         = {
  type: 'object',
  properties: {
    runningAnswer: {
      type: 'string',
      description:
        'the accumulated answer for this lane: merge what you found in your slice INTO the prior answer (or begin it if you are reader 1), kept a coherent whole — the next reader continues it and the brainer reads the final one',
    },
    rabbitHoles: { type: 'array', items: RABBITHOLE },
    nextSources: {
      type: 'array',
      maxItems: 5,
      items: {
        type: 'object',
        properties: {
          ref: {
            type: 'string',
            description: 'an exact url or DOI the content points to, worth fetching directly',
          },
          why: { type: 'string', description: 'one line on why following it advances the goal' },
          // expect/target are advisory (the engine seeds only ref/why into the store) — null-tolerant
          // and un-enumed so a loose value can never fail the whole reader payload into a retry.
          expect: {
            type: ['string', 'null'],
            description:
              'support | attack | neutral — whether following it is expected to SUPPORT or ATTACK `target`',
          },
          target: {
            type: ['number', 'string', 'null'],
            description: 'id of the existing claim this source is expected to support or attack',
          },
        },
        required: ['ref', 'why'],
      },
      description:
        "up to 5 of the content's highest-value outbound citations/links as concrete fetch targets — a later lane fetches each directly",
    },
    claims: {
      type: 'array',
      items: CLAIM_ITEM_STANCE,
      description:
        'load-bearing facts this slice carries — each pinned to a verbatim quote; only facts the answer could rest on, never a transcript of everything read',
    },
    newTerms: {
      type: 'array',
      items: TERM_SEED,
      description:
        "the community's terms of art this slice uses that the digest/query does not — empty when the slice speaks our vocabulary",
    },
    surprise: {
      type: ['string', 'null'],
      description:
        'one line naming the contradiction — set ONLY when this slice contradicts one of the KEY CLAIMS in the digest',
    },
    deadEnds: { type: 'array', items: { type: 'string' } },
  },
  // The channel fields are REQUIRED so a reader consciously reports zero — an empty array is fine and
  // normal on a thin slice, but a MISSING field is indistinguishable from a silently dropped one. Run
  // forensics caught a degraded lane that returned ONLY runningAnswer, omitting claims/deadEnds entirely,
  // so the engine's lane-reopen machinery never fired. newTerms/surprise stay optional (TS ReaderOut keeps
  // its optionals — the engine's `|| []` guards still apply).
  required: ['runningAnswer', 'claims', 'rabbitHoles', 'deadEnds'],
};

const researcher                        = {
  tier: CONFIG.TIER.researcher,
  effort: CONFIG.EFFORT.researcher,
  schema: RESEARCH,
  buildPrompt: buildResearcher,
};
// ╔══ module: src/agents/scout/prompts.ts ═════════════════════════════════
// SCOUT SWARM prompts — three templates + their assembly functions: scoutPlanner (senses the landscape,
// decomposes the query, proposes search angles) → scout (one PROBE per angle) → scoutMerger (folds every
// probe into the final ScoutOut). Template strings are module-level consts; each build* fn only
// assembles/substitutes — it holds no template text itself.



                                                                                         

// ── stage 1: scoutPlanner — ground the angle vocabulary in real search results before proposing anything;
// decompose the query into its distinct axes; propose the probe swarm's search angles ──
const SCOUT_PLANNER_TPL = `{{! scoutPlanner — grounds the angle vocabulary in real search results, decomposes the query, proposes the probe swarm's angles }}
Query: "{{query}}" (mode: {{mode}}). {{net}}
Step 1 — run 1-2 quick WebSearches to sense the landscape's lay. The angle vocabulary below MUST come from what these searches actually surface — never from imagination.
Step 2 — return decomposition: the query's distinct axes, one line each — everything that must ALL be understood to answer it.
Step 3 — return angles: 3 to ${CONFIG.SCOUT_PROBES} distinct search angles for a swarm of scout probes, each {name, searchQuery, why, lens}. searchQuery is a concrete, distinct query for THIS angle — never a reworded restatement of another angle's. No two angles may overlap: each must be built to surface pages the others will not.
MANDATORY whenever it applies to this query:
(a) a DIRECT angle — the query as asked, straight up.
(b) a SKEPTIC angle — counter-evidence: criticism, limitations, failed replications, debunking phrasing.
(c) a RECENT angle — the last ~12 months of developments.
Fill any remaining slots with whichever of these fit THIS query best: datasets/benchmarks, practitioner communities, regulatory/official sources, industry analyses, adjacent-field framings.{{researcherClause}}
`;

const buildScoutPlanner = ({ query, mode, net, researcherNote }                  ) => {
  const researcherClause = researcherNote ? '\n' + researcherNote : '';
  return render(SCOUT_PLANNER_TPL, { query, mode, net, researcherClause });
};

// ── stage 2: scout — one PROBE of the swarm, scoped to a single angle. Unchanged reading substrate from
// v2's single scout (one WebSearch → fetch ≤N sources → the same footer discipline); only the SCOPE
// narrows from "the whole topic" to "this one angle". ──
const SCOUT_TPL = `{{! scout — one probe of the swarm, scoped to a single angle: sweeps it and seeds its rabbit-holes }}
You are scout probe {{index}} of {{total}}, on the angle «{{angleName}}» — {{angleWhy}}. Lens: {{angleLens}}. {{net}}
Step 1 — run WebSearch with: "{{searchQuery}}". You may refine it ONCE if the results are off-angle — stay on THIS angle, do not wander onto another probe's.
Step 2 — pick the up-to-${CONFIG.SCOUT_PROBE_SOURCES} most relevant sources FOR THIS ANGLE and fetch each via mcp__harvester__fetch — built-in WebFetch is denied. For each fetched page, first surface the key facts about "{{query}}" as this angle reveals them, then apply this instruction: <<{{footer}}>> Record the local cache path the fetch tool reports as EVERY claim's cachePath — a claim without its cachePath can never be mechanically verified and stays permanently unpinned; never invent one when the tool did not report it. Skip the footer's Surprise section — no prior claims exist yet.
Step 3 — return ALL SIX fields (an array with nothing to report is [], never omitted): landscape (2-3 sentences on what THIS ANGLE revealed — not the whole topic, and NEVER your findings packed into prose: facts go in claims[], page detail in pages[].summary); pages[] (each: url, 2-3 sentence summary, rabbitHoles[] copied from the page's "Rabbit holes" section as {keyword, why}); nextSources[] union of the pages' "Next sources" sections, each {ref, why}; claims[] union of the pages' "Claims" sections, each pinned to a verbatim quote — a claim without its verbatim quote is worthless, no quote no claim; newTerms[] union of the pages' "New terms" sections; deadEnds[] for any source that timed out, was parked, or was off-topic — do not invent rabbit-holes for those. If every source is dead/unreachable, still return a valid result: landscape from your search, pages [], the dead sources in deadEnds.${EMIT}{{researcherClause}}
`;

const buildScout = ({
  query,
  net,
  footer,
  angleName,
  angleWhy,
  angleLens,
  searchQuery,
  index,
  total,
  researcherNote,
}           ) => {
  const researcherClause = researcherNote ? '\n' + researcherNote : '';
  return render(SCOUT_TPL, {
    query,
    net,
    footer,
    angleName,
    angleWhy,
    angleLens,
    searchQuery,
    index,
    total,
    researcherClause,
  });
};

// ── stage 3: scoutMerger — folds every surviving probe's output into ONE final ScoutOut (same schema).
// No tools: a plain subagent reducing material the probes already gathered — there is no tool-use rabbit
// hole to guard against (mirrors lineageClerk: no FINISH clause). ──
const SCOUT_MERGER_TPL = `{{! scoutMerger — folds every angle probe's output into the final ScoutOut, naming the tensions between angles }}
Query: "{{query}}".
Decomposition (the query's distinct axes): {{decomposition}}
Every surviving probe's output (angle name, what it revealed, its pages/claims/newTerms/deadEnds):
{{probes}}
Return the FINAL scout result:
landscape: ONE rich paragraph weaving every angle together — and NAME THE TENSIONS between them (where, say, the SKEPTIC angle contradicts the DIRECT angle). That tension is the single most valuable seed the next stage can receive — surface it explicitly, never bury it.
pages: the union of every probe's pages, deduped by url, keeping the strongest up to ${CONFIG.SCOUT_PAGES_CAP} — carry each kept page's summary and rabbitHoles EXACTLY as its probe wrote them; you are SELECTING, not rewriting.
nextSources: the union of every probe's nextSources, deduped by ref.
claims: the union of every probe's claims, dropping exact duplicates (the same quote reported by more than one probe).
newTerms: the union, deduped by term.
deadEnds: the union.
Only use what the probes actually returned — never invent a page, claim, or term no probe reported.${EMIT}
`;

const buildScoutMerger = ({ query, decomposition, probes }                 ) =>
  render(SCOUT_MERGER_TPL, { query, decomposition, probes: plain(probes) });
// ╔══ module: src/agents/scout/index.ts ═══════════════════════════════════
// SCOUT SWARM — the wave-0 seed, now three stages instead of one broad sweep: scoutPlanner (sonnet, high)
// runs 1-2 quick WebSearches to ground the landscape then decomposes the query + proposes 3..SCOUT_PROBES
// search angles; a swarm of `scout` probes (haiku, medium — UNCHANGED tier: the page reading stays the
// fixed haiku reader's job, only its scope narrows to one angle) each sweep one angle; scoutMerger (sonnet,
// high, no tools) folds every surviving probe into the final ScoutOut, naming the tensions between angles.
// Every stage degrades to a named JS fallback — see run.ts.



             
        
            
                  
                   
         
                              

// ── stage 1 schema — the planner's decomposition + proposed angles ──
const SCOUT_ANGLE         = {
  type: 'object',
  properties: {
    name: {
      type: 'string',
      description: 'short angle label, e.g. "direct", "skeptic", "recent"',
    },
    searchQuery: {
      type: 'string',
      description: 'a concrete, distinct search query for THIS angle — never a reworded sibling',
    },
    why: { type: 'string', description: 'why this angle matters for the query' },
    lens: {
      type: 'string',
      description: 'the interpretive lens the probe should read its sources through',
    },
  },
  required: ['name', 'searchQuery', 'why'],
};

const SCOUT_PLANNER         = {
  type: 'object',
  properties: {
    decomposition: { type: 'string', description: "the query's distinct axes, one line each" },
    angles: {
      type: 'array',
      items: SCOUT_ANGLE,
      description: `3..${CONFIG.SCOUT_PROBES} distinct, non-overlapping search angles for the probe swarm`,
    },
  },
  required: ['decomposition', 'angles'],
};

const scoutPlanner                          = {
  tier: CONFIG.TIER.scoutPlanner,
  effort: CONFIG.EFFORT.scoutPlanner,
  schema: SCOUT_PLANNER,
  buildPrompt: buildScoutPlanner,
};

// ── stage 2 schema — one probe's return. UNCHANGED shape from v2's single scout: field-compatible so
// scoutMerger (and both JS fallbacks) can reuse this exact schema for the FINAL ScoutOut too. ──
const SCOUT         = {
  type: 'object',
  properties: {
    landscape: {
      type: 'string',
      // shared by probe (2-3 sentences) and merger (one paragraph) — the description binds the
      // INVARIANT both share: run forensics caught a probe packing its entire return in here,
      // omitting every other required key, and burning five identical schema-error retries.
      description:
        'the summary narrative ONLY — facts belong in claims[] and page detail in pages[]; never pack the whole return into this field',
    },
    pages: { type: 'array', items: PAGE },
    deadEnds: { type: 'array', items: { type: 'string' } },
    claims: {
      type: 'array',
      items: CLAIM_ITEM,
      description:
        "union of the fetched pages' load-bearing facts, each pinned to a verbatim quote — no digest exists yet, so never carries `stance`",
    },
    nextSources: {
      type: 'array',
      items: {
        type: 'object',
        properties: {
          ref: {
            type: 'string',
            description: 'an exact url or DOI a fetched page points to, worth fetching directly',
          },
          why: { type: 'string', description: 'one line on why following it matters' },
        },
        required: ['ref', 'why'],
      },
      description:
        "union of the fetched pages' highest-value outbound citations — no ledger exists yet, so never carries expect/target",
    },
    newTerms: {
      type: 'array',
      items: TERM_SEED,
      description: "union of the fetched pages' community terms of art that we did not use",
    },
  },
  required: ['landscape', 'pages'],
};

const scout                   = {
  tier: CONFIG.TIER.scout,
  effort: CONFIG.EFFORT.scout,
  schema: SCOUT,
  buildPrompt: buildScout,
};

// ── stage 3 — scoutMerger: folds every probe's SCOUT-shaped output into ONE final SCOUT-shaped output. ──
const scoutMerger                         = {
  tier: CONFIG.TIER.scoutMerger,
  effort: CONFIG.EFFORT.scoutMerger,
  schema: SCOUT, // same shape as a probe's output — the merger folds N of them into one
  buildPrompt: buildScoutMerger,
};
// ╔══ module: src/agents/scout/run.ts ═════════════════════════════════════




                                                      
             
            
             
       
             
           
                  
                   
           
           
                              

// FALLBACK A — the planner died (or returned no usable angle): degrade to v2's single-scout behavior, one
// probe on the naive query itself. Exported for direct unit coverage.
const FALLBACK_ANGLE             = {
  name: 'direct',
  searchQuery: CONFIG.query,
  why: 'planner died — v2 single-scout fallback',
  lens: '',
};

// a planner angle is usable only when it carries the fields a probe actually needs.
const isUsableAngle = (a         )                  =>
  !!a &&
  typeof (a              ).name === 'string' &&
  !!(a              ).name &&
  typeof (a              ).searchQuery === 'string' &&
  !!(a              ).searchQuery;

// FALLBACK B — the merger died: a pure, deterministic JS merge of every surviving probe's output. Exported
// for direct unit coverage. url dedup via normRef (survivor order, first occurrence wins), capped at
// SCOUT_PAGES_CAP; claims/newTerms deduped by norm() of their quote/term; deadEnds is a plain union.
function mechanicalMerge(survivors                                        )           {
  const landscape = survivors
    .map(({ angle, out }) => '«' + angle.name + '»: ' + out.landscape)
    .join('\n');
  const seenUrls = new Set        ();
  const pages         = [];
  for (const { out } of survivors)
    for (const p of out.pages || [])
      if (p && p.url && !seenUrls.has(normRef(p.url))) {
        seenUrls.add(normRef(p.url));
        pages.push(p);
      }
  const seenQuotes = new Set        ();
  const claims              = [];
  for (const { out } of survivors)
    for (const c of out.claims || [])
      if (c && !seenQuotes.has(norm(c.quote))) {
        seenQuotes.add(norm(c.quote));
        claims.push(c);
      }
  const seenRefs = new Set        ();
  const nextSources               = [];
  for (const { out } of survivors)
    for (const s of out.nextSources || [])
      if (s && s.ref && !seenRefs.has(normRef(s.ref))) {
        seenRefs.add(normRef(s.ref));
        nextSources.push(s);
      }
  const seenTerms = new Set        ();
  const newTerms             = [];
  for (const { out } of survivors)
    for (const t of out.newTerms || [])
      if (t && !seenTerms.has(norm(t.term))) {
        seenTerms.add(norm(t.term));
        newTerms.push(t);
      }
  const deadEnds = survivors.flatMap(({ out }) => out.deadEnds || []);
  return {
    landscape,
    pages: pages.slice(0, CONFIG.SCOUT_PAGES_CAP),
    claims,
    nextSources,
    newTerms,
    deadEnds,
  };
}

// SCOUT SWARM (wave-0 seed): scoutPlanner decomposes the query + proposes 3..SCOUT_PROBES search angles →
// a `scout` probe sweeps each angle in parallel (broad WebSearch → fetch with the rabbit-hole footer,
// scoped to its angle) → scoutMerger folds every surviving probe into the FINAL ScoutOut, naming the
// tensions between angles. Each stage degrades to a named JS fallback; only a total probe wipeout is
// fatal (unchanged from v2's single-scout semantics). Sets rr.scout + rr.scoutRabbitHoles and returns the
// seed leads (the engine seeds the open store from them) — signature/return/assignment unchanged from v2.
async function runScout(rr                )                      {
  phase(CONFIG.PHASE.scout);

  // ── stage 1: scoutPlanner ──
  log('· scout planner DISPATCH · ' + scoutPlanner.tier);
  const planner = await retryAgent                 (
    scoutPlanner.buildPrompt({
      query: CONFIG.query,
      mode: CONFIG.mode,
      net: CONFIG.NET,
      researcherNote: CONFIG.RESEARCHER_NOTE,
    }),
    {
      label: 'scout-planner',
      phase: CONFIG.PHASE.scout,
      model: scoutPlanner.tier,
      effort: scoutPlanner.effort,
      agentType: CONFIG.GENERAL_PURPOSE,
      schema: scoutPlanner.schema,
    },
  );
  const usableAngles =
    planner && Array.isArray(planner.angles) ? planner.angles.filter(isUsableAngle) : [];
  const plannerFellBack = !planner || !usableAngles.length;
  const angles               = plannerFellBack
    ? [FALLBACK_ANGLE]
    : usableAngles.slice(0, CONFIG.SCOUT_PROBES);
  const decomposition = (planner && planner.decomposition) || '';
  if (plannerFellBack) log('  ⚠ scout planner died → v2 single-scout fallback (angle: direct)');
  log('· scout planner · ' + angles.length + ' angles');

  // ── stage 2: the probe swarm — one `scout` call per angle, in parallel ──
  const probeResults = await parallel(
    angles.map((angle, i) => async () => {
      const out = await retryAgent          (
        scout.buildPrompt({
          query: CONFIG.query,
          net: CONFIG.NET,
          footer: CONFIG.FOOTER,
          angleName: angle.name,
          angleWhy: angle.why,
          angleLens: angle.lens || '',
          searchQuery: angle.searchQuery,
          index: i + 1,
          total: angles.length,
          researcherNote: CONFIG.RESEARCHER_NOTE,
        }),
        {
          label: 'scout-probe:' + lab(angle.name),
          phase: CONFIG.PHASE.scout,
          model: scout.tier,
          effort: scout.effort,
          agentType: CONFIG.GENERAL_PURPOSE,
          schema: scout.schema,
        },
      );
      return { angle, out };
    }),
  );
  const survivors = probeResults.filter((r)                                            => !!(r && r.out));
  survivors.forEach(({ angle, out }) =>
    log('· scout probe «' + angle.name + '» RETURN · pages=' + out.pages.length),
  );
  probeResults
    .filter((r) => !r.out)
    .forEach(({ angle }) => log('  ✗ scout probe «' + angle.name + '» DIED'));
  if (!survivors.length) {
    log('✗ scout DIED');
    throw new Error('scout died');
  }

  // ── stage 3: scoutMerger ──
  const mergerProbes                     = survivors.map(({ angle, out }) => ({
    name: angle.name,
    landscape: out.landscape,
    pages: out.pages || [],
    claims: out.claims || [],
    nextSources: out.nextSources || [],
    newTerms: out.newTerms || [],
    deadEnds: out.deadEnds || [],
  }));
  const merged = await retryAgent          (
    scoutMerger.buildPrompt({ query: CONFIG.query, decomposition, probes: mergerProbes }),
    {
      label: 'scout-merger',
      phase: CONFIG.PHASE.scout,
      model: scoutMerger.tier,
      effort: scoutMerger.effort,
      schema: scoutMerger.schema,
    },
  );
  const mergerFellBack = !merged;
  const scoutOut           = merged || mechanicalMerge(survivors);
  const fallbackLabel =
    [plannerFellBack && 'planner', mergerFellBack && 'merger'].filter(Boolean).join('+') || 'none';
  log(
    '· scout merged · pages=' +
      scoutOut.pages.length +
      ' · claims=' +
      (scoutOut.claims || []).length +
      ' (fallback: ' +
      fallbackLabel +
      ')',
  );

  rr.scout = scoutOut;
  const scoutRabbitHoles             = scoutOut.pages.flatMap((p) =>
    (p.rabbitHoles || []).map((l) => ({
      keyword: l.keyword,
      why: l.why,
      path: []            ,
      kind: 'seed'         ,
    })),
  ); // PATH: scout rabbit-holes descend directly from the goal
  rr.scoutRabbitHoles = scoutRabbitHoles;
  log(
    '· scout RETURN · pages=' +
      scoutOut.pages.length +
      ' · rabbit-holes=' +
      scoutRabbitHoles.length +
      ' · claims=' +
      (scoutOut.claims || []).length +
      ' · newTerms=' +
      (scoutOut.newTerms || []).length +
      ' · deadEnds=' +
      (scoutOut.deadEnds || []).length,
  );
  scoutOut.pages.forEach((p, i) =>
    log(
      '    source ' + (i + 1) + ' · rabbit-holes=' + (p.rabbitHoles || []).length + ' · ' + p.url,
    ),
  );
  return scoutRabbitHoles;
}
// ╔══ module: src/agents/synthesiser/prompts.ts ═══════════════════════════
// SYNTHESISER prompts — the report-writer template + its assembly function. Template strings are
// module-level consts; buildSynthesiser only assembles/substitutes the compute-mention clauses.


                                                            

const SYNTHESISER_TPL = `{{! synthesiser — writes the final multi-section cited report }}
Write the final research report (mode={{mode}}) for: "{{query}}".{{focusClause}}{{thinkerClause}}
Work from: the run's accumulated RESULT (the brainer's living memory — answer, working derivation, keyClaimIds, resolved, open gaps, tensions), the hardened facts (each adversarially fact-checked + source-corrected), not raw findings.
Lean on the hardened facts as the source of truth: drop anything they leave unsupported and use the corrected value wherever they revised one.{{computeMention}} Cite sources inline where they matter.
Scout landscape: {{landscape}}
Run result so far (the answer as it ended + its keyClaimIds + the \`working\` derivation):
{{resultSoFar}}
Per-wave log (what each wave pursued + where the answer stood — for the §2 narrative):
{{waveLog}}
Hardened facts (the corrected claims):
{{cleanReports}}
Top remaining open rabbit-holes (for Open questions):
{{openRabbitHoles}}{{ledgerClause}}{{nullAttacksClause}}{{confidenceClause}}
Write \`report\` as markdown with exactly these sections in order: (1) Prompt — the goal; (2) Research waves — per wave: what was pursued and how the answer sharpened (from the per-wave log); (3) Scout landscape; (4) Findings — the synthesized answer, {{computeLeading}}weaving each hardened fact in with its corrected value; (5) Assumptions — the working assumptions the answer leans on (from resultSoFar.assumptions), each with its basis, flagging any that is load-bearing but unconfirmed; (6) Verdict + overall confidence; (7) Plan — concrete operator actions; (8) Open questions. Also return verdict (1-3 sentences), confidence, plan (array of action strings), openQuestions (array).{{FINISH}}
`;

const buildSynthesiser = ({
  mode,
  query,
  landscape,
  resultSoFar,
  waveLog,
  cleanReports,
  focus,
  openRabbitHoles,
  compute,
  thinkerNote,
  ledger,
  nullAttacksSummary,
  computedConfidence,
}                 ) => {
  // the brain folds any finalize derivation into resultSoFar.working — present that as the quantitative result.
  // Key this on CONFIG.compute, NOT merely on a non-empty `working`: with compute OFF a derivation must NEVER be
  // presented even if one leaked into resultSoFar (only an EXPLICIT compute:false suppresses it, so prompt-only
  // callers that omit compute keep the present-when-derived default).
  const hasCompute =
    compute !== false && !!(resultSoFar && resultSoFar.working && resultSoFar.working.trim());
  const thinkerClause = thinkerNote ? '\n\n' + thinkerNote : '';
  const focusClause = focus
    ? `
Emphasis from the finalize director: ${focus}`
    : '';
  const computeMention = hasCompute
    ? ' The `working` field holds the computed derivation (the calculated answer with error bars) — present it verbatim, do not re-derive or second-guess it.'
    : '';
  const computeLeading = hasCompute
    ? 'LEADING with the computed result + its error bars from `working` and showing the derivation, then '
    : '';
  // ledgerClause — cite the ledger inline (v3 FINALIZE): every [cN] the report writes must be a REAL id from
  // this digest; independence is labeled ONLY from cluster counts, never from how many distinctly-named
  // sources happen to appear.
  const ledgerClause = ledger
    ? `
CLAIM LEDGER (ids look like c12, clusters like clu2: c12 [status·clu2·audit] claim = value):
${ledger}
Cite ledger claims inline as [c12] wherever a load-bearing fact appears — every [c12]-style marker must be a real ledger id from the digest above (same c12 notation); label independence ONLY from cluster counts, never from distinct-sounding source names.
Never cite a claim whose audit field reads 'fail' — its quote could not be verified against its source; the engine strips such markers from the shipped report.`
    : '';
  const nullAttacksClause =
    nullAttacksSummary && nullAttacksSummary.length
      ? `
CHALLENGED AND SURVIVED (counter-searched, nothing found): ${plain(nullAttacksSummary)}`
      : '';
  const confidenceClause = computedConfidence
    ? `
Machinery-computed confidence: ${computedConfidence} — your returned confidence may be lower with a stated reason, never higher.`
    : '';
  return render(SYNTHESISER_TPL, {
    mode,
    query,
    focusClause,
    thinkerClause,
    computeMention,
    landscape,
    resultSoFar: plain(resultSoFar),
    waveLog: plain(waveLog),
    cleanReports: plain(cleanReports),
    openRabbitHoles: plain(openRabbitHoles),
    ledgerClause,
    nullAttacksClause,
    confidenceClause,
    computeLeading,
    FINISH,
  });
};
// ╔══ module: src/agents/synthesiser/index.ts ═════════════════════════════
// SYNTHESISER — writes the END report (always) from the hardened facts (source of truth) + the computed
// derivation (verbatim) + the final resultSoFar. Tier: opus (final synthesis). Effort: xhigh.


                                                                           

const REPORT         = {
  type: 'object',
  properties: {
    report: {
      type: 'string',
      description: 'the FULL report as markdown, all 8 sections in order per the contract',
    },
    verdict: { type: 'string', description: '1-3 sentence headline answer' },
    confidence: { type: 'string', enum: ['high', 'medium', 'low'] },
    plan: {
      type: 'array',
      items: { type: 'string' },
      description: 'concrete, opinionated operator actions',
    },
    openQuestions: { type: 'array', items: { type: 'string' } },
  },
  required: ['report', 'verdict', 'confidence', 'plan', 'openQuestions'],
};

const synthesiser                         = {
  tier: CONFIG.TIER.synthesiser,
  effort: CONFIG.EFFORT.synthesiser,
  schema: REPORT,
  buildPrompt: buildSynthesiser,
};
// ╔══ module: src/agents/validator/prompts.ts ═════════════════════════════
// VALIDATOR prompts — the per-wave coverage-gate template + its assembly function. Template strings are
// module-level consts; buildValidator only assembles/substitutes the null-lane clause.


                                                          

const VALIDATE_TPL = `{{! validator — per-wave coverage gate: did this wave's lanes fulfill their requests? }}
You are the VALIDATOR for one research wave of: "{{query}}". Judge cheaply, from the intros below, whether each lane fulfilled what it was sent to find.
Requests this wave (\`#id keyword — why\`):
{{requests}}
What each lane returned (intro only):
{{findings}}{{nullClause}}
For each request return {id, fulfilled, reason}: fulfilled=true when the return actually answers the request; false when it is off-target, empty, or too thin to use (one-line reason). Then set enough — did the wave make real progress overall? — and missing — the specific gaps still open for the next wave to re-pursue.
Return checks, enough, missing.{{FINISH}}
`;

const buildValidator = ({ query, requests, findings, nullLanes }               ) => {
  const nullClause =
    nullLanes && nullLanes.length
      ? `\nLanes that returned nothing (failed outright): ${plain(nullLanes)}`
      : '';
  return render(VALIDATE_TPL, {
    query,
    requests: plain(requests),
    findings: plain(findings),
    nullClause,
    FINISH,
  });
};
// ╔══ module: src/agents/validator/index.ts ═══════════════════════════════
// VALIDATOR — the per-wave coverage gate of the Crawl phase (distinct from the terminal judge). After each
// research wave it asks, cheaply, whether every lane fulfilled its request; the engine re-opens any lane that
// returned null or fulfilled:false (bounded by a per-lane failCount) so the next brainer can re-pursue it.
// Tier: sonnet (a bounded, cheap per-wave check). Effort: medium.


                                                                         

const VALIDATE         = {
  type: 'object',
  properties: {
    checks: {
      type: 'array',
      items: {
        type: 'object',
        properties: {
          id: { type: 'number', description: 'the request id this verdict is for' },
          fulfilled: {
            type: 'boolean',
            description:
              'true = the lane answered its request; false = off-target, empty, or too thin',
          },
          reason: {
            type: 'string',
            description: 'one line — why it fell short (when fulfilled is false)',
          },
        },
        required: ['id', 'fulfilled'],
      },
      description: 'one verdict per request',
    },
    enough: { type: 'boolean', description: 'true = the wave made real progress overall' },
    missing: {
      type: 'array',
      items: { type: 'string' },
      description: 'the specific gaps still open, for the next brainer to re-pursue',
    },
  },
  required: ['checks', 'enough'],
};

const validator                       = {
  tier: CONFIG.TIER.validator,
  effort: CONFIG.EFFORT.validator,
  schema: VALIDATE,
  buildPrompt: buildValidator,
};
// ╔══ module: src/agents/initiator/run.ts ═════════════════════════════════




                                                          
                                                                       

// INITIATOR — shapes the finish to the query: names the load-bearing facts to harden + sets the report focus.
// Returns the facts + synthesiser focus the finalize loop consumes, plus the initiator artifact markdown.
async function runInitiator(
  bs              ,
  topOpen          ,
)                                                                           {
  log(
    '· finalize · initiator · ' +
      initiator.tier +
      ' · naming the facts to harden + the report focus',
  );
  // v3 FINALIZE — the ledger digest (facts can bind to an existing claim via claimId) + the sensitivity
  // ranking (once a derivation has a completed rerun) — pre-rendered so initiator/prompts.ts stays pure
  // clause assembly.
  const ledger = ledgerLines(bs, CONFIG.BRAINER_LEDGER_CAP);
  const sensitivity =
    bs.derivation && bs.derivation.lastRun
      ? sensitivityRanking(
          { inputs: bs.derivation.inputs, sensitivity: bs.derivation.lastRun.sensitivity },
          bs.claims,
        )
      : '';
  const plan = await retryAgent              (
    initiator.buildPrompt({
      query: CONFIG.query,
      resultSoFar: bs.resultSoFar,
      waveLog: bs.waveLog,
      landscape: bs.scout .landscape,
      openRabbitHoles: topOpen,
      mode: CONFIG.mode,
      thinkerNote: CONFIG.THINKER_NOTE,
      ledger,
      sensitivity,
    }),
    {
      label: 'initiator',
      phase: CONFIG.PHASE.finalize,
      model: initiator.tier,
      effort: initiator.effort,
      schema: initiator.schema,
    },
  );
  const facts =
    plan && plan.refinement && Array.isArray(plan.refinement.facts) ? plan.refinement.facts : [];
  const synthFocus = (plan && plan.synthesiser && plan.synthesiser.focus) || '';
  log(
    '· finalize · plan · facts=' +
      facts.length +
      ' · synthFocus=' +
      (synthFocus ? '"' + synthFocus.slice(0, 60) + '"' : 'none'),
  );
  const artifact = withPrompt(
    'initiator',
    '# Initiator — finalize plan\n\n' +
      '## Facts to harden (' +
      facts.length +
      ')\n\n' +
      (facts.map((f, i) => i + 1 + '. **' + f.fact + '** — ' + f.why).join('\n') || '_none_') +
      '\n\n## Synthesiser focus\n\n' +
      (synthFocus || '_none_') +
      '\n',
  );
  return { facts, synthFocus, artifact };
}
// ╔══ module: src/agents/judge/run.ts ═════════════════════════════════════




                                                          
                                                                         

// the prefix shared by the compute-off limitation message and the engine's openGaps dedup, so editing the wording edits both.
const COMPUTE_LIMIT_PREFIX = 'Quantitative derivation unavailable';

// JUDGE — the TERMINAL skeptic of the finalize phase. Judges the hardened answer (goal met, verification real, derivation valid) and
// names the precise fix when not. When compute is off, needsCompute/computeSound are forced (no derivation path). Bounded by MAX_JUDGE_PASSES.
async function runJudge(
  bs              ,
  cleanReports               ,
  focus        ,
  pass        ,
)                           {
  log('· finalize · judge · ' + judge.tier + ' · judging the hardened answer (pass ' + pass + ')');
  // B1-refinement: the judge sees the leftover/unpursued open rabbit-holes (top by score) so it can rule whether a real gap remains and reopen the crawl.
  const openRabbitHoles = [...bs.rabbitHoles]
    .sort((a, b) => (lastScore(b) ?? -1) - (lastScore(a) ?? -1))
    .slice(0, CONFIG.FINALIZE_TOP_OPEN)
    .map((r) => '[' + (lastScore(r) ?? 'new') + '] ' + r.keyword + ' — ' + r.why);
  // v3 FINALIZE — the ledger digest, the challenged-vs-never-challenged split, the computed confidence, and
  // the crawl's own final stop (STOP RECONCILE): all pre-rendered here so judge/prompts.ts stays pure clause
  // assembly. keyClaimIds is the answer's OWN load-bearing set — "never challenged" is scoped to those, not
  // every tentative claim in the ledger.
  const ledger = ledgerLines(bs, CONFIG.BRAINER_LEDGER_CAP);
  const keyClaimIds = (bs.resultSoFar && bs.resultSoFar.keyClaimIds) || [];
  const survivedAttacks = bs.nullAttacks.map(
    (na) =>
      na.topic +
      (na.claimIds.length ? ' → c' + na.claimIds.join(', c') : '') +
      ' (queries: ' +
      na.queries.join('; ') +
      ')',
  );
  const challengedIds = new Set(bs.nullAttacks.flatMap((na) => na.claimIds));
  const neverChallenged = keyClaimIds
    .map((id) => bs.claims.find((c) => c.id === id))
    .filter(
      (c)             => !!c && !c.retracted && !challengedIds.has(c.id) && !c.attacksSurvived,
    )
    .map((c) => 'c' + c.id + ' ' + clip(c.claim, CONFIG.CLAIM_DIGEST_CLIP));
  const out = await retryAgent          (
    judge.buildPrompt({
      query: CONFIG.query,
      resultSoFar: bs.resultSoFar,
      cleanReports,
      focus,
      openRabbitHoles,
      compute: CONFIG.compute,
      mode: CONFIG.mode,
      computeNote: CONFIG.COMPUTE_NOTE,
      thinkerNote: CONFIG.THINKER_NOTE,
      ledger,
      survivedAttacks,
      neverChallenged,
      computedConfidence: computedConfidence(keyClaimIds, bs.claims),
      stop: bs.coord ? bs.coord.stop : undefined,
    }),
    {
      label: 'judge-' + pass,
      phase: CONFIG.PHASE.finalize,
      model: judge.tier,
      effort: judge.effort,
      schema: judge.schema,
    },
  );
  if (out && !CONFIG.compute) {
    // compute off → no derivation can run; computeSound is true because none is PRESENT to be unsound (this never
    // blocks the exit). But do NOT rubber-stamp needsCompute to false: if the judge says the answer genuinely needs
    // a derivation it cannot have, RETURN that honest signal as a STATED LIMITATION for the engine to fold into
    // openGaps (the report's Open questions) — the engine owns every resultSoFar mutation, not this run fn.
    out.computeSound = true;
    if (out.needsCompute && bs.resultSoFar)
      out.computeLimitation =
        COMPUTE_LIMIT_PREFIX +
        ' (compute is off): ' +
        (out.directive ||
          out.reasoning ||
          'the answer rests on a derivation this run could not perform');
  }
  if (out)
    log(
      '· finalize · judge pass ' +
        pass +
        ' · goalMet=' +
        out.goalMet +
        ' verif=' +
        out.verificationSound +
        ' needsCompute=' +
        out.needsCompute +
        ' computeSound=' +
        out.computeSound,
    );
  return out;
}
// ╔══ module: src/agents/prospector/run.ts ════════════════════════════════




                                                      
                                                       

// PROSPECT — one Opus prospector after the scout names the high-value source venues; the brainer assigns the
// relevant subset per lane. Sets rr.highValueSources/languageGuidance/sourcesReasoning and RETURNS the
// 02-prospector.md markdown (the engine writes it); on failure the venues degrade to none.
async function runProspector(rr                )                  {
  log('· prospector DISPATCH · ' + prospector.tier);
  const res = await retryAgent            (
    prospector.buildPrompt({
      query: CONFIG.query,
      landscape: rr.scout .landscape,
      sources: rr.scout .pages.map((p) => p.url),
      thinkerNote: CONFIG.THINKER_NOTE,
      researcherNote: CONFIG.RESEARCHER_NOTE,
    }),
    {
      label: 'prospector',
      phase: CONFIG.PHASE.scout,
      model: prospector.tier,
      effort: prospector.effort,
      schema: prospector.schema,
    },
  );
  rr.highValueSources = (res && res.highValueSources) || [];
  rr.languageGuidance = (res && res.languageGuidance) || '';
  rr.sourcesReasoning = (res && res.reasoning) || '';
  log(
    '· prospector RETURN · venues=' +
      rr.highValueSources.length +
      (rr.languageGuidance ? ' · languages="' + rr.languageGuidance.slice(0, 80) + '"' : '') +
      (res ? '' : ' (FAILED → none; researchers fall back to general search)'),
  );
  rr.highValueSources.forEach((s, i) =>
    log('    venue ' + (i + 1) + ' · ' + s.source + ' — ' + s.goodFor),
  );
  return withPrompt(
    'prospector',
    '# 02 — Prospector\n\n**Query:** ' +
      CONFIG.query +
      (rr.sourcesReasoning ? '\n\n_' + rr.sourcesReasoning + '_' : '') +
      (rr.languageGuidance ? '\n\n**Language routing:** ' + rr.languageGuidance : '') +
      '\n\n## High-value source venues\n\n' +
      (rr.highValueSources
        .map(
          (s, i) =>
            i +
            1 +
            '. **' +
            s.source +
            '**' +
            (s.lang ? ' [' + s.lang + ']' : '') +
            ' — ' +
            s.goodFor,
        )
        .join('\n') || '_(none returned)_') +
      '\n',
  );
}
// ╔══ module: src/agents/refiner/run.ts ═══════════════════════════════════



                                                          
                                                                                 

// REFINE the named load-bearing facts in parallel — one sonnet refine agent per fact; on a re-run the judge `directive`
// rides into each so it re-checks what the judge flagged. A fact carrying a `claimId` gets that ledger claim's
// pinned quote + source rendered into its prompt (THE CLAIM AS PINNED). Returns the hardened reports + the
// refinement artifact markdown (the engine writes/overwrites the refinement file) + the RAW per-fact outputs
// (queriesTried/counterFound/counterNote) so the engine — never this pure run fn — can fold the attack outcome
// into the ledger. passTag keeps labels unique per pass.
async function runRefine(
  bs              ,
  facts                ,
  directive        ,
  passTag        ,
)                                                                                            {
  const refined = await parallel(
    facts.map((f, i) => () => {
      const pinned =
        typeof f.claimId === 'number' ? bs.claims.find((c) => c.id === f.claimId) : undefined;
      return retryAgent           (
        refiner.buildPrompt({
          net: CONFIG.NET,
          query: CONFIG.query,
          fact: f.fact,
          why: f.why,
          directive,
          claimQuote: pinned?.quote,
          claimSource: pinned?.source,
        }),
        {
          label: 'refine-' + passTag + i,
          phase: CONFIG.PHASE.finalize,
          model: refiner.tier,
          effort: refiner.effort,
          schema: refiner.schema,
        },
      );
    }),
  );
  const cleanReports                = facts.map((f, i) => ({
    fact: f.fact,
    why: f.why,
    clean: (refined[i] && refined[i] .report) || '(refine failed)',
  }));
  const artifact =
    '# Refinement — fact-check & harden the load-bearing facts\n\n' +
    (facts.length
      ? facts
          .map((f, i) => {
            const r = refined[i];
            const attackNote = r
              ? '\n\n_counter-search: ' +
                (r.counterFound
                  ? 'FOUND — ' + (r.counterNote || '(unspecified)')
                  : 'none found (survived)') +
                (r.queriesTried && r.queriesTried.length
                  ? ' · queries: ' + r.queriesTried.join('; ')
                  : '') +
                '_'
              : '';
            return (
              '## ' +
              (i + 1) +
              ' — ' +
              f.fact +
              '\n\n_' +
              f.why +
              '_\n\n' +
              ((r && r.report) || '_(refine failed)_') +
              attackNote
            );
          })
          .join('\n\n')
      : '_no facts to harden_') +
    '\n';
  return { cleanReports, artifact, refined };
}
// ╔══ module: src/agents/researchScheduler/run.ts ═════════════════════════




                                                          
                                                                                                      

// SCHEDULER (B4) — discovery. One Sonnet researchScheduler over the WHOLE wave's lanes: per lane (the
// rabbit-hole + its steering `note` + assigned venues + kind/refetch flags), it batches the searches, sizes
// every candidate via mcp__harvester__fetch size_only, and returns the chosen sources grouped per lane id —
// plus two HONESTY side-channels: `venuesServed` (assigned-vs-served venue reconciliation, so a silent
// tier-substitution shows up) and `unsourced` (directive-named refs/venues it could not fetch, reported
// instead of silently dropped). Returns a ScheduleResult; the ENGINE folds the honesty channels into bs
// (lastUnsourced, venueStats.served) — this fn stays pure request/response. Degrades to an empty schedule
// when the scheduler dies.
async function runScheduler(
  bs              ,
  picks              ,
  tag        ,
  phaseName        ,
)                          {
  const empty                 = {
    schedule: new Map                           (),
    unsourced: '',
    venuesServed: new Map                  (),
  };
  if (!picks.length) return empty;
  const out = await retryAgent              (
    researchScheduler.buildPrompt({
      query: CONFIG.query,
      lanes: picks.map((p) => ({
        id: p.id,
        keyword: p.keyword,
        why: p.why,
        note: p.note || '',
        venues: venuesFor(bs, p.sources),
        ref: p.ref,
        kind: p.kind,
        refetch: p.refetch,
      })),
      researcherNote: CONFIG.RESEARCHER_NOTE,
      vocabulary: vocabSummary(bs.vocabulary, CONFIG.SCHED_VOCAB_CAP),
      corruptCache: [...bs.corruptCachePaths],
    }),
    {
      label: 'scheduler-' + tag,
      phase: phaseName,
      model: researchScheduler.tier,
      effort: researchScheduler.effort,
      agentType: CONFIG.GENERAL_PURPOSE,
      schema: researchScheduler.schema,
    },
  );
  // B6 — only accept lanes whose id is a REAL pick this wave: drop hallucinated ids the scheduler may invent;
  // a duplicate id is last-wins (map.set), which is fine — the engine bin-packs whatever set lands on the id.
  const schedule = new Map                           ();
  const venuesServed = new Map                  ();
  const unsourcedLines           = [];
  const keywordOf = new Map(picks.map((p) => [p.id, p.keyword]));
  const pickIds = new Set(picks.map((p) => p.id));
  for (const l of (out && out.lanes) || [])
    if (l && typeof l.id === 'number' && pickIds.has(l.id) && Array.isArray(l.sources)) {
      schedule.set(l.id, l.sources);
      venuesServed.set(
        l.id,
        Array.isArray(l.venuesServed) ? l.venuesServed.filter((v) => typeof v === 'string') : [],
      );
      for (const u of l.unsourced || [])
        if (u && typeof u.ref === 'string' && typeof u.reason === 'string')
          unsourcedLines.push(
            'lane #' + l.id + ' ' + clip(keywordOf.get(l.id) || '', 40) + ': ' + u.ref + ' — ' + u.reason,
          );
    }
  log(
    '    scheduler · ' +
      schedule.size +
      '/' +
      picks.length +
      ' lane(s) sourced · sources=' +
      [...schedule.values()].reduce((n, s) => n + s.length, 0),
  );
  if (unsourcedLines.length) log('    scheduler · ⚠ unsourced: ' + unsourcedLines.length + ' ref(s)');
  return { schedule, unsourced: unsourcedLines.join('\n'), venuesServed };
}
// ╔══ module: src/agents/researcher/run.ts ════════════════════════════════




                                                          
             
            
           
             
             
                 
            
            
              
                  
           
                              

// LANE THREAD (B5) — one SEQUENTIAL reader thread for a lane, carrying the running answer across every read.
// `readers` is the bin-packed list of reader-units (each a set of cache char-windows). Each reader reads its
// slice(s) off disk + digests; only the clean parsed `runningAnswer` is forwarded (handoff hygiene — no
// tool-call/StructuredOutput serialization). Yields one accumulated ResearchOut (or null if every reader failed).
// `claimDigest` + `laneKind` come from the caller (runResearchers computes claimDigest ONCE per wave off the
// ledger; laneKind is this lane's RabbitHole.kind — 'attack' flips the reader's brief to counter-evidence).
async function runLaneThread(
  p            ,
  readers               ,
  tag        ,
  phaseName        ,
  claimDigest        ,
  laneKind           ,
)                              {
  const N = readers.length;
  let priorAnswer = '';
  const rabbitHoles                   = [];
  const nextSources               = [];
  const deadEnds           = [];
  const claims              = [];
  const newTerms             = [];
  let surprise = ''; // keep the FIRST non-empty surprise note — the earliest contradiction this lane hit
  let any = false;
  let failed = false; // B3: any reader returned null (retries exhausted / open() threw) → the lane is INCOMPLETE
  for (let i = 0; i < N; i++) {
    const out = await retryAgent           (
      researcher.buildPrompt({
        query: CONFIG.query,
        trail: trailOf(p.path, p.keyword),
        keyword: p.keyword,
        why: p.why,
        note: p.note || '',
        footer: CONFIG.FOOTER,
        reads: readers[i],
        readerIndex: i + 1,
        readerCount: N,
        priorAnswer, // clean parsed running answer from the prior reader ('' for reader 1) — renders LAST
        claimDigest,
        laneKind,
        researcherNote: CONFIG.RESEARCHER_NOTE,
      }),
      {
        label: 'lane-' + tag + ':' + lab(p.keyword) + '-r' + (i + 1) + 'of' + N,
        phase: phaseName,
        model: researcher.tier,
        effort: researcher.effort,
        agentType: CONFIG.GENERAL_PURPOSE,
        schema: researcher.schema,
      },
    );
    if (out) {
      any = true;
      if (typeof out.runningAnswer === 'string' && out.runningAnswer.trim())
        priorAnswer = clip(out.runningAnswer, CONFIG.HANDOFF_CHARS); // handoff hygiene + B7: bound the forwarded running answer
      for (const rh of out.rabbitHoles || []) rabbitHoles.push(rh);
      for (const ns of out.nextSources || []) nextSources.push(ns);
      for (const d of out.deadEnds || []) deadEnds.push(d);
      for (const c of out.claims || []) claims.push(c);
      for (const t of out.newTerms || []) newTerms.push(t);
      if (!surprise && out.surprise && out.surprise.trim()) surprise = out.surprise;
    } else {
      failed = true; // a dropped chunk must NOT hide behind the surviving readers
    }
  }
  // B3 — if ANY reader on the lane failed (or none produced anything), return null so the validator gate
  // (anyNull) reopens the lane; never emit a confident summary that silently dropped a chunk.
  if (!any || failed) return null;
  const out              = {
    summary: priorAnswer || '(reader returned no answer)',
    rabbitHoles,
    nextSources,
    deadEnds,
    claims,
    newTerms,
  };
  if (surprise) out.surprise = surprise;
  return out;
}

// RUN RESEARCHERS (B5) — consume the scheduler's per-lane source sets: bin-pack each lane into ≤budget
// reader-units, then spawn ONE sequential reader thread per lane in PARALLEL across lanes. A lane with no
// scheduled source returns null (a dead lane → the validator gate reopens it). Returns each lane's accumulated
// ResearchOut (or null), in pick order. The claim digest is computed ONCE here (the ledger only turns over
// between waves, not within one) and threaded into every lane, alongside each pick's own kind (attack-lane
// awareness) — down through runLaneThread into every reader's prompt.
async function runResearchers(
  bs              ,
  picks              ,
  schedule                                ,
  tag        ,
  phaseName        ,
)                                  {
  const claimDigest = claimDigestOf(bs);
  return parallel(
    picks.map((p) => () => {
      // B7: cap sources-per-lane before packing; B2/B7: hand packReaders the slice cap + the token→char ratio.
      const laneSources = (schedule.get(p.id) || []).slice(0, CONFIG.MAX_SOURCES_PER_LANE);
      const readers = packReaders(
        laneSources,
        CONFIG.RESEARCHER_TOKEN_BUDGET,
        CONFIG.CHUNK_OVERLAP_CHARS,
        CONFIG.MAX_SLICES_PER_READER,
        CONFIG.CHARS_PER_TOKEN,
      );
      if (!readers.length) {
        log('    lane #' + p.id + ' ' + lab(p.keyword) + ' — no source scheduled → skipped');
        return Promise.resolve(null);
      }
      return runLaneThread(p, readers, tag, phaseName, claimDigest, p.kind);
    }),
  );
}
// ╔══ module: src/agents/synthesiser/run.ts ═══════════════════════════════




                                                          
                                                                   

// SYNTHESISER — writes the END report (always) from the judged answer (resultSoFar, any derivation folded into `working`) + the hardened facts.
// gated on CONFIG.compute (not just a non-empty `working`) so compute-off runs never present a derivation. Returns the ReportOut; the engine
// prepends the run-args banner and writes result.md.
async function runSynthesiser(
  bs              ,
  cleanReports               ,
  synthFocus        ,
  topOpen          ,
)                            {
  const hasDerivation = !!(
    CONFIG.compute &&
    bs.resultSoFar &&
    bs.resultSoFar.working &&
    bs.resultSoFar.working.trim()
  );
  log(
    '· finalize · synthesiser · ' +
      synthesiser.tier +
      ' · writing the report' +
      (hasDerivation ? ' (with derivation)' : ''),
  );
  // v3 FINALIZE — cite the ledger + the challenged-and-survived summary + the computed confidence floor
  // (pre-rendered here so synthesiser/prompts.ts stays pure clause assembly, mirroring judge/run.ts).
  const ledger = ledgerLines(bs, CONFIG.BRAINER_LEDGER_CAP);
  const nullAttacksSummary = bs.nullAttacks.map(
    (na) => na.topic + (na.claimIds.length ? ' → c' + na.claimIds.join(', c') : ''),
  );
  const keyClaimIds = (bs.resultSoFar && bs.resultSoFar.keyClaimIds) || [];
  return retryAgent           (
    synthesiser.buildPrompt({
      mode: CONFIG.mode,
      query: CONFIG.query,
      landscape: bs.scout .landscape,
      resultSoFar: bs.resultSoFar,
      waveLog: bs.waveLog,
      cleanReports,
      focus: synthFocus,
      openRabbitHoles: topOpen,
      compute: CONFIG.compute,
      thinkerNote: CONFIG.THINKER_NOTE,
      ledger,
      nullAttacksSummary,
      computedConfidence: computedConfidence(keyClaimIds, bs.claims),
    }),
    {
      label: 'synthesiser',
      phase: CONFIG.PHASE.finalize,
      model: synthesiser.tier,
      effort: synthesiser.effort,
      schema: synthesiser.schema,
    },
  );
}
// ╔══ module: src/agents/validator/run.ts ═════════════════════════════════



                                                          
                                                                                             

// VALIDATOR — the per-wave coverage gate (distinct from the terminal judge). Given the wave's lookupNext
// requests + each lane's intro + which lanes died, it rules whether each request was fulfilled and what is still missing.
async function runValidator(
  bs              ,
  wave        ,
  requests                    ,
  findings                    ,
  nullLanes          ,
)                               {
  return retryAgent              (
    validator.buildPrompt({ query: CONFIG.query, requests, findings, nullLanes }),
    {
      label: 'validator-w' + wave,
      phase: CONFIG.PHASE.crawl,
      model: validator.tier,
      effort: validator.effort,
      schema: validator.schema,
    },
  );
}
// ╔══ module: src/brainerState.ts ═════════════════════════════════════════
             
            
        
              
        
             
               
           
             
             
                
            
                 
              
           
           
             
       
                    
        
               
             
                          

// the speculative GATE's reusable outputs — runGate hardens these once; finalizeWinner reuses them for the report.
                            
                        
                     
                              
                    
 

// ─────────────────────────────────────────────────────────────────────────────
// BrainerState — ONE brainer's private crawl state in the brainer tree.
// The single-brainer ResearchReport split its state in two: the RUN GLOBALS (scout
// + prospector seed, set once before any brainer exists) stay on ResearchReport;
// the PER-CRAWL state (the store + resultSoFar + the per-wave logs + the crawl
// outcome) moves here, so N brainers each carry their own. The store reducers in
// store.ts already operate on a structural `state` first-arg, so a BrainerState IS
// a StoreState and the reducers work on it unchanged.
//
// The run.ts agent fns read the same FIELD NAMES they read off ResearchReport — the
// per-crawl ones live here, the read-only globals (scout/highValueSources/…) are
// copied BY REFERENCE at construction (set once, shared, never mutated mid-crawl) —
// so each fn only changed its param TYPE, not its field accesses. The engine still
// owns every mutation + every files[name] = … write; it passes the active brainer in.
// ─────────────────────────────────────────────────────────────────────────────

// active = waving · done = declared done, awaiting gate dispatch (transient) · finalizing = a speculative gate is in
// flight · won = its gate passed (the run's winner) · drained = wrapped up without winning · lost = a CHILD abandoned
// its branch as a dead end.
                                                                                          

// the read-only run globals every BrainerState shares (ResearchReport satisfies this — the scout + prospector
// populate them before the first brainer is constructed, and nothing mutates them during the crawl).
                             
                         
                               
                            
                           
 

// the identity a brainer is born with — the root has parentName=null (and can never declare lost).
                                  
                                                               
                                                       
                                                                                       
                                                                         
                                                                                    
 

class BrainerState {
  // ── identity ──
  name        ;
  parentName               ;
  mandate        ;
  trail        ;
  depth        ;
  status               ;
  gate                 ; // the last speculative-gate judge verdict (null until a gate runs)

  // ── run globals (shared by reference — read-only during the crawl) ──
  scout                 ;
  scoutRabbitHoles            ;
  highValueSources         ;
  languageGuidance        ;

  // ── StoreState (own, mutable — the reducers in store.ts mutate these) ──
  rabbitHoles              ;
  nextId        ;
  pursuedKeys             ;
  pursuedRefs             ;
  pursuedList          ;
  pursuedArchive              ;

  // ── claim ledger (own — the v3 belief substrate) ──
  claims         ; // append-only quote-pinned facts; JS assigns ids + computes cluster/status
  nextClaimId        ; // the ledger's own auto-counter (mirrors nextId on the rabbit-hole store)
  nullAttacks              ; // counter-searches that found nothing (challenged-and-survived state)
  vocabulary        ; // community terms of art collected from the pages
  derivation                   ; // the stored seeded Python artifact + its latest rerun (null until authored)
  derivationDirty         ; // a fresh derivation delta landed this wave — the next ingest forces a rerun regardless of which claims changed
  derivationStale         ; // the last rerun attempt failed — lastRun is kept but the brainer is told it is stale; cleared on the next successful rerun
  lastChangedClaimIds             ; // claim ids added/status-changed THIS wave (ingestWave) — drives the "did a derivation input change" rerun test
  yieldCalib            ; // per-kind predicted-vs-realized lead-yield EMAs
  clusterOf                        ; // lineage KEY → cluster id — the persistent union-find; its OWN keys ARE the "known keys" the lineageClerk is told about (one canonical structure, no second list)
  nextClusterId        ; // next fresh cluster id to mint; 0 is reserved for the shared "unknown lineage" cluster
  chao                  ; // collect-mode coverage estimate over the claim ledger; null until the first collect wave computes it
  venueStats                                                                       ; // per-venue-source lane assignment/yield tally — flags a 0-yield venue to the brainer; served = lanes where the scheduler's chosen sources actually came from this venue (assigned-vs-served reconciliation)
  knownCachePaths             ; // every cache path the scheduler returned this run; the ingest trust-check accepts a claim cachePath only when known or matching the harvester cache signature
  corruptCachePaths             ; // cache paths readers reported CORRUPT (spam/mismatched content); the scheduler is told to never return them

  // ── crawl accumulators (own) ──
  resultSoFar                    ; // this brainer's living memory
  topScores          ; // its own decay signal (plateau detection)
  topScoresBase        ; // where THIS brainer's own waves start in topScores (a child slices its plateau window from its spawn point)
  waveLog                ;
  resultLog                  ;
  validatorLog                     ;
  lastValidatorMissing        ;
  lastUnsourced        ; // the last wave's scheduler honesty report (unsourced refs + venue substitutions), threaded into the next brainer prompt
  coord              ;
  lookupNext              ; // the pending lanes this brainer pursues next wave (was a runCrawl local — per-brainer now)
  starvedWaves        ; // consecutive empty-schedule waves for THIS brainer (B6 starvation guard)
  gateCache                  ; // the speculative gate's hardened outputs, reused by finalizeWinner (no re-harden)
  wave        ; // its own wave counter (root: wave 0 scores the scout seeds, 1..N research)
  bestOpen        ;
  stopReason                   ;

  // ── finalize outcome (own) ──
  rabbitHolesOut                 ;
  synthesiserOut                  ;
  reportOk         ;
  citationsBogus        ; // synthesiser citation lint: [cN] markers stripped because the id was unknown/retracted
  citationsAuditFailed        ; // synthesiser citation lint: [cN] markers stripped because the claim's quote-pin audit failed
  quotesRepinned        ; // claims whose broken quote the auditor replaced with a verified contiguous span
  cachePathsRejected        ; // claims whose cachePath was untrusted (never scheduled + outside the harvester cache) and was stripped to unpinned
  reopenedLaneCount        ; // finalize judge-reopen lanes — feeds metrics.reopenedLanes so crawl-vs-finalize counts reconcile
  goalMet                ; // this brainer's FINAL judge verdict's goalMet (null when no judge ran)
  judgePasses        ; // how many judge passes ran in finalize for this brainer (including the gate on the multi-brainer path)

  constructor(g            , id                 ) {
    this.name = id.name;
    this.parentName = id.parentName;
    this.mandate = id.mandate;
    this.trail = id.trail;
    this.depth = id.depth;
    this.status = 'active';
    this.gate = null;
    // globals by reference (set once, shared, never mutated mid-crawl)
    this.scout = g.scout;
    this.scoutRabbitHoles = g.scoutRabbitHoles;
    this.highValueSources = g.highValueSources;
    this.languageGuidance = g.languageGuidance;
    // own store
    this.rabbitHoles = [];
    this.nextId = 1;
    this.pursuedKeys = new Set();
    this.pursuedRefs = new Set();
    this.pursuedList = [];
    this.pursuedArchive = [];
    // own claim ledger
    this.claims = [];
    this.nextClaimId = 1;
    this.nullAttacks = [];
    this.vocabulary = [];
    this.derivation = null;
    this.derivationDirty = false;
    this.derivationStale = false;
    this.lastChangedClaimIds = new Set();
    this.yieldCalib = {};
    this.clusterOf = {};
    this.nextClusterId = 1;
    this.chao = null;
    this.venueStats = {};
    this.knownCachePaths = new Set();
    this.corruptCachePaths = new Set();
    // own accumulators
    this.resultSoFar = null;
    this.topScores = [];
    this.topScoresBase = 0;
    this.waveLog = [];
    this.resultLog = [];
    this.validatorLog = [];
    this.lastValidatorMissing = '';
    this.lastUnsourced = '';
    this.coord = null;
    this.lookupNext = [];
    this.starvedWaves = 0;
    this.gateCache = null;
    this.wave = 1;
    this.bestOpen = 0;
    this.stopReason = null;
    // own finalize outcome
    this.rabbitHolesOut = [];
    this.synthesiserOut = null;
    this.reportOk = false;
    this.citationsBogus = 0;
    this.citationsAuditFailed = 0;
    this.quotesRepinned = 0;
    this.cachePathsRejected = 0;
    this.reopenedLaneCount = 0;
    this.goalMet = null;
    this.judgePasses = 0;
  }

  get isRoot()          {
    return this.parentName === null;
  }
}

// SPAWN — a clean deep-copy of the parent into a focused CHILD brainer. New arrays + new Sets (JSON-cloned plain
// data, zero shared references) so neither brainer can corrupt the other's store; the parent's resultSoFar is
// cloned as the child's SEED (the parent-authored `mandate` is what aims it). The run globals propagate by
// reference (the constructor copies them off the parent, which already holds them). depth = parent.depth + 1;
// the child joins at the parent's current wave. The engine calls this when it honors a `spawn` delta (caps first).
function spawnBrainer(
  parent              ,
  id                                                  ,
)               {
  const child = new BrainerState(parent, {
    name: id.name,
    parentName: parent.name,
    mandate: id.mandate,
    trail: id.trail,
    depth: parent.depth + 1,
  });
  // deep-copy the parent's store — clean branch (no shared refs): JSON-clone the plain-data arrays, fresh Sets.
  child.rabbitHoles = JSON.parse(JSON.stringify(parent.rabbitHoles));
  child.pursuedArchive = JSON.parse(JSON.stringify(parent.pursuedArchive));
  child.nextId = parent.nextId;
  child.pursuedKeys = new Set(parent.pursuedKeys);
  child.pursuedRefs = new Set(parent.pursuedRefs);
  child.pursuedList = parent.pursuedList.slice();
  child.topScores = parent.topScores.slice();
  // the claim ledger branches with the brainer — JSON-clone the plain-data stores; yieldCalib entries are
  // never mutated in place (updateCalib is pure), so a shallow copy into a fresh object suffices. clusterOf
  // is plain data too (string → number), so a shallow copy is rebuild-safe — no shared reference back to
  // the parent's union-find.
  child.claims = JSON.parse(JSON.stringify(parent.claims));
  child.nextClaimId = parent.nextClaimId;
  child.nullAttacks = JSON.parse(JSON.stringify(parent.nullAttacks));
  child.vocabulary = JSON.parse(JSON.stringify(parent.vocabulary));
  child.derivation = parent.derivation ? JSON.parse(JSON.stringify(parent.derivation)) : null;
  child.derivationDirty = parent.derivationDirty;
  child.derivationStale = parent.derivationStale;
  child.lastChangedClaimIds = new Set(parent.lastChangedClaimIds);
  child.yieldCalib = { ...parent.yieldCalib };
  child.clusterOf = { ...parent.clusterOf };
  child.nextClusterId = parent.nextClusterId;
  child.chao = parent.chao ? { ...parent.chao } : null;
  child.venueStats = JSON.parse(JSON.stringify(parent.venueStats));
  child.knownCachePaths = new Set(parent.knownCachePaths);
  child.corruptCachePaths = new Set(parent.corruptCachePaths);
  child.topScoresBase = parent.topScores.length; // the child's plateau window starts at ITS spawn point
  // the parent AUTHORS the child's resultSoFar — a deep clone of its own living memory, the seed the mandate aims.
  child.resultSoFar = parent.resultSoFar ? JSON.parse(JSON.stringify(parent.resultSoFar)) : null;
  child.wave = parent.wave;
  child.lastUnsourced = parent.lastUnsourced;
  child.citationsAuditFailed = parent.citationsAuditFailed;
  child.quotesRepinned = parent.quotesRepinned;
  child.cachePathsRejected = parent.cachePathsRejected;
  child.reopenedLaneCount = parent.reopenedLaneCount;
  child.goalMet = parent.goalMet;
  child.judgePasses = parent.judgePasses;
  return child;
}
// ╔══ module: src/store.ts ════════════════════════════════════════════════


             
                    
             
             
             
              
             
             
                          

// ─────────────────────────────────────────────────────────────────────────────
// Store reducers — pure functions over a `state` object that carries the crawl's
// rabbit-hole store (rabbitHoles, nextId, pursuedKeys, pursuedList, pursuedArchive).
// In the original these were methods on ResearchReport; here `state` is the first
// arg (the engine passes `this`). Logic is identical.
// ─────────────────────────────────────────────────────────────────────────────

// the brainer-delta subset applyDeltas consumes (the test passes partial coords, so each field is optional).
                   
                        
                  
                          
                     
  

// light near-duplicate check — Jaccard token-set overlap ≥ CONFIG.NEAR_DUP counts as "the same lead, reworded"
// (catches re-orderings the exact norm() match misses); the threshold is kept high so distinct leads are never
// merged. Exported: the v3 chao1 coverage estimate (engine.ts) reuses it to group near-duplicate claims —
// ONE Jaccard near-dup definition for the whole engine, never a second copy.
const tokenSet = (s        )              => new Set(norm(s).split(' ').filter(Boolean));
const nearDup = (a        , b        )          => {
  const A = tokenSet(a);
  const B = tokenSet(b);
  if (!A.size || !B.size) return false;
  let inter = 0;
  for (const t of A) if (B.has(t)) inter++;
  return inter / (A.size + B.size - inter) >= CONFIG.NEAR_DUP;
};

// add-or-find an OPEN rabbit-hole. Dedup by norm(keyword), a near-duplicate keyword, AND normRef(ref) against the
// open store AND the pursued sets; returns the existing/new entry, or null when the keyword/ref is already pursued
// (never re-open a pursued lane or re-fetch a pursued citation). New entries get a fresh id; scoreHistory seeded only when scored.
function addRabbitHole(
  state            ,
  { keyword, why, path, score, wave, ref, kind }                   ,
)                    {
  const k = norm(keyword);
  const r = ref ? normRef(ref) : '';
  if (!k && !r) return null;
  if (k && state.pursuedKeys.has(k)) return null;
  if (r && state.pursuedRefs.has(r)) return null;
  if (k && state.pursuedList.some((p) => nearDup(keyword, p))) return null; // near-duplicate of a pursued lane
  const existing = state.rabbitHoles.find(
    (x) =>
      (k && norm(x.keyword) === k) ||
      (r && x.ref && normRef(x.ref) === r) ||
      (k && nearDup(keyword, x.keyword)),
  );
  if (existing) return existing;
  const scored = typeof score === 'number';
  const rh             = {
    id: state.nextId++,
    keyword: keyword || ref || '',
    why: why || '',
    score: scored ? score : null,
    scoreHistory: scored ? [{ wave, score }] : [],
    path: path || [],
  };
  if (ref) rh.ref = ref;
  if (kind) rh.kind = kind; // the lead's origin channel — keys the yieldCalib table
  state.rabbitHoles.push(rh);
  return rh;
}

// apply the brainer's DELTAS to the open store, in order: rename → drop → rescore → add. scoreHistory carried natively by id (no reconcile).
function applyDeltas(state            , coord            , wave        )       {
  for (const r of coord.rename || []) {
    const rh = state.rabbitHoles.find((x) => x.id === r.id);
    if (rh) {
      rh.keyword = r.keyword;
      if (r.why) rh.why = r.why;
    }
  }
  if (coord.drop && coord.drop.length) {
    const gone = new Set(coord.drop);
    state.rabbitHoles = state.rabbitHoles.filter((x) => !gone.has(x.id));
  }
  for (const r of coord.rescore || []) {
    const rh = state.rabbitHoles.find((x) => x.id === r.id);
    if (rh) {
      rh.score = r.score;
      rh.scoreHistory.push({ wave, score: r.score });
    }
  }
  for (const a of coord.add || [])
    addRabbitHole(state, {
      keyword: a.keyword,
      why: a.why,
      path: [],
      score: a.score,
      wave,
      kind: a.kind,
    });
}

// resolve the brainer's `lookupNext` into open-store entries to pursue NOW: id → existing lead; keyword → originate (or find). Drop any
// already pursued, attach the lane's assigned venues, dedup, then take the highest-scoring up to laneCount (the hard ceiling).
function resolveLookupNext(
  state            ,
  coord                               ,
  wave        ,
  laneCount        ,
)               {
  const picks               = [];
  for (const item of coord.lookupNext || []) {
    let rh                                = null;
    if (typeof item.id === 'number') rh = state.rabbitHoles.find((x) => x.id === item.id);
    else if (item.keyword || item.ref)
      rh = addRabbitHole(state, {
        keyword: item.keyword || '',
        why: item.why,
        path: [],
        score: item.score,
        wave,
        ref: item.ref,
        kind: item.kind, // origin channel rides along when the brainer originates a lane
      });
    if (!rh || state.pursuedKeys.has(norm(rh.keyword))) continue;
    if (item.sources) rh.sources = item.sources;
    if (item.note) rh.note = item.note; // the brainer's per-lane directive → rides to the scheduler + reader as noteFromBrainer
    if (item.ref && !rh.ref) rh.ref = item.ref;
    if (item.refetch) rh.refetch = true; // corrupted-cache remediation rides to the scheduler
    if (!picks.some((p) => p.id === rh.id)) picks.push(rh);
  }
  // v3 CALIBRATION — the sort key is the score weighted by its kind's predicted-vs-realized yield (selection
  // only; the stored score itself is never touched). kind = the stored lead's own kind (an id-resolved pick
  // already carries it; an originated one got it from addRabbitHole's `kind: item.kind` above); an unseen
  // kind (or none) is neutral (calibFactor defaults to 1) — degrades to the old plain-score sort untouched.
  const weighted = (p            )         =>
    (p.score ?? 0) *
    calibFactor(
      state.yieldCalib ?? {},
      p.kind ?? 'origin',
      CONFIG.CALIB_CLAMP_LO,
      CONFIG.CALIB_CLAMP_HI,
    );
  return picks.sort((a, b) => weighted(b) - weighted(a)).slice(0, laneCount);
}

// REOPEN — the inverse of pursue (validator-driven): move a pursued lead back into the open store so the next
// brainer can re-pursue it. Clears its pursued keys/ref + drops it from the archive, and bumps failCount (the cap).
function reopenRabbitHole(state            , rh            )             {
  const ai = state.pursuedArchive.indexOf(rh);
  if (ai >= 0) state.pursuedArchive.splice(ai, 1);
  state.pursuedKeys.delete(norm(rh.keyword));
  if (rh.ref) state.pursuedRefs.delete(normRef(rh.ref));
  const li = state.pursuedList.indexOf(rh.keyword);
  if (li >= 0) state.pursuedList.splice(li, 1);
  rh.failCount = (rh.failCount || 0) + 1;
  // clear the stale directive: the `note` that already produced a dead lane must NOT be re-sent verbatim — the
  // next brainer re-authors a fresh directive (or the scheduler/reader fall back to the rabbit-hole + goal).
  delete rh.note;
  if (!state.rabbitHoles.some((x) => x.id === rh.id)) state.rabbitHoles.push(rh);
  return rh;
}

// PURSUE — MOVE picks out of the open store into the pursued-archive (no delete-on-pursue): the archive keeps each lead's id + scoreHistory + path.
function pursue(state            , picks              )       {
  for (const p of picks) {
    state.pursuedKeys.add(norm(p.keyword));
    if (p.ref) state.pursuedRefs.add(normRef(p.ref));
    state.pursuedList.push(p.keyword);
    state.pursuedArchive.push(p);
  }
  const gone = new Set(picks.map((p) => p.id));
  state.rabbitHoles = state.rabbitHoles.filter((r) => !gone.has(r.id));
}
// ╔══ module: src/engine.ts ═══════════════════════════════════════════════



















             
        
            
              
        
               
        
          
           
             
          
             
                
                 
            
            
              
                 
              
            
                  
           
           
             
           
                    
        
               
                          

log('▶ RR START · mode=' + CONFIG.mode + ' · maxWave=' + CONFIG.maxWave + ' · dir=' + CONFIG.DIR);

// compact "Run arguments" record — the COMPLETE launch args (CONFIG.rawArgs), verbatim as received. Surfaced at the top of result.md
// (and persisted as the `args` object in _rabbitHoles.json) so every output file records exactly how the run was launched.
const runArgsMd = ()         => '> **Run arguments:** `' + JSON.stringify(CONFIG.rawArgs) + '`\n\n';

// idsInText — recover the claim ids an attack-lane's own note/why text names (the brainer/reader reference
// an existing claim the same way the ledger digest renders it: `c12`). Filters to ids that are actually in
// the ledger — never fabricates one — so a nullAttack's claimIds degrades to [] when nothing is recoverable.
const idsInText = (text        , validIds             )           => {
  if (!text) return [];
  const found           = [];
  for (const m of text.matchAll(/\bc(\d+)\b/g)) {
    const id = Number(m[1]);
    if (validIds.has(id) && !found.includes(id)) found.push(id);
  }
  return found;
};

// ─────────────────────────────────────────────────────────────────────────────
// ResearchReport — the pipeline backbone. Holds the crawl state; each phase method (runCrawl / runFinalize /
// reopenCrawl / buildResult / run) orchestrates the loop and owns every `files[name] = …` write. The per-agent
// work (buildPrompt → retryAgent → artifact) lives in each agent's src/agents/<agent>/run.ts; the store reducers
// live in store.ts; the shared agent caller (retryAgent + the debug I/O buffers) lives in runtime.ts.
// ─────────────────────────────────────────────────────────────────────────────
class ResearchReport {
  // ── run globals — set ONCE by the scout + prospector before any brainer exists, then shared read-only
  // (by reference) into every BrainerState. Nothing mutates these during the crawl. ──
  scout                 ;
  scoutRabbitHoles            ;
  highValueSources         ; // prospector's high-value source venues the brainer assigns per lane
  languageGuidance        ; // prospector's non-English routing note (''=English-dominated)
  sourcesReasoning        ;
  laneRecords              ; // debug: per-lane reader feed (venue-utilization analysis) — a GLOBAL sink across all brainers

  files       ; // every output artifact — written ONLY by the engine
  liveBrainers                ; // the brainer tree: the root + every spawned child (one entry when maxParallelBrainers=1)
  winner                     ; // the first brainer whose speculative gate the judge upheld — owns result.md
  lastWaveTriggered         ; // a gate passed → run ONE more global wave (wrap-up), then drain

  constructor() {
    this.scout = null;
    this.scoutRabbitHoles = [];
    this.highValueSources = [];
    this.languageGuidance = '';
    this.sourcesReasoning = '';
    this.laneRecords = [];
    this.files = {};
    this.liveBrainers = [];
    this.winner = null;
    this.lastWaveTriggered = false;
  }

  // SCHEDULER (B4) — thin delegator to runScheduler (researchScheduler/run.ts); the crawl + finalize reopen route
  // their source discovery through here. Returns a Map<lane id, sources> the engine bin-packs into reader-units
  // (the external signature is unchanged so both call sites stay untouched); internally it also folds runScheduler's
  // honesty side-channels into bs: the scheduler's own unsourced report, the served-cache-path allowlist
  // (knownCachePaths — the ingest cachePath-trust check reads this), the assigned-vs-served venue reconciliation,
  // and a mechanical cache-poisoning tripwire (the Zur spam incident: two different URLs, byte-identical spam
  // payload, flowed into three lanes undetected — a same-size+same-chars group spanning ≥2 distinct urls is
  // near-certainly one payload served under two names, so it is surfaced loudly rather than silently trusted).
  async scheduleSources(
    bs              ,
    picks              ,
    tag        ,
    phaseName        ,
  )                                          {
    const res = await runScheduler(bs, picks, tag, phaseName);
    bs.lastUnsourced = res.unsourced; // each wave overwrites; '' clears a prior wave's report
    for (const srcs of res.schedule.values())
      for (const s of srcs || []) if (s && s.path) bs.knownCachePaths.add(s.path);
    for (const p of picks) {
      const served = res.venuesServed.get(p.id) || [];
      for (const src of p.sources || []) {
        const entry = bs.venueStats[src] || (bs.venueStats[src] = { assigned: 0, yielded: 0, served: 0 });
        if (served.includes(src)) entry.served++;
      }
    }
    // IDENTICAL-PAYLOAD DETECTION — flatten every scheduled source across all lanes; a group sharing the exact
    // same size+chars pair that spans ≥2 DISTINCT source urls is the cache-poisoning signature (one payload,
    // multiple names) — never inferred from a single lane alone, since that could be a legitimate re-cited source.
    const bySize = new Map                           ();
    for (const srcs of res.schedule.values())
      for (const s of srcs || []) {
        const key = s.size + '|' + s.chars;
        const g = bySize.get(key);
        if (g) g.push(s);
        else bySize.set(key, [s]);
      }
    for (const g of bySize.values()) {
      const distinctSources = [...new Set(g.map((s) => s.source))];
      if (distinctSources.length >= 2) {
        const urls = distinctSources.join(', ');
        log('  ⚠ identical payloads across distinct urls (cache-poisoning signature): ' + urls);
        bs.lastUnsourced =
          (bs.lastUnsourced ? bs.lastUnsourced + '\n' : '') + '⚠ identical payloads: ' + urls;
      }
    }
    return res.schedule;
  }

  // ═══════════════════════════════════════════════════════════════════════════════════════════════
  // CLAIM-LEDGER INGESTION (v3) — the ONE shared pipeline both wave paths (runCrawl / runOneWave) and
  // the wave-0 scout seed feed through. Order (mirrors V3-DESIGN.md "Per-wave engine order"): sanitize →
  // claim ingest → audit ∥ lineage → status → attack bookkeeping → vocabulary → surprise → yieldCalib →
  // chao. Every agent in the middle (claimAuditor/lineageClerk) degrades to a named null path — a dead
  // agent leaves claims 'pending'/lineageKeyOf-clustered, never blocks the wave. Engine owns every
  // mutation here; the agents' own run.ts stay pure request/response.
  // ═══════════════════════════════════════════════════════════════════════════════════════════════

  // steps 2–4 — claim ingest (clip/dedupe/hallucinated-stance-filter) + the audited/lineage-clerked batch
  // + status recompute. Shared by ingestWave (N real lanes) and ingestScoutClaims (one wave-0 pseudo-lane)
  // so there is exactly ONE claim-ingest code path, not two. Returns the FRESH claims this call ledgered.
  async ingestClaimSeeds(
    bs              ,
    lanes                                                     ,
    wave        ,
    tag        ,
    phaseName        ,
  )                   {
    // stance targets may only point at claims the reader's digest could actually have shown it — i.e. ones
    // that existed BEFORE this call started, never a sibling claim minted later in this very batch.
    const priorIds = new Set(bs.claims.map((c) => c.id));
    const fresh          = [];
    for (const { claims, lane } of lanes) {
      for (const c of claims || []) {
        if (!c || !c.claim || !c.quote) continue; // drop: no claim text or no quote
        const quote = clip(c.quote, CONFIG.QUOTE_MAX_CHARS);
        const dedupeKey = norm(quote) + '|' + norm(c.source);
        // dedupe: an identical quote+source already ledgered (from an earlier wave or another lane this wave)
        if (bs.claims.some((e) => norm(e.quote) + '|' + norm(e.source) === dedupeKey)) continue;
        // STANCE COERCION — run forensics: readers emitted prose targets ('c36 (Dutch GGZ…)') despite the
        // numeric schema, and the whole attack graph was silently dropped at ingest (a non-numeric target
        // failed the priorIds check below no matter what). Salvage the recoverable ones: pull the first
        // digit-run out of the prose and coerce to a number; only a target with NO digit-run at all is
        // truly unrecoverable and drops the stance (never fabricates a target that was not named).
        let stanceIn = c.stance;
        if (stanceIn && typeof stanceIn.target !== 'number') {
          const m = String(stanceIn.target).match(/\d+/);
          stanceIn = m ? { ...stanceIn, target: Number(m[0]) } : undefined;
        }
        const stance = stanceIn && priorIds.has(stanceIn.target) ? stanceIn : undefined; // hallucinated-id filter
        // CACHEPATH TRUST — run forensics: a reader once pinned claims to /tmp scratch files and session
        // tool-result paths, non-reproducible provenance an archiver/persist pass can never follow later.
        // Only a path the scheduler actually returned this run (bs.knownCachePaths), or one matching the
        // harvester's own cache-directory signature (/.fetch/), is trusted — everything else is honestly
        // unpinned rather than silently kept. A null/non-string cachePath (the null-tolerant schema) is
        // simply absent — unpinned without counting as a trust rejection.
        let cachePath = typeof c.cachePath === 'string' && c.cachePath ? c.cachePath : undefined;
        if (cachePath && !bs.knownCachePaths.has(cachePath) && !/\/\.fetch\//.test(cachePath)) {
          cachePath = undefined;
          bs.cachePathsRejected++;
        }
        const claim        = {
          id: bs.nextClaimId++,
          claim: c.claim,
          value: typeof c.value === 'string' && c.value ? c.value : undefined,
          quote,
          source: c.source,
          cachePath,
          entities: scrubEntities(c.entities),
          cluster: -1, // resolved below (applyLineage) — never left unset
          audit: cachePath ? 'pending' : 'unpinned',
          status: 'tentative',
          stance,
          attacksSurvived: 0,
          retracted: false,
          wave,
          lane,
        };
        bs.claims.push(claim);
        fresh.push(claim);
      }
    }
    if (fresh.length) {
      // bs.clusterOf's OWN keys are the canonical "known keys" list — one structure, never a second copy.
      const knownKeys = Object.keys(bs.clusterOf);
      const [auditMap, lineageMap] = await Promise.all([
        runClaimAuditor(bs, fresh, tag, phaseName),
        runLineageClerk(bs, fresh, knownKeys, tag, phaseName),
      ]);
      for (const c of fresh) {
        const v = auditMap.get(c.id);
        if (!v) continue; // a dead auditor leaves it 'pending' (unpinned downstream) — never guessed
        // REPIN CONTRACT — 'repinned' means the sent quote did not match verbatim, but the auditor located
        // a verified contiguous span that DOES carry the claim: adopt it as the claim's quote (through the
        // same scrub + clip every quote passes through at ingest) and count it a genuine pass, not a guess.
        // A 'repinned' verdict with NO newQuote is a malformed repin — never trust an unverifiable
        // replacement, so it reads as a failed pin instead of a silent pass.
        if (v.verdict === 'repinned' && v.newQuote) {
          c.quote = clip(scrubArtifacts(v.newQuote), CONFIG.QUOTE_MAX_CHARS);
          c.audit = 'pass';
          bs.quotesRepinned++;
        } else if (v.verdict === 'repinned') {
          c.audit = 'fail';
        } else {
          c.audit = v.verdict;
        }
      }
      const keyMap = new Map                  ();
      for (const c of fresh)
        keyMap.set(
          c.id,
          lineageMap.has(c.id) ? lineageMap.get(c.id)  : [lineageKeyOf(c)].filter(Boolean),
        );
      this.applyLineage(bs, fresh, keyMap);
    }
    // STATUS — recompute for every non-retracted claim (cheap at this scale); a new claim can change an
    // OLDER claim's status too (a fresh supporter/attacker), so this is a full pass, not just `fresh`.
    for (const c of bs.claims)
      if (!c.retracted)
        c.status = claimStatus(c, bs.claims, bs.nullAttacks, {
          SETTLED_MIN_CLUSTERS: CONFIG.SETTLED_MIN_CLUSTERS,
        });
    return fresh;
  }

  // lineage clustering — a PERSISTENT, incremental union-find keyed by lineage KEY (bs.clusterOf), so a
  // later claim can retroactively MERGE two previously-separate clusters (every existing member of the
  // merged-away cluster is rewritten, both in bs.clusterOf and on every already-ledgered claim). Empty keys
  // (the clerk + the lineageKeyOf fallback both resolved to nothing) → cluster 0, the shared unknown-lineage bucket.
  applyLineage(bs              , fresh         , keyMap                       )       {
    for (const c of fresh) {
      const keys = [...new Set((keyMap.get(c.id) || []).filter(Boolean))];
      if (!keys.length) {
        c.cluster = 0;
        continue;
      }
      const existingIds = [
        ...new Set(keys.map((k) => bs.clusterOf[k]).filter((v) => v !== undefined)),
      ];
      let target        ;
      if (!existingIds.length) target = bs.nextClusterId++;
      else {
        target = Math.min(...existingIds);
        const merge = new Set(existingIds.filter((id) => id !== target));
        if (merge.size) {
          for (const k of Object.keys(bs.clusterOf))
            if (merge.has(bs.clusterOf[k])) bs.clusterOf[k] = target;
          for (const other of bs.claims) if (merge.has(other.cluster)) other.cluster = target;
        }
      }
      for (const k of keys) bs.clusterOf[k] = target;
      c.cluster = target;
    }
  }

  // step 6 — VOCABULARY: merge newTerms into bs.vocabulary by norm(term); a repeat bumps `uses` and keeps
  // the FIRST gloss (never overwritten by a later, possibly thinner, one).
  mergeVocabulary(bs              , terms                        )       {
    for (const t of terms || []) {
      if (!t || !t.term) continue;
      const key = norm(t.term);
      const existing = bs.vocabulary.find((v) => norm(v.term) === key);
      if (existing) existing.uses++;
      else bs.vocabulary.push({ term: t.term, gloss: t.gloss, uses: 1 });
    }
  }

  // step 9 — CHAO (collect mode only): group non-retracted claims by near-dup of norm(claim) (the store's
  // OWN Jaccard near-dup — one definition, reused, never a second copy); abundance = distinct sources/group.
  updateChao(bs              )       {
    const active = bs.claims.filter((c) => !c.retracted);
    const groups            = [];
    for (const c of active) {
      const g = groups.find((g) => nearDup(c.claim, g[0].claim));
      if (g) g.push(c);
      else groups.push([c]);
    }
    bs.chao = chao1(groups.map((g) => ({ sources: new Set(g.map((m) => norm(m.source))).size })));
  }

  // the ONE shared per-wave ingest step — called from BOTH runCrawl and runOneWave immediately after
  // runResearchers, replacing nothing in the existing validator/brainer flow. `raw`/its ClaimSeeds are
  // MUTATED in place (scrub + the surprise append) so the caller's existing `findings = raw.map(...)`
  // construction picks up the scrubbed/annotated text with no change to that code.
  async ingestWave(
    bs              ,
    toPursue              ,
    raw                        ,
    wave        ,
    phaseName        ,
  )                {
    const tag = (bs.isRoot ? '' : bs.name + '-') + 'w' + wave;
    // 1 — SANITIZE: strip structured-output/tool-call leakage from every reader-returned text field
    // (quotes are scrubbed BEFORE the auditor ever sees them).
    for (const r of raw) {
      if (!r) continue;
      r.summary = scrubArtifacts(r.summary);
      for (const c of r.claims || []) {
        c.claim = scrubArtifacts(c.claim);
        c.quote = scrubArtifacts(c.quote);
      }
    }
    // 1b — CORRUPT CACHE QUARANTINE: readers report poisoned cache content via the CORRUPT deadEnds
    // convention (run forensics: a remediation lane was re-pointed at the same poisoned hash because
    // nothing downstream ever consumed a reader's deadEnds). Pull the cache path out of every CORRUPT
    // deadEnd, quarantine it (bs.corruptCachePaths — the scheduler is told to never re-serve it) and drop
    // it from knownCachePaths so the ingest cachePath-trust check stops treating it as reproducible.
    for (const r of raw) {
      for (const d of r?.deadEnds || []) {
        if (!/^\s*CORRUPT/i.test(d)) continue;
        const m = d.match(/(\/[^\s'"«»)]+)/);
        if (!m) continue;
        bs.corruptCachePaths.add(m[1]);
        bs.knownCachePaths.delete(m[1]);
        log('  ⚠ corrupt cache quarantined: ' + m[1]);
      }
    }
    // 2–4 — claim ingest + audit ∥ lineage + status (shared with the scout seed). Snapshot every claim's
    // status BEFORE the ingest so the diff below can tell the derivation-rerun test (D2) which ids actually
    // moved this wave — added, or flipped status (e.g. an attack just landed) — not just "the ledger grew".
    const beforeStatus = new Map(bs.claims.map((c) => [c.id, c.status]));
    const lanes = toPursue.map((p, i) => ({ claims: raw[i]?.claims, lane: p.keyword }));
    const fresh = await this.ingestClaimSeeds(bs, lanes, wave, tag, phaseName);
    const changedIds = new Set        (fresh.map((c) => c.id));
    for (const c of bs.claims) {
      if (c.retracted) continue;
      const prior = beforeStatus.get(c.id);
      if (prior !== undefined && prior !== c.status) changedIds.add(c.id);
    }
    bs.lastChangedClaimIds = changedIds;
    // 5 — ATTACK BOOKKEEPING: an 'attack'-kind lane that landed a counter-claim contests its target; one
    // that landed NOTHING is a completed counter-search that found nothing — first-class state, not silence.
    const validIds = new Set(bs.claims.map((c) => c.id));
    toPursue.forEach((p, i) => {
      if (p.kind !== 'attack') return;
      const rawClaims = raw[i]?.claims || [];
      const landedAnAttack = rawClaims.some((c) => c.stance && c.stance.kind === 'attacks');
      if (landedAnAttack) {
        for (const a of fresh.filter((c) => c.lane === p.keyword && c.stance?.kind === 'attacks')) {
          const target = bs.claims.find((c) => c.id === a.stance .target);
          if (target) target.counter = a.claim; // status recompute (step 4, next wave) picks up 'contested'
        }
      } else {
        const claimIds = idsInText((p.note || '') + ' ' + (p.why || ''), validIds);
        bs.nullAttacks.push({
          topic: p.keyword,
          claimIds,
          queries: [p.keyword],
          wave,
          phase: phaseName,
        });
        for (const id of claimIds) {
          const t = bs.claims.find((c) => c.id === id);
          if (t) t.attacksSurvived = (t.attacksSurvived || 0) + 1;
        }
      }
    });
    // 6 — VOCABULARY
    for (const r of raw) if (r) this.mergeVocabulary(bs, r.newTerms);
    // 7 — SURPRISE: fold into that lane's own finding summary — no schema change, the brainer just sees it.
    raw.forEach((r, i) => {
      if (r && r.surprise)
        r.summary =
          (r.summary || '') + '\n\n⚡ SURPRISE (lane «' + toPursue[i].keyword + '»): ' + r.surprise;
    });
    // 8 — YIELDCALIB: predicted from the lane's last score, realized from what it actually produced.
    toPursue.forEach((p, i) => {
      const kind = p.kind ?? 'origin';
      const predicted = (lastScore(p) ?? CONFIG.CALIB_DEFAULT_SCORE) / 100;
      const auditedPass = fresh.filter((c) => c.lane === p.keyword && c.audit === 'pass').length;
      const freshLeads = (raw[i]?.rabbitHoles?.length || 0) + (raw[i]?.nextSources?.length || 0);
      const realized = Math.min(
        CONFIG.CALIB_REALIZED_MAX,
        (auditedPass + CONFIG.CALIB_LEAD_WEIGHT * freshLeads) / CONFIG.CALIB_NORM,
      );
      bs.yieldCalib[kind] = updateCalib(
        bs.yieldCalib,
        kind,
        predicted,
        realized,
        CONFIG.CALIB_ALPHA,
      );
    });
    // 8b — VENUE YIELD: per pursued lane, tally each assigned venue's assigned/yielded count — a lane
    // "yielded" when it landed ≥1 ledgered claim or ≥1 fresh lead. Flags a persistently-dry venue to the
    // brainer (see utils venuesWithYieldWarn) without ever touching the prospector's own venue schema.
    toPursue.forEach((p, i) => {
      const yielded =
        fresh.some((c) => c.lane === p.keyword) ||
        (raw[i]?.rabbitHoles?.length || 0) + (raw[i]?.nextSources?.length || 0) > 0;
      for (const src of p.sources || []) {
        const s = bs.venueStats[src] || (bs.venueStats[src] = { assigned: 0, yielded: 0, served: 0 });
        s.assigned++;
        if (yielded) s.yielded++;
      }
    });
    // 9 — CHAO (collect mode only)
    if (CONFIG.mode === 'collect') this.updateChao(bs);
  }

  // D1 — STORE the brainer's freshly-authored derivation delta (v3 STEERING). Engine owns the mutation:
  // same code as already stored ⇒ keep the last good rerun (only the inputs metadata changed); new/changed
  // code ⇒ reset lastRun to null (a fresh rerun is owed before the sensitivity numbers can be trusted
  // again). Always dirties the derivation so maybeRerunDerivation's next check fires at least once.
  applyDerivation(bs              , coord       )       {
    const d = coord.derivation;
    if (!CONFIG.compute || !d || !d.code) return;
    const sameCode = !!(bs.derivation && bs.derivation.code === d.code);
    bs.derivation = {
      code: d.code,
      inputs: d.inputs || [],
      lastRun: sameCode ? bs.derivation .lastRun : null,
    };
    bs.derivationDirty = true;
  }

  // D2 — RERUN the stored derivation when it is dirty (freshly authored/re-authored this wave) OR when a
  // claim one of its inputs cites changed this wave (ingestWave's lastChangedClaimIds). A dead rerunner or a
  // script error degrades to a stale flag — lastRun is kept, the brainer is told it is stale (see
  // sensitivityClause), never blocked. Called right after ingestWave, before the validator gate.
  async maybeRerunDerivation(bs              , wave        , phaseName        )                {
    if (!CONFIG.compute || !bs.derivation) return;
    const inputIds = new Set(bs.derivation.inputs.flatMap((i) => i.claimIds || []));
    const inputsChanged = [...bs.lastChangedClaimIds].some((id) => inputIds.has(id));
    if (!bs.derivationDirty && !inputsChanged) return;
    const run = await runRerunner(bs, phaseName);
    if (run) {
      bs.derivation.lastRun = { quantiles: run.quantiles, sensitivity: run.sensitivity, wave };
      bs.derivationDirty = false;
      bs.derivationStale = false;
      log(
        '  · derivation rerun · p50=' +
          (run.quantiles.p50 ?? '?') +
          ' · top=' +
          (topSensitivityInput(run.sensitivity) || '?'),
      );
    } else {
      bs.derivationStale = true;
      log('  · derivation rerun FAILED — lastRun kept, marked stale');
    }
  }

  // ADOPT RESULT SO FAR (v3 batch 6) — the ONE call site every `bs.resultSoFar = coord.resultSoFar`-style
  // assignment goes through. A degenerate wave (e.g. a brain-compute pass distracted by its derivation) can
  // return an EMPTY keyClaimIds even though the prior resultSoFar carried a real one — adopting it verbatim
  // would silently wipe the confidence floor's own input. Guard: when the incoming result carries no
  // keyClaimIds but the prior one did, preserve the prior ids; a genuinely new non-empty set always replaces.
  adoptResultSoFar(bs              , rs             )       {
    const priorIds = bs.resultSoFar && bs.resultSoFar.keyClaimIds;
    bs.resultSoFar =
      (!rs.keyClaimIds || !rs.keyClaimIds.length) && priorIds && priorIds.length
        ? { ...rs, keyClaimIds: priorIds }
        : rs;
  }

  // SCOUT INGEST (wave 0) — the scout's own claims/newTerms flow through the SAME claim-ingest machinery
  // (steps 2/3/4/6) as one pseudo-lane 'scout', kind 'seed'. No digest existed yet (scout claims never
  // carry a stance) and there is no lane to attack/calibrate/chao yet — those steps are skipped outright.
  async ingestScoutClaims(bs              , scoutOut          )                {
    if (!(scoutOut.claims || []).length && !(scoutOut.newTerms || []).length) return;
    await this.ingestClaimSeeds(
      bs,
      [{ claims: scoutOut.claims, lane: 'scout' }],
      0,
      'scout',
      CONFIG.PHASE.scout,
    );
    this.mergeVocabulary(bs, scoutOut.newTerms);
  }

  // Crawl: wave 0 = score the scout rabbit-holes; waves 1..N = pursue → research → re-coordinate until the brainer stops.
  async runCrawl(bs              , scoutRabbitHoles            )                {
    const scoutOut = this.scout ;
    await this.ingestScoutClaims(bs, scoutOut); // v3: seed the claim ledger from the scout's own claims/newTerms

    this.files['01-scout.md'] = withPrompt(
      'scout',
      '# 01 — Scout\n\n**Query:** ' +
        CONFIG.query +
        '\n\n## Landscape\n\n' +
        scoutOut.landscape +
        '\n\n## Sources\n\n' +
        scoutOut.pages
          .map(
            (p, i) =>
              '### ' +
              (i + 1) +
              ' — ' +
              p.url +
              '\n\n' +
              p.summary +
              '\n\n' +
              (p.rabbitHoles || []).map((l) => '- **' + l.keyword + '** — ' + l.why).join('\n'),
          )
          .join('\n\n') +
        '\n\n## Dead ends\n\n' +
        ((scoutOut.deadEnds || []).map((d) => '- ' + d).join('\n') || '_none_') +
        '\n',
    );

    // seed the open store with the scout rabbit-holes (UNSCORED — the brainer scores them this wave via rescore).
    scoutRabbitHoles.forEach((l) =>
      addRabbitHole(bs, {
        keyword: l.keyword,
        why: l.why,
        path: l.path || [],
        wave: 0,
        kind: l.kind,
      }),
    );
    // v3: the scout's own followed-citation leads seed the store the same way a crawl wave's nextSources do —
    // never dropped on the floor just because there is no digest yet at wave 0.
    (scoutOut.nextSources || []).forEach((s) =>
      addRabbitHole(bs, {
        keyword: s.why,
        why: 'followed citation',
        ref: s.ref,
        kind: 'citation',
        path: [],
        wave: 0,
      }),
    );

    const seedFindings            = scoutOut.pages.map((p) => ({
      rabbitHole: p.url,
      summary: p.summary,
    }));
    log(
      '· brainer-w0 DISPATCH · ' +
        brainer.tier +
        ' · scoring ' +
        bs.rabbitHoles.length +
        ' rabbit-hole(s)',
    );
    let coord = await runBrainer(bs, 0, seedFindings, CONFIG.PHASE.scout);
    if (!coord) {
      // Wave-0 brainer dead after all retries (terminal API error / safety-classifier
      // block). Do NOT throw — the scout + prospector material and the seeded claim
      // ledger still carry real verified value. Drain the crawl, stamp the honest
      // stopReason, and let finalize produce a loudly-labelled degraded report
      // (see the DEGRADED banner in runFinalize) instead of destroying the run.
      log('✗ brainer-w0 DIED — crawl aborted; finalizing on scout material only');
      bs.status = 'drained';
      bs.stopReason = 'brainer-dead';
      return;
    }
    applyDeltas(bs, coord, 0);
    this.applyDerivation(bs, coord);
    if (coord.resultSoFar) this.adoptResultSoFar(bs, coord.resultSoFar);
    bs.resultLog.push({ wave: 0, resultSoFar: bs.resultSoFar });
    let lookupNext = resolveLookupNext(bs, coord, 0, laneCount);
    bs.topScores.push(lookupNext.length ? Math.max(...lookupNext.map((p) => p.score ?? 0)) : 0);
    bs.waveLog.push({
      wave: 0,
      pursued: [],
      newRabbitHoles: scoutRabbitHoles.length,
      rabbitHoles: bs.rabbitHoles.length,
      topScore: bs.topScores[bs.topScores.length - 1],
      done: coord.stop.done,
      reason: coord.stop.reason,
    });
    log(
      '· brainer-w0 RETURN · rabbitHoles=' +
        bs.rabbitHoles.length +
        ' · lookupNext=' +
        lookupNext.length +
        '/' +
        (coord.lookupNext || []).length +
        ' · topScore=' +
        bs.topScores[bs.topScores.length - 1] +
        ' · done=' +
        coord.stop.done,
    );
    lookupNext.forEach((p, i) =>
      log(
        '    look-up ' +
          (i + 1) +
          ' · [' +
          (p.score ?? '?') +
          '] #' +
          p.id +
          ' ' +
          p.keyword +
          (p.sources && p.sources.length ? ' · venues=[' + p.sources.join(', ') + ']' : ''),
      ),
    );

    this.files['03-wave-0.md'] = withPrompt(
      'brainer-w0',
      waveMd(0, coord, lookupNext, [], bs.rabbitHoles),
    );

    phase(CONFIG.PHASE.crawl); // scout → prospector → seed brainer = the Scout phase; waves 1..N = Crawl
    let wave = 1;
    let dryStop = false; // collect-mode dry: set when the novelty trajectory has plateaued (diminishing returns)
    let starvedStop = false; // B6: set when the scheduler/readers starved for MAX_STARVED_WAVES in a row
    let starvedWaves = 0; // consecutive all-null / empty-schedule waves
    const baseCap = CONFIG.maxWave === 'auto' ? CONFIG.HARD_CAP : CONFIG.maxWave; // effective wave cap; 'auto' rides up to HARD_CAP, the brainer stops it sooner
    // the crawl runs waves until the brainer declares done, the store dries up, the collect novelty plateaus, or the wave cap is hit.
    while (
      wave <= Math.min(CONFIG.HARD_CAP, baseCap) &&
      !coord.stop.done &&
      lookupNext.length &&
      !dryStop
    ) {
      bs.wave = wave; // keep bs.wave in step with the loop (the rerunner's label + lastRun.wave read it mid-wave)
      // PURSUE — move lookupNext into the pursued-archive (keeps id + scoreHistory + path) and out of the open store, so the brainer
      // re-scores a clean open-only set next wave.
      pursue(bs, lookupNext);
      log(
        '— wave ' +
          wave +
          ' · pursuing ' +
          lookupNext.length +
          ' rabbit-hole(s) · pursued-total=' +
          bs.pursuedList.length +
          ' · archived=' +
          bs.pursuedArchive.length,
      );

      // RESEARCH wave — the SCHEDULER discovers + sizes the sources for the wave's lanes, then code bin-packs
      // each lane and runs ONE sequential reader thread per lane (parallel across lanes); each carries its full TRAIL.
      const toPursue = lookupNext;
      const tag = 'w' + wave;
      const schedule = await this.scheduleSources(bs, toPursue, tag, CONFIG.PHASE.crawl);
      const raw = await runResearchers(bs, toPursue, schedule, tag, CONFIG.PHASE.crawl);
      await this.ingestWave(bs, toPursue, raw, wave, CONFIG.PHASE.crawl); // v3: claim-ledger ingest (mutates raw's text fields + bs)
      await this.maybeRerunDerivation(bs, wave, CONFIG.PHASE.crawl); // v3 STEERING: rerun the stored derivation iff dirty or an input claim changed
      // B6 — guard scheduler-DEATH starvation: a wave is "starved" when the scheduler returned NO usable sources at
      // all (an empty map, or every lane's source list empty). After MAX_STARVED_WAVES in a row, break with an
      // explicit stopReason instead of grinding to HARD_CAP with nothing to read. (An all-null wave whose schedule
      // DID carry sources is a reader failure — left to the validator reopen/cap + brainer-convergence path, which
      // this guard must not preempt.)
      const waveStarved = [...schedule.values()].every((srcs) => !srcs || !srcs.length);
      starvedWaves = waveStarved ? starvedWaves + 1 : 0;
      if (starvedWaves >= CONFIG.MAX_STARVED_WAVES) {
        starvedStop = true;
        log(
          '  wave ' +
            wave +
            ' · scheduler-starved (' +
            starvedWaves +
            ' consecutive empty waves) → stopping',
        );
        break;
      }
      const findings            = raw.map((r, i) => ({
        rabbitHole: toPursue[i].keyword,
        trail: trailOf(toPursue[i].path, toPursue[i].keyword),
        summary: r ? r.summary : '(researcher failed)',
      }));
      if (CONFIG.debug)
        raw.forEach((r, i) =>
          this.laneRecords.push({
            wave,
            keyword: toPursue[i].keyword,
            assignedVenues: toPursue[i].sources || [],
            summary: r ? r.summary : null,
            rabbitHoles: r ? (r.rabbitHoles || []).map((l) => l.keyword) : [],
          }),
        );

      // PATH: each freshly-surfaced rabbit-hole inherits its parent's trail (parent path + parent keyword). The engine adds them to the open
      // store UNSCORED (scoreHistory=[]); deduped against pursued + the current store; the brainer scores them next wave (shown as "new").
      const fresh             = raw.flatMap((r, i) =>
        r && r.rabbitHoles
          ? r.rabbitHoles.map((l) => ({
              keyword: l.keyword,
              why: l.why,
              path: [...(toPursue[i].path || []), toPursue[i].keyword],
              kind: l.kind ?? 'gap',
            }))
          : [],
      );
      // FOLLOW-THE-LINKS: each page's top outbound citations become ref-carrying leads the next lane fetches directly.
      const freshSources             = raw.flatMap((r, i) =>
        r && r.nextSources
          ? r.nextSources.map((s) => ({
              keyword: s.why,
              why: 'followed citation',
              ref: s.ref,
              path: [...(toPursue[i].path || []), toPursue[i].keyword],
              kind: 'citation'         ,
            }))
          : [],
      );
      const beforeAdd = bs.rabbitHoles.length;
      fresh.forEach((l) =>
        addRabbitHole(bs, { keyword: l.keyword, why: l.why, path: l.path, wave, kind: l.kind }),
      );
      freshSources.forEach((l) =>
        addRabbitHole(bs, {
          keyword: l.keyword,
          why: l.why,
          path: l.path,
          wave,
          ref: l.ref,
          kind: l.kind,
        }),
      );
      const newCount = bs.rabbitHoles.length - beforeAdd;
      log(
        '  wave ' +
          wave +
          ' · researchers=' +
          raw.filter(Boolean).length +
          '/' +
          toPursue.length +
          ' · freshRabbitHoles=' +
          (fresh.length + freshSources.length) +
          ' → +' +
          newCount +
          ' new after dedup',
      );

      // VALIDATOR GATE — the per-wave coverage check. Runs only when a lane died, a finding is thin, a read came
      // back CORRUPT, or a lane reported dead ends but landed no claims at all (keeps it cheap otherwise). The
      // reader prompt has always promised "the engine will reopen the lane" on a dead read — before this,
      // deadEnds were never consumed by anything downstream. Re-opens every lane that returned null OR
      // fulfilled:false (bounded per-lane by MAX_LANE_REFAILS) so the next brainer can re-pursue; a lane past
      // the cap is surfaced as a known gap; `missing` threads into the next brainer.
      const anyNull = raw.some((r) => !r);
      const anyThin = findings.some((f) => !f.summary || f.summary.length < CONFIG.VALIDATOR_THIN);
      const anyCorrupt = raw.some((r) => r && (r.deadEnds || []).some((d) => /^\s*CORRUPT/i.test(d)));
      const anyDeadNoClaims = raw.some(
        (r) => r && (r.deadEnds || []).length > 0 && !(r.claims || []).length,
      );
      if (anyNull || anyThin || anyCorrupt || anyDeadNoClaims) {
        const requests = toPursue.map((p) => ({ id: p.id, keyword: p.keyword, why: p.why }));
        const vFindings = findings.map((f, i) => ({
          keyword: f.rabbitHole,
          intro:
            (f.summary || '').slice(0, CONFIG.VALIDATOR_INTRO_CHARS) +
            (raw[i] && (raw[i] .deadEnds || []).length
              ? ' [deadEnds: ' + raw[i] .deadEnds .join('; ').slice(0, 200) + ']'
              : ''),
        }));
        const nullLanes = toPursue.filter((p, i) => !raw[i]).map((p) => p.keyword);
        const val = await runValidator(bs, wave, requests, vFindings, nullLanes);
        const failedIds = new Set        ();
        toPursue.forEach((p, i) => {
          if (!raw[i]) failedIds.add(p.id);
        });
        if (val && Array.isArray(val.checks))
          val.checks.forEach((c) => {
            if (c && c.fulfilled === false && typeof c.id === 'number') failedIds.add(c.id);
          });
        const reopened           = [];
        const cappedGaps           = [];
        for (const id of failedIds) {
          const rh = bs.pursuedArchive.find((r) => r.id === id);
          if (!rh) continue;
          if ((rh.failCount || 0) >= CONFIG.MAX_LANE_REFAILS) cappedGaps.push(rh.keyword);
          else reopened.push(reopenRabbitHole(bs, rh).keyword);
        }
        const missing = (val && val.missing) || [];
        bs.lastValidatorMissing = [
          ...missing,
          ...cappedGaps.map((k) => k + ' (lane retried twice — treat as a known gap)'),
        ]
          .join('; ')
          .slice(0, CONFIG.VALIDATOR_MISSING_CHARS);
        bs.validatorLog.push({
          wave,
          enough: val ? val.enough : null,
          reopened,
          cappedGaps,
          missing,
        });
        log(
          '  wave ' +
            wave +
            ' · validator · enough=' +
            (val ? val.enough : '?') +
            ' · reopened=' +
            reopened.length +
            ' · cappedGaps=' +
            cappedGaps.length,
        );
      } else {
        bs.lastValidatorMissing = '';
      }

      // BRAINER — the single Opus brain re-scores the open store via deltas, updates the running result, and sets the next direction.
      log(
        '  wave ' +
          wave +
          ' · brainer DISPATCH · ' +
          brainer.tier +
          ' · open=' +
          bs.rabbitHoles.length,
      );
      const nextCoord = await runBrainer(bs, wave, findings);
      if (!nextCoord) {
        log('✗ brainer-w' + wave + ' DIED — stopping');
        break;
      }
      coord = nextCoord;
      applyDeltas(bs, coord, wave);
      this.applyDerivation(bs, coord);
      if (coord.resultSoFar) this.adoptResultSoFar(bs, coord.resultSoFar);
      // crash-safety checkpoint — a single zero-cost log line, the wave's FINAL state (after the brainer's
      // deltas have landed): recoverable from the workflow's live output, off the critical path.
      if (CONFIG.checkpoint)
        log(CONFIG.CHECKPOINT_MARK + ' w' + wave + ' ' + JSON.stringify(compactCheckpoint(bs)));
      bs.resultLog.push({ wave, resultSoFar: bs.resultSoFar });
      lookupNext = resolveLookupNext(bs, coord, wave, laneCount);
      bs.topScores.push(lookupNext.length ? Math.max(...lookupNext.map((p) => p.score ?? 0)) : 0);
      bs.waveLog.push({
        wave,
        pursued: toPursue.map((p) => p.keyword),
        newRabbitHoles: newCount,
        rabbitHoles: bs.rabbitHoles.length,
        topScore: bs.topScores[bs.topScores.length - 1],
        done: coord.stop.done,
        reason: coord.stop.reason,
      });
      this.files[padIdx(wave + 3) + '-wave-' + wave + '.md'] = withPrompt(
        'brainer-w' + wave,
        waveMd(wave, coord, lookupNext, findings, bs.rabbitHoles),
      );
      log(
        '  wave ' +
          wave +
          ' · rabbitHoles=' +
          bs.rabbitHoles.length +
          ' · lookupNext=' +
          lookupNext.length +
          '/' +
          (coord.lookupNext || []).length +
          ' · topScore=' +
          bs.topScores[bs.topScores.length - 1] +
          ' · done=' +
          coord.stop.done +
          (coord.stop.done ? ' (' + coord.stop.reason + ')' : ''),
      );
      lookupNext.forEach((p, i) =>
        log(
          '    next ' +
            (i + 1) +
            ' · [' +
            (p.score ?? '?') +
            '] #' +
            p.id +
            ' ' +
            p.keyword +
            (p.sources && p.sources.length ? ' · venues=[' + p.sources.join(', ') + ']' : ''),
        ),
      );

      // collect-mode DRY stop: diminishing returns relative to the run's OWN peak novelty (adapts per topic — no magic absolute floor).
      // B9 — peak/window are computed over the RESEARCH waves only (topScores.slice(1)) so the inflated wave-0 SEED
      // score never masks a plateau; PLATEAU_MIN_WAVES gates on the research-wave count (3 = 3 research waves).
      const crawlScores = bs.topScores.slice(1);
      if (
        CONFIG.mode === 'collect' &&
        !coord.stop.done &&
        crawlScores.length >= CONFIG.PLATEAU_MIN_WAVES
      ) {
        const peak = Math.max(...crawlScores);
        const window = crawlScores.slice(-CONFIG.PLATEAU_WINDOW);
        if (peak > 0 && window.every((s) => s <= peak * CONFIG.QUERY_PLATEAU)) {
          // CHAO STOP ASSIST — a plateau alone is not enough in collect mode when the coverage estimate says
          // there is still a lot unseen: gate the dry-stop on coverage ≥ CHAO_COVERAGE_STOP (no chao yet ⇒ the
          // old plateau-only behavior, degrade-to-null).
          if (bs.chao == null || bs.chao.coverage >= CONFIG.CHAO_COVERAGE_STOP) {
            dryStop = true;
            log(
              '  wave ' +
                wave +
                ' · collect DRY — top novelty plateaued (' +
                window.join(',') +
                ' ≤ ' +
                CONFIG.QUERY_PLATEAU +
                '×peak ' +
                peak +
                ') → stopping',
            );
          } else {
            log(
              '  wave ' +
                wave +
                ' · plateau but coverage ' +
                bs.chao.coverage.toFixed(2) +
                ' < ' +
                CONFIG.CHAO_COVERAGE_STOP +
                ' — continuing',
            );
          }
        }
      }
      wave++;
    }

    // L2 stop classification: the brainer's own satisficing `done` (primary), else why the loop stopped.
    const bestOpen = bs.rabbitHoles.length
      ? Math.max(...bs.rabbitHoles.map((r) => lastScore(r) ?? 0))
      : 0;
    // Classify in priority order: the brainer's own `done`, then a starved scheduler, then an EMPTY store (an
    // empty-store exit is labelled BEFORE the plateau label — B9), then the collect plateau, the wave cap, and a dry store.
    const stopReason             = coord.stop.done
      ? 'brainer-done'
      : starvedStop
        ? 'scheduler-starved'
        : !bs.rabbitHoles.length
          ? 'rabbithole-empty'
          : dryStop
            ? 'collect-dry-plateau'
            : lookupNext.length
              ? 'wave-cap'
              : 'rabbithole-dry';
    log(
      '■ crawl DONE · stopReason=' +
        stopReason +
        ' · waves=' +
        (wave - 1) +
        ' · rabbitHoles=' +
        bs.rabbitHoles.length,
    );

    // VALIDATOR output → file (every wave it ran: enough verdict + reopened lanes + capped known-gaps + missing)
    if (bs.validatorLog.length) {
      this.files[padIdx(wave + 3) + '-validator.md'] =
        '# Validator — per-wave crawl coverage gate\n\n' +
        bs.validatorLog
          .map(
            (v) =>
              '## Wave ' +
              v.wave +
              ' — enough=' +
              v.enough +
              (v.reopened.length ? '\n\n**Reopened (re-pursue):** ' + v.reopened.join(', ') : '') +
              (v.cappedGaps.length
                ? '\n\n**Known gaps (retried twice, not reopened):** ' + v.cappedGaps.join(', ')
                : '') +
              (v.missing.length ? '\n\n**Missing:** ' + v.missing.join('; ') : ''),
          )
          .join('\n\n') +
        '\n';
    }

    // hand the crawl outcome to the later phases
    bs.coord = coord;
    bs.wave = wave;
    bs.bestOpen = bestOpen;
    bs.stopReason = stopReason;
  }

  // CRAWL REOPEN (rare) — the judge found a real evidence gap: pursue its leads through lane readers and fold the
  // findings back into resultSoFar via a brainer pass. Bounded by the leads the judge returns (≤ laneCount, deduped vs pursued).
  async reopenCrawl(bs              , leads                  , directive        )                {
    const picks = leads
      .filter((l) => l && l.keyword && !bs.pursuedKeys.has(norm(l.keyword)))
      .slice(0, laneCount)
      .map((l) => {
        const rh = addRabbitHole(bs, {
          keyword: l.keyword,
          why: l.why,
          path: ['⚖ judge'],
          score: CONFIG.INJECT_SCORE,
          wave: bs.wave,
          kind: 'inject',
        });
        // steer the finalize lane: the judge's directive becomes the lane `note` (so the scheduler + reader get
        // the same WHAT-to-find steering the crawl lanes get); fall back to the lead's own `why` if no directive.
        if (rh) rh.note = directive || l.why || '';
        return rh;
      })
      .filter(Boolean)                ;
    if (!picks.length) {
      log('· finalize · judge reopen · no fresh leads (all already pursued)');
      return;
    }
    pursue(bs, picks);
    bs.reopenedLaneCount += picks.length; // crawl-vs-finalize lane-count reconciliation for metrics
    const schedule = await this.scheduleSources(bs, picks, 'reopen', CONFIG.PHASE.finalize);
    const raw = await runResearchers(bs, picks, schedule, 'reopen', CONFIG.PHASE.finalize);
    // v3 HARVEST (a v2 weak point: this used to discard almost everything the readers gathered) — claims/
    // attacks/vocab flow into the ledger exactly as a crawl wave's do (mutates raw's text fields too, so the
    // findings built below already carry the scrubbed/annotated summaries).
    await this.ingestWave(bs, picks, raw, bs.wave, CONFIG.PHASE.finalize);
    await this.maybeRerunDerivation(bs, bs.wave, CONFIG.PHASE.finalize); // v3: a reopened lane can change a derivation input too — mirror the two wave-loop call sites
    const findings            = raw.map((r, i) => ({
      rabbitHole: picks[i].keyword,
      trail: trailOf(picks[i].path, picks[i].keyword),
      summary: r ? r.summary : '(researcher failed)',
    }));
    // harvest fresh leads into the store too — same inheritance pattern as the crawl wave loop (never dropped
    // on the floor: a judge-reopened lane that surfaces its own follow-ons used to lose them outright).
    const fresh             = raw.flatMap((r, i) =>
      r && r.rabbitHoles
        ? r.rabbitHoles.map((l) => ({
            keyword: l.keyword,
            why: l.why,
            path: [...(picks[i].path || []), picks[i].keyword],
            kind: l.kind ?? 'gap',
          }))
        : [],
    );
    const freshSources             = raw.flatMap((r, i) =>
      r && r.nextSources
        ? r.nextSources.map((s) => ({
            keyword: s.why,
            why: 'followed citation',
            ref: s.ref,
            path: [...(picks[i].path || []), picks[i].keyword],
            kind: 'citation'         ,
          }))
        : [],
    );
    fresh.forEach((l) =>
      addRabbitHole(bs, {
        keyword: l.keyword,
        why: l.why,
        path: l.path,
        wave: bs.wave,
        kind: l.kind,
      }),
    );
    freshSources.forEach((l) =>
      addRabbitHole(bs, {
        keyword: l.keyword,
        why: l.why,
        path: l.path,
        wave: bs.wave,
        ref: l.ref,
        kind: l.kind,
      }),
    );
    const coord = await runBrainer(bs, bs.wave, findings, CONFIG.PHASE.finalize);
    if (coord) {
      applyDeltas(bs, coord, bs.wave); // rename/drop/rescore/add — the coord's deltas, not just resultSoFar
      this.applyDerivation(bs, coord);
      if (coord.resultSoFar) this.adoptResultSoFar(bs, coord.resultSoFar);
    }
    log('· finalize · judge reopen · folded ' + picks.length + ' lane(s) into the answer');
  }

  // ═══════════════════════════════════════════════════════════════════════════════════════════════
  // FINALIZE LEDGER MUTATIONS (v3 batch 4) — the refiner/judge/synthesiser run.ts fns stay pure request/
  // response; every ledger write lives here, engine-owned, mirroring the crawl-phase ingestWave discipline.
  // ═══════════════════════════════════════════════════════════════════════════════════════════════

  // REFINE → LEDGER (attack-recording) — a fact bound to a claimId (the initiator named one) folds its
  // refiner's counter-search outcome into that claim: counterFound sets a contested `counter` note (status
  // recompute picks up `contested` — see utils claimStatus); !counterFound is a completed counter-search
  // that found nothing — first-class nullAttack state, and the claim's attacksSurvived grows. A fact with
  // no claimId, or a dead refiner (refined[i] null), never touches the ledger — exactly v2.
  applyRefineAttacks(
    bs              ,
    facts                ,
    refined                      ,
    phaseName        ,
  )       {
    facts.forEach((f, i) => {
      if (!f.claimId) return;
      const r = refined[i];
      if (!r) return;
      const claim = bs.claims.find((c) => c.id === f.claimId && !c.retracted);
      if (!claim) return;
      if (r.counterFound) {
        claim.counter = r.counterNote || 'refiner found counter-evidence';
      } else {
        bs.nullAttacks.push({
          topic: f.fact,
          claimIds: [f.claimId],
          queries: r.queriesTried || [],
          wave: bs.wave,
          phase: phaseName,
        });
        claim.attacksSurvived = (claim.attacksSurvived || 0) + 1;
      }
    });
    // MECHANICAL COUNTER-PROPAGATION — run forensics: a refine pass falsified the headline claim but the
    // synthesis rubber-stamped the pre-correction answer; only the judge caught it, one pass later than it
    // should have. The engine now propagates every counterFound into the answer + tensions BEFORE the judge
    // ever sees it — single point of failure removed. Runs over EVERY fact the refiner touched, not just
    // claimId-bound ones (a refine pass can falsify the headline answer itself with no ledger claim behind it).
    const corrections = facts
      .map((f, i) =>
        refined[i] && refined[i] .counterFound
          ? f.fact + ' → ' + (refined[i] .counterNote || 'counter-evidence found')
          : null,
      )
      .filter((l)              => !!l);
    if (corrections.length && bs.resultSoFar) {
      const existingAnswer = bs.resultSoFar.answer || '';
      // dedupe across passes — a correction already folded into the answer by an earlier judge pass never repeats.
      const fresh = corrections.filter((l) => !existingAnswer.includes(l));
      if (fresh.length)
        bs.resultSoFar = {
          ...bs.resultSoFar,
          answer:
            existingAnswer +
            '\n\n**Corrections from the refine pass (machine-appended):**\n' +
            fresh.map((l) => '- ' + l).join('\n'),
          tensions: [...(bs.resultSoFar.tensions || []), ...fresh],
        };
    }
    for (const c of bs.claims)
      if (!c.retracted)
        c.status = claimStatus(c, bs.claims, bs.nullAttacks, {
          SETTLED_MIN_CLUSTERS: CONFIG.SETTLED_MIN_CLUSTERS,
        });
  }

  // runRefine + write the refinement file + fold its attack outcomes into the ledger — the ONE call site
  // every refine dispatch (initial pass, judge re-refine, post-reopen re-refine, the speculative gate) goes
  // through, so applyRefineAttacks never gets skipped on any path.
  async refineAndLedger(
    bs              ,
    facts                ,
    directive        ,
    passTag        ,
    fileKey        ,
    phaseName        ,
  )                         {
    const r = await runRefine(bs, facts, directive, passTag);
    this.files[fileKey] = r.artifact;
    this.applyRefineAttacks(bs, facts, r.refined, phaseName);
    return r.cleanReports;
  }

  // JUDGE RETRACTION — a judge naming real (non-hallucinated) ledger ids discredits them: retracted,
  // statuses recomputed, the computed confidence recomputed (logged — the next judge/synthesiser call
  // reads it fresh off the mutated ledger regardless). When a retracted claim backed a derivation input, ONE
  // bounded rerunner call refreshes lastRun (bounded naturally by the MAX_JUDGE_PASSES loop this rides
  // inside — never a second call for the same judgement). No real id named → nothing (degrade-to-null).
  async applyJudgeRetractions(
    bs              ,
    judgement                 ,
    phaseName        ,
  )                {
    const ids = (judgement && judgement.retractClaimIds) || [];
    const real = ids.filter((id) => bs.claims.some((c) => c.id === id && !c.retracted));
    if (!real.length) return;
    for (const id of real) {
      const c = bs.claims.find((c) => c.id === id);
      if (c) c.retracted = true;
    }
    for (const c of bs.claims)
      if (!c.retracted)
        c.status = claimStatus(c, bs.claims, bs.nullAttacks, {
          SETTLED_MIN_CLUSTERS: CONFIG.SETTLED_MIN_CLUSTERS,
        });
    const confidence = computedConfidence(
      (bs.resultSoFar && bs.resultSoFar.keyClaimIds) || [],
      bs.claims,
    );
    log(
      '⚖ judge retracted claims: ' +
        real.map((id) => 'c' + id).join(', ') +
        ' · confidence now ' +
        confidence,
    );
    const inputIds = new Set(
      (bs.derivation ? bs.derivation.inputs : []).flatMap((i) => i.claimIds || []),
    );
    if (bs.derivation && real.some((id) => inputIds.has(id))) {
      const run = await runRerunner(bs, phaseName);
      if (run)
        bs.derivation.lastRun = {
          quantiles: run.quantiles,
          sensitivity: run.sensitivity,
          wave: bs.wave,
        };
    }
  }

  // SYNTHESISER FINISH (citation lint + confidence floor) — shared by the single-brainer finalize + the
  // multi-brainer winner path. Lints [cN] markers against the ledger (strips + logs + counts the bogus
  // ones), then applies the lower-only confidence floor (final = min(stated, computed) — the computed value
  // can never RAISE it) and appends a note to the report when it lowered the stated confidence. Mutates
  // bs.reportOk/citationsBogus/synthesiserOut and writes result.md; every RunResult/metrics field downstream
  // reads bs.synthesiserOut, which now carries the floored `confidence` + linted `report` in place.
  applyReportFinish(bs              , agg                  , label        )       {
    bs.synthesiserOut = agg;
    bs.reportOk = !!(agg && agg.report);
    bs.citationsBogus = 0;
    bs.citationsAuditFailed = 0;
    if (!bs.reportOk) {
      log('✗ ' + label + ' FAILED — no report returned');
      // Salvage: the run's evidence still has value — deliver a degraded result.md from
      // the running answer instead of finishing with no deliverable at all. Same
      // philosophy as the brainer-dead banner: degrade loudly, never destroy value.
      const rsf = bs.resultSoFar;
      const salvage           = [
        '> ⚠️ **DEGRADED REPORT — the synthesiser died (terminal agent failure after all retries).** Below is the raw running answer, not a synthesized report; the full evidence lives in `_claims.md` and the per-wave files.\n',
      ];
      if (rsf && rsf.answer) salvage.push('## Running answer\n\n' + rsf.answer + '\n');
      if (rsf && rsf.working) salvage.push('## Working notes\n\n' + rsf.working + '\n');
      if (rsf && rsf.openGaps && rsf.openGaps.length)
        salvage.push('## Open gaps\n\n' + rsf.openGaps.map((q) => '- ' + q).join('\n') + '\n');
      if (rsf && rsf.tensions && rsf.tensions.length)
        salvage.push('## Tensions\n\n' + rsf.tensions.map((q) => '- ' + q).join('\n') + '\n');
      this.files['result.md'] = runArgsMd() + salvage.join('\n');
      return;
    }
    const { report: linted, bogus, auditFailed } = lintCitations(agg .report, bs.claims);
    bs.citationsBogus = bogus.length;
    bs.citationsAuditFailed = auditFailed.length;
    if (bogus.length)
      log(
        '⚠ ' +
          label +
          ' citation lint — stripped bogus id(s): ' +
          bogus.map((id) => 'c' + id).join(', '),
      );
    if (auditFailed.length)
      log(
        '⚠ ' +
          label +
          ' citation lint — stripped audit-failed id(s): ' +
          auditFailed.map((id) => 'c' + id).join(', '),
      );
    const keyClaimIds = (bs.resultSoFar && bs.resultSoFar.keyClaimIds) || [];
    const computed = computedConfidence(keyClaimIds, bs.claims);
    const final = minConfidence(agg .confidence, computed);
    let report = linted;
    if (final !== agg .confidence)
      report +=
        '\n\n> Confidence adjusted from ' +
        agg .confidence +
        ' to ' +
        final +
        ' — computed from evidence topology (clusters × attack-survival).';
    agg .report = report;
    agg .confidence = final;
    // DEGRADED banner — the crawl never coordinated (wave-0 brainer dead): the report
    // below is built from scout + refine material only, with no steered crawl behind it.
    // Say so at the very top; a silently-normal-looking report would overstate coverage.
    const degradedBanner =
      bs.stopReason === 'brainer-dead'
        ? '> ⚠️ **DEGRADED RUN — the wave-0 brainer died (terminal agent failure after all retries); no steered crawl ran.** This report is synthesized from the scout sweep + refine verification only. Treat coverage as shallow: re-run RR to get the full crawl.\n\n'
        : '';
    this.files['result.md'] = runArgsMd() + degradedBanner + report;
    log(
      '· ' +
        label +
        ' DONE · confidence=' +
        final +
        ' · plan=' +
        (agg .plan || []).length +
        ' step(s) · openQ=' +
        (agg .openQuestions || []).length +
        (bogus.length ? ' · citationsBogus=' + bogus.length : ''),
    );
  }

  // CHILD→PARENT CLAIM MERGE (v3 batch 4, multi-brainer only) — right before the winner (or root, if no
  // gate ever passed) finalizes, fold every OTHER brainer's non-retracted claims into its ledger so a
  // branch that did not win still contributes its evidence (run-forensics fix: a child's four-source
  // [settled] finding used to flatten back to "in-flight" crossing branches). Dedupe by norm(quote)+
  // norm(source) against the target's OWN ledger (a fact both branches independently found is one row, not
  // two); new rows get fresh ids from the target's nextClaimId. Stances are DROPPED — a stance.target id is
  // a claim id in the LOSER's ledger, which does not exist (or means something else) in the target's, so it
  // cannot be remapped; re-cluster each merged claim through the target's OWN clusterOf/union-find via the
  // SAME applyLineage helper ingestClaimSeeds uses, using the deterministic lineageKeyOf fallback (a merge
  // never re-invokes the lineageClerk agent — a pure bookkeeping operation, not a fresh-evidence wave).
  // nullAttacks always merge in, with claimIds remapped through the ids that survived the merge (topic-only
  // [] for any that did not — a dupe or an id from a lane the loser never actually ledgered).
  mergeChildClaims(target              , losers                )       {
    const others = losers.filter((l) => l !== target);
    if (!others.length) return;
    for (const loser of others) {
      const idMap = new Map                (); // loser claim id → target claim id, MERGED claims only
      let dupes = 0;
      const freshMerged          = [];
      for (const c of loser.claims) {
        if (c.retracted) continue;
        const key = norm(c.quote) + '|' + norm(c.source);
        const dupe = target.claims.find(
          (t) => !t.retracted && norm(t.quote) + '|' + norm(t.source) === key,
        );
        if (dupe) {
          dupes++;
          continue;
        }
        const merged        = { ...c, id: target.nextClaimId++, cluster: -1, stance: undefined };
        target.claims.push(merged);
        freshMerged.push(merged);
        idMap.set(c.id, merged.id);
      }
      if (freshMerged.length) {
        const keyMap = new Map(freshMerged.map((m) => [m.id, [lineageKeyOf(m)].filter(Boolean)]));
        this.applyLineage(target, freshMerged, keyMap);
      }
      for (const na of loser.nullAttacks)
        target.nullAttacks.push({
          ...na,
          claimIds: na.claimIds
            .map((id) => idMap.get(id))
            .filter((id)               => id !== undefined),
        });
      log(
        '⇄ merged ' +
          freshMerged.length +
          ' claims (+' +
          loser.nullAttacks.length +
          ' nullAttacks) from ' +
          loser.name +
          ' into ' +
          target.name +
          ' (' +
          dupes +
          ' dupes dropped)',
      );
    }
    for (const c of target.claims)
      if (!c.retracted)
        c.status = claimStatus(c, target.claims, target.nullAttacks, {
          SETTLED_MIN_CLUSTERS: CONFIG.SETTLED_MIN_CLUSTERS,
        });
  }

  // Finalize (end-only). An opus INITIATOR names the load-bearing facts + the report focus → REFINEMENT: one sonnet REFINE pass per fact,
  // hardening it against the sources → an opus JUDGE judges the hardened answer and drives a bounded remediation loop (the brain DERIVES the
  // answer when one is needed / refine re-checks a mis-hardened fact / the crawl reopens on a real gap) → the opus SYNTHESISER writes the END report.
  async runFinalize(bs              )                {
    phase(CONFIG.PHASE.finalize);
    const rabbitHolesOut                  = bs.rabbitHoles
      .map((f) => ({
        id: f.id,
        keyword: f.keyword,
        why: f.why,
        path: f.path || [],
        score: lastScore(f),
        scoreHistory: f.scoreHistory,
        ...(f.note ? { note: f.note } : {}), // B8: surface the per-lane directive in _rabbitHoles.json
      }))
      .sort((a, b) => (b.score ?? -1) - (a.score ?? -1));
    bs.rabbitHolesOut = rabbitHolesOut;
    const topOpen = rabbitHolesOut.slice(0, CONFIG.FINALIZE_TOP_OPEN).map((f) => f.keyword);

    // ── INITIATOR — names the load-bearing facts to harden + sets the report focus ──
    const { facts, synthFocus, artifact: initiatorMd } = await runInitiator(bs, topOpen);
    this.files[padIdx(bs.wave + 4) + '-initiator.md'] = initiatorMd;

    // ── REFINEMENT — one REFINE agent per fact (parallel): adversarially fact-checks, returns the corrected
    // solid claim; refineAndLedger also folds each fact's counter-search outcome into the ledger (v3 batch 4)
    // via applyRefineAttacks — the ONE call site every refine dispatch on this path goes through.
    const refinementFileKey = padIdx(bs.wave + 5) + '-refinement.md';
    log('· finalize · refinement · ' + facts.length + ' fact(s) → refine · ' + refiner.tier);
    let cleanReports = await this.refineAndLedger(
      bs,
      facts,
      '',
      '',
      refinementFileKey,
      CONFIG.PHASE.finalize,
    );
    log('· finalize · refinement DONE · ' + cleanReports.length + ' hardened fact(s)');

    // ── JUDGE loop — terminal skeptic judges, then a bounded remediation loop fixes the single biggest problem and re-judges ──
    // fold the judge's compute-off limitation (returned, not applied by the judge) into openGaps, deduped — the engine
    // is the sole layer that mutates resultSoFar (replace, never alias-mutate a shared object across the loop).
    const foldComputeLimitation = (j                 )                  => {
      if (j && j.computeLimitation && bs.resultSoFar) {
        const gaps = bs.resultSoFar.openGaps || [];
        if (!gaps.some((g) => g.startsWith(COMPUTE_LIMIT_PREFIX)))
          bs.resultSoFar = { ...bs.resultSoFar, openGaps: [...gaps, j.computeLimitation] };
      }
      return j;
    };
    const judgeLog             = [];
    const computeDirectives           = [];
    let pendingDirective = ''; // set only when the judge's last directive was a report-layer fix the
    // remediation loop had no lever for — forwarded to the synthesiser instead of silently dropped.
    let judgement = foldComputeLimitation(await runJudge(bs, cleanReports, synthFocus, 0));
    if (judgement) judgeLog.push(judgement);
    await this.applyJudgeRetractions(bs, judgement, CONFIG.PHASE.finalize);
    let pass = 0;
    while (
      judgement &&
      pass < CONFIG.MAX_JUDGE_PASSES &&
      !(judgement.goalMet && judgement.verificationSound && judgement.computeSound)
    ) {
      pass++;
      const directive = judgement.directive || '';
      const reason = judgement.reasoning || '';
      if (CONFIG.compute && judgement.needsCompute && !judgement.computeSound) {
        // brain FINALIZE-COMPUTE — the brain (code-capable) derives the answer on the hardened facts, per the judge directive
        log('· finalize · judge pass ' + pass + ' → brain finalize-compute · ' + brainer.tier);
        const out = await runBrainerCompute(bs, cleanReports, directive, reason, pass);
        if (out && out.resultSoFar) this.adoptResultSoFar(bs, out.resultSoFar);
        computeDirectives.push(directive || '(derive the answer the goal needs)');
      } else if (!judgement.verificationSound) {
        // RE-REFINE — the judge flagged a mis-hardened / rubber-stamped fact; re-run refine with its directive
        log('· finalize · judge pass ' + pass + ' → re-refine the flagged fact(s)');
        cleanReports = await this.refineAndLedger(
          bs,
          facts,
          directive,
          'r' + pass + '-',
          refinementFileKey,
          CONFIG.PHASE.finalize,
        );
      } else if (
        !judgement.goalMet &&
        judgement.reopenRabbitHoles &&
        judgement.reopenRabbitHoles.length
      ) {
        // CRAWL REOPEN (rare) — a real evidence/coverage gap; reopen the crawl on the judge's leads, then re-harden.
        // reopenDirective (when the judge supplied one) is the reader-facing EXTRACTION directive for the
        // reopened lane; `directive` alone is a poor lane brief — run forensics: "Rewrite the open-differentiator
        // section" (a report-layer fix) was once handed straight to a haiku reader as its research mandate.
        log('· finalize · judge pass ' + pass + ' → reopen the crawl on a real gap');
        await this.reopenCrawl(bs, judgement.reopenRabbitHoles, judgement.reopenDirective || directive);
        cleanReports = await this.refineAndLedger(
          bs,
          facts,
          directive,
          'r' + pass + '-',
          refinementFileKey,
          CONFIG.PHASE.finalize,
        );
      } else {
        // the directive WAS actionable — just at the report layer, not one the crawl/refine/compute levers
        // above can fix. Forward it to the synthesiser instead of the dishonest "no actionable remediation" label.
        log(
          '· finalize · judge pass ' +
            pass +
            ' → no remediation lever fits — directive forwarded to the synthesiser',
        );
        pendingDirective = directive;
        break;
      }
      judgement = foldComputeLimitation(await runJudge(bs, cleanReports, synthFocus, pass));
      if (judgement) judgeLog.push(judgement);
      await this.applyJudgeRetractions(bs, judgement, CONFIG.PHASE.finalize);
    }
    // the FINAL judge verdict's goalMet (null when no judge ever ran) + how many passes ran — feeds metrics.
    bs.goalMet = judgeLog.length ? judgeLog[judgeLog.length - 1].goalMet : null;
    bs.judgePasses = judgeLog.length;

    // JUDGE file — every pass: the four-flag verdict + reasoning + directive + any reopen
    if (judgeLog.length)
      this.files[padIdx(bs.wave + 6) + '-judge.md'] = withPrompt(
        'judge-' + (judgeLog.length - 1),
        '# Judge — finalize-phase terminal skeptic\n\n' +
          judgeLog
            .map(
              (a, i) =>
                '## Pass ' +
                i +
                ' — ' +
                (a.goalMet && a.verificationSound && a.computeSound
                  ? '✓ UPHELD'
                  : '⚔ flagged a problem') +
                '\n\n- goalMet: ' +
                a.goalMet +
                '\n- verificationSound: ' +
                a.verificationSound +
                '\n- needsCompute: ' +
                a.needsCompute +
                '\n- computeSound: ' +
                a.computeSound +
                '\n\n' +
                (a.reasoning || '') +
                (a.directive ? '\n\n**Directive:** ' + a.directive : '') +
                (a.reopenRabbitHoles && a.reopenRabbitHoles.length
                  ? '\n\n**Reopen:**\n' +
                    a.reopenRabbitHoles.map((l) => '- **' + l.keyword + '** — ' + l.why).join('\n')
                  : ''),
            )
            .join('\n\n') +
          '\n',
      );

    // FINALIZE-COMPUTE file — when the brain derived the answer, capture the directive(s) + the derivation folded into `working`
    if (computeDirectives.length)
      this.files['_finalize-compute.md'] =
        '# Finalize compute — the brain derived the answer on the hardened facts\n\n' +
        '## Directive(s)\n\n' +
        computeDirectives.map((d, i) => i + 1 + '. ' + d).join('\n') +
        '\n\n## Derivation (resultSoFar.working)\n\n' +
        ((bs.resultSoFar && bs.resultSoFar.working) || '_none_') +
        '\n';

    // ── SYNTHESISER — writes the END report (always) from the judged answer + the hardened facts; the
    // citation-lint + confidence-floor finish (v3 batch 4) is shared with the multi-brainer winner path.
    // pendingDirective rides along when the judge's last word was a report-layer fix no remediation lever
    // fit — the synthesiser is the one agent that CAN act on it.
    const agg = await runSynthesiser(
      bs,
      cleanReports,
      synthFocus + (pendingDirective ? '\nJudge directive (apply in the report): ' + pendingDirective : ''),
      topOpen,
    );
    this.applyReportFinish(bs, agg, 'finalize');
  }

  // metrics + _rabbitHoles.json + the crawl-tree render, then the final return shape.
  buildResult(bs              )            {
    const { synthesiserOut, reportOk, rabbitHolesOut, coord } = bs;
    // D — the real last wave, not `wave - 1` (which under-reported in multi-brainer mode: a child's `wave`
    // field tracks the GLOBAL wave counter, not its own wave count).
    const wavesRun = bs.waveLog.length ? bs.waveLog[bs.waveLog.length - 1].wave : 0;

    const metrics          = {
      mode: CONFIG.mode,
      dir: CONFIG.DIR,
      wavesRun,
      stopReason: bs.stopReason,
      scoutRabbitHoles: this.scoutRabbitHoles.length,
      prospectorVenues: this.highValueSources.length,
      pursuedTotal: bs.pursuedList.length,
      rabbitHolesFinal: bs.rabbitHoles.length,
      bestOpenScore: bs.bestOpen,
      topScores: bs.topScores,
      // coord is legitimately null when the wave-0 brainer died (stopReason 'brainer-dead')
      // — a degraded run must still deliver its files, never crash here at the finish line.
      done: coord ? coord.stop.done : false,
      reportWritten: reportOk,
      confidence: reportOk ? synthesiserOut .confidence : null,
      claimsTotal: bs.claims.length,
      nullAttacksTotal: bs.nullAttacks.length,
      chao: bs.chao,
      citationsBogus: bs.citationsBogus,
      citationsAuditFailed: bs.citationsAuditFailed,
      auditCounts: {
        pass: bs.claims.filter((c) => c.audit === 'pass').length,
        fail: bs.claims.filter((c) => c.audit === 'fail').length,
        repinned: bs.quotesRepinned, // those claims already read audit 'pass' above — a distinct count, not a 4th audit value
        unpinned: bs.claims.filter((c) => c.audit === 'unpinned').length,
        pending: bs.claims.filter((c) => c.audit === 'pending').length,
      },
      quotesRepinned: bs.quotesRepinned,
      cachePathsRejected: bs.cachePathsRejected,
      venuesUnrouted: this.highValueSources.filter((v) => !bs.venueStats[v.source]).length,
      goalMet: bs.goalMet,
      judgePasses: bs.judgePasses,
      reopenedLanes: bs.reopenedLaneCount,
    };
    log('■ RR DONE · ' + JSON.stringify(metrics));

    // CLAIM LEDGER artifacts — the full machine-readable ledger, and a human-readable grouped view.
    this.files['_claims.json'] = JSON.stringify(
      { claims: bs.claims, nullAttacks: bs.nullAttacks, vocabulary: bs.vocabulary, chao: bs.chao },
      null,
      2,
    );
    this.files['_claims.md'] = claimsMd(bs);

    // SOURCES artifact (run forensics: an ad-hoc cache-dir sweep once archived 136 foreign files out of
    // 191) — the claim-referenced cache files only, so an archiver's copy step follows claim provenance,
    // never a blind directory sweep. Unique by cachePath — a source many claims share is listed once.
    {
      const seenPaths = new Set        ();
      const sources                                          = [];
      for (const c of bs.claims) {
        if (c.retracted || !c.cachePath || seenPaths.has(c.cachePath)) continue;
        seenPaths.add(c.cachePath);
        sources.push({ cachePath: c.cachePath, source: c.source });
      }
      this.files['_sources.json'] = JSON.stringify(
        {
          note: 'claim-referenced cache files — the provenance-filtered set an archiver should copy (persist.js mirrors these into resources/)',
          sources,
        },
        null,
        2,
      );
    }

    this.files['_rabbitHoles.json'] = JSON.stringify(
      {
        args: CONFIG.rawArgs, // the COMPLETE set of arguments the run was launched with, verbatim
        query: CONFIG.query,
        mode: CONFIG.mode,
        stopReason: bs.stopReason,
        topScores: bs.topScores,
        highValueSources: this.highValueSources,
        rabbitHoles: rabbitHolesOut,
        pursued: bs.pursuedArchive,
      },
      null,
      2,
    );

    // CRAWL TREE — reconstruct the branching from the pursued-archive paths (the global trail record) and render it visually.
                     
                 
                                      
                           
                    
      
    const treeRoot           = { kw: CONFIG.query, children: new Map(), score: null };
    for (const l of bs.pursuedArchive) {
      let cur = treeRoot;
      for (const kw of [...(l.path || []), l.keyword]) {
        const k = norm(kw);
        if (!cur.children.has(k)) cur.children.set(k, { kw, children: new Map(), score: null });
        cur = cur.children.get(k) ;
      }
      cur.score =
        l.scoreHistory && l.scoreHistory.length
          ? l.scoreHistory[l.scoreHistory.length - 1].score
          : null;
      if (l.note) cur.note = l.note; // B8: surface the per-lane directive on the tree leaf
    }
    // treeLines + goalLine are FULL (unclipped) — they feed the persisted _tree.md and the returned `tree`; only the
    // live terminal stream is clipped per-line (CONFIG.TREE_LOG_WIDTH) so the file keeps every keyword/note in full.
    const treeLines           = [];
    const walkTree = (node          , prefix        )       => {
      const kids = [...node.children.values()];
      kids.forEach((c, i) => {
        const last = i === kids.length - 1;
        treeLines.push(
          prefix +
            (last ? '└─ ' : '├─ ') +
            c.kw +
            (c.score != null ? '  [' + c.score + ']' : '') +
            (c.note ? '  ‹' + c.note + '›' : ''),
        );
        walkTree(c, prefix + (last ? '   ' : '│  '));
      });
    };
    walkTree(treeRoot, '');
    const goalLine = 'GOAL: ' + CONFIG.query;
    log('');
    log('🌳 CRAWL TREE — how it branched (goal → lanes pursued · [score]):');
    log(clip(goalLine, CONFIG.TREE_LOG_WIDTH));
    treeLines.forEach((l) => log(clip(l, CONFIG.TREE_LOG_WIDTH)));
    this.files['_tree.md'] =
      '# Crawl tree — how the lanes branched\n\n```\n' +
      goalLine +
      '\n' +
      treeLines.join('\n') +
      '\n```\n';

    return {
      query: CONFIG.query,
      mode: CONFIG.mode,
      dir: CONFIG.DIR,
      stopReason: bs.stopReason,
      done: coord ? coord.stop.done : false, // null on a brainer-dead degraded run
      tree: [goalLine, ...treeLines],
      verdict: reportOk ? synthesiserOut .verdict : null,
      confidence: reportOk ? synthesiserOut .confidence : null,
      plan: reportOk ? synthesiserOut .plan : [],
      openQuestions: reportOk ? synthesiserOut .openQuestions : [],
      pursued: bs.pursuedList,
      pursuedArchive: bs.pursuedArchive,
      highValueSources: this.highValueSources,
      rabbitHoles: rabbitHolesOut,
      resultSoFar: bs.resultSoFar,
      waveLog: bs.waveLog,
      metrics,
      files: this.files,
    };
  }

  // ═══════════════════════════════════════════════════════════════════════════════════════════════
  // MULTI-BRAINER (maxParallelBrainers > 1) — the brainer tree. The single-brainer path above
  // (runCrawl / runFinalize) is left UNTOUCHED so its proven behavior is preserved; this parallel
  // orchestration runs only when the operator opts in. Shape: a global wave loop runs every ACTIVE
  // brainer's wave concurrently; a brainer that declares done fires a speculative GATE (initiator →
  // refine → judge) WITHOUT pausing the others; the FIRST gate the judge upholds wins → one more
  // last wave → drain → the winner's report is result.md, the rest preserved as result-<name>.md.
  // ═══════════════════════════════════════════════════════════════════════════════════════════════

  // the 01-scout.md artifact (shared/global; the single path writes its own inline copy).
  writeScoutFile()       {
    const s = this.scout ;
    this.files['01-scout.md'] = withPrompt(
      'scout',
      '# 01 — Scout\n\n**Query:** ' +
        CONFIG.query +
        '\n\n## Landscape\n\n' +
        s.landscape +
        '\n\n## Sources\n\n' +
        s.pages
          .map(
            (p, i) =>
              '### ' +
              (i + 1) +
              ' — ' +
              p.url +
              '\n\n' +
              p.summary +
              '\n\n' +
              (p.rabbitHoles || []).map((l) => '- **' + l.keyword + '** — ' + l.why).join('\n'),
          )
          .join('\n\n') +
        '\n\n## Dead ends\n\n' +
        ((s.deadEnds || []).map((d) => '- ' + d).join('\n') || '_none_') +
        '\n',
    );
  }

  // a brainer's INITIAL pick — one brainer call with NO preceding research (root: scores the scout seeds;
  // child: re-scores its inherited store through its mandate) → sets its first lookupNext. Mirrors wave 0.
  async pickFirst(
    bs              ,
    seedFindings           ,
    gw        ,
    phaseName        ,
  )                {
    bs.wave = gw;
    const coord = await runBrainer(bs, gw, seedFindings, phaseName, {
      canSpawn: false,
      lastWave: false,
    });
    if (!coord) {
      bs.status = 'drained';
      // Honest stop classification: this lane never coordinated a single wave.
      // For the ROOT this is the whole crawl dying at wave 0 — buildResult and the
      // DEGRADED banner in runFinalize key off stopReason + the missing coord.
      bs.stopReason = 'brainer-dead';
      log('  ✗ ' + bs.name + ' pick-first brainer died → drained');
      return;
    }
    bs.coord = coord;
    applyDeltas(bs, coord, gw);
    this.applyDerivation(bs, coord);
    if (coord.resultSoFar) this.adoptResultSoFar(bs, coord.resultSoFar);
    bs.resultLog.push({ wave: gw, resultSoFar: bs.resultSoFar });
    bs.lookupNext = resolveLookupNext(bs, coord, gw, laneCount);
    bs.topScores.push(
      bs.lookupNext.length ? Math.max(...bs.lookupNext.map((p) => p.score ?? 0)) : 0,
    );
    bs.waveLog.push({
      wave: gw,
      pursued: [],
      newRabbitHoles: 0,
      rabbitHoles: bs.rabbitHoles.length,
      topScore: bs.topScores[bs.topScores.length - 1],
      done: coord.stop.done,
      reason: coord.stop.reason,
    });
    this.files[bs.name + '/wave-' + gw + '.md'] = withPrompt(
      'brainer-' + (bs.isRoot ? '' : bs.name + '-') + 'w' + gw,
      waveMd(gw, coord, bs.lookupNext, seedFindings, bs.rabbitHoles),
    );
    if (coord.stop.lost && !bs.isRoot) bs.status = 'lost';
    else if (coord.stop.done) bs.status = 'done';
    else if (!bs.lookupNext.length) bs.status = 'drained';
  }

  // ONE research wave for ONE brainer: pursue its pending lanes → schedule → read → validate → re-coordinate.
  // Mutates bs; sets bs.status (done / lost / drained) and leaves bs.coord (carrying any spawn) for the caller.
  async runOneWave(
    bs              ,
    gw        ,
    isLastWave         ,
    phaseName        ,
    canSpawn         ,
  )                {
    bs.wave = gw;
    const toPursue = bs.lookupNext;
    pursue(bs, toPursue);
    const tag = (bs.isRoot ? '' : bs.name + '-') + 'w' + gw;
    const schedule = await this.scheduleSources(bs, toPursue, tag, phaseName);
    const raw = await runResearchers(bs, toPursue, schedule, tag, phaseName);
    await this.ingestWave(bs, toPursue, raw, gw, phaseName); // v3: claim-ledger ingest (mutates raw's text fields + bs)
    await this.maybeRerunDerivation(bs, gw, phaseName); // v3 STEERING: rerun the stored derivation iff dirty or an input claim changed
    const waveStarved = [...schedule.values()].every((s) => !s || !s.length);
    bs.starvedWaves = waveStarved ? bs.starvedWaves + 1 : 0;
    // B6 — mirror runCrawl's early break: a scheduler-starved wave stops BEFORE findings/validator/brainer
    // ever see it (there is nothing for them to read). Checked right here, not after the brainer dispatch below,
    // so a starved wave never burns a brainer call it cannot do anything useful with.
    if (bs.starvedWaves >= CONFIG.MAX_STARVED_WAVES) {
      bs.status = 'drained';
      bs.stopReason = 'scheduler-starved';
      log(
        '  · ' +
          bs.name +
          ' w' +
          gw +
          ' · scheduler-starved (' +
          bs.starvedWaves +
          ' consecutive empty waves) → drained',
      );
      return;
    }
    const findings            = raw.map((r, i) => ({
      rabbitHole: toPursue[i].keyword,
      trail: trailOf(toPursue[i].path, toPursue[i].keyword),
      summary: r ? r.summary : '(researcher failed)',
    }));
    if (CONFIG.debug)
      raw.forEach((r, i) =>
        this.laneRecords.push({
          wave: gw,
          keyword: bs.name + ':' + toPursue[i].keyword,
          assignedVenues: toPursue[i].sources || [],
          summary: r ? r.summary : null,
          rabbitHoles: r ? (r.rabbitHoles || []).map((l) => l.keyword) : [],
        }),
      );
    const beforeAdd = bs.rabbitHoles.length; // D — honest newRabbitHoles count (was hardcoded 0)
    raw.forEach((r, i) => {
      if (!r) return;
      const par = [...(toPursue[i].path || []), toPursue[i].keyword];
      for (const l of r.rabbitHoles || [])
        addRabbitHole(bs, {
          keyword: l.keyword,
          why: l.why,
          path: par,
          wave: gw,
          kind: l.kind ?? 'gap',
        });
      for (const s of r.nextSources || [])
        addRabbitHole(bs, {
          keyword: s.why,
          why: 'followed citation',
          path: par,
          wave: gw,
          ref: s.ref,
          kind: 'citation',
        });
    });
    const newCount = bs.rabbitHoles.length - beforeAdd;
    // mirrors runCrawl's VALIDATOR GATE widening — the reader prompt has always promised "the engine will
    // reopen the lane" on a dead read; before this, deadEnds were never consumed by anything downstream.
    const anyNull = raw.some((r) => !r);
    const anyThin = findings.some((f) => !f.summary || f.summary.length < CONFIG.VALIDATOR_THIN);
    const anyCorrupt = raw.some((r) => r && (r.deadEnds || []).some((d) => /^\s*CORRUPT/i.test(d)));
    const anyDeadNoClaims = raw.some(
      (r) => r && (r.deadEnds || []).length > 0 && !(r.claims || []).length,
    );
    if (anyNull || anyThin || anyCorrupt || anyDeadNoClaims) {
      const requests = toPursue.map((p) => ({ id: p.id, keyword: p.keyword, why: p.why }));
      const vFindings = findings.map((f, i) => ({
        keyword: f.rabbitHole,
        intro:
          (f.summary || '').slice(0, CONFIG.VALIDATOR_INTRO_CHARS) +
          (raw[i] && (raw[i] .deadEnds || []).length
            ? ' [deadEnds: ' + raw[i] .deadEnds .join('; ').slice(0, 200) + ']'
            : ''),
      }));
      const nullLanes = toPursue.filter((p, i) => !raw[i]).map((p) => p.keyword);
      const val = await runValidator(bs, gw, requests, vFindings, nullLanes);
      const failedIds = new Set        ();
      toPursue.forEach((p, i) => {
        if (!raw[i]) failedIds.add(p.id);
      });
      if (val && Array.isArray(val.checks))
        val.checks.forEach((c) => {
          if (c && c.fulfilled === false && typeof c.id === 'number') failedIds.add(c.id);
        });
      const reopened           = [];
      const cappedGaps           = [];
      for (const id of failedIds) {
        const rh = bs.pursuedArchive.find((r) => r.id === id);
        if (!rh) continue;
        if ((rh.failCount || 0) >= CONFIG.MAX_LANE_REFAILS) cappedGaps.push(rh.keyword);
        else reopened.push(reopenRabbitHole(bs, rh).keyword);
      }
      bs.lastValidatorMissing = [
        ...((val && val.missing) || []),
        ...cappedGaps.map((k) => k + ' (lane retried twice — known gap)'),
      ]
        .join('; ')
        .slice(0, CONFIG.VALIDATOR_MISSING_CHARS);
      bs.validatorLog.push({
        wave: gw,
        enough: val ? val.enough : null,
        reopened,
        cappedGaps,
        missing: (val && val.missing) || [],
      });
    } else bs.lastValidatorMissing = '';
    const coord = await runBrainer(bs, gw, findings, phaseName, { canSpawn, lastWave: isLastWave });
    if (!coord) {
      bs.status = 'drained';
      log('  ✗ ' + bs.name + ' brainer died w' + gw + ' → drained');
      return;
    }
    bs.coord = coord;
    applyDeltas(bs, coord, gw);
    this.applyDerivation(bs, coord);
    if (coord.resultSoFar) this.adoptResultSoFar(bs, coord.resultSoFar);
    // crash-safety checkpoint — a single zero-cost log line, the wave's FINAL state (after the brainer's
    // deltas have landed): recoverable from the workflow's live output, off the critical path.
    if (CONFIG.checkpoint)
      log(CONFIG.CHECKPOINT_MARK + ' w' + gw + ' ' + JSON.stringify(compactCheckpoint(bs)));
    bs.resultLog.push({ wave: gw, resultSoFar: bs.resultSoFar });
    bs.lookupNext = isLastWave ? [] : resolveLookupNext(bs, coord, gw, laneCount);
    bs.topScores.push(
      bs.lookupNext.length ? Math.max(...bs.lookupNext.map((p) => p.score ?? 0)) : 0,
    );
    bs.waveLog.push({
      wave: gw,
      pursued: toPursue.map((p) => p.keyword),
      newRabbitHoles: newCount,
      rabbitHoles: bs.rabbitHoles.length,
      topScore: bs.topScores[bs.topScores.length - 1],
      done: coord.stop.done,
      reason: coord.stop.reason,
    });
    this.files[bs.name + '/wave-' + gw + '.md'] = withPrompt(
      'brainer-' + (bs.isRoot ? '' : bs.name + '-') + 'w' + gw,
      waveMd(gw, coord, bs.lookupNext, findings, bs.rabbitHoles),
    );
    // D — bestOpen kept honest every wave (was never set in this path) so metrics never report bestOpenScore:0
    // while scored leads sit open — mirrors runCrawl's end-of-crawl computation, just refreshed per wave here.
    bs.bestOpen = bs.rabbitHoles.length
      ? Math.max(...bs.rabbitHoles.map((r) => lastScore(r) ?? 0))
      : 0;
    log(
      '  · ' +
        bs.name +
        ' w' +
        gw +
        ' · researchers=' +
        raw.filter(Boolean).length +
        '/' +
        toPursue.length +
        ' · open=' +
        bs.rabbitHoles.length +
        ' · done=' +
        coord.stop.done +
        (coord.stop.lost ? ' · LOST' : ''),
    );
    // note: the scheduler-starved case is handled by the early return above (right after waveStarved is
    // computed) — by the time this classification runs, bs.starvedWaves can no longer be over the cap.
    if (coord.stop.lost && !bs.isRoot) bs.status = 'lost';
    else if (isLastWave)
      bs.status = 'drained'; // the last wave: collect, do not re-gate
    else if (coord.stop.done) bs.status = 'done';
    else if (!bs.lookupNext.length) {
      bs.status = 'drained';
      bs.stopReason = 'rabbithole-dry';
    } else if (CONFIG.mode === 'collect') {
      // a spawned child's plateau window starts at ITS OWN spawn point (topScoresBase), not the parent's
      // history it inherited a clone of — root has topScoresBase=0 so this is slice(1), unchanged.
      const cs = bs.topScores.slice(bs.topScoresBase + 1);
      if (cs.length >= CONFIG.PLATEAU_MIN_WAVES) {
        const peak = Math.max(...cs);
        const win = cs.slice(-CONFIG.PLATEAU_WINDOW);
        if (peak > 0 && win.every((s) => s <= peak * CONFIG.QUERY_PLATEAU)) {
          // CHAO STOP ASSIST — see runCrawl's mirrored gate: a plateau alone does not drain the brainer when
          // the coverage estimate says a lot remains unseen (no chao yet ⇒ the old plateau-only behavior).
          if (bs.chao == null || bs.chao.coverage >= CONFIG.CHAO_COVERAGE_STOP) {
            bs.status = 'drained';
            bs.stopReason = 'collect-dry-plateau';
          } else {
            log(
              '  · ' +
                bs.name +
                ' plateau but coverage ' +
                bs.chao.coverage.toFixed(2) +
                ' < ' +
                CONFIG.CHAO_COVERAGE_STOP +
                ' — continuing',
            );
          }
        }
      }
    }
  }

  // process a brainer's spawn delta (≤1) — spawn a focused child if the maxParallelBrainers / depth caps allow, then aim + seed it.
  // Called SEQUENTIALLY after the parallel wave so the caps are race-free; delegate-and-release drops the branch.
  async processSpawn(parent              , gw        , phaseName        )                {
    const sp = parent.coord && parent.coord.spawn;
    if (!sp || !sp.mandate) return;
    const live = this.liveBrainers.filter(
      (b) => b.status !== 'lost' && b.status !== 'drained',
    ).length;
    if (live >= CONFIG.maxParallelBrainers) {
      log(
        '  · ' +
          parent.name +
          ' spawn refused — maxParallelBrainers cap (' +
          CONFIG.maxParallelBrainers +
          ' live)',
      );
      return;
    }
    if (parent.depth + 1 > CONFIG.MAX_BRAINER_DEPTH) {
      log('  · ' + parent.name + ' spawn refused — depth cap');
      return;
    }
    let branch                        ;
    if (typeof sp.id === 'number') branch = parent.rabbitHoles.find((r) => r.id === sp.id);
    const trail = branch
      ? [...(branch.path || []), branch.keyword].join('  →  ')
      : (parent.trail ? parent.trail + '  →  ' : '') + (sp.keyword || sp.mandate);
    const name = 'b' + this.liveBrainers.length + '-' + lab(sp.keyword || sp.mandate);
    const child = spawnBrainer(parent, { name, mandate: sp.mandate, trail });
    this.liveBrainers.push(child); // reserve the live slot synchronously (before the pickFirst await)
    if (branch) parent.rabbitHoles = parent.rabbitHoles.filter((r) => r.id !== branch .id); // delegate-and-release
    log(
      '  ✚ ' +
        parent.name +
        ' spawned ' +
        name +
        ' — ‹' +
        clip(sp.mandate, CONFIG.MANDATE_CLIP) +
        '›',
    );
    await this.pickFirst(child, [], gw, phaseName); // the child's initial pick, aimed by its mandate
  }

  // the speculative GATE for a brainer that declared done — initiator → refine → judge (no synthesiser). Returns the
  // judge verdict; caches the hardened facts so the winner's report reuses them. Namespaced artifacts; never pauses others.
  async runGate(bs              )                           {
    const rabbitHolesOut                  = bs.rabbitHoles
      .map((f) => ({
        id: f.id,
        keyword: f.keyword,
        why: f.why,
        path: f.path || [],
        score: lastScore(f),
        scoreHistory: f.scoreHistory,
        ...(f.note ? { note: f.note } : {}),
      }))
      .sort((a, b) => (b.score ?? -1) - (a.score ?? -1));
    bs.rabbitHolesOut = rabbitHolesOut;
    const topOpen = rabbitHolesOut.slice(0, CONFIG.FINALIZE_TOP_OPEN).map((f) => f.keyword);
    const { facts, synthFocus, artifact: initiatorMd } = await runInitiator(bs, topOpen);
    this.files[bs.name + '/initiator.md'] = initiatorMd;
    const cleanReports = await this.refineAndLedger(
      bs,
      facts,
      '',
      '',
      bs.name + '/refinement.md',
      CONFIG.PHASE.finalize,
    );
    const verdict = await runJudge(bs, cleanReports, synthFocus, 0);
    await this.applyJudgeRetractions(bs, verdict, CONFIG.PHASE.finalize);
    bs.gateCache = { facts, synthFocus, cleanReports, topOpen };
    // gate-vs-finalize judge-pass reconciliation: the gate IS a judge pass on the multi-brainer path.
    bs.goalMet = verdict ? verdict.goalMet : null;
    bs.judgePasses += 1;
    if (verdict)
      this.files[bs.name + '/judge.md'] =
        '# Gate judge — ' +
        bs.name +
        '\n\n- goalMet: ' +
        verdict.goalMet +
        '\n- verificationSound: ' +
        verdict.verificationSound +
        '\n- computeSound: ' +
        verdict.computeSound +
        '\n\n' +
        (verdict.reasoning || '') +
        (verdict.directive ? '\n\n**Directive:** ' + verdict.directive : '') +
        '\n';
    return verdict;
  }

  // the WINNER's report — its gate already upheld the hardened facts, so reuse them and run the synthesiser → result.md.
  async finalizeWinner(bs              )                {
    phase(CONFIG.PHASE.finalize);
    const gc = bs.gateCache;
    if (!gc) {
      log('  ✗ winner ' + bs.name + ' has no gate cache — falling back to full finalize');
      await this.runFinalize(bs);
      return;
    }
    const agg = await runSynthesiser(bs, gc.cleanReports, gc.synthFocus, gc.topOpen);
    this.applyReportFinish(bs, agg, 'winner ' + bs.name);
  }

  // a non-winning brainer's wrapped-up state — preserved for later review (its living memory + gate verdict if any).
  writeLoserResult(bs              )       {
    this.files['result-' + bs.name + '.md'] =
      '# ' +
      bs.name +
      ' — ' +
      (bs.status === 'lost' ? 'LOST branch (dead end)' : 'wrapped up — not the winner') +
      '\n\n_Mandate: ' +
      (bs.mandate || '(root)') +
      '_\n\n_Trail: ' +
      (bs.trail || '(root)') +
      '_\n\n' +
      resultSoFarMd(bs.resultSoFar) +
      '\n';
  }

  // _brainers.json + _brainers-tree.md — the brainer tree: who spawned whom, mandate, outcome, final status.
  writeBrainerTree()       {
    const recs = this.liveBrainers.map((b) => ({
      name: b.name,
      parent: b.parentName,
      depth: b.depth,
      status: b.status,
      mandate: b.mandate,
      trail: b.trail,
      waves: b.waveLog.length,
      topScores: b.topScores,
      gate: b.gate
        ? {
            goalMet: b.gate.goalMet,
            verificationSound: b.gate.verificationSound,
            computeSound: b.gate.computeSound,
          }
        : null,
      confidence: b.resultSoFar ? b.resultSoFar.confidence : null,
      answer: b.resultSoFar ? clip(b.resultSoFar.answer || '', CONFIG.TREE_ANSWER_CLIP) : null,
    }));
    this.files['_brainers.json'] = JSON.stringify(
      { winner: this.winner ? this.winner.name : null, brainers: recs },
      null,
      2,
    );
    const lines           = [];
    const walk = (parentName               , pre        )       => {
      const cs = this.liveBrainers.filter((b) => b.parentName === parentName);
      cs.forEach((c, i) => {
        const last = i === cs.length - 1;
        const mark =
          this.winner && c.name === this.winner.name
            ? ' ⚑WINNER'
            : c.status === 'lost'
              ? ' ✗lost'
              : ' (' + c.status + ')';
        lines.push(
          pre +
            (last ? '└─ ' : '├─ ') +
            c.name +
            mark +
            (c.mandate ? '  ‹' + clip(c.mandate, CONFIG.MANDATE_CLIP) + '›' : ''),
        );
        walk(c.name, pre + (last ? '   ' : '│  '));
      });
    };
    walk(null, '');
    log('');
    log('🧠 BRAINER TREE:');
    lines.forEach((l) => log(clip(l, CONFIG.TREE_LOG_WIDTH)));
    this.files['_brainers-tree.md'] =
      '# Brainer tree — the brainer-tree run\n\n```\n' +
      (lines.join('\n') || '(root only)') +
      '\n```\n';
  }

  // the GLOBAL multi-brainer loop (maxParallelBrainers > 1): run every active brainer's wave concurrently each global wave; a done brainer
  // fires its gate without pausing the others; the first upheld gate wins → one last wave → drain.
  async runCrawlMulti(root              , scoutRabbitHoles            )                {
    this.writeScoutFile();
    await this.ingestScoutClaims(root, this.scout ); // v3: seed the root's claim ledger from the scout's own claims/newTerms
    scoutRabbitHoles.forEach((l) =>
      addRabbitHole(root, {
        keyword: l.keyword,
        why: l.why,
        path: l.path || [],
        wave: 0,
        kind: l.kind,
      }),
    );
    // v3: the scout's own followed-citation leads seed the store the same way a crawl wave's nextSources do.
    (this.scout .nextSources || []).forEach((s) =>
      addRabbitHole(root, {
        keyword: s.why,
        why: 'followed citation',
        ref: s.ref,
        kind: 'citation',
        path: [],
        wave: 0,
      }),
    );
    const seedFindings            = this.scout .pages.map((p) => ({
      rabbitHole: p.url,
      summary: p.summary,
    }));
    // brainer-0 (the root's first pick) belongs to the Scout phase, beside scout + prospector — NOT a crawl wave.
    phase(CONFIG.PHASE.scout);
    await this.pickFirst(root, seedFindings, 0, CONFIG.PHASE.scout);

                                                                                  
    const gates            = [];
    const cap = CONFIG.maxWave === 'auto' ? CONFIG.HARD_CAP : CONFIG.maxWave;
    let gw = 1;
    while (gw <= Math.min(CONFIG.HARD_CAP, cap)) {
      const isLastWave = this.lastWaveTriggered;
      const active = this.liveBrainers.filter((b) => b.status === 'active' && b.lookupNext.length);
      if (!active.length) {
        const pending = gates.filter((g) => !g.settled);
        if (pending.length) {
          await Promise.race(pending.map((g) => g.promise));
          continue; // a gate settled — it may have reactivated a brainer or set the winner; re-evaluate
        }
        break; // no active brainers and no gates in flight → the run is done
      }
      const phaseName = 'Research w' + gw;
      phase(phaseName);
      const canSpawnNow =
        !isLastWave &&
        this.liveBrainers.filter((b) => b.status !== 'lost' && b.status !== 'drained').length <
          CONFIG.maxParallelBrainers;
      log(
        '— global wave ' +
          gw +
          ' · active=' +
          active.length +
          ' · live=' +
          this.liveBrainers.length +
          (isLastWave ? ' · LAST WAVE' : ''),
      );
      await parallel(
        active.map((bs) => () => this.runOneWave(bs, gw, isLastWave, phaseName, canSpawnNow)),
      );
      // ── post-wave (sequential) — spawns first (cap-safe), then fire gates for brainers that declared done ──
      for (const bs of active)
        if (canSpawnNow && bs.status === 'active' && bs.coord && bs.coord.spawn)
          await this.processSpawn(bs, gw, phaseName);
      for (const bs of active) {
        if (bs.status !== 'done') continue;
        bs.status = 'finalizing';
        const rec          = { bs, settled: false, promise: Promise.resolve() };
        rec.promise = this.runGate(bs).then((verdict) => {
          rec.settled = true;
          bs.gate = verdict;
          const upheld = !!(
            verdict &&
            verdict.goalMet &&
            verdict.verificationSound &&
            verdict.computeSound
          );
          if (upheld) {
            if (!this.winner) {
              this.winner = bs;
              this.lastWaveTriggered = true;
              log('  ⚑ ' + bs.name + ' GATE PASSED — winner; triggering the last wave');
            }
            bs.status = 'won';
          } else {
            const reopen = (verdict && verdict.reopenRabbitHoles) || [];
            if (reopen.length) {
              bs.lookupNext = resolveLookupNext(
                bs,
                {
                  lookupNext: reopen.map((l) => ({
                    keyword: l.keyword,
                    why: l.why,
                    score: CONFIG.INJECT_SCORE,
                    kind: 'inject'         ,
                  })),
                },
                bs.wave,
                laneCount,
              );
              bs.lastValidatorMissing = (verdict && verdict.directive) || '';
              bs.status = bs.lookupNext.length ? 'active' : 'drained';
              log(
                '  ↺ ' +
                  bs.name +
                  ' gate REJECTED — ' +
                  (bs.status === 'active' ? 'continuing on a flagged gap' : 'drained'),
              );
            } else {
              bs.status = 'drained';
              log('  ↺ ' + bs.name + ' gate REJECTED — no concrete gap → drained');
            }
          }
        });
        gates.push(rec);
      }
      if (isLastWave) break;
      gw++;
    }
    await Promise.all(gates.map((g) => g.promise)); // DRAIN — let every in-flight gate settle
    log(
      '■ multi-crawl DONE · brainers=' +
        this.liveBrainers.length +
        ' · winner=' +
        (this.winner ? this.winner.name : 'none'),
    );
  }

  async run()                     {
    const scoutRabbitHoles = await runScout(this);
    this.files['02-prospector.md'] = await runProspector(this); // name the high-value venues before the crawl
    // the ROOT brainer — globals (scout + prospector) are now set, copied by reference into it; the root can never declare lost.
    const root = new BrainerState(this, {
      name: 'root',
      parentName: null,
      mandate: '',
      trail: '',
      depth: 0,
    });
    this.liveBrainers = [root];
    let result           ;
    if (CONFIG.maxParallelBrainers <= 1) {
      // ── single-brainer (default) — the proven path, unchanged ──
      await this.runCrawl(root, scoutRabbitHoles);
      await this.runFinalize(root);
      result = this.buildResult(root);
    } else {
      // ── multi-brainer brainer tree (opt-in via maxParallelBrainers > 1) ──
      await this.runCrawlMulti(root, scoutRabbitHoles);
      const winner = this.winner;
      const target = winner || root;
      // CHILD→PARENT CLAIM MERGE — fold every OTHER brainer's evidence into the target's ledger BEFORE it
      // finalizes, so a losing branch's findings still reach the report instead of vanishing with it.
      this.mergeChildClaims(
        target,
        this.liveBrainers.filter((b) => b !== target),
      );
      if (winner) await this.finalizeWinner(winner);
      else await this.runFinalize(root); // no gate ever passed → full finalize on the root
      for (const bs of this.liveBrainers) if (bs !== (winner || root)) this.writeLoserResult(bs);
      this.writeBrainerTree();
      result = this.buildResult(winner || root);
    }
    if (CONFIG.debug)
      this.files['_debug.md'] = await runDebug(this, this.winner || root, result.metrics); // opt-in Debug & Analysis agent → _debug.md
    return result;
  }
}

// ── entry — the Workflow harness wraps this file in an async scope and awaits its return ──
const rr = new ResearchReport()
return await rr.run()
