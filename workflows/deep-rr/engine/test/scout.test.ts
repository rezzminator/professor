import { describe, it, expect, vi } from 'vitest';
// Descriptor + prompt-builder tests are PURE — static imports (mirrors agents.test.ts / ledgerClerks.test.ts).
import { scout, scoutMerger, scoutPlanner } from '../src/agents/index.js';
import { SCOUT, SCOUT_ANGLE, SCOUT_PLANNER } from '../src/agents/scout/index.js';
import { CONFIG } from '../src/config.js';
import type { AgentOpts, Page, ScoutOut } from '../src/types/index.js';
import type { ResearchReport } from '../src/engine.js';

type AgentStub = (prompt: string, opts: AgentOpts) => unknown;
const DEFAULT_PARALLEL = async <T>(thunks: Array<() => Promise<T>>): Promise<T[]> =>
  Promise.all(thunks.map((t) => t()));

// counting stub factory — records every {prompt, opts} the fake harness `agent()` receives (mirrors
// agents.test.ts / ledgerClerks.test.ts's pattern).
function countingStub(impl: AgentStub) {
  const calls: { prompt: string; opts: AgentOpts }[] = [];
  const stub: AgentStub = (prompt, opts) => {
    calls.push({ prompt, opts });
    return impl(prompt, opts);
  };
  return { stub, calls };
}

// scout/run.ts reads `agent`/`parallel`/`log`/`phase` as ambient globals captured at MODULE LOAD (mirrors
// loadResearcherRun/loadClaimAuditor/etc.) — every behavioral test needs a fresh module instance.
async function loadScoutRun(
  agent: AgentStub,
  query = 'best vector database for production RAG at scale',
) {
  globalThis.args = { query, mode: 'goal' };
  globalThis.agent = async (p: string, o: AgentOpts) => agent(p, o);
  globalThis.log = () => {};
  globalThis.phase = () => {};
  globalThis.parallel = DEFAULT_PARALLEL;
  vi.resetModules();
  return import('../src/agents/scout/run.js');
}
const mkRR = () => ({ scout: null, scoutRabbitHoles: [] }) as unknown as ResearchReport;
const mkPage = (url: string, over: Partial<Page> = {}): Page => ({
  url,
  summary: url + ' summary',
  rabbitHoles: [],
  ...over,
});

// ─────────────────────────────────────────────────────────────────────────────
describe('scout swarm — descriptor shape (v3 batch 2s)', () => {
  it('scout (the probe) keeps its v2 tier/effort — the reading substrate is unchanged', () => {
    expect(scout.tier).toBe('haiku');
    expect(scout.effort).toBe('medium');
    expect(scout.schema).toBe(SCOUT);
    expect(SCOUT.required).toEqual(['landscape', 'pages']);
  });
  it('scoutPlanner: sonnet/high, schema requires decomposition + angles', () => {
    expect(scoutPlanner.tier).toBe('sonnet');
    expect(scoutPlanner.effort).toBe('high');
    expect(scoutPlanner.schema).toBe(SCOUT_PLANNER);
    expect(SCOUT_PLANNER.required).toEqual(['decomposition', 'angles']);
    expect(SCOUT_PLANNER.properties!.angles.items).toBe(SCOUT_ANGLE);
    expect(SCOUT_ANGLE.required).toEqual(['name', 'searchQuery', 'why']);
  });
  it('scoutMerger: sonnet/high, reuses the SCOUT schema (field-compatible with a probe + both fallbacks)', () => {
    expect(scoutMerger.tier).toBe('sonnet');
    expect(scoutMerger.effort).toBe('high');
    expect(scoutMerger.schema).toBe(SCOUT);
  });
  it('CONFIG carries the swarm knobs', () => {
    expect(CONFIG.SCOUT_PROBES).toBe(5);
    expect(CONFIG.SCOUT_PROBE_SOURCES).toBe(3);
    expect(CONFIG.SCOUT_PAGES_CAP).toBe(10);
  });
});

