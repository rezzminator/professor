import { describe, it, expect } from 'vitest';
import {
  plain,
  norm,
  lab,
  padIdx,
  lastScore,
  openLine,
  resultSoFarMd,
  waveMd,
  render,
  packReaders,
  scrubArtifacts,
  topSensitivityInput,
  venuesWithYieldWarn,
} from '../src/utils/index.js';
import type { ResultSoFar, SchedulerSource, Venue } from '../src/types/index.js';

describe('scrubArtifacts — strips structured-output/tool-call leakage (real bug from run forensics)', () => {
  it('strips a complete closing tag', () => {
    expect(scrubArtifacts('before </parameter> after')).toBe('before  after');
  });
  it('strips a complete opening tag with attributes', () => {
    expect(scrubArtifacts('x <StructuredOutput status="done"> y')).toBe('x  y');
  });
  it('strips an <invoke> tag', () => {
    expect(scrubArtifacts('call <invoke name="foo"/> done')).toBe('call  done');
  });
  it('leaves clean prose completely untouched', () => {
    const clean = 'pgvector recall is 0.98 on the LAION benchmark, per the vendor whitepaper.';
    expect(scrubArtifacts(clean)).toBe(clean);
  });
  it('strips a truncated trailing fragment (the harness cut the emission off mid-tag)', () => {
    expect(scrubArtifacts('the answer continues</param')).toBe('the answer continues');
    expect(scrubArtifacts('the answer continues</inv')).toBe('the answer continues');
    expect(scrubArtifacts('the answer continues<function_r')).toBe('the answer continues');
  });
  it('never mistakes ordinary text containing "<" for leakage', () => {
    expect(scrubArtifacts('5 < 10')).toBe('5 < 10');
    expect(scrubArtifacts('x<y')).toBe('x<y');
  });
  it('empty string passes through unchanged', () => {
    expect(scrubArtifacts('')).toBe('');
  });
});

describe('plain', () => {
  it('renders a string as-is', () => expect(plain('hello')).toBe('hello'));
  it('renders a number as-is', () => expect(plain(42)).toBe('42'));
  it('renders a boolean as-is', () => expect(plain(true)).toBe('true'));
  it('renders null as empty string', () => expect(plain(null)).toBe(''));
  it('renders an array one "- el" per line', () =>
    expect(plain(['a', 'b', 'c'])).toBe('- a\n- b\n- c'));
  it('renders an object as key: value lines', () =>
    expect(plain({ a: 1, b: 'two' })).toBe('a: 1\nb: two'));
  it('indents nested objects two spaces', () => {
    expect(plain({ a: 1, b: { c: 2, d: 3 } })).toBe('a: 1\nb:\n  c: 2\n  d: 3');
  });
  it('indents nested array elements two spaces after the dash', () => {
    expect(
      plain([
        { k: 'one', v: 1 },
        { k: 'two', v: 2 },
      ]),
    ).toBe('- k: one\n  v: 1\n- k: two\n  v: 2');
  });
  it('SKIPS empty values by default', () => {
    expect(plain({ a: '', b: [], c: {}, d: null, real: 'v' })).toBe('real: v');
  });
  it('KEEPS named empties as "(none)"', () => {
    expect(plain({ a: '', b: 'v' }, { keep: ['a'] })).toBe('a: (none)\nb: v');
  });
});

describe('norm', () => {
  it('lowercases + collapses non-alphanumerics to single spaces, trimmed', () => {
    expect(norm('Hello, World!')).toBe('hello world');
    expect(norm('  Multiple   Spaces  ')).toBe('multiple spaces');
    expect(norm('CamelCase_99')).toBe('camelcase 99');
  });
  it('handles null/empty', () => {
    expect(norm('')).toBe('');
    expect(norm(null)).toBe('');
  });
});

describe('lab', () => {
  it('slugifies + caps at 24 chars', () => {
    expect(lab('Hello, World!')).toBe('hello-world');
    expect(lab('a very long keyword that exceeds the cap')).toBe('a-very-long-keyword-that');
    expect(lab('a very long keyword that exceeds the cap').length).toBe(24);
  });
});

describe('padIdx', () => {
  it('zero-pads to width 2', () => {
    expect(padIdx(0)).toBe('00');
    expect(padIdx(3)).toBe('03');
    expect(padIdx(12)).toBe('12');
    expect(padIdx(100)).toBe('100');
  });
});

