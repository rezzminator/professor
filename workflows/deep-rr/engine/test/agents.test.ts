import { describe, it, expect, vi } from 'vitest';
import {
  scout,
  prospector,
  brainer,
  validator,
  researchScheduler,
  researcher,
  initiator,
  refiner,
  judge,
  synthesiser,
  debugAnalyst,
} from '../src/agents/index.js';
import { BrainerState } from '../src/brainerState.js';
import type {
  AgentOpts,
  Claim,
  RabbitHole,
  ReadSlice,
  SchedulerSource,
} from '../src/types/index.js';

// The model TIER + reasoning EFFORT live in the central config.js TIER/EFFORT maps; each agent object reads
// its value from CONFIG. This pins that policy (the engine reads <agent>.tier / <agent>.effort / <agent>.schema).
const AGENTS = {
  scout,
  prospector,
  brainer,
  validator,
  researchScheduler,
  researcher,
  initiator,
  refiner,
  judge,
  synthesiser,
  debugAnalyst,
};

describe('agents — shape', () => {
  it('every agent exposes tier + effort + schema + a buildPrompt function', () => {
    for (const [name, a] of Object.entries(AGENTS)) {
      expect(typeof a.tier, name).toBe('string');
      expect(['haiku', 'sonnet', 'opus'], name).toContain(a.tier);
      expect(['medium', 'high', 'xhigh'], name).toContain(a.effort);
      expect(a.schema, name).toBeTypeOf('object');
      expect(a.schema.type, name).toBe('object');
      expect(typeof a.buildPrompt, name).toBe('function');
    }
  });
});

describe('agents — tier policy (from config.TIER)', () => {
  it('workers are haiku; brainer/synthesis/adversarial are opus; refiner + validator are sonnet', () => {
    expect(scout.tier).toBe('haiku');
    expect(researcher.tier).toBe('haiku');
    expect(refiner.tier).toBe('sonnet');
    expect(validator.tier).toBe('sonnet');
    expect(researchScheduler.tier).toBe('sonnet');
    expect(prospector.tier).toBe('opus');
    expect(brainer.tier).toBe('opus');
    expect(initiator.tier).toBe('opus');
    expect(judge.tier).toBe('opus');
    expect(synthesiser.tier).toBe('opus');
    expect(debugAnalyst.tier).toBe('opus');
  });
});

describe('agents — effort policy (from config.EFFORT)', () => {
  it('workers medium; prospector/refiner/debug high; brainer/initiator/judge/synthesiser xhigh', () => {
    expect(scout.effort).toBe('medium');
    expect(researcher.effort).toBe('medium');
    expect(validator.effort).toBe('medium');
    expect(prospector.effort).toBe('high');
    expect(refiner.effort).toBe('high');
    expect(researchScheduler.effort).toBe('high');
    expect(debugAnalyst.effort).toBe('high');
    expect(brainer.effort).toBe('xhigh');
    expect(initiator.effort).toBe('xhigh');
    expect(judge.effort).toBe('xhigh');
    expect(synthesiser.effort).toBe('xhigh');
  });
});

// ─────────────────────────────────────────────────────────────────────────────
// v3 batch 2b — researcher/scout run-fn behavior. Descriptor/shape tests above are PURE (static
// imports); these dynamically reload the module with a stubbed harness `agent()` (mirrors
// ledgerClerks.test.ts's pattern), since researcher/run.ts + scout/run.ts read `agent`/`parallel`
// as ambient globals captured at MODULE LOAD.
type AgentStub = (prompt: string, opts: AgentOpts) => unknown;
const DEFAULT_PARALLEL = async <T>(thunks: Array<() => Promise<T>>): Promise<T[]> =>
  Promise.all(thunks.map((t) => t()));

// counting stub factory — records every {prompt, opts} the fake harness `agent()` receives, so the
// run-fn tests can assert call COUNT + the exact rendered PROMPT (a spy) without any real agent I/O.
function countingStub(impl: AgentStub) {
  const calls: { prompt: string; opts: AgentOpts }[] = [];
  const stub: AgentStub = (prompt, opts) => {
    calls.push({ prompt, opts });
    return impl(prompt, opts);
  };
  return { stub, calls };
}