describe('scoutPlanner — prompt builder', () => {
  it('grounds the angle vocabulary in real searches + states the mandatory DIRECT/SKEPTIC/RECENT angles + the SCOUT_PROBES bound', () => {
    const out = scoutPlanner.buildPrompt({
      query: 'q',
      mode: 'goal',
      net: 'NET-FRAGMENT',
    });
    expect(out).toContain('1-2 quick WebSearches');
    expect(out).toContain('DIRECT angle');
    expect(out).toContain('SKEPTIC angle');
    expect(out).toContain('RECENT angle');
    expect(out).toContain('3 to ' + CONFIG.SCOUT_PROBES);
    expect(out).toContain('decomposition');
    expect(out).not.toContain('{{');
  });
  it('folds in researcherNote when supplied, omits it when empty', () => {
    const withNote = scoutPlanner.buildPrompt({
      query: 'q',
      mode: 'goal',
      net: 'N',
      researcherNote: 'Research note: prefer 2025 sources',
    });
    expect(withNote).toContain('Research note: prefer 2025 sources');
    const none = scoutPlanner.buildPrompt({ query: 'q', mode: 'goal', net: 'N' });
    expect(none).not.toContain('Research note');
  });
});

describe('scoutMerger — prompt builder', () => {
  it('renders the decomposition + every probe (angle name, landscape note, pages/claims/newTerms/deadEnds) + the SCOUT_PAGES_CAP bound', () => {
    const out = scoutMerger.buildPrompt({
      query: 'q',
      decomposition: 'axis A\naxis B',
      probes: [
        {
          name: 'skeptic',
          landscape: 'the skeptic angle found criticism',
          pages: [mkPage('https://a.com')],
          claims: [],
          newTerms: [],
          deadEnds: [],
        },
      ],
    });
    expect(out).toContain('axis A');
    expect(out).toContain('name: skeptic');
    expect(out).toContain('the skeptic angle found criticism');
    expect(out).toContain('NAME THE TENSIONS');
    expect(out).toContain('up to ' + CONFIG.SCOUT_PAGES_CAP);
    expect(out).not.toContain('{{');
  });
});

// ─────────────────────────────────────────────────────────────────────────────
// runScout — the orchestrator. Each test builds its own stub, dispatching on opts.label:
// 'scout-planner' | 'scout-probe:<lab(angleName)>' | 'scout-merger'.
describe('runScout — happy path (planner → 3 probes → merger)', () => {
  it('dispatches one probe per angle with its own angle/footer, merges via scoutMerger, and returns matching SeedLead[]', async () => {
    const PLANNER_OUT = {
      decomposition: 'axis A\naxis B',
      angles: [
        { name: 'direct', searchQuery: 'q direct', why: 'the query as asked', lens: 'literal' },
        { name: 'skeptic', searchQuery: 'q criticism', why: 'counter-evidence', lens: 'critical' },
        { name: 'recent', searchQuery: 'q 2026', why: 'last 12 months', lens: 'timeline' },
      ],
    };
    const PROBE_OUT: Record<string, ScoutOut> = {
      direct: {
        landscape: 'direct angle note',
        pages: [mkPage('https://direct.com', { rabbitHoles: [{ keyword: 'k1', why: 'w1' }] })],
        claims: [{ claim: 'c1', quote: 'q1', source: 'https://direct.com' }],
        newTerms: [],
        deadEnds: [],
      },
      skeptic: {
        landscape: 'skeptic angle note',
        pages: [mkPage('https://skeptic.com', { rabbitHoles: [{ keyword: 'k2', why: 'w2' }] })],
        claims: [],
        newTerms: [{ term: 't1' }],
        deadEnds: [],
      },
      recent: {
        landscape: 'recent angle note',
        pages: [mkPage('https://recent.com')],
        claims: [],
        newTerms: [],
        deadEnds: ['https://dead.com — timed out'],
      },
    };
    const MERGED: ScoutOut = {
      landscape: 'the merged landscape naming the tension between direct and skeptic',
      pages: [PROBE_OUT.direct.pages[0], PROBE_OUT.skeptic.pages[0], PROBE_OUT.recent.pages[0]],
      claims: PROBE_OUT.direct.claims,
      newTerms: PROBE_OUT.skeptic.newTerms,
      deadEnds: PROBE_OUT.recent.deadEnds,
    };
    const { stub, calls } = countingStub((prompt, opts) => {
      if (opts.label === 'scout-planner') return PLANNER_OUT;
      if (opts.label.startsWith('scout-probe:')) {
        const name = opts.label.slice('scout-probe:'.length);
        return PROBE_OUT[name];
      }
      if (opts.label === 'scout-merger') return MERGED;
      throw new Error('unexpected label ' + opts.label);
    });
    const mod = await loadScoutRun(stub);
    const rr = mkRR();
    const leads = await mod.runScout(rr);

    // planner dispatched once; exactly one probe per angle; merger dispatched once.
    expect(calls.filter((c) => c.opts.label === 'scout-planner').length).toBe(1);
    expect(calls.filter((c) => c.opts.label.startsWith('scout-probe:')).length).toBe(3);
    expect(calls.filter((c) => c.opts.label === 'scout-merger').length).toBe(1);

    // each probe's prompt names ITS OWN angle and carries the footer verbatim (CONFIG.FOOTER).
    const directCall = calls.find((c) => c.opts.label === 'scout-probe:direct')!;
    const skepticCall = calls.find((c) => c.opts.label === 'scout-probe:skeptic')!;
    const recentCall = calls.find((c) => c.opts.label === 'scout-probe:recent')!;
    expect(directCall.prompt).toContain('«direct»');
    expect(skepticCall.prompt).toContain('«skeptic»');
    expect(recentCall.prompt).toContain('«recent»');
    for (const c of [directCall, skepticCall, recentCall])
      expect(c.prompt).toContain(CONFIG.FOOTER);

    // the merger's prompt carries the decomposition + every probe's angle name + note.
    const mergerCall = calls.find((c) => c.opts.label === 'scout-merger')!;
    expect(mergerCall.prompt).toContain('axis A');
    expect(mergerCall.prompt).toContain('name: direct');
    expect(mergerCall.prompt).toContain('name: skeptic');
    expect(mergerCall.prompt).toContain('name: recent');

    // rr.scout gets the MERGER's output verbatim; the SeedLead[] shape is unchanged (from the merged pages),
    // plus each scout seed now carries kind:'seed' (finding A4) — its origin channel for the yieldCalib table.
    expect(rr.scout).toEqual(MERGED);
    expect(leads).toEqual([
      { keyword: 'k1', why: 'w1', path: [], kind: 'seed' },
      { keyword: 'k2', why: 'w2', path: [], kind: 'seed' },
    ]);
  });
});

