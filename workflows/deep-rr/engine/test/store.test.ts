import { describe, it, expect } from 'vitest';
import {
  addRabbitHole,
  applyDeltas,
  resolveLookupNext,
  pursue,
  reopenRabbitHole,
} from '../src/store.js';
import type { StoreState } from '../src/types/index.js';

const newState = (): StoreState => ({
  rabbitHoles: [],
  nextId: 1,
  pursuedKeys: new Set(),
  pursuedRefs: new Set(),
  pursuedList: [],
  pursuedArchive: [],
});

describe('addRabbitHole', () => {
  it('assigns fresh ids and seeds scoreHistory only when scored', () => {
    const st = newState();
    const a = addRabbitHole(st, { keyword: 'hnsw tuning', why: 'knobs', wave: 0 })!;
    const b = addRabbitHole(st, { keyword: 'sharding', why: 'scale', score: 55, wave: 1 })!;
    expect(a.id).toBe(1);
    expect(a.score).toBe(null);
    expect(a.scoreHistory).toEqual([]);
    expect(b.id).toBe(2);
    expect(b.score).toBe(55);
    expect(b.scoreHistory).toEqual([{ wave: 1, score: 55 }]);
    expect(st.nextId).toBe(3);
  });
  it('dedups by norm(keyword), returning the existing entry (no new id)', () => {
    const st = newState();
    const a = addRabbitHole(st, { keyword: 'HNSW Tuning!', why: 'x', wave: 0 });
    const dup = addRabbitHole(st, { keyword: 'hnsw   tuning', why: 'y', wave: 1 });
    expect(dup).toBe(a);
    expect(st.rabbitHoles.length).toBe(1);
    expect(st.nextId).toBe(2);
  });
  it('dedups a near-duplicate keyword (a re-ordering) and refuses a near-dup of a pursued lane', () => {
    const st = newState();
    const a = addRabbitHole(st, { keyword: 'vector database scaling cost', why: 'x', wave: 0 })!;
    // same tokens, re-ordered → folds into the existing entry, no new id
    const dup = addRabbitHole(st, { keyword: 'cost scaling vector database', why: 'y', wave: 1 });
    expect(dup).toBe(a);
    // a genuinely different lead is NOT merged
    const other = addRabbitHole(st, { keyword: 'multi tenant isolation', why: 'z', wave: 1 })!;
    expect(other).not.toBe(a);
    expect(st.rabbitHoles.length).toBe(2);
    // a near-duplicate of a pursued lane is refused
    st.pursuedList.push('hnsw ef tuning');
    expect(addRabbitHole(st, { keyword: 'ef tuning hnsw', why: 'q', wave: 2 })).toBe(null);
  });
  it('returns null for an empty keyword or one already pursued', () => {
    const st = newState();
    expect(addRabbitHole(st, { keyword: '', why: 'x', wave: 0 })).toBe(null);
    st.pursuedKeys.add('sharding');
    expect(addRabbitHole(st, { keyword: 'Sharding', why: 'x', wave: 0 })).toBe(null);
    expect(st.rabbitHoles.length).toBe(0);
  });
  it('dedups on the ref URL/DOI (a paper is never queued twice) and accepts a ref-only lead', () => {
    const st = newState();
    const a = addRabbitHole(st, {
      keyword: 'paper A',
      why: 'x',
      ref: 'https://doi.org/10.1/AB',
      wave: 0,
    })!;
    // same DOI in bare form + different keyword → resolves to the existing entry, no new id
    const dup = addRabbitHole(st, { keyword: 'paper A redux', why: 'y', ref: '10.1/ab', wave: 1 });
    expect(dup).toBe(a);
    expect(a.ref).toBe('https://doi.org/10.1/AB');
    // a ref-only lead (no keyword) is still stored, keyed by its ref
    const refOnly = addRabbitHole(st, { keyword: '', why: 'z', ref: 'https://x.com/p', wave: 1 })!;
    expect(refOnly.keyword).toBe('https://x.com/p');
    // an already-pursued ref is refused
    st.pursuedRefs.add('x.com/p');
    expect(
      addRabbitHole(st, { keyword: 'again', why: 'z', ref: 'https://www.x.com/p/', wave: 2 }),
    ).toBe(null);
    expect(st.rabbitHoles.length).toBe(2);
  });
});