describe('lastScore', () => {
  it('returns the last scoreHistory score, else null', () => {
    expect(
      lastScore({
        scoreHistory: [
          { wave: 0, score: 60 },
          { wave: 1, score: 80 },
        ],
      }),
    ).toBe(80);
    expect(lastScore({ scoreHistory: [] })).toBe(null);
  });
});

describe('openLine', () => {
  it('renders a scored entry', () => {
    expect(
      openLine({ id: 7, keyword: 'hnsw', why: 'knobs', scoreHistory: [{ wave: 1, score: 80 }] }),
    ).toBe('#7 [80] hnsw — knobs');
  });
  it('renders an unscored entry as "new"', () => {
    expect(openLine({ id: 8, keyword: 'sharding', why: 'scale', scoreHistory: [] })).toBe(
      '#8 [new] sharding — scale',
    );
  });
  it('appends the kind, ⚔-prefixed for attack (v3 STEERING)', () => {
    expect(
      openLine({
        id: 9,
        keyword: 'counter-trial',
        why: 'refute',
        scoreHistory: [],
        kind: 'attack',
      }),
    ).toBe('#9 [new] counter-trial — refute ⚔attack');
    expect(
      openLine({ id: 10, keyword: 'author X', why: 'recurs', scoreHistory: [], kind: 'entity' }),
    ).toBe('#10 [new] author X — recurs ·entity');
  });
  it('renders no kind suffix (byte-identical) when kind is absent', () => {
    const withoutKind = openLine({ id: 11, keyword: 'a', why: 'b', scoreHistory: [] });
    expect(withoutKind).not.toContain('·');
    expect(withoutKind).not.toContain('⚔');
  });
});

describe('topSensitivityInput', () => {
  it('picks the input name with the largest variance share', () => {
    expect(topSensitivityInput({ a: 0.1, b: 0.6, c: 0.3 })).toBe('b');
  });
  it('returns "" for an empty or missing sensitivity map', () => {
    expect(topSensitivityInput({})).toBe('');
    expect(topSensitivityInput(undefined)).toBe('');
  });
});

describe('venuesWithYieldWarn', () => {
  const venues: Venue[] = [
    { source: 'arXiv', goodFor: 'preprints' },
    { source: 'PubMed', goodFor: 'clinical' },
  ];
  it('suffixes a venue assigned to ≥2 lanes with 0 yield', () => {
    const out = venuesWithYieldWarn(venues, { arXiv: { assigned: 3, yielded: 0 } });
    expect(out.find((v) => v.source === 'arXiv')!.goodFor).toBe('preprints — ⚠ 0 yield in 3 lanes');
    expect(out.find((v) => v.source === 'PubMed')!.goodFor).toBe('clinical'); // untouched — no stats entry
  });
  it('leaves a venue untouched when assigned < 2 or it has yielded something', () => {
    const oneLane = venuesWithYieldWarn(venues, { arXiv: { assigned: 1, yielded: 0 } });
    expect(oneLane.find((v) => v.source === 'arXiv')!.goodFor).toBe('preprints');
    const yielded = venuesWithYieldWarn(venues, { arXiv: { assigned: 3, yielded: 1 } });
    expect(yielded.find((v) => v.source === 'arXiv')!.goodFor).toBe('preprints');
  });
  it('is pure — never mutates the input venues array or its objects', () => {
    const before = JSON.stringify(venues);
    venuesWithYieldWarn(venues, { arXiv: { assigned: 5, yielded: 0 } });
    expect(JSON.stringify(venues)).toBe(before);
  });
  it('a venue with NO stats entry at all earns the never-assigned suffix once wave >= VENUE_UNROUTED_MIN_WAVE (2)', () => {
    const out = venuesWithYieldWarn(venues, { arXiv: { assigned: 3, yielded: 1 } }, 2);
    expect(out.find((v) => v.source === 'PubMed')!.goodFor).toBe(
      'clinical — ⚠ never assigned to any lane yet',
    );
    // a venue that DOES have a stats entry is unaffected by the never-assigned check
    expect(out.find((v) => v.source === 'arXiv')!.goodFor).toBe('preprints');
  });
  it('wave < VENUE_UNROUTED_MIN_WAVE, or wave omitted entirely, leaves a no-stats-entry venue untouched', () => {
    const early = venuesWithYieldWarn(venues, {}, 1);
    expect(early.find((v) => v.source === 'PubMed')!.goodFor).toBe('clinical');
    const noWave = venuesWithYieldWarn(venues, {});
    expect(noWave.find((v) => v.source === 'PubMed')!.goodFor).toBe('clinical');
  });
});

