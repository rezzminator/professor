import { describe, it, expect, vi } from 'vitest';
import { BrainerState } from '../src/brainerState.js';
import { CONFIG } from '../src/config.js';
import type { AgentOpts, Claim, JudgeOut, RunResult } from '../src/types/index.js';
type AgentStub = (prompt: string, opts: AgentOpts) => unknown;

// ── shared canned StructuredOutputs (the engine reads specific fields) ────────
const RSF = {
  answer: 'pgvector for most, Milvus at huge scale',
  confidence: 'medium',
  working: 'cost = nodes * price',
  evidence: [{ fact: 'recall', value: '0.98', source: 'arxiv', status: 'settled' }],
  keyClaimIds: [] as number[], // v3: the ledger ids the answer rests on (empty in these canned fixtures — no ledger claims)
  resolved: ['index types'],
  openGaps: ['multi-tenant isolation'],
  tensions: [],
};
const SCOUT_OUT = {
  landscape: 'the landscape',
  pages: [
    {
      url: 'https://a.com',
      summary: 'page a',
      rabbitHoles: [
        { keyword: 'hnsw tuning', why: 'knobs' },
        { keyword: 'sharding', why: 'scale' },
      ],
    },
  ],
  deadEnds: [],
};
const PROSPECT_OUT = {
  highValueSources: [
    { source: 'arXiv (site:arxiv.org)', goodFor: 'ANN' },
    { source: 'SemiAnalysis', goodFor: 'cost' },
  ],
  reasoning: 'searched',
};
// a lane READER's output (the new researcher shape): the accumulated running answer + the leads it surfaced.
const LANE_OUT = {
  runningAnswer: 'found knobs',
  rabbitHoles: [{ keyword: 'ef tuning', why: 'recall' }],
  deadEnds: [],
};
// the per-wave validator's neutral verdict — nothing failed, nothing to reopen.
const VALIDATE_OUT = { checks: [], enough: true, missing: [] };
// the SCHEDULER stub — echoes one sized source per lane id it sees in the prompt (each lane renders `#id`),
// so every pursued lane bin-packs to exactly ONE reader-unit (size 1000 ≤ budget). Keeps reader labels at `-r1of1`.
function schedulerStub(prompt: string) {
  const ids = [...new Set([...prompt.matchAll(/#(\d+)/g)].map((m) => Number(m[1])))];
  return {
    lanes: ids.map((id) => ({
      id,
      sources: [
        {
          source: 'https://s' + id + '.com',
          path: '/cache/' + id + '.txt',
          size: 1000,
          chars: 2000,
        },
      ],
    })),
  };
}

// load the engine fresh with the given ambient args + agent (config/CONFIG are built at import).
async function loadEngine(
  args: unknown,
  agent: AgentStub,
  {
    parallel,
    pipeline,
  }: { parallel?: typeof globalThis.parallel; pipeline?: typeof globalThis.pipeline } = {},
) {
  globalThis.args = args;
  globalThis.agent = async (p: string, o: AgentOpts) => agent(p, o);
  globalThis.phase = () => {};
  globalThis.log = () => {};
  globalThis.parallel =
    parallel ||
    (async <T>(thunks: Array<() => Promise<T>>): Promise<T[]> =>
      Promise.all(thunks.map((t) => t())));
  globalThis.pipeline =
    pipeline ||
    (async (
      items: unknown[],
      s1: (item: unknown, i: number) => Promise<unknown>,
      s2: (a: unknown, item: unknown, i: number) => Promise<unknown>,
    ): Promise<unknown[]> => {
      const out: unknown[] = [];
      for (let i = 0; i < items.length; i++) {
        const a = await s1(items[i], i);
        out.push(await s2(a, items[i], i));
      }
      return out;
    });
  vi.resetModules();
  const mod = await import('../src/engine.js');
  return mod.ResearchReport;
}
const keys = (r: RunResult) => Object.keys(r.files);

// ── (a) GOAL mode — compute ON, brainer-driven stop, finalize brain-compute ──
function goalAgent(prompt: string, opts: AgentOpts) {
  const L = opts.label;
  if (L === 'scout-probe:direct') return SCOUT_OUT;
  if (L === 'scout-merger') return null; // fallback-B mechanical merge (pure passthrough of the lone survivor)
  if (L === 'prospector') return PROSPECT_OUT;
  if (L === 'brainer-w0')
    return {
      resultSoFar: RSF,
      rescore: [
        { id: 1, score: 80 },
        { id: 2, score: 50 },
      ],
      add: [],
      lookupNext: [{ id: 1, sources: ['arXiv (site:arxiv.org)'] }],
      rename: [],
      drop: [],
      stop: { done: false, reason: 'scoring seeds' },
    };
  if (L === 'brainer-w1')
    return {
      resultSoFar: RSF,
      rescore: [],
      add: [],
      lookupNext: [],
      rename: [],
      drop: [],
      stop: { done: true, reason: 'goal answered' },
    };
  if (L.startsWith('scheduler-')) return schedulerStub(prompt);
  if (L.startsWith('validator-')) return VALIDATE_OUT;
  if (L.startsWith('lane-')) return LANE_OUT;
  if (L === 'initiator')
    return {
      refinement: { facts: [{ fact: 'recall is 0.98', why: 'headline' }] },
      synthesiser: { focus: 'lead with cost' },
    };
  if (L.startsWith('refine-'))
    return { report: 'refined: 0.96 ± 0.02 (verified)', queriesTried: ['q1'], counterFound: false };
  // judge pass 0 → needs a derivation (routes to brain-compute); pass 1 → derivation now sound → exit
  if (L === 'judge-0')
    return {
      goalMet: true,
      verificationSound: true,
      needsCompute: true,
      computeSound: false,
      reasoning: 'the answer needs a blended cost/query derivation',
      directive: 'derive blended cost/query with error bars',
      reopenRabbitHoles: [],
    };
  if (L.startsWith('judge-'))
    return {
      goalMet: true,
      verificationSound: true,
      needsCompute: true,
      computeSound: true,
      reasoning: 'derivation is now sound',
      directive: '',
    };
  if (L.startsWith('brain-compute-'))
    return { resultSoFar: { ...RSF, working: 'blended cost = $0.0003/q ± 10%' } };
  if (L === 'synthesiser')
    return {
      report: '# Report\n\nbody',
      verdict: 'pgvector wins',
      confidence: 'high',
      plan: ['use pgvector'],
      openQuestions: ['multi-tenant'],
    };
  if (L === 'debug-analyst') return { diagnosis: '# Debug\n\nall good' };
  throw new Error('goalAgent: unexpected label ' + L);
}

describe('ResearchReport.run — goal mode (compute on, brainer-driven stop)', () => {
  // 30s: the full mocked pipeline sits at the 5s default's edge and flakes under host load
  it('completes the full pipeline and writes the expected files', { timeout: 30000 }, async () => {
    const RR = await loadEngine(
      { query: 'best vector database for production RAG at scale', mode: 'goal' },
      goalAgent,
    );
    const result = await new RR().run();

    expect(result.stopReason).toBe('brainer-done');
    expect(result.done).toBe(true);
    expect(result.metrics.mode).toBe('goal');
    expect(result.metrics.reportWritten).toBe(true);
    expect(result.verdict).toBe('pgvector wins');
    // the synthesiser stated 'high', but the fixture's RSF carries no keyClaimIds → computed confidence is
    // 'medium' (v3 batch 4 confidence floor: final = min(stated, computed), never raised) — floored down.
    expect(result.confidence).toBe('medium');

    expect(result.files['result.md']).toContain('# Report\n\nbody');
    expect(result.files['result.md']).toContain('Confidence adjusted from high to medium');
    expect(keys(result)).toContain('01-scout.md');
    expect(keys(result)).toContain('02-prospector.md');
    expect(keys(result)).toContain('03-wave-0.md');
    expect(keys(result).some((k) => k.endsWith('-initiator.md'))).toBe(true);
    expect(keys(result).some((k) => k.endsWith('-refinement.md'))).toBe(true);
    expect(keys(result).some((k) => k.endsWith('-judge.md'))).toBe(true);
    // the judge is now the sole terminal skeptic — no crawl-phase sentinel file
    expect(keys(result).some((k) => k.endsWith('-sentinel.md'))).toBe(false);
    // the judge routed needsCompute → the brain derived the answer (folded into resultSoFar.working)
    expect(keys(result)).toContain('_finalize-compute.md');
    expect(result.files['_finalize-compute.md']).toContain('blended cost = $0.0003/q ± 10%');
    expect(keys(result)).toContain('_rabbitHoles.json');
    expect(keys(result)).toContain('_tree.md');
    // the COMPLETE launch args are persisted — surfaced at the top of result.md + as the `args` object in _rabbitHoles.json
    expect(result.files['result.md']).toContain('Run arguments');
    expect(JSON.parse(result.files['_rabbitHoles.json']).args).toEqual({
      query: 'best vector database for production RAG at scale',
      mode: 'goal',
    });
  });
});

// ── (b) COLLECT mode — the dry-plateau stop ─────────────────────────────────
function collectAgent(prompt: string, opts: AgentOpts) {
  const L = opts.label;
  if (L === 'scout-probe:direct') return SCOUT_OUT;
  if (L === 'scout-merger') return null; // fallback-B mechanical merge (pure passthrough of the lone survivor)
  if (L === 'prospector') return PROSPECT_OUT;
  if (L.startsWith('brainer-w')) {
    const w = Number(L.slice('brainer-w'.length));
    // research-wave peak at w1=100, then plateau at 50 (≤ 0.7×peak) for w2,w3 → dry. The wave-0 SEED score (100)
    // is EXCLUDED from the plateau math (B9), so the plateau is judged on the research waves' own trajectory.
    const score = w <= 1 ? 100 : 50;
    return {
      resultSoFar: RSF,
      rescore: [],
      add: [],
      lookupNext: [{ keyword: 'collect lead ' + w, why: 'breadth', score }],
      rename: [],
      drop: [],
      stop: { done: false, reason: 'still collecting' },
    };
  }
  if (L.startsWith('scheduler-')) return schedulerStub(prompt);
  if (L.startsWith('validator-')) return VALIDATE_OUT;
  // v3 CHAO STOP ASSIST: each research-wave lane lands the SAME recurring finding from a DISTINCT source —
  // chao1 groups them as one multiply-corroborated species (never a singleton), so coverage reaches 1.0 by
  // the time the plateau check fires, letting the collect dry-stop through the new coverage gate.
  if (L.startsWith('lane-'))
    return {
      ...LANE_OUT,
      claims: [
        {
          claim: 'the recurring landscape fact',
          quote:
            'the recurring landscape fact keeps turning up across independently examined sources',
          source: 'https://source.example.com/' + L,
        },
      ],
    };
  if (L === 'initiator')
    return {
      refinement: { facts: [] },
      synthesiser: { focus: '' },
    };
  if (L.startsWith('judge-'))
    return {
      goalMet: true,
      verificationSound: true,
      needsCompute: false,
      computeSound: true,
      reasoning: 'inventory complete',
      directive: '',
    };
  if (L === 'synthesiser')
    return {
      report: '# Inventory\n\nbody',
      verdict: 'a broad landscape',
      confidence: 'medium',
      plan: [],
      openQuestions: [],
    };
  throw new Error('collectAgent: unexpected label ' + L);
}

describe('ResearchReport.run — collect mode (dry plateau)', () => {
  // 30s: same full-pipeline shape as the goal-mode run above — flakes at the 5s default under load
  it('stops on the collect dry-plateau and writes a report with no compute', { timeout: 30000 }, async () => {
    const RR = await loadEngine(
      { query: 'survey the vector-db landscape', mode: 'collect' },
      collectAgent,
    );
    const result = await new RR().run();
    expect(result.stopReason).toBe('collect-dry-plateau');
    expect(result.metrics.mode).toBe('collect');
    expect(result.files['result.md']).toContain('# Inventory\n\nbody');
    // the judge upheld first pass → no derivation; refinement file records "no facts to harden"
    expect(keys(result)).not.toContain('_finalize-compute.md');
    expect(keys(result).some((k) => k.endsWith('-judge.md'))).toBe(true);
    expect(keys(result).some((k) => k.endsWith('-refinement.md'))).toBe(true);
  });
});

// ── (c) GOAL mode + debug:true — exercises runDebug + IO capture ─────────────
describe('ResearchReport.run — debug:true', () => {
  it('writes _debug.md with the analyst narrative + raw I/O', async () => {
    const RR = await loadEngine(
      {
        query: 'best vector database for production RAG at scale',
        mode: 'goal',
        debug: true,
        debugPrompt: 'why did wave 2 stall?',
      },
      goalAgent,
    );
    const result = await new RR().run();
    expect(keys(result)).toContain('_debug.md');
    const dbg = result.files['_debug.md'];
    expect(dbg).toContain('Analysis (debug-analyst');
    expect(dbg).toContain('Raw agent I/O');
    expect(dbg).toContain('Debug prompt:');
    expect(dbg).toContain('Run log');
    // checkpoint:true is the default (not overridden above), so wave 1 emitted a ⏺CKPT log line — but
    // it's recovery output, not debug narrative, so runtime.ts's LOG_BUFFER filter must keep it OUT of
    // the Run log section entirely.
    expect(dbg).not.toContain(CONFIG.CHECKPOINT_MARK);
  });
});

// ── (d) degraded — null prospector / lane / refine / synthesiser ─────
function degradedAgent(prompt: string, opts: AgentOpts) {
  const L = opts.label;
  if (L === 'scout-probe:direct') return SCOUT_OUT;
  if (L === 'scout-merger') return null; // fallback-B mechanical merge (pure passthrough of the lone survivor)
  if (L === 'prospector') return null;
  if (L === 'brainer-w0')
    return {
      resultSoFar: RSF,
      rescore: [{ id: 1, score: 80 }],
      add: [],
      lookupNext: [{ id: 1 }],
      rename: [],
      drop: [],
      stop: { done: false, reason: 'go' },
    };
  if (L === 'brainer-w1')
    return {
      resultSoFar: RSF,
      rescore: [],
      add: [],
      lookupNext: [],
      rename: [],
      drop: [],
      stop: { done: true, reason: 'done' },
    };
  if (L.startsWith('scheduler-')) return schedulerStub(prompt);
  // the validator flags the dead lane → the engine reopens it (also reached via the null-lane path)
  if (L.startsWith('validator-'))
    return {
      checks: [{ id: 1, fulfilled: false, reason: 'died' }],
      enough: false,
      missing: ['retry'],
    };
  if (L.startsWith('lane-')) return null;
  if (L === 'initiator')
    return {
      refinement: { facts: [{ fact: 'F', why: 'W' }] },
      synthesiser: { focus: '' },
    };
  if (L.startsWith('refine-')) return null;
  if (L.startsWith('judge-'))
    return {
      goalMet: true,
      verificationSound: true,
      needsCompute: false,
      computeSound: true,
      reasoning: 'upheld',
      directive: '',
    };
  if (L === 'synthesiser') return null;
  throw new Error('degradedAgent: unexpected label ' + L);
}

describe('ResearchReport.run — degraded agents (null guards)', () => {
  it('survives null prospector / lane / refinement / synthesiser', async () => {
    const RR = await loadEngine(
      { query: 'best vector database for production RAG at scale', mode: 'goal' },
      degradedAgent,
    );
    const result = await new RR().run();
    expect(result.highValueSources).toEqual([]); // prospector failed → none
    expect(result.metrics.reportWritten).toBe(false); // synthesiser failed
    expect(result.verdict).toBe(null);
    expect(result.confidence).toBe(null);
    // synthesiser-dead salvage: a degraded result.md carries the running answer, loudly labelled
    expect(result.files['result.md']).toContain('DEGRADED REPORT');
    expect(result.files['result.md']).toContain('## Running answer');
    const refineFile = keys(result).find((k) => k.endsWith('-refinement.md'));
    expect(result.files[refineFile!]).toContain('_(refine failed)_');
  });
});

// ── (e) brainer mid-crawl death — covers the break path + wave-cap classification ──
function brainerDiesMidAgent(prompt: string, opts: AgentOpts) {
  const L = opts.label;
  if (L === 'scout-probe:direct') return SCOUT_OUT;
  if (L === 'scout-merger') return null; // fallback-B mechanical merge (pure passthrough of the lone survivor)
  if (L === 'prospector') return PROSPECT_OUT;
  if (L === 'brainer-w0')
    return {
      resultSoFar: RSF,
      rescore: [{ id: 1, score: 80 }],
      add: [],
      lookupNext: [{ id: 1 }],
      rename: [],
      drop: [],
      stop: { done: false, reason: 'go' },
    };
  if (L === 'brainer-w1') return null; // dies mid-crawl
  if (L.startsWith('scheduler-')) return schedulerStub(prompt);
  if (L.startsWith('validator-')) return VALIDATE_OUT;
  if (L.startsWith('lane-')) return LANE_OUT;
  if (L === 'initiator')
    return {
      refinement: { facts: [] },
      synthesiser: { focus: '' },
    };
  if (L.startsWith('judge-'))
    return {
      goalMet: true,
      verificationSound: true,
      needsCompute: false,
      computeSound: true,
      reasoning: 'upheld',
      directive: '',
    };
  if (L === 'synthesiser')
    return { report: '# R', verdict: 'v', confidence: 'low', plan: [], openQuestions: [] };
  throw new Error('brainerDiesMidAgent: unexpected label ' + L);
}

describe('ResearchReport.run — brainer dies mid-crawl', () => {
  it('stops the crawl and still finalizes (stopReason wave-cap)', async () => {
    const RR = await loadEngine(
      { query: 'best vector database for production RAG at scale', mode: 'goal' },
      brainerDiesMidAgent,
    );
    const result = await new RR().run();
    expect(result.stopReason).toBe('wave-cap');
    expect(result.done).toBe(false);
    expect(result.files['result.md']).toContain('# R');
  });
});

// ── (f) scout death throws (nothing to salvage); wave-0 brainer death degrades ──
describe('ResearchReport.run — worst-case agent deaths', () => {
  it('throws when the scout dies (every probe in the swarm returns null — planner/merger dying alone is not fatal)', async () => {
    const RR = await loadEngine({ query: 'q', mode: 'goal' }, (p: string, o: AgentOpts) =>
      o.label.startsWith('scout-probe:') ? null : {},
    );
    await expect(new RR().run()).rejects.toThrow(/scout died/);
  });
  it('degrades (never throws) when the wave-0 brainer dies — scout-only finalize, loud banner, honest stopReason', async () => {
    const agent = (p: string, o: AgentOpts) => {
      if (o.label === 'scout-probe:direct') return SCOUT_OUT;
      if (o.label === 'scout-merger') return null; // fallback-B mechanical merge (pure passthrough of the lone survivor)
      if (o.label === 'prospector') return PROSPECT_OUT;
      if (o.label === 'brainer-w0') return null; // dies on every retry (e.g. a deterministic safety-classifier block)
      if (o.label === 'initiator')
        return {
          refinement: { facts: [{ fact: 'f', why: 'w' }] },
          synthesiser: { focus: 'x' },
        };
      if (o.label.startsWith('refine-'))
        return { report: 'refined (verified)', queriesTried: ['q1'], counterFound: false };
      if (o.label.startsWith('judge-'))
        return {
          goalMet: true,
          verificationSound: true,
          needsCompute: false,
          computeSound: true,
          reasoning: 'scout material verified',
          directive: '',
        };
      if (o.label === 'synthesiser')
        return {
          report: '# Report\n\nscout-only body',
          verdict: 'v',
          confidence: 'low',
          plan: [],
          openQuestions: [],
        };
      return {};
    };
    const RR = await loadEngine({ query: 'q', mode: 'goal' }, agent);
    const result = await new RR().run();
    expect(result.stopReason).toBe('brainer-dead');
    expect(result.done).toBe(false);
    expect(result.files['result.md']).toContain('DEGRADED RUN');
    expect(result.files['result.md']).toContain('scout-only body');
  });
});

// ── (g) retryAgent — retry-then-success + retries-exhausted→null (debug on) ──
const SCOUT_OUT2 = {
  landscape: 'the landscape',
  pages: [
    {
      url: 'https://a.com',
      summary: 'page a',
      rabbitHoles: [
        { keyword: 'hnsw tuning', why: 'knobs' },
        { keyword: 'sharding', why: 'scale' },
      ],
    },
    { url: 'https://b.com', summary: 'page b', rabbitHoles: [] },
  ],
  deadEnds: ['https://dead.com — timed out'],
};

function makeRetryAgent() {
  let prospectorCalls = 0;
  return (prompt: string, opts: AgentOpts) => {
    const L = opts.label;
    if (L === 'scout-probe:direct') return SCOUT_OUT2;
    if (L === 'scout-merger') return null; // fallback-B mechanical merge (pure passthrough of the lone survivor)
    if (L === 'prospector') {
      prospectorCalls++;
      if (prospectorCalls === 1) throw new Error('transient prospector failure');
      return PROSPECT_OUT;
    }
    if (L === 'synthesiser') throw new Error('synthesiser always fails'); // 3 attempts → degrade to null
    return goalAgent(prompt, opts);
  };
}

describe('ResearchReport.run — retryAgent retry + exhaust paths', () => {
  it('retries a transient failure, degrades an always-failing agent to null, captures error I/O', async () => {
    const RR = await loadEngine(
      { query: 'best vector database for production RAG at scale', mode: 'goal', debug: true },
      makeRetryAgent(),
    );
    const result = await new RR().run();
    expect(result.highValueSources.length).toBe(2); // prospector recovered on retry
    expect(result.metrics.reportWritten).toBe(false); // synthesiser exhausted → null
    expect(result.files['01-scout.md']).toContain('https://dead.com'); // deadEnds branch
    expect(result.files['_debug.md']).toContain('synthesiser always fails'); // error captured in raw I/O
  });
});

// ── (g2) rich goal run — flips the defensive-default branches in one pass ────
const LONG_Q =
  'best vector database for production retrieval-augmented generation at very large enterprise scale and operating cost';
const LONG_KW =
  'unknown venue lane that is deliberately far longer than sixty-four characters to exercise tree truncation';
const SCOUT_OUT3 = {
  landscape: 'the landscape',
  pages: [
    {
      url: 'https://a.com',
      summary: 'page a',
      rabbitHoles: [
        { keyword: 'hnsw tuning', why: 'knobs' },
        { keyword: 'sharding', why: 'scale' },
      ],
    },
    { url: 'https://b.com', summary: 'page b (no rabbitHoles key)' }, // omits rabbitHoles → exercises the `|| []` guard
  ],
}; // omits deadEnds → exercises the `|| []` guard

function richAgent(prompt: string, opts: AgentOpts) {
  const L = opts.label;
  if (L === 'scout-probe:direct') return SCOUT_OUT3;
  if (L === 'scout-merger') return null; // fallback-B mechanical merge (pure passthrough of the lone survivor)
  if (L === 'prospector') return PROSPECT_OUT;
  if (L === 'brainer-w0')
    return {
      resultSoFar: RSF,
      rescore: [
        { id: 1, score: 80 },
        { id: 2, score: 50 },
      ],
      add: [],
      lookupNext: [
        { id: 1 },
        { keyword: LONG_KW, why: 'gap', score: 75, sources: ['Unknown Venue XYZ'] },
      ],
      rename: [],
      drop: [],
      stop: { done: false, reason: 'go' },
    };
  if (L === 'brainer-w1')
    return {
      resultSoFar: RSF,
      rescore: [],
      add: [],
      lookupNext: [],
      rename: [],
      drop: [],
      stop: { done: true, reason: 'answered' },
    };
  if (L.startsWith('scheduler-')) return schedulerStub(prompt);
  if (L.startsWith('validator-')) return VALIDATE_OUT; // null lane still reopens via the null-lane path
  if (L.startsWith('lane-w1:hnsw-tuning')) return LANE_OUT;
  if (L.startsWith('lane-')) return null; // the unknown-venue lane fails → null-researcher branches
  if (L === 'initiator')
    return {
      refinement: { facts: [{ fact: 'F', why: 'W' }] },
      synthesiser: { focus: '' },
    };
  if (L.startsWith('refine-')) return { report: 'r', queriesTried: [], counterFound: false };
  // judge pass 0 → routes to brain finalize-compute; pass 1 → derivation sound → exit
  if (L === 'judge-0')
    return {
      goalMet: true,
      verificationSound: true,
      needsCompute: true,
      computeSound: false,
      reasoning: 'needs a derivation',
      directive: 'derive it',
      reopenRabbitHoles: [],
    };
  if (L.startsWith('judge-'))
    return {
      goalMet: true,
      verificationSound: true,
      needsCompute: true,
      computeSound: true,
      reasoning: 'derivation sound',
      directive: '',
    };
  if (L.startsWith('brain-compute-')) return { resultSoFar: { ...RSF, working: 'derived 42 ± 3' } };
  if (L === 'synthesiser')
    return {
      report: '# R\n\nx',
      verdict: 'v',
      confidence: 'medium',
      plan: ['p'],
      openQuestions: ['q'],
    };
  if (L === 'debug-analyst') return null; // diag null → failed-narrative branch
  throw new Error('richAgent: unexpected label ' + L);
}

describe('ResearchReport.run — rich goal run (defensive defaults)', () => {
  it('handles missing scout fields, unknown venue, null lane/sentinel/analyst, brain finalize-compute', async () => {
    const RR = await loadEngine({ query: LONG_Q, mode: 'goal', debug: true }, richAgent);
    const result = await new RR().run();
    expect(result.stopReason).toBe('brainer-done');
    // the judge routed needsCompute → the brain derived the answer (folded into resultSoFar.working)
    expect(keys(result)).toContain('_finalize-compute.md');
    expect(result.files['_finalize-compute.md']).toContain('derived 42 ± 3');
    expect(result.files['_debug.md']).toContain('_(debug analyst failed'); // null analyst narrative
    expect(result.files['_tree.md']).toContain('enterprise scale and operating cost'); // tree-viz renders the goal line FULL in the file — no clip (the tail used to be truncated at 80 chars)
    expect(result.metrics.reportWritten).toBe(true);
  });
});

// ── (g3) rabbithole-dry / rabbithole-empty stop classification ──────────────
function dryAgent(drop: boolean) {
  return (prompt: string, opts: AgentOpts) => {
    const L = opts.label;
    if (L === 'scout-probe:direct') return SCOUT_OUT;
    if (L === 'scout-merger') return null; // fallback-B mechanical merge (pure passthrough of the lone survivor)
    if (L === 'prospector') return PROSPECT_OUT;
    if (L === 'brainer-w0')
      return {
        resultSoFar: RSF,
        rescore: [
          { id: 1, score: 30 },
          { id: 2, score: 20 },
        ],
        add: [],
        lookupNext: [],
        rename: [],
        drop: drop ? [1, 2] : [],
        stop: { done: false, reason: 'no good leads' },
      };
    if (L === 'initiator')
      return {
        refinement: { facts: [] },
        synthesiser: { focus: '' },
      };
    if (L.startsWith('judge-'))
      return {
        goalMet: true,
        verificationSound: true,
        needsCompute: false,
        computeSound: true,
        reasoning: 'upheld',
        directive: '',
      };
    if (L === 'synthesiser')
      return { report: '# R', verdict: 'v', confidence: 'low', plan: [], openQuestions: [] };
    throw new Error('dryAgent: unexpected label ' + L);
  };
}

describe('ResearchReport.run — dry-store stop classification', () => {
  it('classifies rabbithole-dry when leads remain but none are looked up', async () => {
    const RR = await loadEngine({ query: 'q', mode: 'goal' }, dryAgent(false));
    const result = await new RR().run();
    expect(result.stopReason).toBe('rabbithole-dry');
  });
  it('classifies rabbithole-empty when the store is emptied', async () => {
    const RR = await loadEngine({ query: 'q', mode: 'goal' }, dryAgent(true));
    const result = await new RR().run();
    expect(result.stopReason).toBe('rabbithole-empty');
  });
});

// ── (h) manual knobs — non-auto lane/source branches ────────────────────────
describe('ResearchReport.run — manual lane/source knobs', () => {
  it('runs with fixed knobs (assignSources off, fixed srcCount, fixed laneCount)', async () => {
    const RR = await loadEngine(
      {
        query: 'best vector database for production RAG at scale',
        mode: 'goal',
        parallelLaneResearchAgentsPerWave: 2,
        parallelSourcesPerLaneResearchAgent: 3,
      },
      goalAgent,
    );
    const result = await new RR().run();
    expect(result.stopReason).toBe('brainer-done');
    expect(result.files['result.md']).toContain('# Report\n\nbody');
  });
});

// ── (i) brainer self-compute — code-capable (general-purpose) when compute is on, default subagent when off ──
function captureBrainerType(captured: Array<string | undefined>) {
  return function (prompt: string, opts: AgentOpts) {
    const L = opts.label;
    if (L.startsWith('brainer-')) captured.push(opts.agentType);
    if (L === 'scout-probe:direct') return SCOUT_OUT;
    if (L === 'scout-merger') return null; // fallback-B mechanical merge (pure passthrough of the lone survivor)
    if (L === 'prospector') return PROSPECT_OUT;
    if (L === 'brainer-w0')
      return {
        resultSoFar: RSF,
        rescore: [{ id: 1, score: 80 }],
        add: [],
        lookupNext: [],
        rename: [],
        drop: [],
        stop: { done: true, reason: 'answered' },
      };
    if (L === 'initiator')
      return {
        refinement: { facts: [] },
        synthesiser: { focus: '' },
      };
    if (L.startsWith('judge-'))
      return {
        goalMet: true,
        verificationSound: true,
        needsCompute: false,
        computeSound: true,
        reasoning: 'upheld',
        directive: '',
      };
    if (L === 'synthesiser')
      return { report: '# R', verdict: 'v', confidence: 'medium', plan: [], openQuestions: [] };
    throw new Error('captureBrainerType: unexpected label ' + L);
  };
}

describe('ResearchReport.run — brainer self-compute capability', () => {
  it('runs the brainer as a code-capable general-purpose agent when compute is on, with no separate wave-compute stage', async () => {
    const captured: Array<string | undefined> = [];
    const RR = await loadEngine(
      { query: 'estimate the nearest undetected black hole distance', mode: 'goal' },
      captureBrainerType(captured),
    );
    const result = await new RR().run();
    expect(result.stopReason).toBe('brainer-done');
    expect(captured.length).toBeGreaterThan(0);
    expect(captured.every((t) => t === 'general-purpose')).toBe(true); // compute on → the brainer can write+run code itself
    expect(keys(result).some((k) => k.startsWith('_compute-w'))).toBe(false); // no separate wave-compute artifacts — derived inline into `working`
  });

  it('runs the brainer as the default subagent (no code capability) when compute is off', async () => {
    const captured: Array<string | undefined> = [];
    const RR = await loadEngine(
      {
        query: 'estimate the nearest undetected black hole distance',
        mode: 'goal',
        compute: false,
      },
      captureBrainerType(captured),
    );
    await new RR().run();
    expect(captured.length).toBeGreaterThan(0);
    expect(captured.every((t) => t === undefined)).toBe(true); // compute off → no code capability at all
  });
});

// ── (j) compute flag OFF — no computation runs even when agents request it ───
describe('ResearchReport.run — compute flag off', () => {
  it('runs no computation when args.compute is false, even though the judge requests it', async () => {
    // goalAgent's judge returns needsCompute:true — with compute off the engine forces it false (no derivation path)
    const RR = await loadEngine(
      { query: 'best vector database for production RAG at scale', mode: 'goal', compute: false },
      goalAgent,
    );
    const result = await new RR().run();
    expect(result.stopReason).toBe('brainer-done');
    expect(keys(result).some((k) => k.startsWith('_compute-'))).toBe(false); // NO compute files at all
    expect(result.files['result.md']).toContain('# Report\n\nbody'); // still finalizes normally
    // A1c — the judge asked for a derivation (needsCompute:true); with compute off it is surfaced as an HONEST
    // limitation (open gap → Open questions), never rubber-stamped away into a fabricated-derivation pass
    expect(
      (result.resultSoFar!.openGaps || []).some((g) =>
        /Quantitative derivation unavailable/.test(g),
      ),
    ).toBe(true);
  });
});

// ── (j2) finalize crawl-reopen carries the judge directive as the lane note (A5) ──
function reopenAgent(prompt: string, opts: AgentOpts) {
  const L = opts.label;
  if (L === 'scout-probe:direct') return SCOUT_OUT;
  if (L === 'scout-merger') return null; // fallback-B mechanical merge (pure passthrough of the lone survivor)
  if (L === 'prospector') return PROSPECT_OUT;
  if (L === 'brainer-w0')
    return {
      resultSoFar: RSF,
      rescore: [{ id: 1, score: 80 }],
      add: [],
      lookupNext: [{ id: 1 }],
      rename: [],
      drop: [],
      stop: { done: false, reason: 'go' },
    };
  if (L === 'brainer-w1')
    return {
      resultSoFar: RSF,
      rescore: [],
      add: [],
      lookupNext: [],
      rename: [],
      drop: [],
      stop: { done: true, reason: 'answered' },
    };
  if (L.startsWith('brainer-'))
    return {
      resultSoFar: RSF,
      rescore: [],
      add: [],
      lookupNext: [],
      stop: { done: true, reason: 'folded' },
    }; // the reopen-fold coordinate
  if (L.startsWith('scheduler-')) return schedulerStub(prompt);
  if (L.startsWith('validator-')) return VALIDATE_OUT;
  if (L.startsWith('lane-')) return LANE_OUT;
  if (L === 'initiator')
    return { refinement: { facts: [{ fact: 'F', why: 'W' }] }, synthesiser: { focus: '' } };
  if (L.startsWith('refine-')) return { report: 'r', queriesTried: [], counterFound: false };
  if (L === 'judge-0')
    return {
      goalMet: false, // a real coverage gap → reopen the crawl
      verificationSound: true,
      needsCompute: false,
      computeSound: true,
      reasoning: 'a real evidence gap remains',
      directive: 'DIG INTO THE GAP',
      reopenRabbitHoles: [{ keyword: 'evidence gap', why: 'needed for the headline' }],
    };
  if (L.startsWith('judge-'))
    return {
      goalMet: true,
      verificationSound: true,
      needsCompute: false,
      computeSound: true,
      reasoning: 'gap closed',
      directive: '',
    };
  if (L === 'synthesiser')
    return { report: '# R', verdict: 'v', confidence: 'medium', plan: [], openQuestions: [] };
  throw new Error('reopenAgent: unexpected label ' + L);
}

describe('ResearchReport.run — finalize crawl-reopen steers with the judge directive', () => {
  it('passes the judge directive as the reopened lane note (A5)', async () => {
    const RR = await loadEngine({ query: 'q', mode: 'goal' }, reopenAgent);
    const result = await new RR().run();
    const reopened = result.pursuedArchive.find((r) => r.keyword === 'evidence gap');
    expect(reopened).toBeTruthy();
    expect(reopened!.note).toBe('DIG INTO THE GAP'); // the directive steered the finalize lane
    expect(reopened!.path).toContain('⚖ judge');
  });
});

// ── (k) finalize JUDGE loop bounded by MAX_JUDGE_PASSES ──────────────────────
function capAgent(prompt: string, opts: AgentOpts) {
  const L = opts.label;
  if (L === 'initiator')
    return {
      refinement: { facts: [{ fact: 'recall is 0.98', why: 'headline' }] },
      synthesiser: { focus: 'lead with cost' },
    };
  // the judge is NEVER satisfied (derivation never sound) → every pass routes to brain-compute, so the loop runs to the cap
  if (L.startsWith('judge-'))
    return {
      goalMet: true,
      verificationSound: true,
      needsCompute: true,
      computeSound: false,
      reasoning: 'the derivation still is not sound',
      directive: 'derive again with tighter error bars',
      reopenRabbitHoles: [],
    };
  if (L.startsWith('brain-compute-')) return { resultSoFar: { ...RSF, working: 'derived again' } };
  return goalAgent(prompt, opts);
}

describe('ResearchReport.run — finalize judge loop bounded by MAX_JUDGE_PASSES', () => {
  it('runs the judge at most MAX_JUDGE_PASSES+1 times even when it is never satisfied', async () => {
    const RR = await loadEngine(
      { query: 'best vector database for production RAG at scale', mode: 'goal' },
      capAgent,
    );
    const result = await new RR().run();
    const judgeFile = result.files[keys(result).find((k) => k.endsWith('-judge.md'))!];
    // MAX_JUDGE_PASSES = 2 → the judge runs passes 0,1,2 (3 total), then the loop is capped
    expect(judgeFile).toContain('## Pass 2');
    expect(judgeFile).not.toContain('## Pass 3');
    // the brain derived on each remediation pass, captured into _finalize-compute.md
    expect(keys(result)).toContain('_finalize-compute.md');
  });
});

// ── (l) validator gate — re-opens failing lanes, caps re-fails, surfaces a known gap ──
function refailAgent(prompt: string, opts: AgentOpts) {
  const L = opts.label;
  if (L === 'scout-probe:direct') return SCOUT_OUT; // seeds id1 (hnsw tuning) + id2 (sharding)
  if (L === 'scout-merger') return null; // fallback-B mechanical merge (pure passthrough of the lone survivor)
  if (L === 'prospector') return PROSPECT_OUT;
  if (L === 'brainer-w0')
    return {
      resultSoFar: RSF,
      rescore: [
        { id: 1, score: 80 },
        { id: 2, score: 50 },
      ],
      add: [],
      lookupNext: [{ id: 1 }],
      rename: [],
      drop: [],
      stop: { done: false, reason: 'go' },
    };
  if (L === 'brainer-w1' || L === 'brainer-w2')
    // keep re-pursuing the reopened lane id1
    return {
      resultSoFar: RSF,
      rescore: [],
      add: [],
      lookupNext: [{ id: 1 }],
      rename: [],
      drop: [],
      stop: { done: false, reason: 'retry the dead lane' },
    };
  if (L === 'brainer-w3')
    return {
      resultSoFar: RSF,
      rescore: [],
      add: [],
      lookupNext: [],
      rename: [],
      drop: [],
      stop: { done: true, reason: 'gave up on the dead lane' },
    };
  if (L.startsWith('scheduler-')) return schedulerStub(prompt);
  if (L.startsWith('lane-')) return null; // every lane fails outright
  if (L.startsWith('validator-'))
    return {
      checks: [{ id: 1, fulfilled: false, reason: 'empty' }],
      enough: false,
      missing: ['hnsw depth'],
    };
  if (L === 'initiator') return { refinement: { facts: [] }, synthesiser: { focus: '' } };
  if (L.startsWith('judge-'))
    return {
      goalMet: true,
      verificationSound: true,
      needsCompute: false,
      computeSound: true,
      reasoning: 'ok',
      directive: '',
    };
  if (L === 'synthesiser')
    return { report: '# R', verdict: 'v', confidence: 'low', plan: [], openQuestions: [] };
  throw new Error('refailAgent: unexpected label ' + L);
}

describe('ResearchReport.run — validator reopens then caps a persistently-failing lane', () => {
  it('reopens the dead lane twice, then surfaces it as a known gap (failCount cap)', async () => {
    const RR = await loadEngine({ query: 'q', mode: 'goal' }, refailAgent);
    const result = await new RR().run();
    expect(result.stopReason).toBe('brainer-done');
    const vFile = result.files[keys(result).find((k) => k.endsWith('-validator.md'))!];
    expect(vFile).toContain('Reopened'); // it was moved back to the open store
    expect(vFile).toContain('Known gaps'); // and then capped after MAX_LANE_REFAILS
    // the capped lane carries the cap as its failCount in the pursued archive
    expect(
      result.pursuedArchive.some((r) => r.keyword === 'hnsw tuning' && r.failCount === 2),
    ).toBe(true);
  });
});

// ── (m) validator gate skipped when every lane returns a substantial finding ──
const THICK = 'x'.repeat(200); // ≥ VALIDATOR_THIN, so the gate does not fire
function skipValidatorAgent(prompt: string, opts: AgentOpts) {
  const L = opts.label;
  if (L === 'scout-probe:direct') return SCOUT_OUT;
  if (L === 'scout-merger') return null; // fallback-B mechanical merge (pure passthrough of the lone survivor)
  if (L === 'prospector') return PROSPECT_OUT;
  if (L === 'brainer-w0')
    return {
      resultSoFar: RSF,
      rescore: [{ id: 1, score: 80 }],
      add: [],
      lookupNext: [{ id: 1 }],
      rename: [],
      drop: [],
      stop: { done: false, reason: 'go' },
    };
  if (L === 'brainer-w1')
    return {
      resultSoFar: RSF,
      rescore: [],
      add: [],
      lookupNext: [],
      rename: [],
      drop: [],
      stop: { done: true, reason: 'answered' },
    };
  if (L.startsWith('scheduler-')) return schedulerStub(prompt);
  if (L.startsWith('lane-w1:hnsw-tuning'))
    return { runningAnswer: THICK, rabbitHoles: [], deadEnds: [] };
  if (L === 'initiator') return { refinement: { facts: [] }, synthesiser: { focus: '' } };
  if (L.startsWith('judge-'))
    return {
      goalMet: true,
      verificationSound: true,
      needsCompute: false,
      computeSound: true,
      reasoning: 'ok',
      directive: '',
    };
  if (L === 'synthesiser')
    return { report: '# R', verdict: 'v', confidence: 'high', plan: [], openQuestions: [] };
  // a 'validator-*' label here would throw → proves the gate skipped it
  throw new Error('skipValidatorAgent: unexpected label ' + L);
}

describe('ResearchReport.run — validator gate skips when findings are substantial', () => {
  it('does not run the validator (no -validator.md) when no lane died and findings are thick', async () => {
    const RR = await loadEngine({ query: 'q', mode: 'goal' }, skipValidatorAgent);
    const result = await new RR().run();
    expect(result.stopReason).toBe('brainer-done');
    expect(keys(result).some((k) => k.endsWith('-validator.md'))).toBe(false);
  });
});

// ── (n) B6 — scheduler-death starvation: an empty schedule N waves running → stopReason scheduler-starved ──
function starveAgent(prompt: string, opts: AgentOpts) {
  const L = opts.label;
  if (L === 'scout-probe:direct') return SCOUT_OUT;
  if (L === 'scout-merger') return null; // fallback-B mechanical merge (pure passthrough of the lone survivor)
  if (L === 'prospector') return PROSPECT_OUT;
  if (L.startsWith('brainer-w')) {
    const w = Number(L.slice('brainer-w'.length));
    // originate a FRESH pursuable lane every wave, so the crawl keeps trying (only the scheduler is starving it)
    return {
      resultSoFar: RSF,
      rescore: [],
      add: [],
      lookupNext: [{ keyword: 'fresh lead ' + w, why: 'breadth', score: 80 }],
      rename: [],
      drop: [],
      stop: { done: false, reason: 'still going' },
    };
  }
  if (L.startsWith('scheduler-')) {
    // the scheduler echoes the lane ids but discovers NO usable sources (every candidate walled/dead)
    const ids = [...new Set([...prompt.matchAll(/#(\d+)/g)].map((m) => Number(m[1])))];
    return { lanes: ids.map((id) => ({ id, sources: [] })) };
  }
  if (L.startsWith('validator-')) return VALIDATE_OUT;
  if (L.startsWith('lane-')) return LANE_OUT; // never reached — empty source lists spawn no readers
  if (L === 'initiator') return { refinement: { facts: [] }, synthesiser: { focus: '' } };
  if (L.startsWith('judge-'))
    return {
      goalMet: true,
      verificationSound: true,
      needsCompute: false,
      computeSound: true,
      reasoning: 'ok',
      directive: '',
    };
  if (L === 'synthesiser')
    return { report: '# R', verdict: 'v', confidence: 'low', plan: [], openQuestions: [] };
  throw new Error('starveAgent: unexpected label ' + L);
}

describe('ResearchReport.run — scheduler-death starvation guard (B6)', () => {
  it('breaks the crawl with stopReason scheduler-starved after MAX_STARVED_WAVES empty schedules', async () => {
    const RR = await loadEngine({ query: 'q', mode: 'goal' }, starveAgent);
    const result = await new RR().run();
    expect(result.stopReason).toBe('scheduler-starved');
    expect(result.metrics.wavesRun).toBeLessThan(15); // did NOT grind to HARD_CAP
    expect(result.files['result.md']).toContain('# R'); // still finalizes
  });
});

// ── (n3) B6 fix (finding 3) — runOneWave (the multi-brainer path) now mirrors runCrawl's early break: a
//     starved wave drains BEFORE findings/validator/brainer ever see it, instead of dispatching a brainer
//     call it cannot do anything useful with. Unit-style (mirrors the topScoresBase test above): call
//     runOneWave directly so a brainer call-count proves the wave that CROSSES MAX_STARVED_WAVES never dispatches it.
describe('ResearchReport.runOneWave — B6 starved-wave early return skips the brainer dispatch (finding 3)', () => {
  const mkBs = () =>
    new BrainerState(
      { scout: SCOUT_OUT, scoutRabbitHoles: [], highValueSources: [], languageGuidance: '' },
      { name: 'root', parentName: null, mandate: '', trail: '', depth: 0 },
    );
  it('drains with stopReason scheduler-starved on the 2nd consecutive empty schedule, WITHOUT calling the brainer that wave', async () => {
    let brainerCalls = 0;
    const agent: AgentStub = (prompt: string, opts: AgentOpts) => {
      const L = opts.label;
      if (L.startsWith('scheduler-')) {
        // every lane starves — the schedule carries ids but NO usable sources (mirrors starveAgent above).
        const ids = [...new Set([...prompt.matchAll(/#(\d+)/g)].map((m) => Number(m[1])))];
        return { lanes: ids.map((id) => ({ id, sources: [] })) };
      }
      if (L.startsWith('validator-')) return VALIDATE_OUT;
      if (L.startsWith('brainer-')) {
        brainerCalls++;
        return {
          resultSoFar: RSF,
          rescore: [],
          add: [],
          rename: [],
          drop: [],
          lookupNext: [{ keyword: 'wave1-fresh-lead', why: 'w', score: 70 }],
          stop: { done: false, reason: 'go' },
        };
      }
      throw new Error('starved-runOneWave test: unexpected label ' + L);
    };
    const RR = await loadEngine(
      { query: 'q', mode: 'goal', checkpoint: false, debug: false },
      agent,
    );
    const rr = new RR();
    const bs = mkBs();
    bs.lookupNext = [
      {
        id: 1,
        keyword: 'seed lead',
        why: 'w',
        score: 60,
        scoreHistory: [{ wave: 0, score: 60 }],
        path: [],
      },
    ];
    // wave 1 — the FIRST starved wave: below MAX_STARVED_WAVES (2), so it runs the brainer as usual.
    await rr.runOneWave(bs, 1, false, 'Research', false);
    expect(bs.starvedWaves).toBe(1);
    expect(bs.status).toBe('active');
    expect(brainerCalls).toBe(1);
    // wave 2 — the SECOND consecutive starved wave crosses MAX_STARVED_WAVES: must drain immediately,
    // never reaching findings/validator/brainer this wave (the call count must NOT increment).
    await rr.runOneWave(bs, 2, false, 'Research', false);
    expect(bs.starvedWaves).toBe(2);
    expect(bs.status).toBe('drained');
    expect(bs.stopReason).toBe('scheduler-starved');
    expect(brainerCalls).toBe(1); // unchanged — wave 2 never dispatched the brainer
  });
});

// ── (n2) B6 — scheduleSources drops hallucinated ids + duplicate-id last-wins ──
describe('ResearchReport.scheduleSources — id discipline (B6)', () => {
  it('keeps only real pick ids and resolves a duplicate id last-wins', async () => {
    const agent = (_p: string, o: AgentOpts) => {
      if (o.label.startsWith('scheduler-'))
        return {
          lanes: [
            { id: 1, sources: [{ source: 'a', path: '/c/a', size: 1, chars: 2 }] },
            { id: 999, sources: [{ source: 'b', path: '/c/b', size: 1, chars: 2 }] }, // hallucinated id → dropped
            { id: 1, sources: [{ source: 'c', path: '/c/c', size: 1, chars: 2 }] }, // duplicate id → last wins
          ],
        };
      return {};
    };
    const RR = await loadEngine({ query: 'q' }, agent);
    const rr = new RR();
    const picks = [{ id: 1, keyword: 'k', why: 'w', score: 50, scoreHistory: [], path: [] }] as any;
    const map = await rr.scheduleSources(
      {
        highValueSources: [],
        venueStats: {},
        knownCachePaths: new Set(),
        corruptCachePaths: new Set(),
        lastUnsourced: '',
      } as any,
      picks,
      'test',
      'Research',
    );
    expect([...map.keys()]).toEqual([1]); // 999 dropped
    expect(map.get(1)![0].source).toBe('c'); // last-wins
  });
});

// ── (o) B3 — a partial reader failure (one of N readers null) fails the WHOLE lane (gate reopens it) ──
function bigSchedulerStub(prompt: string) {
  const ids = [...new Set([...prompt.matchAll(/#(\d+)/g)].map((m) => Number(m[1])))];
  // a big source (size > budget) → packReaders splits it into 2 reader-units (labels -r1of2 / -r2of2)
  return {
    lanes: ids.map((id) => ({
      id,
      sources: [
        {
          source: 'https://big' + id,
          path: '/cache/big' + id + '.txt',
          size: 260000,
          chars: 520000,
        },
      ],
    })),
  };
}
function partialFailAgent(prompt: string, opts: AgentOpts) {
  const L = opts.label;
  if (L === 'scout-probe:direct') return SCOUT_OUT;
  if (L === 'scout-merger') return null; // fallback-B mechanical merge (pure passthrough of the lone survivor)
  if (L === 'prospector') return PROSPECT_OUT;
  if (L === 'brainer-w0')
    return {
      resultSoFar: RSF,
      rescore: [{ id: 1, score: 80 }],
      add: [],
      lookupNext: [{ id: 1 }],
      rename: [],
      drop: [],
      stop: { done: false, reason: 'go' },
    };
  if (L === 'brainer-w1')
    return {
      resultSoFar: RSF,
      rescore: [],
      add: [],
      lookupNext: [],
      rename: [],
      drop: [],
      stop: { done: true, reason: 'answered' },
    };
  if (L.startsWith('scheduler-')) return bigSchedulerStub(prompt);
  if (L.includes('-r2of')) return null; // the SECOND reader-unit fails → a dropped chunk
  if (L.startsWith('lane-')) return LANE_OUT; // the first reader-unit succeeds
  if (L.startsWith('validator-'))
    return {
      checks: [{ id: 1, fulfilled: false, reason: 'partial' }],
      enough: false,
      missing: ['rest'],
    };
  if (L === 'initiator') return { refinement: { facts: [] }, synthesiser: { focus: '' } };
  if (L.startsWith('judge-'))
    return {
      goalMet: true,
      verificationSound: true,
      needsCompute: false,
      computeSound: true,
      reasoning: 'ok',
      directive: '',
    };
  if (L === 'synthesiser')
    return { report: '# R', verdict: 'v', confidence: 'low', plan: [], openQuestions: [] };
  throw new Error('partialFailAgent: unexpected label ' + L);
}

describe('ResearchReport.run — a dropped chunk fails the lane (B3 coverage gate)', () => {
  it('reopens a lane when one of its reader-units failed (never masks the drop behind survivors)', async () => {
    const RR = await loadEngine({ query: 'q', mode: 'goal' }, partialFailAgent);
    const result = await new RR().run();
    // the lane returned null despite reader-1 succeeding → the validator gate fired and reopened it
    const vKey = keys(result).find((k) => k.endsWith('-validator.md'));
    expect(vKey).toBeTruthy();
    expect(result.files[vKey!]).toContain('Reopened');
    expect(result.files[vKey!]).toContain('hnsw tuning');
  });
});

// ── (p) B8 — the per-lane note is surfaced in waveMd + _rabbitHoles.json + _tree.md (no debug mode needed) ──
function noteAgent(prompt: string, opts: AgentOpts) {
  const L = opts.label;
  if (L === 'brainer-w0')
    return {
      resultSoFar: RSF,
      rescore: [{ id: 1, score: 80 }],
      add: [],
      lookupNext: [{ id: 1, note: 'DIG-NOTE', sources: ['arXiv (site:arxiv.org)'] }],
      rename: [],
      drop: [],
      stop: { done: false, reason: 'go' },
    };
  return goalAgent(prompt, opts);
}

describe('ResearchReport.run — per-lane note observability (B8)', () => {
  it('surfaces the lane note in the wave file, _rabbitHoles.json, and _tree.md', async () => {
    const RR = await loadEngine({ query: 'q', mode: 'goal' }, noteAgent);
    const result = await new RR().run();
    expect(result.files['03-wave-0.md']).toContain('note: DIG-NOTE');
    expect(
      JSON.parse(result.files['_rabbitHoles.json']).pursued.some(
        (r: { note?: string }) => r.note === 'DIG-NOTE',
      ),
    ).toBe(true);
    expect(result.files['_tree.md']).toContain('DIG-NOTE');
  });
});

// ── (q) MULTI-BRAINER (maxParallelBrainers > 1) — fork-and-branch: spawn a child, child declares LOST, root wins the gate ──
describe('ResearchReport.run — multi-brainer fork-and-branch (B>1)', () => {
  // root: w0 score seeds → w1 SPAWN a child onto the sharding branch (still active) → w2 declare done (→ gate → winner).
  // child b1-sharding: pick a lane → w2 declare its branch LOST. Asserts: tree has 2 nodes, winner=root, loser file written.
  function multiAgent(prompt: string, opts: AgentOpts) {
    const L = opts.label;
    const coord = (over: Record<string, unknown>) => ({
      resultSoFar: RSF,
      rescore: [],
      add: [],
      lookupNext: [],
      rename: [],
      drop: [],
      stop: { done: false, reason: 'x' },
      ...over,
    });
    if (L === 'scout-probe:direct') return SCOUT_OUT;
    if (L === 'scout-merger') return null; // fallback-B mechanical merge (pure passthrough of the lone survivor)
    if (L === 'prospector') return PROSPECT_OUT;
    // ROOT
    if (L === 'brainer-w0')
      return coord({
        rescore: [
          { id: 1, score: 80 },
          { id: 2, score: 78 },
        ],
        lookupNext: [{ id: 1, sources: ['arXiv (site:arxiv.org)'] }],
      });
    if (L === 'brainer-w1')
      return coord({
        spawn: { id: 2, mandate: 'sharding' }, // fork a child onto the sharding branch
        lookupNext: [{ keyword: 'root next lane', why: 'more', score: 75 }],
      });
    if (L === 'brainer-w2') return coord({ stop: { done: true, reason: 'root answered' } });
    // CHILD  b1-sharding
    if (L === 'brainer-b1-sharding-w1')
      return coord({ lookupNext: [{ keyword: 'shard internals', why: 'deep', score: 70 }] });
    if (L === 'brainer-b1-sharding-w2')
      return coord({ stop: { done: false, lost: true, reason: 'sharding was a dead end' } });
    // shared sub-agents
    if (L.startsWith('scheduler-')) return schedulerStub(prompt);
    if (L.startsWith('validator-')) return VALIDATE_OUT;
    if (L.startsWith('lane-')) return LANE_OUT;
    if (L === 'initiator')
      return { refinement: { facts: [{ fact: 'f', why: 'w' }] }, synthesiser: { focus: 'lead' } };
    if (L.startsWith('refine-'))
      return { report: 'hardened fact', queriesTried: [], counterFound: false };
    if (L.startsWith('judge-'))
      return {
        goalMet: true,
        verificationSound: true,
        needsCompute: false,
        computeSound: true,
        reasoning: 'solid',
        directive: '',
        reopenRabbitHoles: [],
      };
    if (L === 'synthesiser')
      return {
        report: '# Winner report\n\nbody',
        verdict: 'root wins',
        confidence: 'high',
        plan: ['ship'],
        openQuestions: [],
      };
    if (L === 'debug-analyst') return { diagnosis: '# Debug\n\nok' };
    throw new Error('multiAgent: unexpected label ' + L);
  }

  it('forks a child, abandons a lost branch, and the root wins the gate', async () => {
    const RR = await loadEngine(
      { query: 'q', mode: 'goal', maxParallelBrainers: 2, debug: false },
      multiAgent,
    );
    const rr = new RR();
    const result = await rr.run();
    // two brainers in the tree: the root + the spawned child
    expect(rr.liveBrainers.length).toBe(2);
    const names = rr.liveBrainers.map((b: { name: string }) => b.name).sort();
    expect(names).toEqual(['b1-sharding', 'root']);
    // the root won its gate → canonical report
    expect(rr.winner!.name).toBe('root');
    expect(result.files['result.md']).toContain('Winner report');
    // the child abandoned its branch
    const child = rr.liveBrainers.find((b: { name: string }) => b.name === 'b1-sharding')!;
    expect(child.status).toBe('lost');
    // outputs: brainer tree + the loser's preserved partial
    expect(result.files['_brainers-tree.md']).toContain('b1-sharding');
    expect(result.files['_brainers-tree.md']).toContain('⚑WINNER');
    expect(JSON.parse(result.files['_brainers.json']).winner).toBe('root');
    expect(result.files['result-b1-sharding.md']).toContain('LOST');
  });
});

// ── (r) v3 CLAIM-LEDGER INGESTION — scrub, dedupe, hallucinated-stance filter, batched audit ∥ lineage
//     (incl. a cross-wave persistent cluster merge), computed status, attack bookkeeping (landed +
//     nullAttack), vocabulary merge, yieldCalib, and the _claims.json/_claims.md artifacts ──────────────
const SCOUT_OUT_LEDGER = {
  landscape: 'v3 ledger test landscape',
  pages: [],
  claims: [
    {
      claim: 'SCOUT_CLAIM: Acme drug reduces cardiovascular risk',
      quote: 'Acme drug reduces cardiovascular risk by 30 percent in the trial cohort text',
      source: 'https://scout.example.com/ACME-MARKER-trial',
      cachePath: '/cache/.fetch/scout-trial.txt', // matches the harvester cache signature → trusted at ingest
      entities: { funder: 'Acme Corp' },
    },
  ],
  newTerms: [{ term: 'RCT', gloss: 'randomized controlled trial' }],
  deadEnds: [],
};

// id-agnostic stubs — parse {id, claim}/{id, source} pairs straight off the rendered prompt (plain()
// renders object keys in declaration order: id then claim, or id then source) so these never depend on
// hardcoding which numeric ids the engine happens to assign.
function claimAuditStub(prompt: string) {
  const checks: { id: number; verdict: 'pass' | 'fail' }[] = [];
  for (const m of prompt.matchAll(/id: (\d+)\s*\n\s*claim: ([^\n]*)/g))
    checks.push({ id: Number(m[1]), verdict: m[2].includes('BETA_CLAIM') ? 'fail' : 'pass' });
  return { checks };
}
function lineageClerkStub(prompt: string) {
  const links: { id: number; keys: string[] }[] = [];
  for (const m of prompt.matchAll(/id: (\d+)\s*\n\s*source: ([^\n]*)/g)) {
    const [, idStr, source] = m;
    if (source.includes('OTHERFUNDER')) continue; // simulate "the clerk missed this one" → lineageKeyOf fallback
    links.push({ id: Number(idStr), keys: source.includes('ACME-MARKER') ? ['funder:acme'] : [] });
  }
  return { links };
}

function ledgerAgent() {
  return (prompt: string, opts: AgentOpts) => {
    const L = opts.label;
    if (L === 'scout-probe:direct') return SCOUT_OUT_LEDGER;
    if (L === 'scout-merger') return null; // fallback-B mechanical merge (pure passthrough of the lone survivor)
    if (L === 'prospector') return PROSPECT_OUT;
    if (L === 'claim-audit-scout' || L === 'claim-audit-w1') return claimAuditStub(prompt);
    if (L === 'lineage-scout' || L === 'lineage-w1') return lineageClerkStub(prompt);
    if (L === 'brainer-w0')
      return {
        resultSoFar: RSF,
        rescore: [],
        add: [],
        rename: [],
        drop: [],
        lookupNext: [
          { keyword: 'source beta', why: 'beta lead', score: 90, kind: 'gap' },
          { keyword: 'source gamma', why: 'gamma lead', score: 80, kind: 'gap' },
          { keyword: 'attack landed', why: 'counter search', score: 70, kind: 'attack' },
          {
            keyword: 'attack empty',
            why: 'counter search',
            score: 60,
            kind: 'attack',
            note: 'counter search for c1',
          },
        ],
        stop: { done: false, reason: 'scoring seeds' },
      };
    if (L === 'brainer-w1')
      return {
        resultSoFar: RSF,
        rescore: [],
        add: [],
        rename: [],
        drop: [],
        lookupNext: [],
        stop: { done: true, reason: 'ledger test done' },
      };
    if (L.startsWith('scheduler-')) return schedulerStub(prompt);
    if (L.startsWith('validator-')) return VALIDATE_OUT;
    if (L === 'lane-w1:source-beta-r1of1')
      return {
        runningAnswer: 'beta lane summary. ' + THICK,
        rabbitHoles: [],
        nextSources: [],
        deadEnds: [],
        claims: [
          {
            claim: 'BETA_CLAIM: Beta compound improves outcome',
            quote: 'Beta compound improves outcome significantly across the studied cohort text',
            source: 'https://beta.example.com/ACME-MARKER-beta',
            cachePath: '/cache/.fetch/beta.txt', // matches the harvester cache signature → trusted at ingest
          },
          // an exact duplicate (same quote+source) — must be deduped, never a second ledger row
          {
            claim: 'BETA_CLAIM: Beta compound improves outcome',
            quote: 'Beta compound improves outcome significantly across the studied cohort text',
            source: 'https://beta.example.com/ACME-MARKER-beta',
            cachePath: '/cache/.fetch/beta.txt',
          },
        ],
        newTerms: [{ term: 'Biomarker', gloss: 'a measurable indicator' }],
      };
    if (L === 'lane-w1:source-gamma-r1of1')
      return {
        runningAnswer: 'gamma lane summary. ' + THICK,
        rabbitHoles: [],
        nextSources: [],
        deadEnds: [],
        claims: [
          {
            claim: 'GAMMA_CLAIM: Gamma finding without pinning',
            quote: 'Gamma finding without pinning is reported anecdotally in the piece here',
            source: 'https://gamma.example.com/ACME-MARKER-gamma',
            // no cachePath → unpinned, never sent to the auditor
          },
          {
            claim: 'DELTA_CLAIM: Delta unrelated finding',
            quote: 'Delta unrelated finding appears in a completely separate context here today',
            source: 'https://delta.example.com/INDEPENDENT-marker',
            stance: { target: 9999, kind: 'supports' }, // hallucinated target id → dropped on ingest
          },
        ],
        // same term, different case + a DIFFERENT gloss — must bump uses, never overwrite the first gloss
        newTerms: [{ term: 'biomarker', gloss: 'a different gloss that must be ignored' }],
      };
    if (L === 'lane-w1:attack-landed-r1of1')
      return {
        runningAnswer: 'attack landed lane summary. ' + THICK,
        rabbitHoles: [],
        nextSources: [],
        deadEnds: [],
        surprise: 'the new trial data conflicts with the original claim',
        claims: [
          {
            claim: 'EPSILON_CLAIM: Attack claim undermines the trial',
            quote: 'Attack claim undermines the trial results reported by the original study text',
            source: 'https://epsilon.example.com/OTHERFUNDER-marker',
            cachePath: '/cache/.fetch/epsilon.txt', // matches the harvester cache signature → trusted at ingest
            stance: { target: 1, kind: 'attacks' }, // targets the scout's claim (id 1) — a REAL prior id
          },
        ],
        newTerms: [],
      };
    if (L === 'lane-w1:attack-empty-r1of1')
      return {
        runningAnswer: 'attack empty lane summary. ' + THICK,
        rabbitHoles: [],
        nextSources: [],
        deadEnds: [],
        claims: [], // landed NOTHING → a nullAttack, not silence
        newTerms: [],
      };
    if (L === 'initiator') return { refinement: { facts: [] }, synthesiser: { focus: '' } };
    if (L.startsWith('refine-')) return { report: 'r', queriesTried: [], counterFound: false };
    if (L.startsWith('judge-'))
      return {
        goalMet: true,
        verificationSound: true,
        needsCompute: false,
        computeSound: true,
        reasoning: 'ok',
        directive: '',
      };
    if (L === 'synthesiser')
      return { report: '# R', verdict: 'v', confidence: 'medium', plan: [], openQuestions: [] };
    throw new Error('ledgerAgent: unexpected label ' + L);
  };
}

describe('ResearchReport.run — v3 claim-ledger ingestion', () => {
  it('ingests + dedupes + audits + clusters (incl. cross-wave merge) + runs attack bookkeeping + merges vocabulary + updates yieldCalib + writes the ledger artifacts', async () => {
    const RR = await loadEngine(
      { query: 'v3 ledger test query', mode: 'goal', debug: false },
      ledgerAgent(),
    );
    const rr = new RR();
    const result = await rr.run();

    expect(result.stopReason).toBe('brainer-done');

    const ledger = JSON.parse(result.files['_claims.json']);
    const byText = (needle: string) =>
      ledger.claims.find((c: { claim: string }) => c.claim.includes(needle));
    const a = byText('SCOUT_CLAIM');
    const b = byText('BETA_CLAIM');
    const g = byText('GAMMA_CLAIM');
    const d = byText('DELTA_CLAIM');
    const e = byText('EPSILON_CLAIM');
    expect(a).toBeTruthy();
    expect(b).toBeTruthy();
    expect(g).toBeTruthy();
    expect(d).toBeTruthy();
    expect(e).toBeTruthy();
    // dedupe: the beta lane emitted the SAME claim twice — only one survives
    expect(
      ledger.claims.filter((c: { claim: string }) => c.claim.includes('BETA_CLAIM')).length,
    ).toBe(1);
    // audit verdicts
    expect(a.audit).toBe('pass'); // the scout's claim, cachePath set, auditor said pass
    expect(b.audit).toBe('fail'); // the auditor said fail
    expect(g.audit).toBe('unpinned'); // no cachePath → never sent to the auditor
    expect(e.audit).toBe('pass');
    // hallucinated-stance filter — DELTA's stance targeted a non-existent id 9999
    expect(d.stance).toBeUndefined();
    // lineage clustering: a/b/g share the SAME clerk key across WAVES (scout wave0, then wave1) → the
    // SAME cluster (a persistent union-find, not recomputed from scratch); DELTA (explicit empty keys)
    // → cluster 0; EPSILON (the clerk "missed" it) → the lineageKeyOf fallback → its OWN new cluster
    expect(b.cluster).toBe(a.cluster);
    expect(g.cluster).toBe(a.cluster);
    expect(d.cluster).toBe(0);
    expect(e.cluster).not.toBe(a.cluster);
    expect(e.cluster).not.toBe(0);
    // status: GAMMA shares a's cluster but nothing else supports it → still tentative
    expect(g.status).toBe('tentative');
    // attack bookkeeping: EPSILON attacked claim A (id 1) and LANDED → A is contested + carries a counter
    expect(a.status).toBe('contested');
    expect(a.counter).toContain('EPSILON_CLAIM');
    // nullAttack: the "attack empty" lane landed NOTHING but its `note` named "c1" (claim A) as the
    // intended target → recovered into claimIds; A's attacksSurvived bumped — challenged, not silent
    expect(ledger.nullAttacks.length).toBe(1);
    expect(ledger.nullAttacks[0].topic).toBe('attack empty');
    expect(ledger.nullAttacks[0].claimIds).toEqual([a.id]);
    expect(a.attacksSurvived).toBeGreaterThanOrEqual(1);
    // vocabulary: RCT from the scout; Biomarker merged case-insensitively — uses bumped, FIRST gloss kept
    expect(ledger.vocabulary.some((t: { term: string }) => t.term === 'RCT')).toBe(true);
    const bio = ledger.vocabulary.find((t: { term: string }) => /biomarker/i.test(t.term));
    expect(bio.uses).toBe(2);
    expect(bio.gloss).toBe('a measurable indicator');
    // chao is collect-mode only — stays null here (goal mode)
    expect(result.metrics.chao).toBeNull();
    expect(result.metrics.claimsTotal).toBe(ledger.claims.length);
    expect(result.metrics.nullAttacksTotal).toBe(1);
    // yieldCalib: both lane kinds pursued this wave got an entry
    expect(rr.liveBrainers[0].yieldCalib.gap).toBeTruthy();
    expect(rr.liveBrainers[0].yieldCalib.attack).toBeTruthy();
    // the human-readable ledger view groups by status and surfaces the challenged-and-survived nullAttack
    expect(result.files['_claims.md']).toContain('## Challenged and survived');
    expect(result.files['_claims.md']).toContain('attack empty');
    expect(result.files['_claims.md']).toContain('## Vocabulary');
    // surprise folded into the attack-landed lane's OWN finding summary (no schema change)
    const waveFile = Object.keys(result.files).find((k) => k.endsWith('-wave-1.md'));
    expect(result.files[waveFile!]).toContain('⚡ SURPRISE');
  });

  it('checkpoint:true (default) logs a ⏺CKPT line at the end of each wave — zero-cost, no agent', async () => {
    const RR = await loadEngine(
      { query: 'v3 ledger test query', mode: 'goal', debug: false },
      ledgerAgent(),
    );
    const logs: string[] = [];
    globalThis.log = (m?: unknown) => logs.push(typeof m === 'string' ? m : String(m));
    await new RR().run();
    const ckptLines = logs.filter((l) => l.startsWith(CONFIG.CHECKPOINT_MARK));
    expect(ckptLines.length).toBeGreaterThan(0); // wave 1 (this fixture stops after one research wave)
    expect(ckptLines[0]).toContain(' w1 ');
    const payload = JSON.parse(ckptLines[0].slice(ckptLines[0].indexOf('{')));
    expect(payload.wave).toBe(1);
    expect(typeof payload.nullAttacks).toBe('number'); // a COUNT, not the array
  });

  it('checkpoint:false logs no ⏺CKPT line', async () => {
    const RR = await loadEngine(
      { query: 'v3 ledger test query', mode: 'goal', debug: false, checkpoint: false },
      ledgerAgent(),
    );
    const logs: string[] = [];
    globalThis.log = (m?: unknown) => logs.push(typeof m === 'string' ? m : String(m));
    await new RR().run();
    expect(logs.some((l) => l.startsWith(CONFIG.CHECKPOINT_MARK))).toBe(false);
  });
});

// ── (r2) v3 STANCE COERCION + CACHEPATH TRUST + AUDITOR REPIN — the ingest-time honesty gates.
//     Direct calls to ingestClaimSeeds (mirrors the scheduleSources B6 direct-call style above) — these are
//     small mechanical per-claim checks that do not need the whole pipeline driven end to end.
describe('ResearchReport.ingestClaimSeeds — stance coercion, cachePath trust, and auditor repin', () => {
  const globals = {
    scout: null,
    scoutRabbitHoles: [],
    highValueSources: [],
    languageGuidance: '',
  };
  const mkBs = () =>
    new BrainerState(globals as any, {
      name: 'root',
      parentName: null,
      mandate: '',
      trail: '',
      depth: 0,
    });

  it('coerces a prose stance target with a recoverable digit-run; drops one with no digits at all', async () => {
    const RR = await loadEngine({ query: 'q' }, () => null); // claim-audit/lineage both die — irrelevant here
    const rr = new RR();
    const bs = mkBs();
    bs.claims.push({
      id: 36,
      claim: 'existing claim c36',
      quote: 'q',
      source: 's',
      cluster: 0,
      audit: 'pass',
      status: 'tentative',
      attacksSurvived: 0,
      retracted: false,
      wave: 0,
      lane: 'seed',
    } as Claim);
    const fresh = await rr.ingestClaimSeeds(
      bs,
      [
        {
          lane: 'l',
          claims: [
            {
              claim: 'recoverable prose target',
              quote: 'quote text long enough to carry the fact',
              source: 's2',
              stance: { target: 'c36 (Dutch GGZ admin reduction)', kind: 'attacks' },
            },
            {
              claim: 'unrecoverable prose target',
              quote: 'another quote text long enough to carry it',
              source: 's3',
              stance: { target: 'no numeric id named at all', kind: 'attacks' },
            },
          ] as any,
        },
      ],
      1,
      'w1',
      'Research',
    );
    expect(fresh[0].stance).toEqual({ target: 36, kind: 'attacks' });
    expect(fresh[1].stance).toBeUndefined();
  });

  it('null-scrubs worker-emitted nulls (v3.2.3): null value/cachePath/entity fields ingest cleanly, and a null cachePath is NOT counted as a trust rejection', async () => {
    const RR = await loadEngine({ query: 'q' }, () => null); // dead auditor/clerk — the scrub is engine-side
    const rr = new RR();
    const bs = mkBs();
    const fresh = await rr.ingestClaimSeeds(
      bs,
      [
        {
          lane: 'l',
          claims: [
            {
              claim: 'claim with nulled optionals',
              quote: 'a quote long enough to carry the fact',
              source: 's1',
              value: null,
              cachePath: null,
              entities: { authors: null, funder: null, dataset: null, venue: 'NeurIPS' },
            },
            {
              claim: 'claim with all-null entities',
              quote: 'another quote long enough to carry it',
              source: 's2',
              entities: { funder: null, dataset: null },
            },
          ] as any,
        },
      ],
      1,
      'w1',
      'Research',
    );
    expect(fresh[0].value).toBeUndefined();
    expect(fresh[0].cachePath).toBeUndefined();
    expect(fresh[0].audit).toBe('unpinned'); // no cachePath → honestly unpinned
    expect(fresh[0].entities).toEqual({ venue: 'NeurIPS' }); // null members dropped, real one kept
    expect(fresh[1].entities).toBeUndefined(); // nothing survived the scrub
    expect(bs.cachePathsRejected).toBe(0); // a null cachePath is absent, not untrusted
  });

  it('strips an untrusted cachePath to unpinned + counts cachePathsRejected; a harvester-signature path stays pending', async () => {
    const RR = await loadEngine({ query: 'q' }, () => null); // dead auditor — verdicts never override the initial audit value
    const rr = new RR();
    const bs = mkBs();
    const fresh = await rr.ingestClaimSeeds(
      bs,
      [
        {
          lane: 'l',
          claims: [
            {
              claim: 'untrusted path claim',
              quote: 'quote text long enough to carry the fact here',
              source: 's1',
              cachePath: '/tmp/foo.txt', // not scheduler-known, no /.fetch/ signature → untrusted
            },
            {
              claim: 'trusted path claim',
              quote: 'another quote long enough to carry the fact here',
              source: 's2',
              cachePath: '/x/harvester/.fetch/html/a.md', // matches the harvester cache signature → trusted
            },
          ],
        },
      ],
      1,
      'w1',
      'Research',
    );
    const untrusted = fresh.find((c) => c.claim === 'untrusted path claim')!;
    const trusted = fresh.find((c) => c.claim === 'trusted path claim')!;
    expect(untrusted.cachePath).toBeUndefined();
    expect(untrusted.audit).toBe('unpinned');
    expect(bs.cachePathsRejected).toBe(1);
    expect(trusted.cachePath).toBe('/x/harvester/.fetch/html/a.md');
    expect(trusted.audit).toBe('pending'); // auditor never ran a verdict on it — stays pending, never guessed
  });

  it('a claim whose cachePath the scheduler itself returned this run (bs.knownCachePaths) is also trusted', async () => {
    const RR = await loadEngine({ query: 'q' }, () => null);
    const rr = new RR();
    const bs = mkBs();
    bs.knownCachePaths.add('/cache/scheduled-1.txt');
    const fresh = await rr.ingestClaimSeeds(
      bs,
      [
        {
          lane: 'l',
          claims: [
            {
              claim: 'scheduler-known path claim',
              quote: 'a quote long enough to carry the fact right here',
              source: 's1',
              cachePath: '/cache/scheduled-1.txt',
            },
          ],
        },
      ],
      1,
      'w1',
      'Research',
    );
    expect(fresh[0].cachePath).toBe('/cache/scheduled-1.txt');
    expect(bs.cachePathsRejected).toBe(0);
  });

  it('auditor repin: a verdict of repinned + newQuote replaces the quote (clipped) and passes, counting quotesRepinned; repinned with no newQuote fails', async () => {
    const repinAgent = (prompt: string, opts: AgentOpts) => {
      if (opts.label.startsWith('claim-audit-'))
        return {
          checks: [
            { id: 1, verdict: 'repinned', newQuote: 'the verified contiguous replacement span' },
            { id: 2, verdict: 'repinned' }, // no newQuote — a malformed repin, never trusted
          ],
        };
      return null; // lineage clerk dies — irrelevant to this check
    };
    const RR = await loadEngine({ query: 'q' }, repinAgent);
    const rr = new RR();
    const bs = mkBs();
    const fresh = await rr.ingestClaimSeeds(
      bs,
      [
        {
          lane: 'l',
          claims: [
            {
              claim: 'repinned with a replacement',
              quote: 'a broken ... spliced quote',
              source: 's1',
              cachePath: '/x/harvester/.fetch/html/a.md',
            },
            {
              claim: 'repinned with no replacement',
              quote: 'another broken ... spliced quote',
              source: 's2',
              cachePath: '/x/harvester/.fetch/html/b.md',
            },
          ],
        },
      ],
      1,
      'w1',
      'Research',
    );
    const withReplacement = fresh.find((c) => c.claim === 'repinned with a replacement')!;
    const withoutReplacement = fresh.find((c) => c.claim === 'repinned with no replacement')!;
    expect(withReplacement.quote).toBe('the verified contiguous replacement span');
    expect(withReplacement.audit).toBe('pass');
    expect(bs.quotesRepinned).toBe(1);
    expect(withoutReplacement.audit).toBe('fail');
  });
});

// ── (s) v3 CHAO1 — the collect-mode coverage estimate is computed + surfaced in metrics + _claims.json ──
describe('ResearchReport.run — v3 chao1 coverage estimate (collect mode)', () => {
  it('computes bs.chao after a collect-mode wave with claims', async () => {
    const RR = await loadEngine(
      { query: 'collect ledger query', mode: 'collect', debug: false },
      ledgerAgent(),
    );
    const result = await new RR().run();
    expect(result.metrics.chao).not.toBeNull();
    expect(result.metrics.chao!.coverage).toBeGreaterThanOrEqual(0);
    expect(JSON.parse(result.files['_claims.json']).chao).not.toBeNull();
  });
});

// ── (t) v3 SANITIZE — a leaked structured-output tag is stripped from the reader summary before it
//     reaches the wave file (findings scrubbing) ─────────────────────────────────────────────────────
describe('ResearchReport.run — v3 sanitize scrubs structured-output leakage from findings', () => {
  it('strips a leaked </parameter> tag from the lane summary before it reaches the wave file', async () => {
    const agent = (prompt: string, opts: AgentOpts) => {
      const L = opts.label;
      if (L === 'scout-probe:direct') return SCOUT_OUT;
      if (L === 'scout-merger') return null;
      if (L === 'prospector') return PROSPECT_OUT;
      if (L === 'brainer-w0')
        return {
          resultSoFar: RSF,
          rescore: [{ id: 1, score: 80 }],
          add: [],
          rename: [],
          drop: [],
          lookupNext: [{ id: 1 }],
          stop: { done: false, reason: 'go' },
        };
      if (L === 'brainer-w1')
        return {
          resultSoFar: RSF,
          rescore: [],
          add: [],
          rename: [],
          drop: [],
          lookupNext: [],
          stop: { done: true, reason: 'done' },
        };
      if (L.startsWith('scheduler-')) return schedulerStub(prompt);
      if (L.startsWith('validator-')) return VALIDATE_OUT;
      if (L.startsWith('lane-'))
        return {
          runningAnswer: 'clean text before the leak' + '</parameter>' + ' clean text after',
          rabbitHoles: [],
          deadEnds: [],
        };
      if (L === 'initiator') return { refinement: { facts: [] }, synthesiser: { focus: '' } };
      if (L.startsWith('judge-'))
        return {
          goalMet: true,
          verificationSound: true,
          needsCompute: false,
          computeSound: true,
          reasoning: 'ok',
          directive: '',
        };
      if (L === 'synthesiser')
        return { report: '# R', verdict: 'v', confidence: 'low', plan: [], openQuestions: [] };
      throw new Error('sanitize test: unexpected label ' + L);
    };
    const RR = await loadEngine({ query: 'q', mode: 'goal', debug: false }, agent);
    const result = await new RR().run();
    const waveFile = Object.keys(result.files).find((k) => k.endsWith('-wave-1.md'));
    expect(result.files[waveFile!]).not.toContain('</parameter>');
    expect(result.files[waveFile!]).toContain('clean text before the leak');
    expect(result.files[waveFile!]).toContain('clean text after');
  });
});

// ── (u) v3 HONEST WAVE COUNTERS in the multi-brainer path (runOneWave) — newRabbitHoles was hardcoded
//     0, and wavesRun (`wave - 1`) under-reported; both are fixed via bs.waveLog ─────────────────────
describe('ResearchReport.run — v3 honest wave counters in the multi-brainer path (runOneWave)', () => {
  it('computes newRabbitHoles (not hardcoded 0) and the real wavesRun from waveLog', async () => {
    const agent = (prompt: string, opts: AgentOpts) => {
      const L = opts.label;
      if (L === 'scout-probe:direct') return SCOUT_OUT;
      if (L === 'scout-merger') return null;
      if (L === 'prospector') return PROSPECT_OUT;
      if (L === 'brainer-w0')
        return {
          resultSoFar: RSF,
          rescore: [{ id: 1, score: 80 }],
          add: [],
          rename: [],
          drop: [],
          lookupNext: [{ id: 1 }],
          stop: { done: false, reason: 'go' },
        };
      if (L === 'brainer-w1')
        return {
          resultSoFar: RSF,
          rescore: [],
          add: [],
          rename: [],
          drop: [],
          lookupNext: [],
          stop: { done: true, reason: 'answered' },
        };
      if (L.startsWith('scheduler-')) return schedulerStub(prompt);
      if (L.startsWith('validator-')) return VALIDATE_OUT;
      if (L.startsWith('lane-'))
        return {
          runningAnswer: 'multi-brainer lane summary. ' + THICK,
          rabbitHoles: [{ keyword: 'freshly surfaced gap', why: 'new lead' }],
          deadEnds: [],
        };
      if (L === 'initiator') return { refinement: { facts: [] }, synthesiser: { focus: '' } };
      if (L.startsWith('judge-'))
        return {
          goalMet: true,
          verificationSound: true,
          needsCompute: false,
          computeSound: true,
          reasoning: 'ok',
          directive: '',
        };
      if (L === 'synthesiser')
        return { report: '# R', verdict: 'v', confidence: 'medium', plan: [], openQuestions: [] };
      throw new Error('honest-counters test: unexpected label ' + L);
    };
    const RR = await loadEngine(
      { query: 'q', mode: 'goal', maxParallelBrainers: 2, debug: false },
      agent,
    );
    const result = await new RR().run();
    const w1 = result.waveLog.find((w) => w.wave === 1)!;
    expect(w1.newRabbitHoles).toBeGreaterThan(0); // was hardcoded 0 before the fix
    expect(result.metrics.wavesRun).toBe(1); // was `wave - 1` = 0 before the fix
  });
});

// ── (u2) v3 batch 6 fix — a CHILD's collect-mode plateau window slices from ITS OWN topScoresBase, not
//     the parent's inherited history (the old bug: runOneWave always used topScores.slice(1), so a child
//     that inherited a HIGH parent peak could never plateau relative to its own trajectory — or, as tested
//     here, could be WRONGLY declared collect-dry-plateau by a peak it never earned). Unit-style: call
//     runOneWave directly on a hand-built child BrainerState (mirrors the applyDerivation unit-test pattern
//     above), so the inherited topScores/topScoresBase can be set precisely without a real spawn.
describe('ResearchReport.runOneWave — collect-mode plateau slices from the CHILD spawn point (topScoresBase)', () => {
  const mkChild = (): BrainerState => {
    const bs = new BrainerState(
      { scout: SCOUT_OUT, scoutRabbitHoles: [], highValueSources: [], languageGuidance: '' },
      { name: 'child', parentName: 'root', mandate: 'm', trail: 't', depth: 1 },
    );
    // simulate spawnBrainer: 2 inherited entries with a HIGH peak (95), then the child's own pick-first
    // score (60, index 2 = topScoresBase) — both must be excluded from the child's OWN plateau window.
    bs.topScoresBase = 2;
    bs.topScores = [20, 95, 60];
    bs.lookupNext = [
      {
        id: 1,
        keyword: 'seed lead',
        why: 'w',
        score: 60,
        scoreHistory: [{ wave: 1, score: 60 }],
        path: [],
      },
    ];
    return bs;
  };
  // one lookupNext lead per wave, scored by the caller — drives bs.topScores.push(...) deterministically.
  // withClaim: emit one ledger claim on every lane read (same quote+source each time, so only the FIRST
  // sticks — ingestClaimSeeds dedups the rest) so collect-mode's chao1 coverage reads 1.0 (a single group,
  // one source, no singleton left unseen) — otherwise the CHAO STOP ASSIST gate (a real, separate mechanism)
  // would block every plateau drain on an empty ledger's coverage=0, masking this test's actual target.
  function waveAgent(scoresByWave: Record<number, number>, withClaim = false): AgentStub {
    return (prompt: string, opts: AgentOpts) => {
      const L = opts.label;
      if (L.startsWith('scheduler-')) return schedulerStub(prompt);
      if (L.startsWith('lane-'))
        return {
          runningAnswer: 'own-wave lane summary. ' + THICK,
          rabbitHoles: [],
          nextSources: [],
          claims: withClaim
            ? [
                {
                  claim: 'own-wave finding',
                  quote: 'a verbatim quote backing the own-wave finding text',
                  source: 'https://own-wave.example.com/p',
                },
              ]
            : [],
          newTerms: [],
          deadEnds: [],
        };
      if (L.startsWith('brainer-child-w')) {
        const gw = Number(L.slice('brainer-child-w'.length));
        return {
          resultSoFar: RSF,
          rescore: [],
          add: [],
          rename: [],
          drop: [],
          lookupNext: [{ keyword: 'own-wave-' + gw + '-lead', why: 'w', score: scoresByWave[gw] }],
          stop: { done: false, reason: 'go' },
        };
      }
      throw new Error('waveAgent: unexpected label ' + L);
    };
  }

  it('does NOT drain on the inherited peak — its own post-spawn window (50,48,47) is healthy relative to its OWN peak (50), even though the old slice(1) bug would have read it against the inherited 95 (threshold 66.5 ≥ 48,47 → false plateau)', async () => {
    const RR = await loadEngine(
      { query: 'q', mode: 'collect', checkpoint: false, debug: false },
      waveAgent({ 2: 50, 3: 48, 4: 47 }),
    );
    const rr = new RR();
    const bs = mkChild();
    for (const gw of [2, 3, 4]) await rr.runOneWave(bs, gw, false, 'Research', false);
    expect(bs.topScores).toEqual([20, 95, 60, 50, 48, 47]);
    expect(bs.status).toBe('active'); // NOT drained — the fix reads the plateau against 50, not the inherited 95
    expect(bs.stopReason).not.toBe('collect-dry-plateau');
  });

  it('DOES drain when its own post-spawn window genuinely plateaus relative to its own peak (80 → 55,54 ≤ 56)', async () => {
    const RR = await loadEngine(
      { query: 'q', mode: 'collect', checkpoint: false, debug: false },
      waveAgent({ 2: 80, 3: 55, 4: 54 }, true),
    );
    const rr = new RR();
    const bs = mkChild();
    for (const gw of [2, 3, 4]) await rr.runOneWave(bs, gw, false, 'Research', false);
    expect(bs.topScores).toEqual([20, 95, 60, 80, 55, 54]);
    expect(bs.chao!.coverage).toBe(1); // one ledgered claim, one group, no singleton left unseen — clears CHAO_COVERAGE_STOP
    expect(bs.status).toBe('drained');
    expect(bs.stopReason).toBe('collect-dry-plateau');
  });
});

// ── (v) v3 SCOUT INGEST also wired into runCrawlMulti (not just runCrawl) ────────────────────────────
describe('ResearchReport.run — v3 scout ingest in the multi-brainer path', () => {
  it('seeds the root claim ledger from the scout claims/newTerms before the root brainer runs', async () => {
    const agent = (prompt: string, opts: AgentOpts) => {
      const L = opts.label;
      if (L === 'scout-probe:direct') return SCOUT_OUT_LEDGER;
      if (L === 'scout-merger') return null;
      if (L === 'prospector') return PROSPECT_OUT;
      if (L === 'claim-audit-scout') return claimAuditStub(prompt);
      if (L === 'lineage-scout') return lineageClerkStub(prompt);
      if (L === 'brainer-w0')
        return {
          resultSoFar: RSF,
          rescore: [],
          add: [],
          rename: [],
          drop: [],
          lookupNext: [],
          stop: { done: true, reason: 'nothing to pursue' },
        };
      if (L === 'initiator') return { refinement: { facts: [] }, synthesiser: { focus: '' } };
      if (L.startsWith('judge-'))
        return {
          goalMet: true,
          verificationSound: true,
          needsCompute: false,
          computeSound: true,
          reasoning: 'ok',
          directive: '',
        };
      if (L === 'synthesiser')
        return { report: '# R', verdict: 'v', confidence: 'medium', plan: [], openQuestions: [] };
      throw new Error('multi scout-ingest test: unexpected label ' + L);
    };
    const RR = await loadEngine(
      { query: 'q', mode: 'goal', maxParallelBrainers: 2, debug: false },
      agent,
    );
    const result = await new RR().run();
    const ledger = JSON.parse(result.files['_claims.json']);
    expect(ledger.claims.some((c: { claim: string }) => c.claim.includes('SCOUT_CLAIM'))).toBe(
      true,
    );
    expect(ledger.vocabulary.some((t: { term: string }) => t.term === 'RCT')).toBe(true);
  });
});

// ── (w) v3 STEERING — the brainer's derivation store + rerun wiring (batch 3): a brainer-authored
//     derivation gets STORED, the rerunner fires the NEXT wave (label rerun-w<wave>), and the sensitivity
//     it produces shows up in the FOLLOWING brainer's own prompt; a later failed rerun marks it STALE
//     while keeping the last good numbers — never blocking the crawl. ─────────────────────────────────
function derivationAgent(prompt: string, opts: AgentOpts) {
  const L = opts.label;
  if (L === 'scout-probe:direct') return SCOUT_OUT;
  if (L === 'scout-merger') return null;
  if (L === 'prospector') return PROSPECT_OUT;
  if (L === 'brainer-w0')
    return {
      resultSoFar: RSF,
      rescore: [{ id: 1, score: 80 }],
      add: [],
      lookupNext: [{ id: 1 }],
      rename: [],
      drop: [],
      stop: { done: false, reason: 'go' },
    };
  if (L === 'brainer-w1')
    return {
      resultSoFar: RSF,
      rescore: [],
      add: [],
      lookupNext: [{ keyword: 'w2 lane', why: 'continue', score: 70 }],
      rename: [],
      drop: [],
      derivation: {
        code: 'print(1)',
        inputs: [{ name: 'x', dist: 'wide', claimIds: [], prior: true }],
      },
      stop: { done: false, reason: 'derive' },
    };
  if (L === 'brainer-w2')
    // re-emits the SAME code (only re-authoring, not changing it) → dirties the derivation again for w3
    // while (per the sameCode rule) keeping the lastRun the w2 rerun just produced.
    return {
      resultSoFar: RSF,
      rescore: [],
      add: [],
      lookupNext: [{ keyword: 'w3 lane', why: 'continue', score: 60 }],
      rename: [],
      drop: [],
      derivation: {
        code: 'print(1)',
        inputs: [{ name: 'x', dist: 'wide', claimIds: [], prior: true }],
      },
      stop: { done: false, reason: 'still deriving' },
    };
  if (L === 'brainer-w3')
    return {
      resultSoFar: RSF,
      rescore: [],
      add: [],
      lookupNext: [],
      rename: [],
      drop: [],
      stop: { done: true, reason: 'done' },
    };
  if (L === 'rerun-w2') return { ok: true, quantiles: { p50: 100 }, sensitivity: { x: 0.8 } };
  if (L === 'rerun-w3') return { ok: false, note: 'script exploded' }; // a normal (non-throwing) failure
  if (L.startsWith('scheduler-')) return schedulerStub(prompt);
  if (L.startsWith('validator-')) return VALIDATE_OUT;
  if (L.startsWith('lane-')) return LANE_OUT;
  if (L === 'initiator') return { refinement: { facts: [] }, synthesiser: { focus: '' } };
  if (L.startsWith('judge-'))
    return {
      goalMet: true,
      verificationSound: true,
      needsCompute: false,
      computeSound: true,
      reasoning: 'ok',
      directive: '',
    };
  if (L === 'synthesiser')
    return { report: '# R', verdict: 'v', confidence: 'medium', plan: [], openQuestions: [] };
  throw new Error('derivationAgent: unexpected label ' + L);
}

describe('ResearchReport.run — v3 derivation store + rerun wiring (batch 3)', () => {
  it('stores the derivation, reruns it next wave, and surfaces DERIVATION STATE in the FOLLOWING brainer prompt; a later failed rerun marks it STALE while keeping the last good numbers', async () => {
    const RR = await loadEngine({ query: 'q', mode: 'goal', debug: false }, derivationAgent);
    const rr = new RR();
    const result = await rr.run();
    expect(result.stopReason).toBe('brainer-done');
    const wave2Key = Object.keys(result.files).find((k) => k.endsWith('-wave-2.md'))!;
    const wave3Key = Object.keys(result.files).find((k) => k.endsWith('-wave-3.md'))!;
    // wave 2's brainer prompt (captured via withPrompt) shows the sensitivity the w2 rerun just produced.
    expect(result.files[wave2Key]).toContain('DERIVATION STATE');
    expect(result.files[wave2Key]).toContain('p50=100');
    expect(result.files[wave2Key]).toContain('x: 0.80');
    expect(result.files[wave2Key]).not.toContain('STALE');
    // wave 3's rerun died → stale flag shown, but the last GOOD numbers (from w2) are still there, not wiped.
    expect(result.files[wave3Key]).toContain('DERIVATION STATE');
    expect(result.files[wave3Key]).toContain('p50=100');
    expect(result.files[wave3Key]).toContain('STALE — last rerun failed');
    expect(rr.liveBrainers[0].derivation!.lastRun).toEqual({
      quantiles: { p50: 100 },
      sensitivity: { x: 0.8 },
      wave: 2,
    });
    expect(rr.liveBrainers[0].derivationStale).toBe(true);
  });
});

// ── (w2) v3 STEERING — applyDerivation / maybeRerunDerivation unit coverage (the store+rerun rules
//     directly, mirroring the scheduleSources id-discipline unit-test pattern above) ─────────────────
describe('ResearchReport.applyDerivation / maybeRerunDerivation — unit coverage (batch 3)', () => {
  const mkBs = () =>
    new BrainerState(
      { scout: null, scoutRabbitHoles: [], highValueSources: [], languageGuidance: '' },
      { name: 'root', parentName: null, mandate: '', trail: '', depth: 0 },
    );

  it('applyDerivation stores a fresh delta, dirties it, lastRun null for genuinely NEW code', async () => {
    const RR = await loadEngine({ query: 'q' }, () => ({}));
    const rr = new RR();
    const bs = mkBs();
    rr.applyDerivation(bs, {
      derivation: {
        code: 'print(1)',
        inputs: [{ name: 'x', dist: 'wide', claimIds: [], prior: true }],
      },
    } as never);
    expect(bs.derivation).toEqual({
      code: 'print(1)',
      inputs: [{ name: 'x', dist: 'wide', claimIds: [], prior: true }],
      lastRun: null,
    });
    expect(bs.derivationDirty).toBe(true);
  });

  it('applyDerivation KEEPS lastRun when the re-emitted code is byte-identical (only inputs changed)', async () => {
    const RR = await loadEngine({ query: 'q' }, () => ({}));
    const rr = new RR();
    const bs = mkBs();
    const priorRun = { quantiles: { p50: 1 }, sensitivity: {}, wave: 2 };
    bs.derivation = { code: 'print(1)', inputs: [], lastRun: priorRun };
    rr.applyDerivation(bs, {
      derivation: {
        code: 'print(1)',
        inputs: [{ name: 'y', dist: 'wide', claimIds: [3], prior: false }],
      },
    } as never);
    expect(bs.derivation!.lastRun).toEqual(priorRun); // kept — same code
    expect(bs.derivation!.inputs).toEqual([
      { name: 'y', dist: 'wide', claimIds: [3], prior: false },
    ]);
    expect(bs.derivationDirty).toBe(true); // always dirtied
  });

  it('applyDerivation is a no-op when compute is off, or coord carries no derivation', async () => {
    const off = await loadEngine({ query: 'q', compute: false }, () => ({}));
    const rrOff = new off();
    const bsOff = mkBs();
    rrOff.applyDerivation(bsOff, {
      derivation: { code: 'x', inputs: [] },
    } as never);
    expect(bsOff.derivation).toBe(null);
    const on = await loadEngine({ query: 'q' }, () => ({}));
    const rrOn = new on();
    const bsOn = mkBs();
    rrOn.applyDerivation(bsOn, {} as never);
    expect(bsOn.derivation).toBe(null);
    expect(bsOn.derivationDirty).toBe(false);
  });

  it('maybeRerunDerivation reruns when dirty, stores lastRun, clears dirty/stale', async () => {
    const RR = await loadEngine({ query: 'q' }, (_p: string, o: AgentOpts) =>
      o.label === 'rerun-w3'
        ? { ok: true, quantiles: { p50: 42 }, sensitivity: { a: 0.9 } }
        : (() => {
            throw new Error('unexpected label ' + o.label);
          })(),
    );
    const rr = new RR();
    const bs = mkBs();
    bs.wave = 3; // runRerunner labels off bs.wave, not the method's `wave` param — keep them in step
    bs.derivation = { code: 'x', inputs: [], lastRun: null };
    bs.derivationDirty = true;
    await rr.maybeRerunDerivation(bs, 3, 'Research');
    expect(bs.derivation!.lastRun).toEqual({
      quantiles: { p50: 42 },
      sensitivity: { a: 0.9 },
      wave: 3,
    });
    expect(bs.derivationDirty).toBe(false);
    expect(bs.derivationStale).toBe(false);
  });

  it('a changed input claim (not dirty) still triggers a rerun; an untouched one skips it', async () => {
    let calls = 0;
    const RR = await loadEngine({ query: 'q' }, () => {
      calls++;
      return { ok: true, quantiles: {}, sensitivity: {} };
    });
    const rr = new RR();
    const bs = mkBs();
    bs.derivation = {
      code: 'x',
      inputs: [{ name: 'a', dist: 'd', claimIds: [7], prior: false }],
      lastRun: { quantiles: {}, sensitivity: {}, wave: 1 },
    };
    bs.derivationDirty = false;
    bs.lastChangedClaimIds = new Set([7]); // claim 7 (an input's claimId) changed this wave
    await rr.maybeRerunDerivation(bs, 2, 'Research');
    expect(calls).toBe(1);
  });

  it('an untouched claim (no dirty, no matching change) skips the rerun entirely — no agent call', async () => {
    let calls = 0;
    const RR = await loadEngine({ query: 'q' }, () => {
      calls++;
      return { ok: true };
    });
    const rr = new RR();
    const bs = mkBs();
    bs.derivation = {
      code: 'x',
      inputs: [{ name: 'a', dist: 'd', claimIds: [7], prior: false }],
      lastRun: { quantiles: {}, sensitivity: {}, wave: 1 },
    };
    bs.derivationDirty = false;
    bs.lastChangedClaimIds = new Set([99]); // unrelated claim
    await rr.maybeRerunDerivation(bs, 2, 'Research');
    expect(calls).toBe(0);
  });

  it('a dead/failing rerunner keeps lastRun and marks it stale (never blocks)', async () => {
    const RR = await loadEngine({ query: 'q' }, () => ({ ok: false, note: 'boom' }));
    const rr = new RR();
    const bs = mkBs();
    const priorRun = { quantiles: { p50: 1 }, sensitivity: {}, wave: 1 };
    bs.derivation = { code: 'x', inputs: [], lastRun: priorRun };
    bs.derivationDirty = true;
    await rr.maybeRerunDerivation(bs, 2, 'Research');
    expect(bs.derivation!.lastRun).toEqual(priorRun);
    expect(bs.derivationStale).toBe(true);
  });

  it('compute:false ⇒ never reruns even when dirty', async () => {
    let calls = 0;
    const RR = await loadEngine({ query: 'q', compute: false }, () => {
      calls++;
      return {};
    });
    const rr = new RR();
    const bs = mkBs();
    bs.derivation = { code: 'x', inputs: [], lastRun: null };
    bs.derivationDirty = true;
    await rr.maybeRerunDerivation(bs, 1, 'Research');
    expect(calls).toBe(0);
  });
});

// ── (x) v3 CHAO STOP ASSIST — a collect-mode plateau alone is not enough: low coverage blocks the dry
//     stop (continues to the wave cap), high coverage lets it through (batch 3) ─────────────────────
function chaoGateAgent(convergent: boolean) {
  let laneCalls = 0;
  return (prompt: string, opts: AgentOpts) => {
    const L = opts.label;
    if (L === 'scout-probe:direct') return SCOUT_OUT;
    if (L === 'scout-merger') return null;
    if (L === 'prospector') return PROSPECT_OUT;
    if (L.startsWith('brainer-w')) {
      const w = Number(L.slice('brainer-w'.length));
      const score = w <= 1 ? 100 : 50; // peak at w1, plateau at ≤0.7×peak from w2 on
      return {
        resultSoFar: RSF,
        rescore: [],
        add: [],
        lookupNext: [{ keyword: 'chao lead ' + w, why: 'breadth', score }],
        rename: [],
        drop: [],
        stop: { done: false, reason: 'still collecting' },
      };
    }
    if (L.startsWith('scheduler-')) return schedulerStub(prompt);
    if (L.startsWith('validator-')) return VALIDATE_OUT;
    if (L.startsWith('lane-')) {
      laneCalls++;
      // convergent: the SAME finding from a DISTINCT source each wave → one multiply-corroborated
      // species (chao1 coverage → 1). non-convergent: a genuinely DIFFERENT finding each wave → every
      // species stays a singleton (coverage stays low) — the gate must tell these apart.
      return {
        runningAnswer: 'collected',
        rabbitHoles: [],
        nextSources: [],
        deadEnds: [],
        claims: [
          {
            claim: convergent ? 'the recurring landscape fact' : 'unique fact number ' + laneCalls,
            quote: convergent
              ? 'the recurring landscape fact keeps turning up across independently examined sources'
              : 'unique fact number ' +
                laneCalls +
                ' appears only in this one specific source text',
            source: 'https://source.example.com/' + laneCalls,
          },
        ],
      };
    }
    if (L === 'initiator') return { refinement: { facts: [] }, synthesiser: { focus: '' } };
    if (L.startsWith('judge-'))
      return {
        goalMet: true,
        verificationSound: true,
        needsCompute: false,
        computeSound: true,
        reasoning: 'ok',
        directive: '',
      };
    if (L === 'synthesiser')
      return { report: '# R', verdict: 'v', confidence: 'medium', plan: [], openQuestions: [] };
    throw new Error('chaoGateAgent: unexpected label ' + L);
  };
}

describe('ResearchReport.run — v3 CHAO stop assist gates the collect dry-plateau on coverage (batch 3)', () => {
  it('blocks the dry stop when the plateau holds but coverage stays low (runs to the wave cap instead)', async () => {
    const RR = await loadEngine(
      { query: 'q', mode: 'collect', maxWave: 4, debug: false },
      chaoGateAgent(false),
    );
    const result = await new RR().run();
    expect(result.stopReason).toBe('wave-cap'); // NOT collect-dry-plateau — coverage never caught up
    expect(result.metrics.chao!.coverage).toBeLessThan(0.9);
  });
  it('allows the dry stop once coverage reaches the threshold', async () => {
    const RR = await loadEngine(
      { query: 'q', mode: 'collect', maxWave: 5, debug: false },
      chaoGateAgent(true),
    );
    const result = await new RR().run();
    expect(result.stopReason).toBe('collect-dry-plateau');
    expect(result.metrics.chao!.coverage).toBeGreaterThanOrEqual(0.9);
  });
});

// ── (y) v3 VENUE YIELD — a venue assigned to ≥2 lanes that both land nothing gets a ⚠ suffix on its
//     goodFor in the very next brainer prompt (batch 3) ──────────────────────────────────────────────
function venueWarnAgent(prompt: string, opts: AgentOpts) {
  const L = opts.label;
  if (L === 'scout-probe:direct') return SCOUT_OUT; // seeds id1 'hnsw tuning', id2 'sharding'
  if (L === 'scout-merger') return null;
  if (L === 'prospector') return PROSPECT_OUT; // highValueSources incl. {source:'arXiv (site:arxiv.org)', goodFor:'ANN'}
  if (L === 'brainer-w0')
    return {
      resultSoFar: RSF,
      rescore: [
        { id: 1, score: 80 },
        { id: 2, score: 70 },
      ],
      add: [],
      lookupNext: [
        { id: 1, sources: ['arXiv (site:arxiv.org)'] },
        { id: 2, sources: ['arXiv (site:arxiv.org)'] },
      ],
      rename: [],
      drop: [],
      stop: { done: false, reason: 'go' },
    };
  if (L === 'brainer-w1')
    return {
      resultSoFar: RSF,
      rescore: [],
      add: [],
      lookupNext: [],
      rename: [],
      drop: [],
      stop: { done: true, reason: 'done' },
    };
  if (L.startsWith('scheduler-')) return schedulerStub(prompt);
  if (L.startsWith('validator-')) return VALIDATE_OUT;
  // both lanes land NOTHING — no claims, no fresh leads — a genuine 0-yield wave for the shared venue.
  if (L.startsWith('lane-'))
    return { runningAnswer: 'came up empty', rabbitHoles: [], nextSources: [], deadEnds: [] };
  if (L === 'initiator') return { refinement: { facts: [] }, synthesiser: { focus: '' } };
  if (L.startsWith('judge-'))
    return {
      goalMet: true,
      verificationSound: true,
      needsCompute: false,
      computeSound: true,
      reasoning: 'ok',
      directive: '',
    };
  if (L === 'synthesiser')
    return { report: '# R', verdict: 'v', confidence: 'medium', plan: [], openQuestions: [] };
  throw new Error('venueWarnAgent: unexpected label ' + L);
}

describe('ResearchReport.run — v3 venue yield warning (batch 3)', () => {
  it('flags a venue assigned to ≥2 lanes with 0 yield via a ⚠ suffix on its goodFor', async () => {
    const RR = await loadEngine({ query: 'q', mode: 'goal', debug: false }, venueWarnAgent);
    const result = await new RR().run();
    const wave1Key = Object.keys(result.files).find((k) => k.endsWith('-wave-1.md'))!;
    expect(result.files[wave1Key]).toContain('arXiv (site:arxiv.org)');
    expect(result.files[wave1Key]).toContain('ANN — ⚠ 0 yield in 2 lanes');
  });
});

// ── (z) v3 SCHEDULER VOCABULARY — the field's own terms of art thread into the scheduler prompt once
//     the scout has seeded one (batch 3) ─────────────────────────────────────────────────────────────
describe('ResearchReport.run — v3 scheduler carries the community vocabulary (batch 3)', () => {
  it('threads bs.vocabulary into the scheduler prompt once the scout has seeded a term', async () => {
    const schedulerPrompts: string[] = [];
    const agent = (prompt: string, opts: AgentOpts) => {
      const L = opts.label;
      if (L.startsWith('scheduler-')) schedulerPrompts.push(prompt);
      if (L === 'scout-probe:direct') return SCOUT_OUT_LEDGER; // carries newTerms: [{term:'RCT', ...}]
      if (L === 'scout-merger') return null;
      if (L === 'prospector') return PROSPECT_OUT;
      if (L === 'claim-audit-scout') return claimAuditStub(prompt);
      if (L === 'lineage-scout') return lineageClerkStub(prompt);
      if (L === 'brainer-w0')
        // SCOUT_OUT_LEDGER carries no pages/rabbitHoles (only claims/newTerms) — originate the wave-1
        // lane by keyword rather than looking up a nonexistent seeded id.
        return {
          resultSoFar: RSF,
          rescore: [],
          add: [],
          lookupNext: [{ keyword: 'w1 lane', why: 'go', score: 80 }],
          rename: [],
          drop: [],
          stop: { done: false, reason: 'go' },
        };
      if (L === 'brainer-w1')
        return {
          resultSoFar: RSF,
          rescore: [],
          add: [],
          lookupNext: [],
          rename: [],
          drop: [],
          stop: { done: true, reason: 'done' },
        };
      if (L.startsWith('scheduler-')) return schedulerStub(prompt);
      if (L.startsWith('validator-')) return VALIDATE_OUT;
      if (L.startsWith('lane-')) return LANE_OUT;
      if (L === 'initiator') return { refinement: { facts: [] }, synthesiser: { focus: '' } };
      if (L.startsWith('judge-'))
        return {
          goalMet: true,
          verificationSound: true,
          needsCompute: false,
          computeSound: true,
          reasoning: 'ok',
          directive: '',
        };
      if (L === 'synthesiser')
        return { report: '# R', verdict: 'v', confidence: 'medium', plan: [], openQuestions: [] };
      throw new Error('scheduler-vocab test: unexpected label ' + L);
    };
    const RR = await loadEngine({ query: 'q', mode: 'goal', debug: false }, agent);
    await new RR().run();
    expect(schedulerPrompts.length).toBeGreaterThan(0);
    expect(schedulerPrompts.some((p) => p.includes('COMMUNITY VOCABULARY'))).toBe(true);
    expect(schedulerPrompts.some((p) => p.includes('RCT (1)'))).toBe(true);
  });
});

// ═══════════════════════════════════════════════════════════════════════════════════════════════
// v3 BATCH 4 — FINALIZE/verification rewire: refiner attack-recording, judge retraction, synthesiser
// citation lint + confidence floor, reopenCrawl HARVEST, and the multi-brainer CHILD→PARENT CLAIM MERGE.
// ═══════════════════════════════════════════════════════════════════════════════════════════════

// a fully-valid minimal ResultSoFar (every required field present) — local to this section so it does not
// collide with the file's own module-level RSF fixture (which carries a fixed keyClaimIds:[]).
const RSF_MIN2 = {
  answer: '',
  confidence: 'medium',
  evidence: [],
  resolved: [],
  openGaps: [],
  tensions: [],
  working: '',
};

// ── (aa) refiner attack-recording + initiator claimId threading, driven end-to-end through runFinalize ──
describe('ResearchReport.run — v3 FINALIZE refiner attack-recording + initiator claimId threading (batch 4)', () => {
  it('threads claimId from the initiator into the refine prompt (THE CLAIM AS PINNED), then folds the outcome into the ledger: survived → nullAttack + attacksSurvived; counterFound → contested', async () => {
    const refinePrompts: Record<string, string> = {};
    const agent = (prompt: string, opts: AgentOpts) => {
      const L = opts.label;
      refinePrompts[L] = prompt;
      if (L === 'scout-probe:direct')
        return {
          landscape: 'l',
          pages: [],
          claims: [
            {
              claim: 'claim one — survives an attack',
              quote: 'the exact verbatim span backing claim one',
              source: 'https://a.example.com/1',
              cachePath: '/cache/1.txt',
            },
            {
              claim: 'claim two — a counter turns up',
              quote: 'the exact verbatim span backing claim two',
              source: 'https://b.example.com/2',
              cachePath: '/cache/2.txt',
            },
          ],
          deadEnds: [],
        };
      if (L === 'scout-merger') return null;
      if (L === 'prospector') return PROSPECT_OUT;
      if (L === 'claim-audit-scout')
        return {
          checks: [
            { id: 1, verdict: 'pass' },
            { id: 2, verdict: 'pass' },
          ],
        };
      if (L === 'lineage-scout') return { links: [] }; // deterministic lineageKeyOf fallback — irrelevant to this test
      if (L === 'brainer-w0')
        return {
          resultSoFar: { ...RSF_MIN2, answer: 'seeded', keyClaimIds: [1, 2] },
          rescore: [],
          add: [],
          rename: [],
          drop: [],
          lookupNext: [],
          stop: { done: true, reason: 'seeded and answered' },
        };
      if (L === 'initiator')
        return {
          refinement: {
            facts: [
              { fact: 'claim one — survives an attack', why: 'load-bearing', claimId: 1 },
              { fact: 'claim two — a counter turns up', why: 'load-bearing', claimId: 2 },
            ],
          },
          synthesiser: { focus: '' },
        };
      if (L === 'refine-0')
        return {
          report: 'claim one, hardened',
          queriesTried: ['counter-search for claim one'],
          counterFound: false,
        };
      if (L === 'refine-1')
        return {
          report: 'claim two, hardened',
          queriesTried: ['counter-search for claim two'],
          counterFound: true,
          counterNote: 'a real contradiction turned up',
        };
      if (L.startsWith('judge-'))
        return {
          goalMet: true,
          verificationSound: true,
          needsCompute: false,
          computeSound: true,
          reasoning: 'upheld',
          directive: '',
          retractClaimIds: [],
        };
      if (L === 'synthesiser')
        return { report: '# R', verdict: 'v', confidence: 'medium', plan: [], openQuestions: [] };
      throw new Error('refiner-attack test: unexpected label ' + L);
    };
    const RR = await loadEngine({ query: 'q', mode: 'goal', debug: false }, agent);
    const rr = new RR();
    await rr.run();
    const bs = rr.liveBrainers[0];

    // initiator → refiner claimId threading: the refine prompt for the claimId-bound fact carries the
    // ledger claim's OWN pinned quote/source, not just the fact text.
    expect(refinePrompts['refine-0']).toContain('THE CLAIM AS PINNED');
    expect(refinePrompts['refine-0']).toContain('the exact verbatim span backing claim one');
    expect(refinePrompts['refine-1']).toContain('the exact verbatim span backing claim two');
    expect(refinePrompts['refine-1']).toContain('https://b.example.com/2');

    // survived (counterFound:false) → a completed counter-search that found nothing is first-class state
    const claim1 = bs.claims.find((c: Claim) => c.id === 1)!;
    expect(claim1.attacksSurvived).toBe(1);
    expect(claim1.status).not.toBe('contested');
    const na = bs.nullAttacks.find((n) => n.claimIds.includes(1))!;
    expect(na).toBeTruthy();
    expect(na.topic).toBe('claim one — survives an attack');
    expect(na.queries).toEqual(['counter-search for claim one']);

    // counterFound:true → contested, with the counter note recorded
    const claim2 = bs.claims.find((c: Claim) => c.id === 2)!;
    expect(claim2.counter).toBe('a real contradiction turned up');
    expect(claim2.status).toBe('contested');
  });
});

// ── (bb) judge retraction — unit coverage on applyJudgeRetractions directly ──
describe('ResearchReport.applyJudgeRetractions — unit coverage (batch 4)', () => {
  const mkBs = () =>
    new BrainerState(
      { scout: null, scoutRabbitHoles: [], highValueSources: [], languageGuidance: '' },
      { name: 'root', parentName: null, mandate: '', trail: '', depth: 0 },
    );
  const mkClaim = (id: number, over: Partial<Claim> = {}): Claim => ({
    id,
    claim: 'claim ' + id,
    quote: 'q' + id,
    source: 'https://x.example.com/' + id,
    cluster: id,
    audit: 'pass',
    status: 'tentative',
    attacksSurvived: 0,
    retracted: false,
    wave: 1,
    lane: 'l',
    ...over,
  });
  const judgeOut = (over: Partial<JudgeOut>) => ({
    goalMet: true,
    verificationSound: true,
    needsCompute: false,
    computeSound: true,
    reasoning: 'x',
    ...over,
  });

  it('retracts real ids + recomputes statuses; a hallucinated id (not in the ledger) is ignored', async () => {
    const RR = await loadEngine({ query: 'q' }, () => {
      throw new Error('no agent expected — no derivation input was retracted');
    });
    const rr = new RR();
    const bs = mkBs();
    bs.claims = [mkClaim(1), mkClaim(2)];
    await rr.applyJudgeRetractions(bs, judgeOut({ retractClaimIds: [1, 999] }), 'Finalize');
    expect(bs.claims.find((c) => c.id === 1)!.retracted).toBe(true);
    expect(bs.claims.find((c) => c.id === 2)!.retracted).toBe(false); // untouched
  });

  it('a null judgement, or one with no/empty retractClaimIds, is a no-op (degrade-to-null)', async () => {
    const RR = await loadEngine({ query: 'q' }, () => {
      throw new Error('no agent expected');
    });
    const rr = new RR();
    const bs = mkBs();
    bs.claims = [mkClaim(1)];
    await rr.applyJudgeRetractions(bs, null, 'Finalize');
    await rr.applyJudgeRetractions(bs, judgeOut({ retractClaimIds: [] }), 'Finalize');
    await rr.applyJudgeRetractions(bs, judgeOut({}), 'Finalize');
    expect(bs.claims[0].retracted).toBe(false);
  });

  it('an id that is ONLY hallucinated (no real id in the batch) never fires the rerunner', async () => {
    let calls = 0;
    const RR = await loadEngine({ query: 'q' }, () => {
      calls++;
      return { ok: true };
    });
    const rr = new RR();
    const bs = mkBs();
    bs.claims = [mkClaim(1)];
    bs.derivation = {
      code: 'x',
      inputs: [{ name: 'x', dist: 'd', claimIds: [1], prior: false }],
      lastRun: { quantiles: {}, sensitivity: {}, wave: 1 },
    };
    await rr.applyJudgeRetractions(bs, judgeOut({ retractClaimIds: [999] }), 'Finalize');
    expect(calls).toBe(0);
    expect(bs.derivation.lastRun).toEqual({ quantiles: {}, sensitivity: {}, wave: 1 }); // untouched
  });

  it('fires ONE bounded rerunner call when a retracted claim backed a derivation input, refreshing lastRun', async () => {
    let calls = 0;
    const RR = await loadEngine({ query: 'q' }, (_p: string, o: AgentOpts) => {
      calls++;
      expect(o.label).toBe('rerun-w4');
      return { ok: true, quantiles: { p50: 7 }, sensitivity: { x: 0.9 } };
    });
    const rr = new RR();
    const bs = mkBs();
    bs.wave = 4;
    bs.claims = [mkClaim(1)];
    bs.derivation = {
      code: 'x',
      inputs: [{ name: 'x', dist: 'd', claimIds: [1], prior: false }],
      lastRun: { quantiles: {}, sensitivity: {}, wave: 1 },
    };
    await rr.applyJudgeRetractions(bs, judgeOut({ retractClaimIds: [1] }), 'Finalize');
    expect(calls).toBe(1);
    expect(bs.derivation.lastRun).toEqual({
      quantiles: { p50: 7 },
      sensitivity: { x: 0.9 },
      wave: 4,
    });
  });

  it('does NOT fire the rerunner when the retracted claim is not a derivation input', async () => {
    let calls = 0;
    const RR = await loadEngine({ query: 'q' }, () => {
      calls++;
      return { ok: true };
    });
    const rr = new RR();
    const bs = mkBs();
    bs.claims = [mkClaim(1), mkClaim(2)];
    bs.derivation = {
      code: 'x',
      inputs: [{ name: 'x', dist: 'd', claimIds: [2], prior: false }],
      lastRun: { quantiles: {}, sensitivity: {}, wave: 1 },
    };
    await rr.applyJudgeRetractions(bs, judgeOut({ retractClaimIds: [1] }), 'Finalize');
    expect(calls).toBe(0);
  });
});

// ── (cc) synthesiser finish — citation lint + confidence floor, unit coverage on applyReportFinish ──
describe('ResearchReport.applyReportFinish — citation lint + confidence floor (batch 4)', () => {
  const mkBs = () =>
    new BrainerState(
      { scout: null, scoutRabbitHoles: [], highValueSources: [], languageGuidance: '' },
      { name: 'root', parentName: null, mandate: '', trail: '', depth: 0 },
    );
  const mkClaim = (id: number, over: Partial<Claim> = {}): Claim => ({
    id,
    claim: 'claim ' + id,
    quote: 'q',
    source: 's',
    cluster: id,
    audit: 'pass',
    status: 'tentative',
    attacksSurvived: 0,
    retracted: false,
    wave: 1,
    lane: 'l',
    ...over,
  });

  it('lints [cN] markers: strips unknown + retracted ids (counted + logged), keeps real live ones', async () => {
    const RR = await loadEngine({ query: 'q' }, () => ({}));
    const rr = new RR();
    const bs = mkBs();
    bs.claims = [mkClaim(1, { status: 'settled' }), mkClaim(2, { retracted: true })];
    bs.resultSoFar = { ...RSF_MIN2, keyClaimIds: [1] };
    const agg = {
      report: 'the answer [c1] holds, but [c2] and [c9] do not.',
      verdict: 'v',
      confidence: 'medium' as const,
      plan: [],
      openQuestions: [],
    };
    rr.applyReportFinish(bs, agg, 'test');
    expect(bs.reportOk).toBe(true);
    expect(bs.citationsBogus).toBe(2);
    expect(agg.report).toContain('the answer [c1] holds, but  and  do not.');
    expect(rr.files['result.md']).toContain('the answer [c1] holds');
  });

  it('confidence floor LOWERS a high stated confidence to the computed value, appending the adjustment note', async () => {
    const RR = await loadEngine({ query: 'q' }, () => ({}));
    const rr = new RR();
    const bs = mkBs();
    // a CONTESTED key claim → computedConfidence is 'low' regardless of how confident the synthesiser felt
    bs.claims = [mkClaim(1, { status: 'contested' })];
    bs.resultSoFar = { ...RSF_MIN2, keyClaimIds: [1] };
    const agg = {
      report: '# body',
      verdict: 'v',
      confidence: 'high' as const,
      plan: [],
      openQuestions: [],
    };
    rr.applyReportFinish(bs, agg, 'test');
    expect(agg.confidence).toBe('low');
    expect(agg.report).toContain('Confidence adjusted from high to low');
    expect(agg.report).toContain('computed from evidence topology');
  });

  it('confidence floor NEVER RAISES a low stated confidence even when computed is high', async () => {
    const RR = await loadEngine({ query: 'q' }, () => ({}));
    const rr = new RR();
    const bs = mkBs();
    // every key claim settled → computedConfidence is 'high', but the synthesiser stated 'low'
    bs.claims = [mkClaim(1, { status: 'settled' })];
    bs.resultSoFar = { ...RSF_MIN2, keyClaimIds: [1] };
    const agg = {
      report: '# body',
      verdict: 'v',
      confidence: 'low' as const,
      plan: [],
      openQuestions: [],
    };
    rr.applyReportFinish(bs, agg, 'test');
    expect(agg.confidence).toBe('low'); // unchanged — never raised
    expect(agg.report).not.toContain('Confidence adjusted');
  });

  it('a dead synthesiser (agg null) sets reportOk false and citationsBogus 0, never throws', async () => {
    const RR = await loadEngine({ query: 'q' }, () => ({}));
    const rr = new RR();
    const bs = mkBs();
    rr.applyReportFinish(bs, null, 'test');
    expect(bs.reportOk).toBe(false);
    expect(bs.citationsBogus).toBe(0);
    expect(bs.synthesiserOut).toBe(null);
  });
});

// ── (dd) reopenCrawl HARVEST — unit coverage directly on the method ──
describe('ResearchReport.reopenCrawl — v3 HARVEST (batch 4)', () => {
  it('ingests the reopen readers claims into the ledger, harvests fresh rabbitHoles/nextSources into the store, and applies the coord deltas (not just resultSoFar)', async () => {
    const agent = (prompt: string, opts: AgentOpts) => {
      const L = opts.label;
      if (L.startsWith('scheduler-')) return schedulerStub(prompt);
      if (L.startsWith('lineage-')) return { links: [] };
      if (L.startsWith('lane-'))
        return {
          runningAnswer: 'reopen finding',
          rabbitHoles: [{ keyword: 'fresh gap', why: 'follow-on' }],
          nextSources: [{ ref: 'https://cited.example.com/x', why: 'top citation' }],
          deadEnds: [],
          claims: [
            {
              claim: 'reopen claim',
              quote: 'a verbatim reopen quote long enough to matter here today',
              source: 'https://reopen.example.com/a',
            },
          ],
          newTerms: [],
        };
      if (L.startsWith('brainer-'))
        return {
          resultSoFar: { ...RSF_MIN2, answer: 'folded' },
          rescore: [],
          add: [{ keyword: 'parked lead', why: 'later', score: 40 }],
          rename: [],
          drop: [],
          lookupNext: [],
          stop: { done: false, reason: 'folded' },
        };
      throw new Error('reopenCrawl HARVEST test: unexpected label ' + L);
    };
    const RR = await loadEngine({ query: 'q', mode: 'goal' }, agent);
    const rr = new RR();
    const bs = new BrainerState(
      {
        scout: { landscape: 'l', pages: [], deadEnds: [] },
        scoutRabbitHoles: [],
        highValueSources: [],
        languageGuidance: '',
      },
      { name: 'root', parentName: null, mandate: '', trail: '', depth: 0 },
    );
    bs.wave = 2;
    const before = bs.rabbitHoles.length;
    await rr.reopenCrawl(bs, [{ keyword: 'evidence gap', why: 'needed' }], 'DIG INTO THE GAP');

    // claims/attacks/vocab flowed into the ledger (ingestWave), not discarded
    expect(bs.claims.some((c) => c.claim === 'reopen claim')).toBe(true);
    // fresh rabbitHoles + nextSources both landed in the store — the harvest, not just resultSoFar
    expect(bs.rabbitHoles.length).toBeGreaterThan(before);
    expect(bs.rabbitHoles.some((r) => r.keyword === 'fresh gap')).toBe(true);
    expect(bs.rabbitHoles.some((r) => r.why === 'followed citation')).toBe(true);
    // the coord's OWN deltas were applied via applyDeltas — the parked `add` lead is in the store
    expect(bs.rabbitHoles.some((r) => r.keyword === 'parked lead')).toBe(true);
    expect(bs.resultSoFar!.answer).toBe('folded');
  });
});

// ── (dd2) reopenCrawl — v3 batch 6 review fixes: kind threading (finding A) + the derivation rerun that
//     reopenCrawl never fired at all (finding B) ──────────────────────────────────────────────────────
describe('ResearchReport.reopenCrawl — v3 batch 6 fixes (findings A + B)', () => {
  const mkBs = () =>
    new BrainerState(
      {
        scout: { landscape: 'l', pages: [], deadEnds: [] },
        scoutRabbitHoles: [],
        highValueSources: [],
        languageGuidance: '',
      },
      { name: 'root', parentName: null, mandate: '', trail: '', depth: 0 },
    );

  it('threads kind through every reopenCrawl path: the judge-injected pick gets kind:"inject"; a harvested rabbitHole (no kind) defaults to "gap"; a harvested nextSource gets "citation" (finding A)', async () => {
    const agent = (prompt: string, opts: AgentOpts) => {
      const L = opts.label;
      if (L.startsWith('scheduler-')) return schedulerStub(prompt);
      if (L.startsWith('lane-'))
        return {
          runningAnswer: 'reopen finding',
          rabbitHoles: [{ keyword: 'fresh gap', why: 'follow-on' }], // kind omitted → defaults to 'gap'
          nextSources: [{ ref: 'https://cited.example.com/x', why: 'top citation' }],
          deadEnds: [],
          claims: [],
          newTerms: [],
        };
      if (L.startsWith('brainer-'))
        return {
          resultSoFar: { ...RSF_MIN2, answer: 'folded' },
          rescore: [],
          add: [],
          rename: [],
          drop: [],
          lookupNext: [],
          stop: { done: false, reason: 'folded' },
        };
      throw new Error('reopenCrawl kind test: unexpected label ' + L);
    };
    const RR = await loadEngine({ query: 'q', mode: 'goal' }, agent);
    const rr = new RR();
    const bs = mkBs();
    bs.wave = 2;
    await rr.reopenCrawl(bs, [{ keyword: 'evidence gap', why: 'needed' }], 'DIG INTO THE GAP');

    expect(bs.pursuedArchive.find((r) => r.keyword === 'evidence gap')!.kind).toBe('inject');
    expect(bs.rabbitHoles.find((r) => r.keyword === 'fresh gap')!.kind).toBe('gap');
    expect(bs.rabbitHoles.find((r) => r.why === 'followed citation')!.kind).toBe('citation');
  });

  it('reruns the stored derivation when a reopened lane ingests a claim that is one of its inputs — this exact path was dead before (finding B)', async () => {
    let rerunCalls = 0;
    const agent = (prompt: string, opts: AgentOpts) => {
      const L = opts.label;
      if (L.startsWith('scheduler-')) return schedulerStub(prompt);
      if (L.startsWith('lineage-')) return { links: [] };
      if (L.startsWith('lane-'))
        return {
          runningAnswer: 'reopen finding',
          rabbitHoles: [],
          nextSources: [],
          deadEnds: [],
          claims: [
            {
              claim: 'a fresh derivation-input claim',
              quote: 'a verbatim reopen quote long enough to matter here today',
              source: 'https://reopen.example.com/a',
            },
          ],
          newTerms: [],
        };
      if (L.startsWith('brainer-'))
        return {
          resultSoFar: { ...RSF_MIN2, answer: 'folded' },
          rescore: [],
          add: [],
          rename: [],
          drop: [],
          lookupNext: [],
          stop: { done: false, reason: 'folded' },
        };
      if (L === 'rerun-w2') {
        rerunCalls++;
        return { ok: true, quantiles: { p50: 7 }, sensitivity: { a: 1 } };
      }
      throw new Error('reopenCrawl rerun test: unexpected label ' + L);
    };
    const RR = await loadEngine({ query: 'q', mode: 'goal' }, agent);
    const rr = new RR();
    const bs = mkBs();
    bs.wave = 2;
    // a stored derivation whose ONLY input cites claim id 1 — the id the reopen reader's fresh claim mints
    // (this bs's own nextClaimId starts at 1 and nothing else ingests first).
    bs.derivation = {
      code: 'x',
      inputs: [{ name: 'a', dist: 'd', claimIds: [1], prior: false }],
      lastRun: { quantiles: { p50: 1 }, sensitivity: {}, wave: 1 },
    };
    bs.derivationDirty = false;
    await rr.reopenCrawl(bs, [{ keyword: 'evidence gap', why: 'needed' }], 'DIG INTO THE GAP');

    expect(bs.claims[0].id).toBe(1); // confirms the premise the test is built on
    expect(rerunCalls).toBe(1);
    expect(bs.derivation!.lastRun).toEqual({
      quantiles: { p50: 7 },
      sensitivity: { a: 1 },
      wave: 2,
    });
  });
});

// ── (ee) mergeChildClaims — CHILD→PARENT CLAIM MERGE (batch 4) ──
describe('ResearchReport.mergeChildClaims — CHILD→PARENT CLAIM MERGE (batch 4)', () => {
  const mkBs = (name: string) =>
    new BrainerState(
      { scout: null, scoutRabbitHoles: [], highValueSources: [], languageGuidance: '' },
      {
        name,
        parentName: name === 'root' ? null : 'root',
        mandate: '',
        trail: '',
        depth: name === 'root' ? 0 : 1,
      },
    );
  const mkClaim = (id: number, over: Partial<Claim> = {}): Claim => ({
    id,
    claim: 'c' + id,
    quote: 'quote ' + id,
    source: 'https://x.example.com/' + id,
    cluster: 0,
    audit: 'pass',
    status: 'tentative',
    attacksSurvived: 0,
    retracted: false,
    wave: 1,
    lane: 'l',
    ...over,
  });

  it('dedupes by quote+source, drops stances, remaps nullAttack claimIds (topic-only when the claim did not merge), recomputes statuses, and logs the summary', async () => {
    const logs: string[] = [];
    const RR = await loadEngine({ query: 'q' }, () => ({}));
    globalThis.log = (m?: unknown) => logs.push(typeof m === 'string' ? m : String(m));
    const rr = new RR();
    const target = mkBs('root');
    target.nextClaimId = 3;
    target.claims = [mkClaim(1, { quote: 'shared quote', source: 'https://shared.com/p' })];
    const loser = mkBs('b1-branch');
    loser.claims = [
      mkClaim(1, { quote: 'shared quote', source: 'https://shared.com/p' }), // dupe of target's own c1
      mkClaim(2, {
        quote: 'unique quote',
        source: 'https://unique.com/q',
        stance: { target: 1, kind: 'supports' }, // a stance whose target id lives in the LOSER's ledger
      }),
      mkClaim(3, { quote: 'retracted quote', source: 'https://gone.com/r', retracted: true }), // never merged
    ];
    loser.nullAttacks = [
      {
        topic: 'a survived attack on claim 2',
        claimIds: [2],
        queries: ['q'],
        wave: 1,
        phase: 'Research',
      },
      {
        topic: 'attack on the retracted claim',
        claimIds: [3],
        queries: ['q2'],
        wave: 1,
        phase: 'Research',
      },
    ];

    rr.mergeChildClaims(target, [target, loser]);

    // dedupe: claim 1 (dupe) and claim 3 (retracted) never merge — only claim 2 does
    expect(target.claims.length).toBe(2);
    const merged = target.claims.find((c: Claim) => c.quote === 'unique quote')!;
    expect(merged.id).toBe(3); // a FRESH id from the target's own nextClaimId, not the loser's id 2
    // stances DROPPED — the loser's stance.target (1) does not map across ledgers
    expect(merged.stance).toBeUndefined();
    // nullAttacks: BOTH merge in; claimIds remap through the ids that survived, else topic-only []
    expect(target.nullAttacks.length).toBe(2);
    const remapped = target.nullAttacks.find((na) => na.topic === 'a survived attack on claim 2')!;
    expect(remapped.claimIds).toEqual([merged.id]);
    const topicOnly = target.nullAttacks.find(
      (na) => na.topic === 'attack on the retracted claim',
    )!;
    expect(topicOnly.claimIds).toEqual([]);
    // the merge log line names the counts
    expect(
      logs.some(
        (l) =>
          l.includes('⇄ merged 1 claims (+2 nullAttacks)') &&
          l.includes('b1-branch') &&
          l.includes('root') &&
          l.includes('1 dupes dropped'),
      ),
    ).toBe(true);
  });

  it('is a no-op when there are no other brainers to merge', async () => {
    const RR = await loadEngine({ query: 'q' }, () => ({}));
    const rr = new RR();
    const target = mkBs('root');
    target.claims = [mkClaim(1)];
    rr.mergeChildClaims(target, [target]);
    expect(target.claims.length).toBe(1);
  });
});

// ═══════════════════════════════════════════════════════════════════════════════════════════════
// v3.2.0 REGRESSION PINS — one focused test per production failure class the v3.2.0 campaign fixed.
// Standing directive: every failure class discovered in production gets a pinned test so it can
// never recur silently.
// ═══════════════════════════════════════════════════════════════════════════════════════════════

// ── (1) CORRUPT cache quarantine (ingestWave step 1b) — run forensics: a poisoned cache file was
//     re-served to the very remediation lane opened to fix it. ──
describe('ResearchReport.ingestWave — CORRUPT deadEnd quarantines the cache path (v3.2.0)', () => {
  it('moves a CORRUPT-flagged path into bs.corruptCachePaths and evicts it from bs.knownCachePaths', async () => {
    const RR = await loadEngine({ query: 'q' }, () => null); // no claims land → claim-audit/lineage never dispatch
    const rr = new RR();
    const bs = new BrainerState(
      { scout: null, scoutRabbitHoles: [], highValueSources: [], languageGuidance: '' },
      { name: 'root', parentName: null, mandate: '', trail: '', depth: 0 },
    );
    bs.knownCachePaths.add('/x/harvester/.fetch/html/spam.md'); // scheduled THIS run — would otherwise stay trusted
    const toPursue = [{ id: 1, keyword: 'k', why: 'w', score: 50, scoreHistory: [], path: [] }] as any;
    const raw = [
      {
        summary: 'thin',
        claims: [],
        deadEnds: ['CORRUPT: /x/harvester/.fetch/html/spam.md — thai gambling spam'],
      },
    ] as any;
    await rr.ingestWave(bs, toPursue, raw, 1, 'Research');
    expect(bs.corruptCachePaths.has('/x/harvester/.fetch/html/spam.md')).toBe(true);
    expect(bs.knownCachePaths.has('/x/harvester/.fetch/html/spam.md')).toBe(false);
  });
});

// ── (2) Validator-trigger widening (runCrawl/runOneWave) — the ORIGINAL gate only looked at
//     anyNull/anyThin; a dead-ended-but-zero-claims lane, or a CORRUPT deadEnd alone, must ALSO
//     trip it even when every reader is non-null and every summary is thick. Driven end-to-end via
//     .run() (mirrors the (m) "validator gate skips" test above) — the predicate itself lives
//     inline in the wave loop with no extractable seam, so this exercises it through its real caller
//     rather than duplicating the expression. ──
function deadEndsNoClaimsAgent(prompt: string, opts: AgentOpts) {
  const L = opts.label;
  if (L === 'scout-probe:direct') return SCOUT_OUT;
  if (L === 'scout-merger') return null;
  if (L === 'prospector') return PROSPECT_OUT;
  if (L === 'brainer-w0')
    return {
      resultSoFar: RSF,
      rescore: [{ id: 1, score: 80 }],
      add: [],
      lookupNext: [{ id: 1 }],
      rename: [],
      drop: [],
      stop: { done: false, reason: 'go' },
    };
  if (L === 'brainer-w1')
    return {
      resultSoFar: RSF,
      rescore: [],
      add: [],
      lookupNext: [],
      rename: [],
      drop: [],
      stop: { done: true, reason: 'answered' },
    };
  if (L.startsWith('scheduler-')) return schedulerStub(prompt);
  if (L.startsWith('lane-w1:hnsw-tuning'))
    return {
      runningAnswer: THICK,
      rabbitHoles: [],
      claims: [],
      deadEnds: ['walled off, no content extracted'], // non-CORRUPT dead end, but zero claims landed
    };
  if (L.startsWith('validator-')) return VALIDATE_OUT;
  if (L === 'initiator') return { refinement: { facts: [] }, synthesiser: { focus: '' } };
  if (L.startsWith('judge-'))
    return {
      goalMet: true,
      verificationSound: true,
      needsCompute: false,
      computeSound: true,
      reasoning: 'ok',
      directive: '',
    };
  if (L === 'synthesiser')
    return { report: '# R', verdict: 'v', confidence: 'high', plan: [], openQuestions: [] };
  throw new Error('deadEndsNoClaimsAgent: unexpected label ' + L);
}

describe('ResearchReport.run — validator gate widens to a dead-ends-but-zero-claims lane (v3.2.0)', () => {
  it('fires the validator even though every reader is non-null and thick, because a lane reported deadEnds with no claims', async () => {
    const RR = await loadEngine({ query: 'q', mode: 'goal' }, deadEndsNoClaimsAgent);
    const result = await new RR().run();
    expect(result.stopReason).toBe('brainer-done');
    expect(keys(result).some((k) => k.endsWith('-validator.md'))).toBe(true);
  });
});

function corruptAloneAgent(prompt: string, opts: AgentOpts) {
  const L = opts.label;
  if (L === 'scout-probe:direct') return SCOUT_OUT;
  if (L === 'scout-merger') return null;
  if (L === 'prospector') return PROSPECT_OUT;
  if (L === 'brainer-w0')
    return {
      resultSoFar: RSF,
      rescore: [{ id: 1, score: 80 }],
      add: [],
      lookupNext: [{ id: 1 }],
      rename: [],
      drop: [],
      stop: { done: false, reason: 'go' },
    };
  if (L === 'brainer-w1')
    return {
      resultSoFar: RSF,
      rescore: [],
      add: [],
      lookupNext: [],
      rename: [],
      drop: [],
      stop: { done: true, reason: 'answered' },
    };
  if (L.startsWith('scheduler-')) return schedulerStub(prompt);
  if (L.startsWith('lane-w1:hnsw-tuning'))
    return {
      runningAnswer: THICK,
      rabbitHoles: [],
      claims: [{ claim: 'a landed claim', quote: 'a quote long enough to carry the fact', source: 's' }],
      deadEnds: ['CORRUPT: /x/harvester/.fetch/html/spam.md — thai gambling spam'], // claims landed too — isolates anyCorrupt
    };
  if (L.startsWith('claim-audit-')) return null; // dead auditor — irrelevant to this check
  if (L.startsWith('lineage-')) return null; // dead lineage clerk — irrelevant to this check
  if (L.startsWith('validator-')) return VALIDATE_OUT;
  if (L === 'initiator') return { refinement: { facts: [] }, synthesiser: { focus: '' } };
  if (L.startsWith('judge-'))
    return {
      goalMet: true,
      verificationSound: true,
      needsCompute: false,
      computeSound: true,
      reasoning: 'ok',
      directive: '',
    };
  if (L === 'synthesiser')
    return { report: '# R', verdict: 'v', confidence: 'high', plan: [], openQuestions: [] };
  throw new Error('corruptAloneAgent: unexpected label ' + L);
}

describe('ResearchReport.run — validator gate widens to a CORRUPT deadEnd alone (v3.2.0)', () => {
  it('fires the validator on a CORRUPT deadEnd even when the lane also landed claims under a thick summary', async () => {
    const RR = await loadEngine({ query: 'q', mode: 'goal' }, corruptAloneAgent);
    const result = await new RR().run();
    expect(result.stopReason).toBe('brainer-done');
    expect(keys(result).some((k) => k.endsWith('-validator.md'))).toBe(true);
  });
});

// ── (3) Identical-payload detector (scheduleSources) — run forensics: byte-identical Thai-spam
//     payloads for two distinct drzur.com URLs flowed into 3 lanes undetected. ──
describe('ResearchReport.scheduleSources — identical-payload detector (v3.2.0)', () => {
  const mkBs = () =>
    new BrainerState(
      { scout: null, scoutRabbitHoles: [], highValueSources: [], languageGuidance: '' },
      { name: 'root', parentName: null, mandate: '', trail: '', depth: 0 },
    );

  it('flags two distinct source urls sharing the exact same size+chars as a cache-poisoning signature', async () => {
    const agent = (_p: string, o: AgentOpts) => {
      if (o.label.startsWith('scheduler-'))
        return {
          lanes: [
            {
              id: 1,
              sources: [{ source: 'https://drzur.com/a', path: '/cache/a.txt', size: 5000, chars: 12000 }],
            },
            {
              id: 2,
              // byte-identical payload (same size+chars), served under a DIFFERENT url — the poisoning signature
              sources: [{ source: 'https://drzur.com/b', path: '/cache/b.txt', size: 5000, chars: 12000 }],
            },
          ],
        };
      return {};
    };
    const RR = await loadEngine({ query: 'q' }, agent);
    const rr = new RR();
    const bs = mkBs();
    const picks = [
      { id: 1, keyword: 'k1', why: 'w', score: 50, scoreHistory: [], path: [] },
      { id: 2, keyword: 'k2', why: 'w', score: 50, scoreHistory: [], path: [] },
    ] as any;
    await rr.scheduleSources(bs, picks, 'w1', 'Research');
    expect(bs.lastUnsourced).toContain('⚠ identical payloads');
    expect(bs.lastUnsourced).toContain('https://drzur.com/a');
    expect(bs.lastUnsourced).toContain('https://drzur.com/b');
  });

  it('does NOT flag two same-size+chars sources sharing the SAME url (a legitimate re-cite, not poisoning)', async () => {
    const agent = (_p: string, o: AgentOpts) => {
      if (o.label.startsWith('scheduler-'))
        return {
          lanes: [
            {
              id: 1,
              sources: [
                { source: 'https://a.com', path: '/cache/a.txt', size: 5000, chars: 12000 },
                { source: 'https://a.com', path: '/cache/a2.txt', size: 5000, chars: 12000 },
              ],
            },
          ],
        };
      return {};
    };
    const RR = await loadEngine({ query: 'q' }, agent);
    const rr = new RR();
    const bs = mkBs();
    const picks = [{ id: 1, keyword: 'k1', why: 'w', score: 50, scoreHistory: [], path: [] }] as any;
    await rr.scheduleSources(bs, picks, 'w1', 'Research');
    expect(bs.lastUnsourced).toBe('');
  });
});

// ── (4) scheduleSources honesty folding — run forensics: a reddit-priority lane was silently
//     substituted with vendor blogs, no flag anywhere. ──
describe('ResearchReport.scheduleSources — honesty folding (v3.2.0)', () => {
  const mkBs = () =>
    new BrainerState(
      { scout: null, scoutRabbitHoles: [], highValueSources: [], languageGuidance: '' },
      { name: 'root', parentName: null, mandate: '', trail: '', depth: 0 },
    );

  it('sets bs.lastUnsourced from the scheduler report, clears it on a clean following wave, folds served paths into knownCachePaths, and tallies venueStats.served only for served venues', async () => {
    const agent = (_p: string, o: AgentOpts) => {
      if (o.label === 'scheduler-w1')
        return {
          lanes: [
            {
              id: 1,
              sources: [
                { source: 'https://reddit.com/x', path: '/cache/reddit-x.txt', size: 10, chars: 20 },
              ],
              venuesServed: ['reddit'], // 'vendorblog' was ALSO assigned but never actually served
              unsourced: [{ ref: 'vendor blog deep-dive', reason: 'walled' }],
            },
          ],
        };
      if (o.label === 'scheduler-w2')
        return {
          lanes: [
            {
              id: 1,
              sources: [
                { source: 'https://reddit.com/y', path: '/cache/reddit-y.txt', size: 10, chars: 20 },
              ],
              venuesServed: ['reddit'],
              // no unsourced this wave — a clean run
            },
          ],
        };
      return {};
    };
    const RR = await loadEngine({ query: 'q' }, agent);
    const rr = new RR();
    const bs = mkBs();
    const picks = [
      {
        id: 1,
        keyword: 'k',
        why: 'w',
        score: 50,
        scoreHistory: [],
        path: [],
        sources: ['reddit', 'vendorblog'],
      },
    ] as any;

    await rr.scheduleSources(bs, picks, 'w1', 'Research');
    expect(bs.lastUnsourced).toBe('lane #1 k: vendor blog deep-dive — walled');
    expect(bs.knownCachePaths.has('/cache/reddit-x.txt')).toBe(true);
    expect(bs.venueStats['reddit'].served).toBe(1);
    expect(bs.venueStats['vendorblog'].served).toBe(0); // assigned but never served — never silently counted as served

    await rr.scheduleSources(bs, picks, 'w2', 'Research');
    expect(bs.lastUnsourced).toBe(''); // the clean wave clears the prior wave's report
    expect(bs.knownCachePaths.has('/cache/reddit-y.txt')).toBe(true);
    expect(bs.venueStats['reddit'].served).toBe(2); // served again this wave
  });
});

// ── (5) applyRefineAttacks mechanical counter-propagation — run forensics: a refine pass falsified
//     the headline claim and the synthesis rubber-stamped the pre-correction answer; only the judge
//     caught it, one pass late. ──
describe('ResearchReport.applyRefineAttacks — mechanical counter-propagation (v3.2.0)', () => {
  const mkBs = () =>
    new BrainerState(
      { scout: null, scoutRabbitHoles: [], highValueSources: [], languageGuidance: '' },
      { name: 'root', parentName: null, mandate: '', trail: '', depth: 0 },
    );

  it('folds a counterFound refine into the claim + answer + tensions, and never duplicates on a repeat call', async () => {
    const RR = await loadEngine({ query: 'q' }, () => null);
    const rr = new RR();
    const bs = mkBs();
    bs.wave = 2;
    bs.claims.push({
      id: 1,
      claim: 'the headline claim',
      quote: 'q',
      source: 's',
      cluster: 0,
      audit: 'pass',
      status: 'tentative',
      attacksSurvived: 0,
      retracted: false,
      wave: 1,
      lane: 'l',
    } as Claim);
    bs.resultSoFar = {
      answer: 'pgvector wins on cost',
      confidence: 'high',
      keyClaimIds: [1],
      resolved: [],
      openGaps: [],
      tensions: [],
      working: '',
    };
    const facts = [{ fact: 'the headline claim', why: 'headline', claimId: 1 }];
    const refined = [
      {
        report: 'r',
        queriesTried: ['q1'],
        counterFound: true,
        counterNote: 'actually pgvector loses on cost',
      },
    ];

    rr.applyRefineAttacks(bs, facts, refined, 'Finalize');
    expect(bs.claims[0].counter).toBe('actually pgvector loses on cost');
    expect(bs.resultSoFar!.answer).toContain('Corrections from the refine pass (machine-appended)');
    expect(bs.resultSoFar!.answer).toContain('the headline claim → actually pgvector loses on cost');
    expect(bs.resultSoFar!.tensions).toEqual(['the headline claim → actually pgvector loses on cost']);

    // calling it AGAIN with the same correction must not duplicate — the judge should never see it twice
    rr.applyRefineAttacks(bs, facts, refined, 'Finalize');
    const occurrences = (
      bs.resultSoFar!.answer.match(/the headline claim → actually pgvector loses on cost/g) || []
    ).length;
    expect(occurrences).toBe(1);
    expect(bs.resultSoFar!.tensions).toEqual(['the headline claim → actually pgvector loses on cost']);
  });

  it('records a null attack + attacksSurvived when no counter-evidence was found, and never touches resultSoFar', async () => {
    const RR = await loadEngine({ query: 'q' }, () => null);
    const rr = new RR();
    const bs = mkBs();
    bs.wave = 3;
    bs.claims.push({
      id: 5,
      claim: 'a survived claim',
      quote: 'q',
      source: 's',
      cluster: 0,
      audit: 'pass',
      status: 'tentative',
      attacksSurvived: 0,
      retracted: false,
      wave: 1,
      lane: 'l',
    } as Claim);
    bs.resultSoFar = {
      answer: 'the original answer',
      confidence: 'high',
      keyClaimIds: [5],
      resolved: [],
      openGaps: [],
      tensions: [],
      working: '',
    };
    const facts = [{ fact: 'a survived claim', why: 'why', claimId: 5 }];
    const refined = [{ report: 'r', queriesTried: ['q1', 'q2'], counterFound: false }];
    rr.applyRefineAttacks(bs, facts, refined, 'Finalize');
    expect(bs.claims[0].attacksSurvived).toBe(1);
    expect(bs.nullAttacks).toEqual([
      { topic: 'a survived claim', claimIds: [5], queries: ['q1', 'q2'], wave: 3, phase: 'Finalize' },
    ]);
    expect(bs.resultSoFar!.answer).toBe('the original answer'); // untouched — no correction to fold
    expect(bs.resultSoFar!.tensions).toEqual([]);
  });
});

// ── (6) buildResult honest metrics + _sources.json provenance — run forensics: citationsBogus:0
//     masked a 46% audit-fail rate across three runs; an ad-hoc archiver copied 136 foreign files. ──
describe('ResearchReport.buildResult — honest metrics + _sources.json provenance (v3.2.0)', () => {
  it('computes real audit tallies (including a retracted claim), venuesUnrouted, judge/goal passthrough, and a dedup+retraction-filtered _sources.json', async () => {
    const RR = await loadEngine({ query: 'q' }, () => null);
    const rr = new RR();
    rr.highValueSources = [
      { source: 'arxiv', goodFor: 'ANN' },
      { source: 'reddit', goodFor: 'anecdote' },
      { source: 'never-routed-venue', goodFor: 'x' }, // never assigned this run — no venueStats entry at all
    ];
    const bs = new BrainerState(
      { scout: null, scoutRabbitHoles: [], highValueSources: rr.highValueSources, languageGuidance: '' },
      { name: 'root', parentName: null, mandate: '', trail: '', depth: 0 },
    );
    bs.venueStats = {
      arxiv: { assigned: 2, yielded: 1, served: 2 },
      reddit: { assigned: 1, yielded: 0, served: 1 },
    };
    bs.claims = [
      {
        id: 1,
        claim: 'a',
        quote: 'q1',
        source: 's1',
        cachePath: '/x/harvester/.fetch/html/a.md',
        cluster: 0,
        audit: 'pass',
        status: 'tentative',
        attacksSurvived: 0,
        retracted: false,
        wave: 1,
        lane: 'l',
      },
      {
        id: 2,
        claim: 'b',
        quote: 'q2',
        source: 's2',
        cluster: 0,
        audit: 'fail',
        status: 'tentative',
        attacksSurvived: 0,
        retracted: false,
        wave: 1,
        lane: 'l',
      },
      {
        id: 3,
        claim: 'c',
        quote: 'q3',
        source: 's3',
        cluster: 0,
        audit: 'unpinned',
        status: 'tentative',
        attacksSurvived: 0,
        retracted: false,
        wave: 1,
        lane: 'l',
      },
      {
        id: 4,
        claim: 'd',
        quote: 'q4',
        source: 's4',
        cluster: 0,
        audit: 'pending',
        status: 'tentative',
        attacksSurvived: 0,
        retracted: false,
        wave: 1,
        lane: 'l',
      },
      {
        id: 5,
        claim: 'e',
        quote: 'q5',
        source: 's5',
        cachePath: '/x/harvester/.fetch/html/retracted.md', // retracted — must be EXCLUDED from _sources.json
        cluster: 0,
        audit: 'pass',
        status: 'tentative',
        attacksSurvived: 0,
        retracted: true,
        wave: 1,
        lane: 'l',
      },
      {
        id: 6,
        claim: 'f',
        quote: 'q6',
        source: 's6',
        cachePath: '/x/harvester/.fetch/html/a.md', // SAME cachePath as claim 1 — must dedupe to one entry
        cluster: 0,
        audit: 'pass',
        status: 'tentative',
        attacksSurvived: 0,
        retracted: false,
        wave: 1,
        lane: 'l',
      },
    ] as Claim[];
    bs.quotesRepinned = 1;
    bs.cachePathsRejected = 2;
    bs.goalMet = true;
    bs.judgePasses = 2;
    bs.reopenedLaneCount = 3;
    bs.stopReason = 'brainer-done';
    bs.reportOk = false; // no synthesiser output needed for this check

    const result = rr.buildResult(bs);

    expect(result.metrics.auditCounts).toEqual({ pass: 3, fail: 1, repinned: 1, unpinned: 1, pending: 1 });
    expect(result.metrics.claimsTotal).toBe(6); // includes the retracted claim — never silently dropped from the count
    expect(result.metrics.venuesUnrouted).toBe(1); // never-routed-venue never got a venueStats entry
    expect(result.metrics.goalMet).toBe(true);
    expect(result.metrics.judgePasses).toBe(2);
    expect(result.metrics.reopenedLanes).toBe(3);
    expect(result.metrics.cachePathsRejected).toBe(2);
    expect(result.metrics.quotesRepinned).toBe(1);

    const sources = JSON.parse(result.files['_sources.json'] as string).sources;
    expect(sources).toEqual([{ cachePath: '/x/harvester/.fetch/html/a.md', source: 's1' }]);
  });
});
