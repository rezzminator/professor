import { describe, it, expect } from 'vitest';
import {
  domainOf,
  lineageKeyOf,
  scrubEntities,
  claimStatus,
  computedConfidence,
  minConfidence,
  claimDigestOf,
  chao1,
  updateCalib,
  calibFactor,
  ledgerLines,
  vocabSummary,
  lintCitations,
  sensitivityRanking,
  compactCheckpoint,
} from '../src/utils/index.js';
import type {
  Claim,
  Derivation,
  DerivInput,
  NullAttack,
  ResultSoFar,
  Term,
  YieldCalib,
} from '../src/types/index.js';

// a minimal valid Claim with per-test overrides (defaults: cluster 0, audit pass, unretracted).
const claim = (over: Partial<Claim> & { id: number }): Claim => ({
  claim: 'fact ' + over.id,
  quote: 'quote',
  source: 'https://example.com/p',
  cluster: 0,
  audit: 'pass',
  status: 'tentative',
  attacksSurvived: 0,
  retracted: false,
  wave: 1,
  lane: 'lane',
  ...over,
});
const CFG = { SETTLED_MIN_CLUSTERS: 2 };
const NO_ATTACKS: NullAttack[] = [];

describe('domainOf', () => {
  it('returns the host without www', () => {
    expect(domainOf('https://www.example.com/some/path')).toBe('example.com');
    expect(domainOf('http://sub.example.org/x')).toBe('sub.example.org');
  });
  it('returns the DOI registrant prefix for a DOI (bare or resolver form)', () => {
    expect(domainOf('10.1234/abcd.5')).toBe('10.1234');
    expect(domainOf('https://doi.org/10.1234/abcd.5')).toBe('10.1234');
  });
  it('returns "" when nothing resolvable', () => {
    expect(domainOf('')).toBe('');
    expect(domainOf(null)).toBe('');
  });
});

describe('scrubEntities — null-scrub of worker-emitted provenance (v3.2.3 null-tolerant schemas)', () => {
  it('keeps only non-empty strings; drops null/empty/junk members', () => {
    expect(
      scrubEntities({ authors: ['A. Author', null, ''], funder: null, dataset: '  ', venue: 'ICML' }),
    ).toEqual({ authors: ['A. Author'], venue: 'ICML' });
  });
  it('returns undefined when nothing survives — or when the input is null/junk', () => {
    expect(scrubEntities({ funder: null, dataset: null })).toBeUndefined();
    expect(scrubEntities(null)).toBeUndefined();
    expect(scrubEntities(undefined)).toBeUndefined();
    expect(scrubEntities('not an object')).toBeUndefined();
    expect(scrubEntities(['array'])).toBeUndefined();
  });
  it('passes a fully-populated entities object through intact', () => {
    const e = { authors: ['X', 'Y'], funder: 'NIH', dataset: 'MIMIC-IV', venue: 'Nature' };
    expect(scrubEntities(e)).toEqual(e);
  });
});

describe('lineageKeyOf — the deterministic lineage fallback', () => {
  it('prefers the funder', () => {
    expect(
      lineageKeyOf({
        entities: { funder: 'Acme Corp.', venue: 'NeurIPS' },
        source: 'https://x.com/p',
      }),
    ).toBe('acme corp');
  });
  it('falls back to the venue when no funder', () => {
    expect(lineageKeyOf({ entities: { venue: 'NeurIPS 2025' }, source: 'https://x.com/p' })).toBe(
      'neurips 2025',
    );
  });
  it('falls back to the source domain when no entities', () => {
    expect(lineageKeyOf({ source: 'https://www.example.com/paper' })).toBe('example com');
    expect(lineageKeyOf({ entities: {}, source: 'https://www.example.com/paper' })).toBe(
      'example com',
    );
  });
  it('uses the DOI registrant prefix for a DOI source', () => {
    expect(lineageKeyOf({ source: '10.1234/abcd' })).toBe('10 1234');
  });
  it('returns "" when nothing resolvable (caller maps it to cluster 0)', () => {
    expect(lineageKeyOf({ source: '' })).toBe('');
  });
});