describe('applyDeltas', () => {
  it('applies rename → drop → rescore → add and pushes scoreHistory', () => {
    const st = newState();
    addRabbitHole(st, { keyword: 'one', why: 'a', wave: 0 }); // id 1
    addRabbitHole(st, { keyword: 'two', why: 'b', wave: 0 }); // id 2
    addRabbitHole(st, { keyword: 'three', why: 'c', wave: 0 }); // id 3
    applyDeltas(
      st,
      {
        rename: [{ id: 1, keyword: 'one-renamed', why: 'a2' }],
        drop: [3],
        rescore: [
          { id: 1, score: 70 },
          { id: 2, score: 40 },
        ],
        add: [{ keyword: 'four', why: 'd', score: 88 }],
      },
      1,
    );
    const byId = Object.fromEntries(st.rabbitHoles.map((r) => [r.id, r]));
    expect(byId[1].keyword).toBe('one-renamed');
    expect(byId[1].why).toBe('a2');
    expect(byId[1].score).toBe(70);
    expect(byId[1].scoreHistory).toEqual([{ wave: 1, score: 70 }]);
    expect(byId[3]).toBeUndefined(); // dropped
    expect(byId[4].score).toBe(88); // added (id 4), scoreHistory seeded
    expect(byId[4].scoreHistory).toEqual([{ wave: 1, score: 88 }]);
  });
  it('tolerates missing delta arrays and unknown ids', () => {
    const st = newState();
    addRabbitHole(st, { keyword: 'one', why: 'a', wave: 0 });
    expect(() => applyDeltas(st, { rescore: [{ id: 999, score: 5 }] }, 1)).not.toThrow();
    expect(st.rabbitHoles[0].score).toBe(null);
  });
});

describe('resolveLookupNext', () => {
  it('resolves ids + originates keywords, skips pursued, sorts desc, caps at laneCount', () => {
    const st = newState();
    addRabbitHole(st, { keyword: 'a', why: '', score: 10, wave: 0 }); // id 1
    addRabbitHole(st, { keyword: 'b', why: '', score: 90, wave: 0 }); // id 2
    st.pursuedKeys.add('c');
    st.rabbitHoles.push({ id: 3, keyword: 'c', why: '', score: 99, scoreHistory: [], path: [] });
    const picks = resolveLookupNext(
      st,
      {
        lookupNext: [
          { id: 1, sources: ['arXiv'] },
          { keyword: 'd', why: 'new', score: 50 }, // originate → id 4
          { id: 3 }, // pursued → skipped
          { id: 1 }, // dup id → skipped
        ],
      },
      1,
      2,
    );
    expect(picks.map((p) => p.keyword)).toEqual(['d', 'a']); // 50 > 10, capped to 2
    expect(picks.find((p) => p.id === 1)!.sources).toEqual(['arXiv']);
  });
  it('passes the lead kind through when ORIGINATING a lane (keys the yieldCalib table)', () => {
    const st = newState();
    const a = addRabbitHole(st, { keyword: 'stored', why: 'x', wave: 0, kind: 'gap' })!;
    expect(a.kind).toBe('gap'); // addRabbitHole stores the origin channel
    const picks = resolveLookupNext(
      st,
      { lookupNext: [{ keyword: 'originated', why: 'y', score: 50, kind: 'attack' }] },
      1,
      5,
    );
    expect(picks.find((p) => p.keyword === 'originated')!.kind).toBe('attack');
  });
  it('v3 CALIBRATION: a hot kind (ratio>1) outranks a cold kind (ratio<1) even with a lower raw score', () => {
    const st = newState();
    // 'gap' is cold (0.5×), 'attack' is hot (1.5×): 60×0.5=30 vs 50×1.5=75 → attack wins despite the lower raw score
    st.yieldCalib = { gap: { n: 3, ratio: 0.5 }, attack: { n: 3, ratio: 1.5 } };
    const picks = resolveLookupNext(
      st,
      {
        lookupNext: [
          { keyword: 'cold lead', why: 'x', score: 60, kind: 'gap' },
          { keyword: 'hot lead', why: 'y', score: 50, kind: 'attack' },
        ],
      },
      1,
      5,
    );
    expect(picks.map((p) => p.keyword)).toEqual(['hot lead', 'cold lead']);
  });
  it('v3 CALIBRATION: an unseen/absent kind (or no yieldCalib at all) degrades to the plain-score sort', () => {
    const st = newState();
    const picks = resolveLookupNext(
      st,
      {
        lookupNext: [
          { keyword: 'a', why: '', score: 40 },
          { keyword: 'b', why: '', score: 90 },
        ],
      },
      1,
      5,
    );
    expect(picks.map((p) => p.keyword)).toEqual(['b', 'a']); // unchanged from the pre-calibration behavior
  });
  it('attaches the brainer note + ref onto the resolved lane (rides to the scheduler/reader)', () => {
    const st = newState();
    addRabbitHole(st, { keyword: 'a', why: '', score: 10, wave: 0 }); // id 1
    const picks = resolveLookupNext(
      st,
      { lookupNext: [{ id: 1, note: 'find ef/M tradeoffs; else recall curve', ref: '10.1/x' }] },
      1,
      5,
    );
    expect(picks[0].note).toBe('find ef/M tradeoffs; else recall curve');
    expect(picks[0].ref).toBe('10.1/x');
  });
});