describe('resultSoFarMd', () => {
  it('returns _none_ for non-objects', () => {
    expect(resultSoFarMd(null)).toBe('_none_');
    expect(resultSoFarMd('x' as unknown as ResultSoFar)).toBe('_none_'); // exercise the non-object guard
  });
  it('renders the full memory block with working', () => {
    const md = resultSoFarMd({
      answer: 'A',
      confidence: 'high',
      working: 'cost = x',
      keyClaimIds: [1, 2],
      resolved: ['r'],
      openGaps: ['g'],
      tensions: ['t'],
    });
    expect(md).toContain('**Answer:** A');
    expect(md).toContain('**Confidence:** high');
    expect(md).toContain('**Working:**\n\ncost = x');
    expect(md).toContain('**Resolved:**\n- r');
  });
  it('renders an Assumptions block when present', () => {
    const md = resultSoFarMd({
      answer: 'A',
      confidence: 'high',
      working: '',
      keyClaimIds: [],
      assumptions: [{ claim: 'demand holds', basis: 'one analyst note — unconfirmed' }],
      resolved: [],
      openGaps: [],
      tensions: [],
    });
    expect(md).toContain('**Assumptions:**\n- **demand holds** — one analyst note — unconfirmed');
  });
  it('omits the working block when empty and shows _none_ for empty lists (K2: no dead Evidence section)', () => {
    const md = resultSoFarMd({
      answer: 'A',
      confidence: 'low',
      working: '',
      keyClaimIds: [],
      resolved: [],
      openGaps: [],
      tensions: [],
    });
    expect(md).not.toContain('**Working:**');
    expect(md).not.toContain('**Evidence:**'); // the ledger owns evidence now — the dead section is gone
    expect(md).toContain('**Resolved:**\n_none_');
  });
});

describe('waveMd', () => {
  it('renders a wave block with picks + open store', () => {
    const coord = {
      stop: { done: false, reason: 'more to verify' },
      resultSoFar: {
        answer: 'A',
        confidence: 'med',
        working: '',
        keyClaimIds: [],
        resolved: [],
        openGaps: [],
        tensions: [],
      },
    };
    const picks = [
      { id: 1, keyword: 'hnsw', why: 'knobs', score: 80, path: [], sources: ['arXiv'] },
    ];
    const store = [
      { id: 1, keyword: 'hnsw', scoreHistory: [{ wave: 1, score: 80 }] },
      { id: 2, keyword: 'sharding', scoreHistory: [] },
    ];
    const md = waveMd(2, coord, picks, [], store);
    expect(md).toContain('# Wave 2 — Brainer');
    expect(md).toContain('**done:** false — more to verify');
    expect(md).toContain('## Looking up next (1)');
    expect(md).toContain('1. **[80]** #1 hnsw');
    expect(md).toContain('venues: arXiv');
    expect(md).toContain('## Open rabbit-holes (scored)');
    expect(md.endsWith('\n')).toBe(true);
  });
});

describe('render', () => {
  it('fills placeholders from vars', () => expect(render('a {{x}} b\n', { x: 'Z' })).toBe('a Z b'));
  it('renders an absent key as empty string', () =>
    expect(render('[{{missing}}]\n', {})).toBe('[]'));
  it('strips exactly ONE trailing newline', () => {
    expect(render('line\n', {})).toBe('line');
    expect(render('line\n\n', {})).toBe('line\n');
    expect(render('line', {})).toBe('line');
  });
  it('substitutes a placeholder whose value carries a newline', () => {
    expect(render('a{{c}}\n', { c: '\nmore' })).toBe('a\nmore');
  });
});