describe('claimStatus', () => {
  it('contested: an unretracted attacking claim targets it — even with plenty of support', () => {
    const target = claim({ id: 1, cluster: 1, attacksSurvived: 2 });
    const all = [
      target,
      claim({ id: 2, cluster: 2, stance: { target: 1, kind: 'supports' } }),
      claim({ id: 3, cluster: 3, stance: { target: 1, kind: 'supports' } }),
      claim({ id: 4, cluster: 4, stance: { target: 1, kind: 'attacks' } }),
    ];
    expect(claimStatus(target, all, NO_ATTACKS, CFG)).toBe('contested');
  });
  it('a RETRACTED attacker no longer contests', () => {
    const target = claim({ id: 1, cluster: 1 });
    const all = [
      target,
      claim({ id: 2, cluster: 2, stance: { target: 1, kind: 'attacks' }, retracted: true }),
    ];
    expect(claimStatus(target, all, NO_ATTACKS, CFG)).toBe('tentative');
  });
  it('settled: 2 clusters + a survived attack', () => {
    const target = claim({ id: 1, cluster: 1, attacksSurvived: 1 });
    const all = [target, claim({ id: 2, cluster: 2, stance: { target: 1, kind: 'supports' } })];
    expect(claimStatus(target, all, NO_ATTACKS, CFG)).toBe('settled');
  });
  it('settled: SETTLED_MIN_CLUSTERS+1 clusters even with no attack survived', () => {
    const target = claim({ id: 1, cluster: 1 });
    const all = [
      target,
      claim({ id: 2, cluster: 2, stance: { target: 1, kind: 'supports' } }),
      claim({ id: 3, cluster: 3, stance: { target: 1, kind: 'supports' } }),
    ];
    expect(claimStatus(target, all, NO_ATTACKS, CFG)).toBe('settled');
  });
  it('tentative: 2 clusters but never challenged', () => {
    const target = claim({ id: 1, cluster: 1 });
    const all = [target, claim({ id: 2, cluster: 2, stance: { target: 1, kind: 'supports' } })];
    expect(claimStatus(target, all, NO_ATTACKS, CFG)).toBe('tentative');
  });
  it('cluster 0 counts as at most ONE cluster no matter how many claims sit in it', () => {
    const target = claim({ id: 1, cluster: 0, attacksSurvived: 1 });
    const all = [
      target,
      claim({ id: 2, cluster: 0, stance: { target: 1, kind: 'supports' } }),
      claim({ id: 3, cluster: 0, stance: { target: 1, kind: 'supports' } }),
    ];
    expect(claimStatus(target, all, NO_ATTACKS, CFG)).toBe('tentative'); // {0} = 1 cluster < 2
  });
  it('audit-failed and retracted supporters are excluded from support counting', () => {
    const target = claim({ id: 1, cluster: 1, attacksSurvived: 1 });
    const all = [
      target,
      claim({ id: 2, cluster: 2, audit: 'fail', stance: { target: 1, kind: 'supports' } }),
      claim({ id: 3, cluster: 3, retracted: true, stance: { target: 1, kind: 'supports' } }),
    ];
    expect(claimStatus(target, all, NO_ATTACKS, CFG)).toBe('tentative'); // only its own cluster counts
  });
  it('a nullAttack naming the claim counts as a survived attack (before the counter is bumped)', () => {
    const target = claim({ id: 1, cluster: 1 });
    const all = [target, claim({ id: 2, cluster: 2, stance: { target: 1, kind: 'supports' } })];
    const nullAttacks: NullAttack[] = [
      { topic: 't', claimIds: [1], queries: ['q'], wave: 2, phase: 'Research' },
    ];
    expect(claimStatus(target, all, nullAttacks, CFG)).toBe('settled');
  });
  it('a set `counter` contests the claim even with no attacking ledger claim (v3 batch 4 — the refiner landed a counter-search with no new claim row)', () => {
    const target = claim({ id: 1, cluster: 1, counter: 'refiner found counter-evidence' });
    const all = [target, claim({ id: 2, cluster: 2, stance: { target: 1, kind: 'supports' } })];
    expect(claimStatus(target, all, NO_ATTACKS, CFG)).toBe('contested');
  });
  it("a subject's own audit-fail verdict caps it at tentative, even with 2 independent supporting clusters + a survived attack", () => {
    const target = claim({ id: 1, cluster: 1, audit: 'fail', attacksSurvived: 1 });
    const all = [
      target,
      claim({ id: 2, cluster: 2, stance: { target: 1, kind: 'supports' } }),
      claim({ id: 3, cluster: 3, stance: { target: 1, kind: 'supports' } }),
    ];
    expect(claimStatus(target, all, NO_ATTACKS, CFG)).toBe('tentative'); // NOT settled — audit fail blocks it
  });
  it('contested-by-attack still wins over an audit-fail subject (guard order: contested checked first)', () => {
    const target = claim({ id: 1, cluster: 1, audit: 'fail' });
    const all = [target, claim({ id: 2, cluster: 2, stance: { target: 1, kind: 'attacks' } })];
    expect(claimStatus(target, all, NO_ATTACKS, CFG)).toBe('contested');
  });
});