async function loadResearcherRun(agent: AgentStub, parallel = DEFAULT_PARALLEL) {
  globalThis.args = { query: 'best vector database for production RAG at scale', mode: 'goal' };
  globalThis.agent = async (p: string, o: AgentOpts) => agent(p, o);
  globalThis.log = () => {};
  globalThis.phase = () => {};
  globalThis.parallel = parallel;
  vi.resetModules();
  return import('../src/agents/researcher/run.js');
}
const mkPick = (over: Partial<RabbitHole> = {}): RabbitHole => ({
  id: 1,
  keyword: 'x',
  why: 'w',
  score: 80,
  scoreHistory: [],
  path: [],
  ...over,
});
// a minimal valid Claim with per-call overrides (mirrors ledgerClerks.test.ts's fixture).
const mkClaim = (id: number, over: Partial<Claim> = {}): Claim => ({
  id,
  claim: 'claim ' + id,
  quote: 'quote ' + id,
  source: 'https://example.com/' + id,
  cluster: 0,
  audit: 'pending',
  status: 'tentative',
  attacksSurvived: 0,
  retracted: false,
  wave: 1,
  lane: 'lane',
  ...over,
});

describe('runLaneThread — accumulates claims/newTerms across readers, keeps the FIRST non-empty surprise', () => {
  it('concatenates claims + newTerms from every reader; a later contradiction never overwrites the first', async () => {
    let call = 0;
    const { stub } = countingStub(() => {
      call++;
      return call === 1
        ? {
            runningAnswer: 'a1',
            claims: [{ claim: 'c1', quote: 'q1', source: 's1' }],
            newTerms: [{ term: 't1' }],
            surprise: 'first contradiction',
          }
        : {
            runningAnswer: 'a2',
            claims: [{ claim: 'c2', quote: 'q2', source: 's2' }],
            newTerms: [{ term: 't2' }],
            surprise: 'second contradiction',
          };
    });
    const mod = await loadResearcherRun(stub);
    const readers: ReadSlice[][] = [
      [{ source: 'https://a.com', cachePath: '/cache/a.txt', offset: 0, limit: 100 }],
      [{ source: 'https://b.com', cachePath: '/cache/b.txt', offset: 0, limit: 100 }],
    ];
    const out = await mod.runLaneThread(mkPick(), readers, 'w1', 'Research', '');
    expect(out).not.toBeNull();
    expect(out!.claims).toEqual([
      { claim: 'c1', quote: 'q1', source: 's1' },
      { claim: 'c2', quote: 'q2', source: 's2' },
    ]);
    expect(out!.newTerms).toEqual([{ term: 't1' }, { term: 't2' }]);
    expect(out!.surprise).toBe('first contradiction'); // NOT 'second contradiction'
  });
});

describe('runResearchers — threads claimDigest (from bs.claims) + laneKind (from each pick.kind) into every reader prompt', () => {
  it('every lane gets the same KEY CLAIMS digest; only the attack-kind lane gets the ATTACK clause', async () => {
    const { stub, calls } = countingStub(() => ({
      runningAnswer: 'ans',
      claims: [],
      newTerms: [],
    }));
    const mod = await loadResearcherRun(stub);
    const bs = new BrainerState(
      { scout: null, scoutRabbitHoles: [], highValueSources: [], languageGuidance: '' },
      { name: 'root', parentName: null, mandate: '', trail: '', depth: 0 },
    );
    bs.claims = [mkClaim(1, { claim: 'pgvector recall is 0.98' })];
    const picks: RabbitHole[] = [
      mkPick({ id: 1, keyword: 'attack-lane', kind: 'attack' }),
      mkPick({ id: 2, keyword: 'gap-lane', kind: 'gap' }),
    ];
    const schedule = new Map<number, SchedulerSource[]>([
      [1, [{ source: 'https://a.com', path: '/cache/a.txt', size: 100, chars: 200 }]],
      [2, [{ source: 'https://b.com', path: '/cache/b.txt', size: 100, chars: 200 }]],
    ]);
    await mod.runResearchers(bs, picks, schedule, 'w1', 'Research');
    expect(calls.length).toBe(2);
    const attackCall = calls.find((c) => c.prompt.includes('attack-lane'))!;
    const gapCall = calls.find((c) => c.prompt.includes('gap-lane'))!;
    expect(attackCall.prompt).toContain('KEY CLAIMS SO FAR');
    expect(attackCall.prompt).toContain('pgvector recall is 0.98');
    expect(attackCall.prompt).toContain('ATTACK lane');
    expect(gapCall.prompt).toContain('KEY CLAIMS SO FAR'); // the same wave-level digest reaches every lane
    expect(gapCall.prompt).not.toContain('ATTACK lane'); // only the attack-kind lane gets the clause
  });
});

