import { describe, it, expect, vi } from 'vitest';
// Descriptor + prompt-builder tests are PURE — static imports (mirrors agents.test.ts / prompts.test.ts).
import { claimAuditor, lineageClerk, rerunner } from '../src/agents/index.js';
import { FINISH } from '../src/agents/shared.js';
import { BrainerState } from '../src/brainerState.js';
import type { AgentOpts, Claim, Derivation } from '../src/types/index.js';

type AgentStub = (prompt: string, opts: AgentOpts) => unknown;
const DEFAULT_PARALLEL = async <T>(thunks: Array<() => Promise<T>>): Promise<T[]> =>
  Promise.all(thunks.map((t) => t()));

// counting stub factory — records every {prompt, opts} the fake harness `agent()` receives, so the
// run-fn tests can assert call COUNT and LABELS (a spy) without any real agent I/O.
function countingStub(impl: AgentStub) {
  const calls: { prompt: string; opts: AgentOpts }[] = [];
  const stub: AgentStub = (prompt, opts) => {
    calls.push({ prompt, opts });
    return impl(prompt, opts);
  };
  return { stub, calls };
}

// a minimal valid Claim with per-call overrides (mirrors the claims.test.ts fixture).
const mkClaim = (id: number, over: Partial<Claim> = {}): Claim => ({
  id,
  claim: 'claim ' + id,
  quote: 'quote ' + id,
  source: 'https://example.com/' + id,
  cachePath: '/cache/' + id + '.txt',
  cluster: 0,
  audit: 'pending',
  status: 'tentative',
  attacksSurvived: 0,
  retracted: false,
  wave: 1,
  lane: 'lane',
  ...over,
});

// ── dynamic loaders — each agent's run.ts reads globals (agent/parallel) captured at MODULE LOAD, so
// every behavioral test needs a fresh module instance (vi.resetModules() + a dynamic import), exactly
// like engine.test.ts's loadEngine. Descriptor/prompt-builder tests are pure and use the static imports above.
async function loadClaimAuditor(agent: AgentStub, parallel = DEFAULT_PARALLEL) {
  globalThis.args = { query: 'q' };
  globalThis.agent = async (p: string, o: AgentOpts) => agent(p, o);
  globalThis.log = () => {};
  globalThis.phase = () => {};
  globalThis.parallel = parallel;
  vi.resetModules();
  return import('../src/agents/claimAuditor/run.js');
}
async function loadLineageClerk(agent: AgentStub, parallel = DEFAULT_PARALLEL) {
  globalThis.args = { query: 'q' };
  globalThis.agent = async (p: string, o: AgentOpts) => agent(p, o);
  globalThis.log = () => {};
  globalThis.phase = () => {};
  globalThis.parallel = parallel;
  vi.resetModules();
  return import('../src/agents/lineageClerk/run.js');
}
async function loadRerunner(agent: AgentStub) {
  globalThis.args = { query: 'q' };
  globalThis.agent = async (p: string, o: AgentOpts) => agent(p, o);
  globalThis.log = () => {};
  globalThis.phase = () => {};
  globalThis.parallel = DEFAULT_PARALLEL;
  vi.resetModules();
  return import('../src/agents/rerunner/run.js');
}

// ─────────────────────────────────────────────────────────────────────────────
describe('v3 ledger clerks — descriptor shape', () => {
  it('claimAuditor: haiku/medium, schema requires checks', () => {
    expect(claimAuditor.tier).toBe('haiku');
    expect(claimAuditor.effort).toBe('medium');
    expect(claimAuditor.schema.type).toBe('object');
    expect(claimAuditor.schema.required).toEqual(['checks']);
    expect(typeof claimAuditor.buildPrompt).toBe('function');
  });
  it('lineageClerk: sonnet/medium (finding J — fuzzy entity resolution is judgment, not grep), schema requires links', () => {
    expect(lineageClerk.tier).toBe('sonnet');
    expect(lineageClerk.effort).toBe('medium');
    expect(lineageClerk.schema.type).toBe('object');
    expect(lineageClerk.schema.required).toEqual(['links']);
    expect(typeof lineageClerk.buildPrompt).toBe('function');
  });
  it('rerunner: haiku/low, schema requires ok', () => {
    expect(rerunner.tier).toBe('haiku');
    expect(rerunner.effort).toBe('low');
    expect(rerunner.schema.type).toBe('object');
    expect(rerunner.schema.required).toEqual(['ok']);
    expect(typeof rerunner.buildPrompt).toBe('function');
  });
});