describe('computedConfidence', () => {
  const claims = [
    claim({ id: 1, status: 'settled' }),
    claim({ id: 2, status: 'settled' }),
    claim({ id: 3, status: 'tentative' }),
    claim({ id: 4, status: 'contested' }),
    claim({ id: 5, status: 'settled', retracted: true }),
    claim({ id: 6, status: 'tentative', audit: 'fail' }),
  ];
  it('high iff EVERY key claim is settled', () =>
    expect(computedConfidence([1, 2], claims)).toBe('high'));
  it('low iff ANY key claim is contested', () =>
    expect(computedConfidence([1, 4], claims)).toBe('low'));
  it('medium otherwise (a tentative key claim)', () =>
    expect(computedConfidence([1, 3], claims)).toBe('medium'));
  it('medium on empty keyClaimIds', () => expect(computedConfidence([], claims)).toBe('medium'));
  it('an unknown id blocks high (medium — not enough to condemn it)', () => {
    expect(computedConfidence([1, 99], claims)).toBe('medium');
  });
  it('a retracted key claim forces low — worse than merely unsettled', () => {
    expect(computedConfidence([1, 5], claims)).toBe('low');
  });
  it('a key claim the mechanical audit FAILED forces low, same as retracted', () => {
    expect(computedConfidence([1, 6], claims)).toBe('low');
  });
});

describe('claimDigestOf — the KEY CLAIMS SO FAR digest', () => {
  it('empty ledger → ""', () => expect(claimDigestOf({ claims: [] })).toBe(''));
  it('renders `c12 clip(claim,90)` lines, most recent (highest id) first', () => {
    const digest = claimDigestOf({
      claims: [claim({ id: 1, claim: 'first fact' }), claim({ id: 2, claim: 'second fact' })],
    });
    expect(digest).toBe('c2 second fact\nc1 first fact');
  });
  it('skips retracted claims', () => {
    const digest = claimDigestOf({
      claims: [claim({ id: 1, claim: 'kept' }), claim({ id: 2, claim: 'gone', retracted: true })],
    });
    expect(digest).toBe('c1 kept');
  });
  it('caps at CLAIM_DIGEST_CAP (30) even when the ledger holds more', () => {
    const claims = Array.from({ length: 35 }, (_, i) => claim({ id: i + 1, claim: 'fact ' + i }));
    const lines = claimDigestOf({ claims }).split('\n');
    expect(lines.length).toBe(30);
    expect(lines[0]).toBe('c35 fact 34'); // most recent first
  });
  it('clips a long claim to 90 chars (clip adds the … ellipsis)', () => {
    const long = 'x'.repeat(120);
    const digest = claimDigestOf({ claims: [claim({ id: 1, claim: long })] });
    expect(digest).toBe('c1 ' + long.slice(0, 87) + '…');
  });
});

describe('minConfidence — the lower-only rule', () => {
  it('takes the minimum over low < medium < high', () => {
    expect(minConfidence('high', 'low')).toBe('low');
    expect(minConfidence('low', 'high')).toBe('low');
    expect(minConfidence('medium', 'high')).toBe('medium');
    expect(minConfidence('high', 'medium')).toBe('medium');
    expect(minConfidence('high', 'high')).toBe('high');
    expect(minConfidence('low', 'low')).toBe('low');
  });
});