describe('runScout — FALLBACK A (planner dies → one probe on the raw query)', () => {
  it('spawns exactly one probe, its prompt carries the raw query, not an invented angle', async () => {
    const { stub, calls } = countingStub((prompt, opts) => {
      if (opts.label === 'scout-planner') return null; // planner died
      if (opts.label.startsWith('scout-probe:'))
        return {
          landscape: 'L',
          pages: [mkPage('https://a.com')],
          claims: [],
          newTerms: [],
          deadEnds: [],
        };
      if (opts.label === 'scout-merger')
        return {
          landscape: 'L',
          pages: [mkPage('https://a.com')],
          claims: [],
          newTerms: [],
          deadEnds: [],
        };
      throw new Error('unexpected label ' + opts.label);
    });
    const mod = await loadScoutRun(stub, 'the raw naive query');
    const rr = mkRR();
    await mod.runScout(rr);
    const probeCalls = calls.filter((c) => c.opts.label.startsWith('scout-probe:'));
    expect(probeCalls.length).toBe(1);
    expect(probeCalls[0].prompt).toContain('the raw naive query');
    expect(probeCalls[0].opts.label).toBe('scout-probe:direct');
  });
  it('also fires when the planner returns unusable angles (empty list) — not just on a hard null', async () => {
    const { stub, calls } = countingStub((prompt, opts) => {
      if (opts.label === 'scout-planner') return { decomposition: 'x', angles: [] };
      if (opts.label.startsWith('scout-probe:'))
        return { landscape: 'L', pages: [], claims: [], newTerms: [], deadEnds: [] };
      if (opts.label === 'scout-merger')
        return { landscape: 'L', pages: [], claims: [], newTerms: [], deadEnds: [] };
      throw new Error('unexpected label ' + opts.label);
    });
    const mod = await loadScoutRun(stub);
    await mod.runScout(mkRR());
    expect(calls.filter((c) => c.opts.label.startsWith('scout-probe:')).length).toBe(1);
  });
});

describe('runScout — one probe dies (merger receives only the survivors)', () => {
  it('the merger prompt carries the 2 survivors, not the dead angle', async () => {
    const PLANNER_OUT = {
      decomposition: 'd',
      angles: [
        { name: 'alpha', searchQuery: 'qa', why: 'wa', lens: 'la' },
        { name: 'beta', searchQuery: 'qb', why: 'wb', lens: 'lb' },
        { name: 'gamma', searchQuery: 'qc', why: 'wc', lens: 'lc' },
      ],
    };
    const { stub, calls } = countingStub((prompt, opts) => {
      if (opts.label === 'scout-planner') return PLANNER_OUT;
      if (opts.label === 'scout-probe:beta') return null; // beta dies
      if (opts.label.startsWith('scout-probe:'))
        return {
          landscape: opts.label + ' note',
          pages: [mkPage('https://' + opts.label + '.com')],
          claims: [],
          newTerms: [],
          deadEnds: [],
        };
      if (opts.label === 'scout-merger')
        return {
          landscape: 'M',
          pages: [mkPage('https://m.com')],
          claims: [],
          newTerms: [],
          deadEnds: [],
        };
      throw new Error('unexpected label ' + opts.label);
    });
    const mod = await loadScoutRun(stub);
    await mod.runScout(mkRR());
    const mergerCall = calls.find((c) => c.opts.label === 'scout-merger')!;
    expect(mergerCall.prompt).toContain('name: alpha');
    expect(mergerCall.prompt).toContain('name: gamma');
    expect(mergerCall.prompt).not.toContain('name: beta');
  });
});