describe('v3 ledger clerks — prompt builders', () => {
  it('claimAuditor renders each item + FINISH', () => {
    const out = claimAuditor.buildPrompt({
      items: [
        {
          id: 1,
          claim: 'the sky is blue',
          quote: 'the sky appears blue',
          cachePath: '/cache/1.txt',
        },
      ],
    });
    expect(out).toContain('the sky is blue');
    expect(out).toContain('the sky appears blue');
    expect(out).toContain('/cache/1.txt');
    expect(out).toContain(FINISH.trim());
    expect(out).not.toContain('{{');
  });
  it('lineageClerk renders items + knownKeys, and carries NO FINISH (no tools)', () => {
    const out = lineageClerk.buildPrompt({
      items: [{ id: 1, source: 'https://a.com', entities: { funder: 'Pfizer Inc.' } }],
      knownKeys: ['funder:pfizer'],
    });
    expect(out).toContain('Pfizer Inc.');
    expect(out).toContain('funder:pfizer');
    expect(out).not.toContain(FINISH.trim());
    expect(out).not.toContain('{{');
  });
  it('rerunner renders the code + inputs JSON + FINISH', () => {
    const out = rerunner.buildPrompt({ code: 'print("hi")', inputsJson: '{"a":1}' });
    expect(out).toContain('print("hi")');
    expect(out).toContain('{"a":1}');
    expect(out).toContain(FINISH.trim());
  });
});

// ─────────────────────────────────────────────────────────────────────────────
describe('claimAuditor — run fn', () => {
  it('zero auditable claims → empty Map, no agent spawned', async () => {
    const { stub, calls } = countingStub(() => ({ checks: [] }));
    const mod = await loadClaimAuditor(stub);
    const out = await mod.runClaimAuditor(
      {} as never,
      [mkClaim(1, { cachePath: undefined }), mkClaim(2, { cachePath: undefined })],
      'w1',
      'Research',
    );
    expect(out.size).toBe(0);
    expect(calls.length).toBe(0);
  });
  it('empty claims array → empty Map, no agent spawned', async () => {
    const { stub, calls } = countingStub(() => ({ checks: [] }));
    const mod = await loadClaimAuditor(stub);
    const out = await mod.runClaimAuditor({} as never, [], 'w1', 'Research');
    expect(out.size).toBe(0);
    expect(calls.length).toBe(0);
  });
  it('single chunk (≤50) → one call, label carries no -b suffix', async () => {
    const { stub, calls } = countingStub((prompt) => {
      const ids = [...prompt.matchAll(/id: (\d+)/g)].map((m) => Number(m[1]));
      return { checks: ids.map((id) => ({ id, verdict: 'pass' as const })) };
    });
    const mod = await loadClaimAuditor(stub);
    const claims = [mkClaim(1), mkClaim(2), mkClaim(3)];
    const out = await mod.runClaimAuditor({} as never, claims, 'w1', 'Research');
    expect(calls.length).toBe(1);
    expect(calls[0].opts.label).toBe('claim-audit-w1');
    expect(out.size).toBe(3);
    expect(out.get(1)).toEqual({ verdict: 'pass', note: undefined });
  });
  it('chunking math: 125 auditable claims / AUDIT_BATCH=50 → 3 concurrent calls, -b0/-b1/-b2 labels', async () => {
    const { stub, calls } = countingStub((prompt) => {
      const ids = [...prompt.matchAll(/id: (\d+)/g)].map((m) => Number(m[1]));
      return { checks: ids.map((id) => ({ id, verdict: 'pass' as const })) };
    });
    const mod = await loadClaimAuditor(stub);
    const claims = Array.from({ length: 125 }, (_, i) => mkClaim(i + 1));
    const out = await mod.runClaimAuditor({} as never, claims, 'w3', 'Research');
    expect(calls.length).toBe(3);
    const labels = calls.map((c) => c.opts.label).sort();
    expect(labels).toEqual(['claim-audit-w3-b0', 'claim-audit-w3-b1', 'claim-audit-w3-b2']);
    expect(out.size).toBe(125);
  });
  it("drops hallucinated ids AND cross-chunk contamination (a later chunk cannot overwrite an earlier chunk's claim)", async () => {
    const { stub } = countingStub((prompt) => {
      const ids = [...prompt.matchAll(/id: (\d+)/g)].map((m) => Number(m[1]));
      const checks: { id: number; verdict: 'pass' | 'fail' }[] = ids.map((id) => ({
        id,
        verdict: 'pass',
      }));
      checks.push({ id: 99999, verdict: 'fail' }); // hallucinated id — belongs to no chunk
      if (ids[0] === 51) checks.push({ id: 1, verdict: 'fail' }); // chunk 2 spuriously reporting chunk 1's id
      return { checks };
    });
    const mod = await loadClaimAuditor(stub);
    const claims = Array.from({ length: 60 }, (_, i) => mkClaim(i + 1)); // 2 chunks: 1-50, 51-60
    const out = await mod.runClaimAuditor({} as never, claims, 'w4', 'Research');
    expect(out.size).toBe(60);
    expect(out.has(99999)).toBe(false);
    expect(out.get(1)).toEqual({ verdict: 'pass', note: undefined }); // chunk 1's legit verdict survives
  });
  it('a dead (null) chunk contributes nothing — its claims stay unset (pending)', async () => {
    const { stub } = countingStub((prompt) => {
      if (prompt.includes('id: 51')) throw new Error('agent died');
      const ids = [...prompt.matchAll(/id: (\d+)/g)].map((m) => Number(m[1]));
      return { checks: ids.map((id) => ({ id, verdict: 'pass' as const })) };
    });
    const mod = await loadClaimAuditor(stub);
    const claims = Array.from({ length: 60 }, (_, i) => mkClaim(i + 1));
    const out = await mod.runClaimAuditor({} as never, claims, 'w5', 'Research');
    expect(out.size).toBe(50); // only chunk 1 (ids 1-50) survives
    expect(out.has(51)).toBe(false);
    expect(out.has(60)).toBe(false);
  });
});