// runScout coverage (the wave-0 scout SWARM: planner → probes → merger) lives in scout.test.ts —
// its dynamic-reload harness needs 3 distinct stub labels ('scout-planner', 'scout-probe:*', 'scout-merger'),
// unlike the single-call pattern the rest of this file's run-fn tests share.

// ─────────────────────────────────────────────────────────────────────────────
// `agents` arg (per-seat model/effort override) — every agents/<name>/index.ts reads CONFIG.TIER.<seat> /
// CONFIG.EFFORT.<seat> at MODULE-EVAL time, so exercising an override needs a fresh CONFIG: set
// globalThis.args, vi.resetModules(), then a fresh dynamic import of the agents barrel (mirrors
// loadResearcherRun/loadClaimAuditor above). The pure top-of-file AGENTS policy table above is built from
// the STATIC import (test/setup.ts's default args, no override) and stays the pinned default expectation.
async function loadAgents(args: unknown, log: (m?: unknown) => void = () => {}) {
  globalThis.args = args;
  globalThis.log = log;
  globalThis.agent = async () => ({});
  globalThis.parallel = DEFAULT_PARALLEL;
  globalThis.phase = () => {};
  vi.resetModules();
  return import('../src/agents/index.js');
}

describe('agents — `agents` arg overrides propagate through CONFIG into the Agent objects', () => {
  it('defaults are unchanged with no override (pins the existing policy table)', async () => {
    const mod = await loadAgents({ query: 'q' });
    expect(mod.scout.tier).toBe('haiku');
    expect(mod.researcher.tier).toBe('haiku');
    expect(mod.brainer.tier).toBe('opus');
    expect(mod.brainer.effort).toBe('xhigh');
    expect(mod.judge.tier).toBe('opus');
    expect(mod.judge.effort).toBe('xhigh');
  });

  it('an override retunes exactly the named seat/field, leaving the rest at their defaults', async () => {
    const mod = await loadAgents({
      query: 'q',
      agents: { researcher: { model: 'sonnet' }, judge: { effort: 'high' } },
    });
    expect(mod.researcher.tier).toBe('sonnet');
    expect(mod.judge.effort).toBe('high');
    expect(mod.scout.tier).toBe('haiku'); // untouched seat
    expect(mod.judge.tier).toBe('opus'); // untouched field of the same overridden seat
  });

  it('rejects (module evaluation throws) on an unknown seat / bad model / bad effort', async () => {
    await expect(loadAgents({ query: 'q', agents: { bogus: { model: 'opus' } } })).rejects.toThrow(
      /unknown agent seat "bogus"/,
    );
    await expect(
      loadAgents({ query: 'q', agents: { researcher: { model: 'gpt4' } } }),
    ).rejects.toThrow(/model must be one of/);
    await expect(
      loadAgents({ query: 'q', agents: { researcher: { effort: 'ultra' } } }),
    ).rejects.toThrow(/effort must be one of/);
  });

  it('warns loudly when brainer.model is downgraded below opus, and still applies the override', async () => {
    const warnings: unknown[] = [];
    const mod = await loadAgents({ query: 'q', agents: { brainer: { model: 'haiku' } } }, (m) =>
      warnings.push(m),
    );
    expect(mod.brainer.tier).toBe('haiku');
    expect(warnings.some((w) => typeof w === 'string' && /erratically/.test(w))).toBe(true);
  });
});