describe('chao1 — the coverage estimator', () => {
  it('estimates unseen groups from singletons/doubletons (n2 > 0)', () => {
    // n1 = 3, n2 = 2 → unseen = 9/4 = 2.25; coverage = 6 / 8.25
    const groups = [1, 1, 1, 2, 2, 3].map((sources) => ({ sources }));
    const { unseen, coverage } = chao1(groups);
    expect(unseen).toBeCloseTo(2.25);
    expect(coverage).toBeCloseTo(6 / 8.25);
  });
  it('uses the bias-corrected form when n2 = 0', () => {
    // n1 = 2, n2 = 0 → unseen = 2·1/2 = 1; coverage = 3/4
    const { unseen, coverage } = chao1([{ sources: 1 }, { sources: 1 }, { sources: 3 }]);
    expect(unseen).toBe(1);
    expect(coverage).toBe(0.75);
  });
  it('full coverage when nothing is a singleton', () => {
    const { unseen, coverage } = chao1([{ sources: 3 }, { sources: 4 }]);
    expect(unseen).toBe(0);
    expect(coverage).toBe(1);
  });
  it('returns {0, 0} for an empty group list — nothing observed is not complete', () => {
    expect(chao1([])).toEqual({ unseen: 0, coverage: 0 });
  });
});

describe('updateCalib', () => {
  it('EMA-steps a fresh kind from the neutral prior ratio 1', () => {
    // obs = 2/1 = 2 → ratio = 1 + 0.3·(2 − 1) = 1.3
    expect(updateCalib({}, 'gap', 1, 2, 0.3)).toEqual({ n: 1, ratio: 1.3 });
  });
  it('EMA-steps an existing entry toward the new observation', () => {
    const calib: YieldCalib = { gap: { n: 2, ratio: 0.8 } };
    // obs = 0.4/0.8 = 0.5 → ratio = 0.8 + 0.3·(0.5 − 0.8) = 0.71
    const next = updateCalib(calib, 'gap', 0.8, 0.4, 0.3);
    expect(next.n).toBe(3);
    expect(next.ratio).toBeCloseTo(0.71);
  });
  it('predicted ≤ 0 → identity (an unpredicted lead teaches nothing)', () => {
    const calib: YieldCalib = { gap: { n: 2, ratio: 0.8 } };
    expect(updateCalib(calib, 'gap', 0, 1, 0.3)).toEqual({ n: 2, ratio: 0.8 });
    expect(updateCalib(calib, 'gap', -1, 1, 0.3)).toEqual({ n: 2, ratio: 0.8 });
    expect(updateCalib({}, 'attack', 0, 1, 0.3)).toEqual({ n: 0, ratio: 1 });
  });
  it('is PURE — never mutates the passed calib', () => {
    const calib: YieldCalib = { gap: { n: 2, ratio: 0.8 } };
    updateCalib(calib, 'gap', 1, 2, 0.3);
    expect(calib).toEqual({ gap: { n: 2, ratio: 0.8 } });
  });
});

describe('calibFactor', () => {
  const LO = 0.5;
  const HI = 1.5;
  it('an unseen kind is neutral (1)', () => expect(calibFactor({}, 'gap', LO, HI)).toBe(1));
  it('clamps a hot kind to the ceiling', () =>
    expect(calibFactor({ gap: { n: 5, ratio: 3 } }, 'gap', LO, HI)).toBe(HI));
  it('clamps a cold kind to the floor', () =>
    expect(calibFactor({ gap: { n: 5, ratio: 0.1 } }, 'gap', LO, HI)).toBe(LO));
  it('passes an in-range ratio through', () =>
    expect(calibFactor({ gap: { n: 5, ratio: 1.2 } }, 'gap', LO, HI)).toBe(1.2));
});

// a fully-valid minimal ResultSoFar (every required field present) — tests override just keyClaimIds.
const RSF_MIN: ResultSoFar = {
  answer: '',
  confidence: '',
  keyClaimIds: [],
  resolved: [],
  openGaps: [],
  tensions: [],
  working: '',
};