describe('lineageClerk — run fn', () => {
  it('empty claims → empty Map, no agent spawned', async () => {
    const { stub, calls } = countingStub(() => ({ links: [] }));
    const mod = await loadLineageClerk(stub);
    const out = await mod.runLineageClerk({} as never, [], [], 'w1', 'Research');
    expect(out.size).toBe(0);
    expect(calls.length).toBe(0);
  });
  it('single chunk (≤80) → one call, label carries no -b suffix', async () => {
    const { stub, calls } = countingStub((prompt) => {
      const ids = [...prompt.matchAll(/id: (\d+)/g)].map((m) => Number(m[1]));
      return { links: ids.map((id) => ({ id, keys: ['funder:pfizer'] })) };
    });
    const mod = await loadLineageClerk(stub);
    const claims = [mkClaim(1), mkClaim(2)];
    const out = await mod.runLineageClerk({} as never, claims, ['funder:pfizer'], 'w1', 'Research');
    expect(calls.length).toBe(1);
    expect(calls[0].opts.label).toBe('lineage-w1');
    expect(out.get(1)).toEqual(['funder:pfizer']);
  });
  it('chunking math: 100 claims / LINEAGE_BATCH=80 → 2 concurrent calls, -b0/-b1 labels', async () => {
    const { stub, calls } = countingStub((prompt) => {
      const ids = [...prompt.matchAll(/id: (\d+)/g)].map((m) => Number(m[1]));
      return { links: ids.map((id) => ({ id, keys: [] })) };
    });
    const mod = await loadLineageClerk(stub);
    const claims = Array.from({ length: 100 }, (_, i) => mkClaim(i + 1));
    const out = await mod.runLineageClerk({} as never, claims, [], 'w2', 'Research');
    expect(calls.length).toBe(2);
    const labels = calls.map((c) => c.opts.label).sort();
    expect(labels).toEqual(['lineage-w2-b0', 'lineage-w2-b1']);
    expect(out.size).toBe(100);
  });
  it('drops hallucinated ids AND cross-chunk contamination', async () => {
    const { stub } = countingStub((prompt) => {
      const ids = [...prompt.matchAll(/id: (\d+)/g)].map((m) => Number(m[1]));
      const links = ids.map((id) => ({ id, keys: ['venue:apj'] }));
      links.push({ id: 99999, keys: ['funder:ghost'] });
      if (ids[0] === 81) links.push({ id: 1, keys: ['funder:wrong'] });
      return { links };
    });
    const mod = await loadLineageClerk(stub);
    const claims = Array.from({ length: 90 }, (_, i) => mkClaim(i + 1)); // 2 chunks: 1-80, 81-90
    const out = await mod.runLineageClerk({} as never, claims, [], 'w3', 'Research');
    expect(out.size).toBe(90);
    expect(out.has(99999)).toBe(false);
    expect(out.get(1)).toEqual(['venue:apj']); // chunk 1's legit link survives, chunk 2's spurious one dropped
  });
  it('a dead (null) chunk contributes nothing', async () => {
    const { stub } = countingStub((prompt) => {
      if (prompt.includes('id: 81')) throw new Error('agent died');
      const ids = [...prompt.matchAll(/id: (\d+)/g)].map((m) => Number(m[1]));
      return { links: ids.map((id) => ({ id, keys: [] })) };
    });
    const mod = await loadLineageClerk(stub);
    const claims = Array.from({ length: 90 }, (_, i) => mkClaim(i + 1));
    const out = await mod.runLineageClerk({} as never, claims, [], 'w4', 'Research');
    expect(out.size).toBe(80);
    expect(out.has(81)).toBe(false);
  });
});

