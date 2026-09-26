import { describe, it, expect } from 'vitest';
import { Configs, CONFIG } from '../src/config.js';

describe('Configs validation', () => {
  it('throws on a missing query', () => expect(() => new Configs({})).toThrow(/non-empty string/));
  it('throws on an empty / whitespace query', () =>
    expect(() => new Configs({ query: '   ' })).toThrow(/non-empty string/));
  it('throws on a non-object args', () => {
    expect(() => new Configs(42)).toThrow(/JSON object/);
    expect(() => new Configs([])).toThrow(/JSON object/);
    expect(() => new Configs(null)).toThrow(/JSON object/);
  });
  it('throws on invalid JSON string args', () =>
    expect(() => new Configs('{not json')).toThrow(/not valid JSON/));
  it('parses a JSON string args', () =>
    expect(new Configs(JSON.stringify({ query: 'x' })).query).toBe('x'));
});

describe('Configs canonicalization', () => {
  it('canonicalizes mode: anything ≠ collect → goal', () => {
    expect(new Configs({ query: 'q', mode: 'collect' }).mode).toBe('collect');
    expect(new Configs({ query: 'q', mode: 'goal' }).mode).toBe('goal');
    expect(new Configs({ query: 'q', mode: 'weird' }).mode).toBe('goal');
    expect(new Configs({ query: 'q' }).mode).toBe('goal');
  });
  it('clamps maxWave to [5,15] or keeps "auto"', () => {
    expect(new Configs({ query: 'q' }).maxWave).toBe('auto');
    expect(new Configs({ query: 'q', maxWave: 'auto' }).maxWave).toBe('auto');
    expect(new Configs({ query: 'q', maxWave: 3 }).maxWave).toBe(5);
    expect(new Configs({ query: 'q', maxWave: 10 }).maxWave).toBe(10);
    expect(new Configs({ query: 'q', maxWave: 99 }).maxWave).toBe(15);
    expect(new Configs({ query: 'q', maxWave: 'nope' }).maxWave).toBe('auto');
  });
  it('clamps the lane + source knobs to [1,5] or keeps "auto"', () => {
    expect(new Configs({ query: 'q' }).parallelLaneResearchAgentsPerWave).toBe('auto');
    expect(
      new Configs({ query: 'q', parallelLaneResearchAgentsPerWave: 9 })
        .parallelLaneResearchAgentsPerWave,
    ).toBe(5);
    expect(
      new Configs({ query: 'q', parallelSourcesPerLaneResearchAgent: 0 })
        .parallelSourcesPerLaneResearchAgent,
    ).toBe('auto');
    expect(
      new Configs({ query: 'q', parallelSourcesPerLaneResearchAgent: 3 })
        .parallelSourcesPerLaneResearchAgent,
    ).toBe(3);
  });
  it('derives slug / tag / DIR', () => {
    const c = new Configs({ query: 'Hello, World!' });
    expect(c.slug).toBe('hello-world');
    expect(c.DIR).toBe('RR/hello-world');
    const t = new Configs({ query: 'Hello, World!', tag: 'v2' });
    expect(t.slug).toBe('hello-world-v2');
    expect(t.DIR).toBe('RR/hello-world-v2');
  });
  it('reads booleans + strings with type-guarded defaults', () => {
    expect(new Configs({ query: 'q' }).debug).toBe(true); // debug is ON by default
    expect(new Configs({ query: 'q', debug: false }).debug).toBe(false); // only debug:false turns it off
    expect(new Configs({ query: 'q', debug: true }).debug).toBe(true);
    expect(new Configs({ query: 'q', debug: 'yes' }).debug).toBe(true); // wrong type → default (true)
    expect(new Configs({ query: 'q', debugPrompt: 'why?' }).debugPrompt).toBe('why?');
    expect(new Configs({ query: 'q' }).debugPrompt).toBe('');
  });
  it('reads computeNote and folds it into COMPUTE_NOTE', () => {
    expect(new Configs({ query: 'q', computeNote: 'use sympy' }).computeNote).toBe('use sympy');
    expect(new Configs({ query: 'q' }).computeNote).toBe('');
    expect(new Configs({ query: 'q', computeNote: 'use sympy' }).COMPUTE_NOTE).toMatch(/use sympy/);
  });
  it('captures the COMPLETE launch args verbatim on rawArgs', () => {
    const a = { query: 'q', mode: 'collect', tag: 'v2', computerNote: 'use scipy', debug: false };
    expect(new Configs(a).rawArgs).toEqual(a);
  });
  it('exposes the run-wide constants', () => {
    const c = new Configs({ query: 'q' });
    expect(c.HARD_CAP).toBe(15);
    expect(c.QUERY_PLATEAU).toBe(0.7);
    expect(c.INJECT_SCORE).toBe(90);
    expect(c.AGENT_RETRIES).toBe(2);
    expect(c.MAX_JUDGE_PASSES).toBe(2); // finalize: max remediation passes the judge may drive (judge runs ≤ MAX+1)
    expect(c.MAX_LANE_REFAILS).toBe(2); // crawl: max validator re-opens of one lane before it becomes a known gap
    expect(c.VALIDATOR_THIN).toBe(120); // crawl: finding length under which the validator gate fires
    expect(c.PHASE.crawl).toBe('Research');
  });
  it('centralizes the scattered caps + char budgets (single source of truth)', () => {
    const c = new Configs({ query: 'q' });
    expect(c.AUTO_CAP).toBe(5); // the auto-mode lanes/wave + sources/lane cap
    expect(c.AUTO_SOURCE_DEFAULT).toBe(2);
    expect(c.NEAR_DUP).toBe(0.85);
    expect(c.FINALIZE_TOP_OPEN).toBe(6);
    expect(c.VALIDATOR_INTRO_CHARS).toBe(240);
    expect(c.VALIDATOR_MISSING_CHARS).toBe(300);
    expect(c.PLATEAU_MIN_WAVES).toBe(3);
    expect(c.PLATEAU_WINDOW).toBe(2);
    expect(c.GENERAL_PURPOSE).toBe('general-purpose');
  });
  it('defines the forward knobs for the scheduler/researcher redesign', () => {
    const c = new Configs({ query: 'q' });
    expect(c.RESEARCHER_TOKEN_BUDGET).toBe(130000);
    expect(c.BRAINER_LANE_CAP).toBe(5);
    expect(c.CHUNK_OVERLAP_CHARS).toBe(2000);
  });
  it('anchors the reader budget + holds the scheduler/reader/starvation caps (B7/B10)', () => {
    const c = new Configs({ query: 'q' });
    expect(c.CHARS_PER_TOKEN).toBe(2);
    expect(c.MAX_SLICES_PER_READER).toBe(8);
    expect(c.MAX_SOURCES_PER_LANE).toBe(12);
    expect(c.HANDOFF_CHARS).toBe(16000);
    expect(c.MAX_STARVED_WAVES).toBe(2);
    // B10 — the budget fits the researcher tier's context window, and (in chars) exceeds the overlap re-read
    expect(c.RESEARCHER_TOKEN_BUDGET).toBeLessThanOrEqual(c.CONTEXT[c.TIER.researcher]);
    expect(c.RESEARCHER_TOKEN_BUDGET * c.CHARS_PER_TOKEN).toBeGreaterThan(c.CHUNK_OVERLAP_CHARS);
  });
  it('coerces the compute arg STRICTLY — falsy strings/numbers never default to true (B8)', () => {
    expect(new Configs({ query: 'q' }).compute).toBe(true); // unset → default true
    expect(new Configs({ query: 'q', compute: false }).compute).toBe(false);
    expect(new Configs({ query: 'q', compute: 'false' }).compute).toBe(false);
    expect(new Configs({ query: 'q', compute: 'no' }).compute).toBe(false);
    expect(new Configs({ query: 'q', compute: 0 }).compute).toBe(false);
    expect(new Configs({ query: 'q', compute: 'true' }).compute).toBe(true);
    expect(new Configs({ query: 'q', compute: 1 }).compute).toBe(true);
    expect(() => new Configs({ query: 'q', compute: 'maybe' })).toThrow(/boolean/);
    expect(() => new Configs({ query: 'q', compute: 2 })).toThrow(/boolean/);
  });
  it('coerces the checkpoint arg STRICTLY, defaulting true (mirrors compute)', () => {
    expect(new Configs({ query: 'q' }).checkpoint).toBe(true); // unset → default true
    expect(new Configs({ query: 'q', checkpoint: false }).checkpoint).toBe(false);
    expect(new Configs({ query: 'q', checkpoint: 'no' }).checkpoint).toBe(false);
    expect(new Configs({ query: 'q', checkpoint: 1 }).checkpoint).toBe(true);
    expect(() => new Configs({ query: 'q', checkpoint: 'maybe' })).toThrow(/boolean/);
  });
  it('holds the CHECKPOINT_MARK log-line prefix', () => {
    expect(new Configs({ query: 'q' }).CHECKPOINT_MARK).toBe('⏺CKPT');
  });
  it('holds the claim-ledger knobs (v3)', () => {
    const c = new Configs({ query: 'q' });
    expect(c.QUOTE_MAX_CHARS).toBe(300);
    expect(c.CLAIM_DIGEST_CAP).toBe(30);
    expect(c.CLAIM_DIGEST_CLIP).toBe(90);
    expect(c.CALIB_DEFAULT_SCORE).toBe(50);
    expect(c.AUDIT_BATCH).toBe(50);
    expect(c.LINEAGE_BATCH).toBe(80);
    expect(c.SETTLED_MIN_CLUSTERS).toBe(2);
    expect(c.VOI_SENS_THRESHOLD).toBe(0.15);
    expect(c.CALIB_CLAMP_LO).toBe(0.5);
    expect(c.CALIB_CLAMP_HI).toBe(1.5);
    expect(c.CALIB_NORM).toBe(4);
    expect(c.CALIB_ALPHA).toBe(0.3);
    expect(c.CALIB_LEAD_WEIGHT).toBe(0.3);
    expect(c.CALIB_REALIZED_MAX).toBe(2);
    expect(c.CHAO_COVERAGE_STOP).toBe(0.9);
  });
  it('holds the v3 STEERING knobs (batch 3)', () => {
    const c = new Configs({ query: 'q' });
    expect(c.BRAINER_LEDGER_CAP).toBe(120);
    expect(c.CLAIM_LINE_CLIP).toBe(120);
    expect(c.SENSITIVITY_CLIP).toBe(60);
    expect(c.TREE_ANSWER_CLIP).toBe(400);
    expect(c.MANDATE_CLIP).toBe(60);
    expect(c.SCHED_VOCAB_CAP).toBe(20);
  });
  it('holds the v3 FINALIZE knob (batch 4)', () => {
    const c = new Configs({ query: 'q' });
    expect(c.VENUE_WARN_MIN).toBe(2);
  });
  it('tiers the v3 ledger clerks (claimAuditor/lineageClerk/rerunner)', () => {
    const c = new Configs({ query: 'q' });
    expect(c.TIER.claimAuditor).toBe('haiku');
    // lineageClerk alone is promoted to sonnet (finding J) — fuzzy entity resolution against a growing
    // canon (same-as spellings, merges) is judgment, not grep.
    expect(c.TIER.lineageClerk).toBe('sonnet');
    expect(c.TIER.rerunner).toBe('haiku');
    expect(c.EFFORT.claimAuditor).toBe('medium');
    expect(c.EFFORT.lineageClerk).toBe('medium');
    expect(c.EFFORT.rerunner).toBe('low');
  });
  it('holds the scout SWARM knobs + tiers scoutPlanner/scoutMerger (v3 batch 2s)', () => {
    const c = new Configs({ query: 'q' });
    expect(c.SCOUT_PROBES).toBe(5);
    expect(c.SCOUT_PROBE_SOURCES).toBe(3);
    expect(c.SCOUT_PAGES_CAP).toBe(10);
    expect(c.TIER.scout).toBe('haiku'); // the probe — unchanged from v2
    expect(c.TIER.scoutPlanner).toBe('sonnet');
    expect(c.TIER.scoutMerger).toBe('sonnet');
    expect(c.EFFORT.scout).toBe('medium');
    expect(c.EFFORT.scoutPlanner).toBe('high');
    expect(c.EFFORT.scoutMerger).toBe('high');
  });
  it('builds the 6-channel FOOTER (gaps + attacks, stance-tagged sources, quote-pinned claims, terms, surprise)', () => {
    const c = new Configs({ query: 'q' });
    expect(c.FOOTER).toContain('"Rabbit holes"');
    expect(c.FOOTER).toContain("SOURCE'S OWN TERMINOLOGY");
    expect(c.FOOTER).toContain('counter-evidence search');
    expect(c.FOOTER).toContain('kind:"entity"'); // entity leads — a recurring author/venue/dataset worth following
    expect(c.FOOTER).toContain('"Next sources"');
    expect(c.FOOTER).toContain('SUPPORT or ATTACK');
    expect(c.FOOTER).toContain('"Claims"');
    expect(c.FOOTER).toContain('VERBATIM quote of at most ' + c.QUOTE_MAX_CHARS + ' characters');
    expect(c.FOOTER).toContain('"New terms"');
    expect(c.FOOTER).toContain('"Surprise" note ONLY when the page contradicts');
    expect(c.FOOTER).toMatch(/do not pad/);
  });
  it('normalizes mode case-insensitively + trims, unrecognized → goal (B8)', () => {
    expect(new Configs({ query: 'q', mode: 'COLLECT' }).mode).toBe('collect');
    expect(new Configs({ query: 'q', mode: '  Collect ' }).mode).toBe('collect');
    expect(new Configs({ query: 'q', mode: 'GOAL' }).mode).toBe('goal');
    expect(new Configs({ query: 'q', mode: 'weird' }).mode).toBe('goal'); // unrecognized → goal (warns, never throws)
  });
  it('holds the central TIER + EFFORT maps (incl. the forthcoming researchScheduler)', () => {
    const c = new Configs({ query: 'q' });
    expect(c.TIER.brainer).toBe('opus');
    expect(c.TIER.scout).toBe('haiku');
    expect(c.TIER.researchScheduler).toBe('sonnet');
    expect(c.EFFORT.brainer).toBe('xhigh');
    expect(c.EFFORT.scout).toBe('medium');
    expect(c.EFFORT.researchScheduler).toBe('high');
  });
});