describe('ledgerLines — the brainer CLAIM LEDGER digest (v3 STEERING)', () => {
  it('returns "" for an empty ledger', () => {
    expect(ledgerLines({ claims: [], resultSoFar: null }, 10)).toBe('');
  });
  it('renders "c12 [status·clu2·audit] claim = value — source"', () => {
    const claims = [
      claim({
        id: 1,
        status: 'settled',
        cluster: 2,
        audit: 'pass',
        claim: 'X causes Y',
        value: '42',
        source: 'https://a.com',
      }),
    ];
    expect(ledgerLines({ claims, resultSoFar: null }, 10)).toBe(
      'c1 [settled·clu2·pass] X causes Y = 42 — https://a.com',
    );
  });
  it('omits the "= value" segment when the claim carries no value', () => {
    const claims = [claim({ id: 1, claim: 'X' })];
    expect(ledgerLines({ claims, resultSoFar: null }, 10)).toBe(
      'c1 [tentative·clu0·pass] X — https://example.com/p',
    );
  });
  it('excludes retracted claims', () => {
    const claims = [
      claim({ id: 1, claim: 'kept' }),
      claim({ id: 2, claim: 'gone', retracted: true }),
    ];
    expect(ledgerLines({ claims, resultSoFar: null }, 10)).toBe(
      'c1 [tentative·clu0·pass] kept — https://example.com/p',
    );
  });
  it('clips a long claim to CLAIM_LINE_CLIP (120) chars', () => {
    const long = 'x'.repeat(150);
    const claims = [claim({ id: 1, claim: long })];
    const out = ledgerLines({ claims, resultSoFar: null }, 10);
    expect(out).toBe(
      'c1 [tentative·clu0·pass] ' + long.slice(0, 117) + '… — https://example.com/p',
    );
  });
  it('orders latest keyClaimIds first, then contested, then settled, then tentative by recency', () => {
    const claims = [
      claim({ id: 1, status: 'tentative', claim: 'old tentative' }),
      claim({ id: 2, status: 'settled', claim: 'a settled one' }),
      claim({ id: 3, status: 'contested', claim: 'a contested one' }),
      claim({ id: 4, status: 'tentative', claim: 'newer tentative' }),
      claim({ id: 5, status: 'tentative', claim: 'the key one' }),
    ];
    const out = ledgerLines({ claims, resultSoFar: { ...RSF_MIN, keyClaimIds: [5] } }, 10);
    const ids = out.split('\n').map((l) => Number(l.match(/^c(\d+)/)![1]));
    expect(ids).toEqual([5, 3, 2, 4, 1]);
  });
  it('caps at `cap` lines with a "(+N more)" tail', () => {
    const claims = Array.from({ length: 5 }, (_, i) => claim({ id: i + 1, claim: 'fact ' + i }));
    const out = ledgerLines({ claims, resultSoFar: null }, 3);
    const lines = out.split('\n');
    expect(lines.length).toBe(4); // 3 claim lines + the tail
    expect(lines[3]).toBe('(+2 more)');
  });
  it('no tail when everything fits under the cap', () => {
    const claims = [claim({ id: 1, claim: 'a' }), claim({ id: 2, claim: 'b' })];
    const out = ledgerLines({ claims, resultSoFar: null }, 10);
    expect(out).not.toContain('more)');
  });
});

describe('vocabSummary — the scheduler COMMUNITY VOCABULARY input', () => {
  it('returns "" for an empty vocabulary', () => expect(vocabSummary([], 20)).toBe(''));
  it('renders "term (n)" sorted by uses descending, comma-joined', () => {
    const vocab: Term[] = [
      { term: 'RCT', uses: 2 },
      { term: 'biomarker', uses: 5 },
      { term: 'confounder', uses: 3 },
    ];
    expect(vocabSummary(vocab, 20)).toBe('biomarker (5), confounder (3), RCT (2)');
  });
  it('caps at `cap` terms (top by uses)', () => {
    const vocab: Term[] = [
      { term: 'a', uses: 1 },
      { term: 'b', uses: 9 },
      { term: 'c', uses: 5 },
    ];
    expect(vocabSummary(vocab, 2)).toBe('b (9), c (5)');
  });
  it('is pure — never mutates the passed vocabulary array', () => {
    const vocab: Term[] = [
      { term: 'a', uses: 1 },
      { term: 'b', uses: 9 },
    ];
    const before = JSON.stringify(vocab);
    vocabSummary(vocab, 20);
    expect(JSON.stringify(vocab)).toBe(before);
  });
});