describe('runScout — every probe dies (the hard fatal path, unchanged v2 semantics)', () => {
  it('throws "scout died" when every probe in the swarm returns null', async () => {
    const { stub } = countingStub((prompt, opts) =>
      opts.label.startsWith('scout-probe:')
        ? null
        : { decomposition: 'd', angles: [{ name: 'x', searchQuery: 'q', why: 'w' }] },
    );
    const mod = await loadScoutRun(stub);
    await expect(mod.runScout(mkRR())).rejects.toThrow(/scout died/);
  });
});

describe('runScout — FALLBACK B (merger dies → pure JS mechanical merge)', () => {
  it('url-dedups (first occurrence wins) + caps at SCOUT_PAGES_CAP + quote/term-dedups (case-insensitive)', async () => {
    const directPages = Array.from({ length: 6 }, (_, i) => mkPage('https://d' + i + '.com'));
    const skepticPages = [
      mkPage('https://d0.com', { summary: 'duplicate of direct’s d0 — must be dropped' }),
      ...Array.from({ length: 6 }, (_, i) => mkPage('https://s' + i + '.com')),
    ];
    const PLANNER_OUT = {
      decomposition: 'd',
      angles: [
        { name: 'direct', searchQuery: 'qa', why: 'wa', lens: '' },
        { name: 'skeptic', searchQuery: 'qb', why: 'wb', lens: '' },
      ],
    };
    const { stub } = countingStub((prompt, opts) => {
      if (opts.label === 'scout-planner') return PLANNER_OUT;
      if (opts.label === 'scout-probe:direct')
        return {
          landscape: 'direct note',
          pages: directPages,
          claims: [{ claim: 'c1', quote: 'Same Quote Here', source: 's1' }],
          newTerms: [{ term: 'Foo' }],
          deadEnds: ['dead1'],
        };
      if (opts.label === 'scout-probe:skeptic')
        return {
          landscape: 'skeptic note',
          pages: skepticPages,
          claims: [{ claim: 'c2', quote: 'same quote here', source: 's2' }], // dup by norm(quote)
          newTerms: [{ term: 'foo' }], // dup by norm(term)
          deadEnds: ['dead2'],
        };
      if (opts.label === 'scout-merger') return null; // merger died → mechanical merge
      throw new Error('unexpected label ' + opts.label);
    });
    const mod = await loadScoutRun(stub);
    const rr = mkRR();
    await mod.runScout(rr);
    const out = rr.scout!;

    // landscape: angle notes joined, «angle»: note lines.
    expect(out.landscape).toBe('«direct»: direct note\n«skeptic»: skeptic note');
    // pages: 12 unique urls (the d0 dup dropped) capped to SCOUT_PAGES_CAP=10, insertion order kept —
    // direct's 6 first, then skeptic's first 4 (s0..s3); s4/s5 fall off the cap.
    expect(out.pages.length).toBe(CONFIG.SCOUT_PAGES_CAP);
    expect(out.pages.map((p) => p.url)).toEqual([
      'https://d0.com',
      'https://d1.com',
      'https://d2.com',
      'https://d3.com',
      'https://d4.com',
      'https://d5.com',
      'https://s0.com',
      'https://s1.com',
      'https://s2.com',
      'https://s3.com',
    ]);
    expect(out.pages[0].summary).not.toContain('duplicate'); // direct's version survives, not skeptic's dup
    // claims: quote-deduped (case-insensitive via norm) — direct's c1 survives, skeptic's c2 dropped.
    expect(out.claims).toEqual([{ claim: 'c1', quote: 'Same Quote Here', source: 's1' }]);
    // newTerms: term-deduped (case-insensitive) — direct's 'Foo' survives, skeptic's 'foo' dropped.
    expect(out.newTerms).toEqual([{ term: 'Foo' }]);
    // deadEnds: plain union, no dedup.
    expect(out.deadEnds).toEqual(['dead1', 'dead2']);
  });
});