describe('packReaders — the mechanical splitter (B5)', () => {
  const BUDGET = 130000;
  const OVERLAP = 2000;
  const CPT = 2; // charsPerToken these cases assume (= CONFIG.CHARS_PER_TOKEN), passed explicitly now that the param is required
  const src = (size: number, chars: number, n = 1): SchedulerSource => ({
    source: 'https://s' + n + '.com',
    path: '/cache/' + n + '.txt',
    size,
    chars,
  });

  it('COMBINES many small sources that fit the budget into ONE reader-unit', () => {
    const sources = Array.from({ length: 8 }, (_, i) => src(1000, 2000, i)); // 8×1000 = 8000 ≤ budget
    const readers = packReaders(sources, BUDGET, OVERLAP, Infinity, CPT);
    expect(readers.length).toBe(1); // one reader holds all 8 whole files
    expect(readers[0].length).toBe(8);
    expect(readers[0].every((r) => r.offset === 0 && r.limit === 2000)).toBe(true);
  });

  it('SPLITS an oversized source across ceil(size/budget) readers with overlap at each boundary', () => {
    const readers = packReaders([src(260000, 520000)], BUDGET, OVERLAP, Infinity, CPT); // 2× budget → 2 readers
    expect(readers.length).toBe(2);
    expect(readers[0]).toEqual([
      { source: 'https://s1.com', cachePath: '/cache/1.txt', offset: 0, limit: 260000 },
    ]);
    // second window starts OVERLAP chars early (258000) and runs to the end (520000)
    expect(readers[1]).toEqual([
      { source: 'https://s1.com', cachePath: '/cache/1.txt', offset: 258000, limit: 262000 },
    ]);
  });

  it('one protocol for "1 big + 2 small": the big splits, the smalls combine → 3 readers', () => {
    const readers = packReaders(
      [src(260000, 520000, 0), src(1000, 2000, 1), src(1000, 2000, 2)],
      BUDGET,
      OVERLAP,
      Infinity,
      CPT,
    );
    expect(readers.length).toBe(3);
    expect(readers[0].length).toBe(1); // big, part 1
    expect(readers[1].length).toBe(1); // big, part 2
    expect(readers[2].length).toBe(2); // the two smalls combined into the trailing reader
  });

  it('flushes the current pack before a small source that would overflow the budget', () => {
    const readers = packReaders(
      [src(120000, 240000, 0), src(120000, 240000, 1)],
      BUDGET,
      OVERLAP,
      Infinity,
      CPT,
    );
    expect(readers.length).toBe(2); // 120k + 120k > budget → cannot share a reader
    expect(readers[0].length).toBe(1);
    expect(readers[1].length).toBe(1);
  });

  it('guards junk: missing chars are derived from size, and pathless/empty sources are dropped', () => {
    const readers = packReaders(
      [
        { source: 'a', path: '/c/a.txt', size: 1000, chars: 0 } as SchedulerSource, // chars 0 → derive ~2000 from size
        { source: 'b', path: '', size: 1000, chars: 2000 } as SchedulerSource, // no path → dropped
      ],
      BUDGET,
      OVERLAP,
      Infinity,
      CPT,
    );
    expect(readers.length).toBe(1);
    expect(readers[0].length).toBe(1);
    expect(readers[0][0].limit).toBe(2000); // chars derived as size×2
  });

  it('returns no readers for an empty source list', () => {
    expect(packReaders([], BUDGET, OVERLAP, Infinity, CPT)).toEqual([]);
  });

  it('splits on the CHAR ceiling even when the token size says it fits (B2 unit consistency)', () => {
    // size 1000 ≤ budget, but chars 600000 ≫ budgetChars (260000) → must split by the char dimension
    const readers = packReaders([src(1000, 600000)], BUDGET, OVERLAP, Infinity, CPT);
    expect(readers.length).toBe(3); // ceil(600000 / 260000)
    expect(readers.every((r) => r.length === 1)).toBe(true);
    expect(readers.every((r) => r[0].limit <= 260000 + OVERLAP)).toBe(true); // no window exceeds the char ceiling + overlap
  });

  it('caps the slices combined into ONE reader at maxSlices (B7)', () => {
    const sources = Array.from({ length: 10 }, (_, i) => src(1000, 2000, i)); // 1 reader uncapped
    const readers = packReaders(sources, BUDGET, OVERLAP, 3, CPT); // cap 3 whole sources per reader
    expect(readers.length).toBe(4); // ceil(10 / 3)
    expect(readers[0].length).toBe(3);
    expect(readers[3].length).toBe(1);
  });

  it('honors a custom charsPerToken for the char ceiling', () => {
    // charsPerToken 1 → budgetChars = budget (130000); chars 200000 > 130000 → must split
    const readers = packReaders([src(1000, 200000)], BUDGET, OVERLAP, Infinity, 1);
    expect(readers.length).toBeGreaterThan(1);
  });
});