describe('CONFIG singleton', () => {
  it('is built from the ambient args (setup query, goal mode)', () => {
    expect(CONFIG.mode).toBe('goal');
    expect(typeof CONFIG.query).toBe('string');
    expect(CONFIG.RUBRIC).toMatch(/MODE = goal/);
    expect(CONFIG.STOP).toMatch(/goal is answered/);
  });
});

describe('Configs mode-dependent prompt fragments', () => {
  it('builds the collect RUBRIC + STOP', () => {
    const c = new Configs({ query: 'q', mode: 'collect' });
    expect(c.RUBRIC).toMatch(/MODE = collect/);
    expect(c.STOP).toMatch(/novelty trajectory/);
  });
});

describe('Configs — per-seat `agents` override (model/effort)', () => {
  it('applies no overrides when agents is absent, null, or undefined', () => {
    expect(new Configs({ query: 'q' }).TIER.researcher).toBe('haiku');
    expect(new Configs({ query: 'q', agents: null }).TIER.researcher).toBe('haiku');
    expect(new Configs({ query: 'q', agents: undefined }).TIER.researcher).toBe('haiku');
  });
  it('overrides exactly the named seat/field, leaving every other seat at its default', () => {
    const c = new Configs({
      query: 'q',
      agents: { researcher: { model: 'sonnet' }, judge: { effort: 'high' } },
    });
    expect(c.TIER.researcher).toBe('sonnet');
    expect(c.EFFORT.judge).toBe('high');
    // untouched fields/seats keep their defaults
    expect(c.EFFORT.researcher).toBe('medium');
    expect(c.TIER.judge).toBe('opus');
    expect(c.TIER.scout).toBe('haiku');
    expect(c.EFFORT.scout).toBe('medium');
  });
  it('a seat override may set model and effort together', () => {
    const c = new Configs({ query: 'q', agents: { scout: { model: 'opus', effort: 'high' } } });
    expect(c.TIER.scout).toBe('opus');
    expect(c.EFFORT.scout).toBe('high');
  });
  it('throws on an unknown seat name, listing the valid seats', () => {
    expect(() => new Configs({ query: 'q', agents: { bogus: { model: 'opus' } } })).toThrow(
      /unknown agent seat "bogus"/,
    );
    expect(() => new Configs({ query: 'q', agents: { bogus: { model: 'opus' } } })).toThrow(
      /researcher/, // the thrown message names the valid seats
    );
  });
  it('throws on an invalid model value', () => {
    expect(() => new Configs({ query: 'q', agents: { researcher: { model: 'gpt4' } } })).toThrow(
      /model must be one of/,
    );
  });
  it('throws on an invalid effort value', () => {
    expect(() => new Configs({ query: 'q', agents: { researcher: { effort: 'ultra' } } })).toThrow(
      /effort must be one of/,
    );
  });
  it('throws when agents is not a plain object (string/array)', () => {
    expect(() => new Configs({ query: 'q', agents: 'nope' })).toThrow(/agents must be an object/);
    expect(() => new Configs({ query: 'q', agents: [] })).toThrow(/agents must be an object/);
  });
  it('throws when a seat override is not a plain object', () => {
    expect(() => new Configs({ query: 'q', agents: { researcher: 'sonnet' } })).toThrow(
      /agents\.researcher must be an object/,
    );
    expect(() => new Configs({ query: 'q', agents: { researcher: null } })).toThrow(
      /agents\.researcher must be an object/,
    );
  });
  it('warns loudly (never throws) when brainer.model is downgraded below opus', () => {
    const warnings: unknown[] = [];
    (globalThis as unknown as { log: (m?: unknown) => void }).log = (m) => warnings.push(m);
    try {
      const c = new Configs({ query: 'q', agents: { brainer: { model: 'sonnet' } } });
      expect(c.TIER.brainer).toBe('sonnet');
      expect(warnings.some((w) => typeof w === 'string' && /erratically/.test(w))).toBe(true);
    } finally {
      (globalThis as unknown as { log: () => void }).log = () => {};
    }
  });
  it('never warns when brainer keeps opus (default or explicit)', () => {
    const warnings: unknown[] = [];
    (globalThis as unknown as { log: (m?: unknown) => void }).log = (m) => warnings.push(m);
    try {
      new Configs({ query: 'q' });
      new Configs({ query: 'q', agents: { brainer: { model: 'opus' } } });
      expect(warnings.length).toBe(0);
    } finally {
      (globalThis as unknown as { log: () => void }).log = () => {};
    }
  });
});

describe('Configs — parallelSourcesPerLaneResearchAgent governs MAX_SOURCES_PER_LANE (B7)', () => {
  it('defaults MAX_SOURCES_PER_LANE to 12 when the knob is auto', () => {
    expect(new Configs({ query: 'q' }).MAX_SOURCES_PER_LANE).toBe(12);
  });
  it('a positive override governs MAX_SOURCES_PER_LANE directly', () => {
    const c = new Configs({ query: 'q', parallelSourcesPerLaneResearchAgent: 3 });
    expect(c.parallelSourcesPerLaneResearchAgent).toBe(3);
    expect(c.MAX_SOURCES_PER_LANE).toBe(3);
  });
  it('an out-of-range override clamps to [1,AUTO_CAP] before governing MAX_SOURCES_PER_LANE', () => {
    const c = new Configs({ query: 'q', parallelSourcesPerLaneResearchAgent: 99 });
    expect(c.parallelSourcesPerLaneResearchAgent).toBe(c.AUTO_CAP);
    expect(c.MAX_SOURCES_PER_LANE).toBe(c.AUTO_CAP);
  });
});