describe('lintCitations — v3 SYNTHESISER citation lint (batch 4)', () => {
  const claims = [claim({ id: 1 }), claim({ id: 2, retracted: true })];
  it('keeps a [cN] marker whose id is a live (non-retracted) ledger claim', () => {
    const { report, bogus } = lintCitations('pgvector wins [c1] on recall.', claims);
    expect(report).toBe('pgvector wins [c1] on recall.');
    expect(bogus).toEqual([]);
  });
  it('strips a marker whose id is unknown, collecting it in bogus', () => {
    const { report, bogus } = lintCitations('per [c99] the answer holds.', claims);
    expect(report).toBe('per  the answer holds.');
    expect(bogus).toEqual([99]);
  });
  it('strips a marker pointing at a RETRACTED claim (no longer live evidence)', () => {
    const { report, bogus } = lintCitations('see [c2] for detail.', claims);
    expect(report).toBe('see  for detail.');
    expect(bogus).toEqual([2]);
  });
  it('keeps real ids and strips bogus ones in the same pass, counting every bogus id', () => {
    const { report, bogus } = lintCitations('[c1] holds but [c2] and [c7] do not.', claims);
    expect(report).toBe('[c1] holds but  and  do not.');
    expect(bogus).toEqual([2, 7]);
  });
  it('strips a marker whose live claim FAILED its quote-pin audit, collecting it in auditFailed (not bogus)', () => {
    const withFailed = [claim({ id: 1 }), claim({ id: 3, audit: 'fail' })];
    const { report, bogus, auditFailed } = lintCitations(
      'pgvector wins [c1] but the pin on [c3] is discredited.',
      withFailed,
    );
    expect(report).toBe('pgvector wins [c1] but the pin on  is discredited.');
    expect(bogus).toEqual([]);
    expect(auditFailed).toEqual([3]);
  });
  it('passes through unchanged text with no markers, and empty input', () => {
    expect(lintCitations('no citations here', claims)).toEqual({
      report: 'no citations here',
      bogus: [],
      auditFailed: [],
    });
    expect(lintCitations('', claims)).toEqual({ report: '', bogus: [], auditFailed: [] });
  });
});

describe('sensitivityRanking — the initiator SENSITIVITY RANKING body (v3 batch 4)', () => {
  it('returns "" when there is no derivation (no completed rerun yet)', () => {
    expect(sensitivityRanking(null, [])).toBe('');
    expect(sensitivityRanking(undefined, [])).toBe('');
  });
  it('orders inputs by variance share, highest first, naming their backing claims', () => {
    const claims = [claim({ id: 1, claim: 'cost per query is $0.0003' })];
    const inputs: DerivInput[] = [
      { name: 'volume', dist: 'lognormal', claimIds: [], prior: true },
      { name: 'cost', dist: 'lognormal', claimIds: [1], prior: false },
    ];
    const out = sensitivityRanking({ inputs, sensitivity: { cost: 0.7, volume: 0.2 } }, claims);
    const lines = out.split('\n');
    expect(lines[0]).toBe('- cost (0.70) — backed by c1 cost per query is $0.0003');
    expect(lines[1]).toBe('- volume (0.20) — PRIOR (no backing claim yet)');
  });
  it('excludes a retracted backing claim from the "backed by" list', () => {
    const claims = [claim({ id: 1, claim: 'retracted evidence', retracted: true })];
    const inputs: DerivInput[] = [{ name: 'x', dist: 'wide', claimIds: [1], prior: false }];
    const out = sensitivityRanking({ inputs, sensitivity: { x: 0.5 } }, claims);
    expect(out).toBe('- x (0.50)');
  });
});

