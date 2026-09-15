import { describe, it, expect } from 'vitest';
import { BrainerState, spawnBrainer, type RunGlobals } from '../src/brainerState.js';
import type { Claim } from '../src/types/index.js';

const globals: RunGlobals = {
  scout: null,
  scoutRabbitHoles: [],
  highValueSources: [],
  languageGuidance: '',
};
const root = (): BrainerState =>
  new BrainerState(globals, { name: 'root', parentName: null, mandate: '', trail: '', depth: 0 });

describe('BrainerState — claim ledger init', () => {
  it('constructs with an empty ledger and topScoresBase 0', () => {
    const b = root();
    expect(b.claims).toEqual([]);
    expect(b.nullAttacks).toEqual([]);
    expect(b.vocabulary).toEqual([]);
    expect(b.derivation).toBe(null);
    expect(b.yieldCalib).toEqual({});
    expect(b.topScoresBase).toBe(0);
  });
  it('constructs with the v3 STEERING fields at their neutral defaults (batch 3)', () => {
    const b = root();
    expect(b.derivationDirty).toBe(false);
    expect(b.derivationStale).toBe(false);
    expect(b.lastChangedClaimIds).toEqual(new Set());
    expect(b.venueStats).toEqual({});
  });
});

describe('spawnBrainer — the ledger branches with the brainer (no shared refs)', () => {
  it('deep-copies claims/nullAttacks/vocabulary/derivation and shallow-copies yieldCalib', () => {
    const p = root();
    p.claims.push({
      id: 1,
      claim: 'f',
      quote: 'q',
      source: 's',
      cluster: 1,
      audit: 'pass',
      status: 'tentative',
      attacksSurvived: 0,
      retracted: false,
      wave: 1,
      lane: 'l',
    } as Claim);
    p.nullAttacks.push({ topic: 't', claimIds: [1], queries: ['q'], wave: 1, phase: 'Research' });
    p.vocabulary.push({ term: 'ANN', gloss: 'approximate nearest neighbor', uses: 2 });
    p.derivation = { code: 'print(1)', inputs: [], lastRun: null };
    p.yieldCalib = { gap: { n: 1, ratio: 1.2 } };
    p.topScores = [80, 70, 60];
    const c = spawnBrainer(p, { name: 'child', mandate: 'm', trail: 't' });
    // equal by value, disjoint by reference — neither brainer can corrupt the other's ledger
    expect(c.claims).toEqual(p.claims);
    expect(c.claims).not.toBe(p.claims);
    expect(c.claims[0]).not.toBe(p.claims[0]);
    expect(c.nullAttacks).toEqual(p.nullAttacks);
    expect(c.nullAttacks).not.toBe(p.nullAttacks);
    expect(c.vocabulary).toEqual(p.vocabulary);
    expect(c.vocabulary).not.toBe(p.vocabulary);
    expect(c.derivation).toEqual(p.derivation);
    expect(c.derivation).not.toBe(p.derivation);
    expect(c.yieldCalib).toEqual(p.yieldCalib);
    expect(c.yieldCalib).not.toBe(p.yieldCalib); // fresh object (entries are replaced, never mutated)
  });
  it('deep-copies venueStats/lastChangedClaimIds and carries derivationDirty/derivationStale (batch 3)', () => {
    const p = root();
    p.venueStats = { arxiv: { assigned: 2, yielded: 0, served: 0 } };
    p.lastChangedClaimIds = new Set([1, 2]);
    p.derivationDirty = true;
    p.derivationStale = true;
    const c = spawnBrainer(p, { name: 'child', mandate: 'm', trail: 't' });
    expect(c.venueStats).toEqual(p.venueStats);
    expect(c.venueStats).not.toBe(p.venueStats);
    expect(c.lastChangedClaimIds).toEqual(p.lastChangedClaimIds);
    expect(c.lastChangedClaimIds).not.toBe(p.lastChangedClaimIds);
    expect(c.derivationDirty).toBe(true);
    expect(c.derivationStale).toBe(true);
  });
  it('records topScoresBase at the spawn point (the child slices its plateau from there)', () => {
    const p = root();
    p.topScores = [80, 70, 60];
    const c = spawnBrainer(p, { name: 'child', mandate: 'm', trail: 't' });
    expect(c.topScoresBase).toBe(3);
    expect(c.topScores).toEqual([80, 70, 60]); // history carried, base marks where its own waves start
  });
  it('a spawned child of a spawned child gets a null derivation clone when the parent has none', () => {
    const p = root();
    const c = spawnBrainer(p, { name: 'child', mandate: 'm', trail: 't' });
    expect(c.derivation).toBe(null);
  });
});