describe('rerunner — run fn', () => {
  const mkBs = (derivation: Derivation | null, wave = 1) => {
    const bs = new BrainerState(
      { scout: null, scoutRabbitHoles: [], highValueSources: [], languageGuidance: '' },
      { name: 'root', parentName: null, mandate: '', trail: '', depth: 0 },
    );
    bs.derivation = derivation;
    bs.wave = wave;
    return bs;
  };
  it('null derivation → null, no agent spawned', async () => {
    const { stub, calls } = countingStub(() => ({ ok: true }));
    const mod = await loadRerunner(stub);
    const out = await mod.runRerunner(mkBs(null), 'Research');
    expect(out).toBeNull();
    expect(calls.length).toBe(0);
  });
  it('ok:true → returns quantiles/sensitivity, label rerun-w<wave>, inputs passed verbatim', async () => {
    const { stub, calls } = countingStub(() => ({
      ok: true,
      quantiles: { p50: 1.5 },
      sensitivity: { x: 0.3 },
    }));
    const mod = await loadRerunner(stub);
    const derivation: Derivation = {
      code: 'print(1)',
      inputs: [{ name: 'x', dist: 'normal', claimIds: [1], prior: false }],
      lastRun: null,
    };
    const out = await mod.runRerunner(mkBs(derivation, 4), 'Research');
    expect(out).toEqual({ quantiles: { p50: 1.5 }, sensitivity: { x: 0.3 } });
    expect(calls.length).toBe(1);
    expect(calls[0].opts.label).toBe('rerun-w4');
    expect(calls[0].prompt).toContain(JSON.stringify(derivation.inputs));
  });
  it('ok:false → returns null (does not retry on a normal ok:false return)', async () => {
    const { stub, calls } = countingStub(() => ({ ok: false, note: 'ValueError: bad input' }));
    const mod = await loadRerunner(stub);
    const derivation: Derivation = { code: 'x', inputs: [], lastRun: null };
    const out = await mod.runRerunner(mkBs(derivation), 'Research');
    expect(out).toBeNull();
    expect(calls.length).toBe(1);
  });
  it('a dead agent (throws) degrades to null', async () => {
    const { stub } = countingStub(() => {
      throw new Error('agent died');
    });
    const mod = await loadRerunner(stub);
    const derivation: Derivation = { code: 'x', inputs: [], lastRun: null };
    const out = await mod.runRerunner(mkBs(derivation), 'Research');
    expect(out).toBeNull();
  });
});