// compactCheckpoint replaces the old scribe-agent checkpoint file: a LEAN per-wave recovery snapshot,
// logged behind CONFIG.CHECKPOINT_MARK — no quotes, no cachePaths, no entities (the journal + _claims.json
// already hold the full evidentiary backing; this is the human/agent-readable recovery line).
describe('compactCheckpoint — the per-wave crash-safety recovery snapshot', () => {
  it('shapes open leads (id/keyword/lastScore/kind), the pursued list, and nullAttacks as a COUNT', () => {
    const bs = {
      wave: 3,
      resultSoFar: RSF_MIN,
      rabbitHoles: [
        {
          id: 1,
          keyword: 'hnsw tuning',
          scoreHistory: [{ wave: 1, score: 80 }],
          kind: 'gap' as const,
        },
        { id: 2, keyword: 'no score yet', scoreHistory: [] },
      ],
      pursuedList: ['hnsw tuning'],
      claims: [] as Claim[],
      nullAttacks: [
        { topic: 't', claimIds: [1], queries: ['q'], wave: 1, phase: 'Research' },
      ] as NullAttack[],
      derivation: null,
    };
    const snap = compactCheckpoint(bs);
    expect(snap.wave).toBe(3);
    expect(snap.resultSoFar).toBe(RSF_MIN);
    expect(snap.open).toEqual([
      { id: 1, keyword: 'hnsw tuning', score: 80, kind: 'gap' },
      { id: 2, keyword: 'no score yet', score: null, kind: undefined },
    ]);
    expect(snap.pursued).toEqual(['hnsw tuning']);
    expect(snap.nullAttacks).toBe(1); // a COUNT, not the array
  });
  it('carries only LEAN claim fields (id/claim/value/source/status/cluster) — no quote, cachePath, or entities — and drops retracted claims', () => {
    const long = 'x'.repeat(150);
    const claims = [
      claim({
        id: 1,
        claim: long,
        value: '0.98',
        source: 'https://a.com',
        status: 'settled',
        cluster: 2,
        quote: 'a verbatim quote never pinned to the checkpoint line',
        cachePath: '/cache/1.txt',
        entities: { funder: 'Acme Corp' },
      }),
      claim({ id: 2, claim: 'gone', retracted: true }),
    ];
    const bs = {
      wave: 1,
      resultSoFar: null,
      rabbitHoles: [],
      pursuedList: [],
      claims,
      nullAttacks: [] as NullAttack[],
      derivation: null,
    };
    const snap = compactCheckpoint(bs);
    expect(snap.claims.length).toBe(1); // the retracted claim is excluded
    expect(snap.claims[0]).toEqual({
      id: 1,
      claim: long.slice(0, 117) + '…', // clipped to CONFIG.CLAIM_LINE_CLIP (120), same rule as ledgerLines
      value: '0.98',
      source: 'https://a.com',
      status: 'settled',
      cluster: 2,
    });
    expect(Object.keys(snap.claims[0]).sort()).toEqual(
      ['id', 'claim', 'value', 'source', 'status', 'cluster'].sort(),
    );
    const json = JSON.stringify(snap);
    expect(json).not.toContain('verbatim quote');
    expect(json).not.toContain('cachePath');
    expect(json).not.toContain('/cache/1.txt');
    expect(json).not.toContain('Acme Corp');
  });
  it('summarizes a derivation to input NAMES + lastRun QUANTILES only — no sensitivity, no wave', () => {
    const derivation: Derivation = {
      code: 'print(1)',
      inputs: [{ name: 'x', dist: 'normal', claimIds: [1], prior: false }],
      lastRun: { quantiles: { p50: 1.5 }, sensitivity: { x: 0.9 }, wave: 2 },
    };
    const bs = {
      wave: 2,
      resultSoFar: null,
      rabbitHoles: [],
      pursuedList: [],
      claims: [] as Claim[],
      nullAttacks: [] as NullAttack[],
      derivation,
    };
    expect(compactCheckpoint(bs).derivation).toEqual({ inputs: ['x'], lastRun: { p50: 1.5 } });
  });
  it('a derivation authored but not yet rerun → lastRun null; no derivation → null outright', () => {
    const authored: Derivation = { code: 'x', inputs: [], lastRun: null };
    const bsAuthored = {
      wave: 1,
      resultSoFar: null,
      rabbitHoles: [],
      pursuedList: [],
      claims: [] as Claim[],
      nullAttacks: [] as NullAttack[],
      derivation: authored,
    };
    expect(compactCheckpoint(bsAuthored).derivation).toEqual({ inputs: [], lastRun: null });
    expect(compactCheckpoint({ ...bsAuthored, derivation: null }).derivation).toBeNull();
  });
});