describe('pursue', () => {
  it('moves picks into the archive, removes them from the store, marks pursuedKeys', () => {
    const st = newState();
    const a = addRabbitHole(st, { keyword: 'alpha', why: '', score: 10, wave: 0 })!;
    const b = addRabbitHole(st, { keyword: 'beta', why: '', score: 20, wave: 0 })!;
    addRabbitHole(st, { keyword: 'gamma', why: '', score: 30, wave: 0 });
    pursue(st, [a, b]);
    expect(st.rabbitHoles.map((r) => r.keyword)).toEqual(['gamma']);
    expect(st.pursuedArchive.map((r) => r.keyword)).toEqual(['alpha', 'beta']);
    expect(st.pursuedList).toEqual(['alpha', 'beta']);
    expect(st.pursuedKeys.has('alpha')).toBe(true);
    expect(st.pursuedKeys.has('beta')).toBe(true);
  });
  it('records a pursued lead’s ref so the citation is never fetched again', () => {
    const st = newState();
    const a = addRabbitHole(st, {
      keyword: 'alpha',
      why: '',
      ref: 'https://doi.org/10.9/Z',
      wave: 0,
    })!;
    pursue(st, [a]);
    expect(st.pursuedRefs.has('10.9/z')).toBe(true);
    expect(addRabbitHole(st, { keyword: 'alpha2', why: '', ref: '10.9/Z', wave: 1 })).toBe(null);
  });
});

describe('reopenRabbitHole', () => {
  it('moves a pursued lead back to the open store, clears its keys/ref, bumps failCount', () => {
    const st = newState();
    const a = addRabbitHole(st, {
      keyword: 'alpha',
      why: '',
      score: 40,
      ref: 'https://doi.org/10.1/A',
      wave: 0,
    })!;
    pursue(st, [a]);
    expect(st.rabbitHoles.length).toBe(0);
    expect(st.pursuedKeys.has('alpha')).toBe(true);
    expect(st.pursuedRefs.has('10.1/a')).toBe(true);
    const re = reopenRabbitHole(st, a);
    expect(re).toBe(a);
    expect(st.rabbitHoles).toContain(a); // back in the open store
    expect(st.pursuedArchive).not.toContain(a); // dropped from the archive
    expect(st.pursuedKeys.has('alpha')).toBe(false); // key cleared → re-pursuable
    expect(st.pursuedRefs.has('10.1/a')).toBe(false); // ref cleared
    expect(st.pursuedList).not.toContain('alpha');
    expect(a.failCount).toBe(1);
    reopenRabbitHole(st, a); // re-opening an already-open lead just bumps the cap counter
    expect(a.failCount).toBe(2);
    expect(st.rabbitHoles.filter((r) => r === a).length).toBe(1); // not duplicated
  });
  it('clears the stale directive `note` so a dead-lane directive is not re-sent verbatim (A5)', () => {
    const st = newState();
    const a = addRabbitHole(st, { keyword: 'alpha', why: '', score: 40, wave: 0 })!;
    a.note = 'find X; else Y'; // the directive that already produced a dead lane
    pursue(st, [a]);
    reopenRabbitHole(st, a);
    expect(a.note).toBeUndefined(); // cleared → the next brainer re-authors a fresh directive
  });
});
