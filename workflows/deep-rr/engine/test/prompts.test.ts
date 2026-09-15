import { describe, it, expect } from 'vitest';
// Per-agent prompt-builders now live on the agent objects (X.buildPrompt); the shared guard clauses in agents/shared.js.
// The aliases below keep the case table + snapshot KEYS byte-identical, so these snapshots remain the prompt byte-equivalence proof.
import {
  scout,
  scoutMerger,
  prospector,
  brainer,
  buildBrainerCompute,
  validator,
  researchScheduler,
  researcher,
  initiator,
  refiner,
  judge,
  synthesiser,
  debugAnalyst,
} from '../src/agents/index.js';
import { FINISH, WEB_ONLY } from '../src/agents/shared.js';
import type {
  SynthesiserArgs,
  CleanReport,
  JudgeArgs,
  InitiatorArgs,
  ResultSoFar,
  Venue,
  WaveLogEntry,
} from '../src/types/index.js';
const SCOUT_PROMPT = scout.buildPrompt;
const PROSPECTOR_PROMPT = prospector.buildPrompt;
const BRAINER_PROMPT = brainer.buildPrompt;
const VALIDATOR_PROMPT = validator.buildPrompt;
const SCHEDULER_PROMPT = researchScheduler.buildPrompt;
const RESEARCHER_PROMPT = researcher.buildPrompt;
const INITIATOR_PROMPT = initiator.buildPrompt;
const REFINE_PROMPT = refiner.buildPrompt;
const JUDGE_PROMPT = judge.buildPrompt;
const BRAIN_COMPUTE_PROMPT = buildBrainerCompute;
const SYNTHESISER_PROMPT = synthesiser.buildPrompt;
const DEBUG_PROMPT = debugAnalyst.buildPrompt;

const Q = 'best vector database for production RAG at scale';
const NET = 'NET-FRAGMENT';
const FOOTER = 'FOOTER-FRAGMENT';
const venues: Venue[] = [
  { source: 'arXiv (site:arxiv.org)', goodFor: 'ANN indexes' },
  { source: 'SemiAnalysis', goodFor: 'cost' },
];
const resultSoFar: ResultSoFar = {
  answer: 'pgvector',
  confidence: 'medium',
  evidence: [{ fact: 'recall', value: '0.98', source: 'arxiv', status: 'settled' }],
  keyClaimIds: [1],
  resolved: ['idx'],
  openGaps: ['mt'],
  tensions: [],
  working: 'cost=x',
};
const waveLog: WaveLogEntry[] = [
  { wave: 0, pursued: [], topScore: 0, done: false, reason: 'seed' },
];
const cleanReports: CleanReport[] = [{ fact: 'recall', why: 'lb', clean: '0.96 ± 0.02' }];
// scout is now one PROBE of the swarm, scoped to a single angle — a representative angle for the builder tests.
const SCOUT_ANGLE_ARGS = {
  angleName: 'skeptic',
  angleWhy: 'surfaces counter-evidence',
  angleLens: 'critical',
  searchQuery: Q + ' criticism',
  index: 1,
  total: 3,
};

// each builder on a representative input → { label, output }
const cases = [
  ['SCOUT_PROMPT', SCOUT_PROMPT({ query: Q, net: NET, footer: FOOTER, ...SCOUT_ANGLE_ARGS })],
  [
    'PROSPECTOR_PROMPT',
    PROSPECTOR_PROMPT({ query: Q, landscape: 'L', sources: ['https://a.com'] }),
  ],
  [
    'BRAINER_PROMPT/w0',
    BRAINER_PROMPT({
      wave: 0,
      query: Q,
      rubric: 'RUBRIC',
      landscape: 'L',
      pursuedList: [],
      open: [],
      findings: [],
      topScores: [],
      resultSoFar: null,
      stop: 'STOP',
      mode: 'goal',
      venues: [],
    }),
  ],
  [
    'BRAINER_PROMPT/wN',
    BRAINER_PROMPT({
      wave: 3,
      query: Q,
      rubric: 'RUBRIC',
      landscape: 'L',
      pursuedList: ['a'],
      open: ['#1 [80] x — y'],
      findings: [{ rabbitHole: 'x', summary: 's' }],
      topScores: [90, 70, 50],
      resultSoFar,
      stop: 'STOP',
      mode: 'goal',
      venues,
    }),
  ],
  [
    'BRAINER_PROMPT/collect',
    BRAINER_PROMPT({
      wave: 2,
      query: Q,
      rubric: 'RUBRIC',
      landscape: 'L',
      pursuedList: ['a'],
      open: ['#1 [80] x — y'],
      findings: [],
      topScores: [60, 40],
      resultSoFar,
      stop: 'STOP',
      mode: 'collect',
      venues,
    }),
  ],
  [
    'VALIDATOR_PROMPT',
    VALIDATOR_PROMPT({
      query: Q,
      requests: [{ id: 1, keyword: 'hnsw tuning', why: 'recall knobs' }],
      findings: [{ keyword: 'hnsw tuning', intro: 'covers ef/M tradeoffs' }],
      nullLanes: ['sharding'],
    }),
  ],
  [
    'SCHEDULER_PROMPT',
    SCHEDULER_PROMPT({
      query: Q,
      lanes: [
        {
          id: 1,
          keyword: 'hnsw tuning',
          why: 'recall knobs',
          note: 'find ef/M build-vs-query tradeoffs; if absent, the default recall/latency curve',
          venues,
          ref: '',
        },
      ],
    }),
  ],
  [
    'RESEARCHER_PROMPT',
    RESEARCHER_PROMPT({
      query: Q,
      trail: 'goal  →  x',
      keyword: 'x',
      why: 'w',
      note: 'extract the recall numbers; fallback to latency if recall is absent',
      footer: FOOTER,
      reads: [{ source: 'https://a.com', cachePath: '/cache/a.txt', offset: 0, limit: 2000 }],
      readerIndex: 2,
      readerCount: 3,
      priorAnswer: 'so far: pgvector recall ~0.96',
      claimDigest: 'c1 pgvector recall is 0.98 on the LAION benchmark',
    }),
  ],
  [
    'INITIATOR_PROMPT',
    INITIATOR_PROMPT({ query: Q, resultSoFar, waveLog, landscape: 'L', openRabbitHoles: ['x'] }),
  ],
  ['REFINE_PROMPT', REFINE_PROMPT({ net: NET, query: Q, fact: 'F', why: 'W' })],
  [
    'JUDGE_PROMPT',
    JUDGE_PROMPT({
      query: Q,
      resultSoFar,
      cleanReports,
      focus: 'lead with cost',
      openRabbitHoles: ['[70] multi-tenant isolation — unverified at scale'],
      compute: true,
    }),
  ],
  [
    'BRAIN_COMPUTE_PROMPT',
    BRAIN_COMPUTE_PROMPT({
      query: Q,
      resultSoFar,
      hardenedFacts: cleanReports,
      directive: 'derive blended cost/query with error bars',
      reason: 'the answer needs a number',
    }),
  ],
  [
    'SYNTHESISER_PROMPT',
    SYNTHESISER_PROMPT({
      mode: 'goal',
      query: Q,
      landscape: 'L',
      resultSoFar,
      waveLog,
      cleanReports,
      focus: 'lead with cost',
      openRabbitHoles: ['x'],
    }),
  ],
  [
    'DEBUG_PROMPT',
    DEBUG_PROMPT({
      query: Q,
      focus: 'why?',
      metrics: {
        mode: 'goal',
        dir: 'RR/x',
        wavesRun: 1,
        stopReason: 'brainer-done',
        scoutRabbitHoles: 3,
        prospectorVenues: 4,
        pursuedTotal: 5,
        rabbitHolesFinal: 2,
        bestOpenScore: 80,
        topScores: [80],
        done: true,
        reportWritten: true,
        confidence: 'high',
        claimsTotal: 0,
        nullAttacksTotal: 0,
        chao: null,
        citationsBogus: 0,
        citationsAuditFailed: 0,
        auditCounts: { pass: 0, fail: 0, repinned: 0, unpinned: 0, pending: 0 },
        quotesRepinned: 0,
        cachePathsRejected: 0,
        venuesUnrouted: 0,
        goalMet: true,
        judgePasses: 1,
        reopenedLanes: 0,
      },
      waveLog,
      resultLog: [],
      highValueSources: venues,
      laneRecords: [],
    }),
  ],
];

describe('prompt builders', () => {
  for (const [label, out] of cases) {
    it(`${label} → non-empty string, no leftover holes, anchors the query`, () => {
      expect(typeof out).toBe('string');
      expect(out.length).toBeGreaterThan(0);
      expect(out).not.toContain('{{');
      expect(out).not.toContain('}}');
    });
    it(`${label} → full-string snapshot`, () => {
      expect(out).toMatchSnapshot();
    });
  }
  it('the query anchors the prompts that take it', () => {
    expect(SCOUT_PROMPT({ query: Q, net: NET, footer: FOOTER, ...SCOUT_ANGLE_ARGS })).toContain(Q);
    expect(REFINE_PROMPT({ net: NET, query: Q, fact: 'F', why: 'W' })).toContain(Q);
  });
  it('FINISH / WEB_ONLY guard clauses ride into the right prompts', () => {
    expect(
      BRAINER_PROMPT({
        wave: 0,
        query: Q,
        rubric: 'R',
        landscape: 'L',
        pursuedList: [],
        open: [],
        findings: [],
        topScores: [],
        resultSoFar: null,
        stop: 'S',
        mode: 'goal',
        venues: [],
      }),
    ).toContain(FINISH.trim());
    expect(REFINE_PROMPT({ net: NET, query: Q, fact: 'F', why: 'W' })).toContain(WEB_ONLY.trim());
  });
});

describe('researcherNote — the web-research/probe-agent passthrough', () => {
  const RN = 'Research note: prefer 2025 primary sources';
  // the recipients (scout, prospector, scheduler, researcher, brainer) — each carrying researcherNote
  const recipients = {
    SCOUT_PROMPT: SCOUT_PROMPT({
      query: Q,
      net: NET,
      footer: FOOTER,
      ...SCOUT_ANGLE_ARGS,
      researcherNote: RN,
    }),
    PROSPECTOR_PROMPT: PROSPECTOR_PROMPT({
      query: Q,
      landscape: 'L',
      sources: ['https://a.com'],
      researcherNote: RN,
    }),
    SCHEDULER_PROMPT: SCHEDULER_PROMPT({
      query: Q,
      lanes: [{ id: 1, keyword: 'x', why: 'w', note: 'n', venues, ref: '' }],
      researcherNote: RN,
    }),
    RESEARCHER_PROMPT: RESEARCHER_PROMPT({
      query: Q,
      trail: 'goal  →  x',
      keyword: 'x',
      why: 'w',
      note: 'n',
      footer: FOOTER,
      reads: [{ source: 'https://a.com', cachePath: '/cache/a.txt', offset: 0, limit: 100 }],
      readerIndex: 1,
      readerCount: 1,
      priorAnswer: '',
      claimDigest: '',
      researcherNote: RN,
    }),
    BRAINER_PROMPT: BRAINER_PROMPT({
      wave: 1,
      query: Q,
      rubric: 'R',
      landscape: 'L',
      pursuedList: [],
      open: [],
      findings: [],
      topScores: [],
      resultSoFar: null,
      stop: 'S',
      mode: 'goal',
      venues: [],
      researcherNote: RN,
    }),
  };
  for (const [label, out] of Object.entries(recipients)) {
    it(`${label} folds in researcherNote when supplied`, () => expect(out).toContain(RN));
  }
  it('renders nothing when researcherNote is empty (the recipients)', () => {
    expect(
      SCOUT_PROMPT({ query: Q, net: NET, footer: FOOTER, ...SCOUT_ANGLE_ARGS, researcherNote: '' }),
    ).not.toContain('Research note');
    expect(
      PROSPECTOR_PROMPT({ query: Q, landscape: 'L', sources: ['https://a.com'] }),
    ).not.toContain('Research note');
    expect(
      RESEARCHER_PROMPT({
        query: Q,
        trail: 't',
        keyword: 'x',
        why: 'w',
        note: 'n',
        footer: FOOTER,
        reads: [{ source: 'https://a.com', cachePath: '/cache/a.txt', offset: 0, limit: 100 }],
        readerIndex: 1,
        readerCount: 1,
        priorAnswer: '',
        claimDigest: '',
      }),
    ).not.toContain('Research note');
    expect(
      BRAINER_PROMPT({
        wave: 1,
        query: Q,
        rubric: 'R',
        landscape: 'L',
        pursuedList: [],
        open: [],
        findings: [],
        topScores: [],
        resultSoFar: null,
        stop: 'S',
        mode: 'goal',
        venues: [],
      }),
    ).not.toContain('Research note');
  });
  it('never reaches the non-recipient agents even if a note is passed (initiator / judge / synthesiser)', () => {
    // these three builders do not accept researcherNote; the casts deliberately smuggle it in to prove it never reaches the prompt.
    expect(
      INITIATOR_PROMPT({
        query: Q,
        resultSoFar,
        waveLog,
        landscape: 'L',
        openRabbitHoles: ['x'],
        researcherNote: RN,
      } as InitiatorArgs),
    ).not.toContain(RN);
    expect(
      JUDGE_PROMPT({
        query: Q,
        resultSoFar,
        cleanReports,
        focus: 'f',
        openRabbitHoles: [],
        compute: true,
        researcherNote: RN,
      } as JudgeArgs),
    ).not.toContain(RN);
    expect(
      SYNTHESISER_PROMPT({
        mode: 'goal',
        query: Q,
        landscape: 'L',
        resultSoFar,
        waveLog,
        cleanReports,
        focus: 'f',
        openRabbitHoles: ['x'],
        researcherNote: RN,
      } as SynthesiserArgs),
    ).not.toContain(RN);
  });
});

describe('multilingual routing — conditional, prospector → brainer → researcher', () => {
  const guidance =
    'Cover Chinese (CNKI) and Japanese (J-STAGE) — most clinical work on this disease is published there.';
  const nativeVenues: Venue[] = [
    { source: 'CNKI', goodFor: 'Chinese biomedical literature', lang: 'zh' },
    { source: 'arXiv (site:arxiv.org)', goodFor: 'preprints' },
  ];
  const brainerBase = {
    wave: 1,
    query: Q,
    rubric: 'R',
    landscape: 'L',
    pursuedList: [],
    open: [],
    findings: [],
    topScores: [],
    resultSoFar: null,
    stop: 'S',
    mode: 'goal' as const,
  };
  it('languageGuidance threads prospector → brainer when non-empty', () => {
    const out = BRAINER_PROMPT({
      ...brainerBase,
      venues: nativeVenues,
      languageGuidance: guidance,
    });
    expect(out).toContain(guidance);
    expect(out).toContain('non-English');
  });
  it('the brainer language clause renders nothing for an English-dominated run', () => {
    const en = BRAINER_PROMPT({ ...brainerBase, venues, languageGuidance: '' });
    expect(en).not.toContain('non-English');
    // empty languageGuidance is byte-identical to passing none at all
    expect(en).toBe(BRAINER_PROMPT({ ...brainerBase, venues }));
  });
  it('a non-English venue triggers the scheduler translate clause + lang tag (the scheduler now searches)', () => {
    const out = SCHEDULER_PROMPT({
      query: Q,
      lanes: [{ id: 1, keyword: 'x', why: 'w', note: 'n', venues: nativeVenues, ref: '' }],
    });
    expect(out).toContain('CNKI [zh] (Chinese biomedical literature)');
    expect(out).toContain('translate its query terms');
  });
  it('an all-English lane leaves the scheduler prompt untouched (no translate clause, no lang tag)', () => {
    const out = SCHEDULER_PROMPT({
      query: Q,
      lanes: [{ id: 1, keyword: 'x', why: 'w', note: 'n', venues, ref: '' }],
    });
    expect(out).not.toContain('translate its query terms');
    expect(out).toContain('arXiv (site:arxiv.org) (ANN indexes)'); // un-tagged, exactly as before
  });
});

describe('follow-the-links — the scheduler takes a carried ref directly', () => {
  const lane = { id: 1, keyword: 'x', why: 'w', note: 'n', venues };
  it('surfaces a carried ref to the scheduler as a direct source', () => {
    const out = SCHEDULER_PROMPT({ query: Q, lanes: [{ ...lane, ref: '10.1234/foo' }] });
    expect(out).toContain('10.1234/foo');
    expect(out).toContain('ref (fetch directly)');
  });
  it('renders no ref line (byte-identical) when no ref is carried', () => {
    const none = SCHEDULER_PROMPT({ query: Q, lanes: [{ ...lane, ref: '' }] });
    expect(none).not.toContain('ref (fetch directly)');
    expect(none).toBe(SCHEDULER_PROMPT({ query: Q, lanes: [lane] }));
  });
});

describe('researcher — claim ledger channels (v3 batch 2b)', () => {
  const base = {
    query: Q,
    trail: 'goal  →  x',
    keyword: 'x',
    why: 'w',
    note: 'n',
    footer: FOOTER,
    reads: [{ source: 'https://a.com', cachePath: '/cache/a.txt', offset: 0, limit: 100 }],
    readerIndex: 1,
    readerCount: 1,
    priorAnswer: '',
  };
  it('renders the KEY CLAIMS SO FAR section when claimDigest is non-empty', () => {
    const out = RESEARCHER_PROMPT({ ...base, claimDigest: 'c1 pgvector recall is 0.98' });
    expect(out).toContain('KEY CLAIMS SO FAR');
    expect(out).toContain('ids look like c12');
    expect(out).toContain('SUPPORTS or ATTACKS');
    expect(out).toContain('c1 pgvector recall is 0.98');
  });
  it('omits the KEY CLAIMS section entirely when claimDigest is empty — DIRECTIVE flows straight into READ NOW', () => {
    const none = RESEARCHER_PROMPT({ ...base, claimDigest: '' });
    expect(none).not.toContain('KEY CLAIMS SO FAR');
    expect(none).toContain('DIRECTIVE — what to extract, with ranked fallbacks: n\nREAD NOW —');
  });
  it('always carries the no-quote-no-claim discipline line', () => {
    const out = RESEARCHER_PROMPT({ ...base, claimDigest: '' });
    expect(out).toContain('no quote, no claim');
    expect(out).toContain('Extract each load-bearing fact as a claim');
  });
  it('an ATTACK lane gets the counter-evidence primary-output clause', () => {
    const out = RESEARCHER_PROMPT({ ...base, claimDigest: '', laneKind: 'attack' });
    expect(out).toContain('ATTACK lane');
    expect(out).toContain("kind:'attacks'");
    expect(out).toContain('never manufacture doubt');
  });
  it('a non-attack (or absent) laneKind renders no attack clause — byte-identical', () => {
    const gap = RESEARCHER_PROMPT({ ...base, claimDigest: '', laneKind: 'gap' });
    const none = RESEARCHER_PROMPT({ ...base, claimDigest: '' });
    expect(gap).not.toContain('ATTACK lane');
    expect(gap).toBe(none);
  });
});

describe('scout — claim ledger channels (v3 batch 2b)', () => {
  it('Step 3 enumerates claims + newTerms, mirroring the no-quote-no-claim rule', () => {
    const out = SCOUT_PROMPT({ query: Q, net: NET, footer: FOOTER, ...SCOUT_ANGLE_ARGS });
    expect(out).toContain('claims[]');
    expect(out).toContain('no quote no claim');
    expect(out).toContain('newTerms[]');
  });
});

// E — the scout schema now carries nextSources (the FOOTER's "Next sources" channel, previously discarded);
// the probe prompt enumerates it and skips the footer's Surprise section (no prior claims exist at wave 0).
describe('scout — nextSources channel (v3 batch 6, finding E)', () => {
  it('Step 3 enumerates nextSources and Step 2 skips the Surprise section', () => {
    const out = SCOUT_PROMPT({ query: Q, net: NET, footer: FOOTER, ...SCOUT_ANGLE_ARGS });
    expect(out).toContain('nextSources[]');
    expect(out).toContain("Skip the footer's Surprise section");
  });
  it('scoutMerger unions nextSources deduped by ref', () => {
    const out = scoutMerger.buildPrompt({ query: Q, decomposition: 'axis', probes: [] });
    expect(out).toContain('nextSources: the union of every probe');
    expect(out).toContain('deduped by ref');
  });
});

describe('scout — one probe of the swarm, scoped to a single angle (v3 batch 2s)', () => {
  it('names its angle, why, lens, and its OWN searchQuery — not the raw query', () => {
    const out = SCOUT_PROMPT({ query: Q, net: NET, footer: FOOTER, ...SCOUT_ANGLE_ARGS });
    expect(out).toContain('«skeptic»');
    expect(out).toContain('surfaces counter-evidence');
    expect(out).toContain('critical');
    expect(out).toContain(SCOUT_ANGLE_ARGS.searchQuery);
    expect(out).toContain('probe 1 of 3');
  });
  it('carries the footer verbatim (the rabbit-hole discipline applies to every fetched page)', () => {
    const out = SCOUT_PROMPT({ query: Q, net: NET, footer: FOOTER, ...SCOUT_ANGLE_ARGS });
    expect(out).toContain('<<' + FOOTER + '>>');
  });
});

describe('validator — per-wave coverage gate', () => {
  const base = {
    query: Q,
    requests: [{ id: 1, keyword: 'k', why: 'w' }],
    findings: [{ keyword: 'k', intro: 'i' }],
  };
  it('lists the dead lanes when any returned nothing', () => {
    const out = VALIDATOR_PROMPT({ ...base, nullLanes: ['sharding'] });
    expect(out).toContain('Lanes that returned nothing');
    expect(out).toContain('sharding');
  });
  it('renders nothing (byte-identical) when no lane died', () => {
    const none = VALIDATOR_PROMPT({ ...base, nullLanes: [] });
    expect(none).not.toContain('Lanes that returned nothing');
  });
});

describe('validator → brainer feedback', () => {
  const base = {
    wave: 2,
    query: Q,
    rubric: 'R',
    landscape: 'L',
    pursuedList: [],
    open: [],
    findings: [],
    topScores: [],
    resultSoFar: null,
    stop: 'S',
    mode: 'goal' as const,
    venues: [],
  };
  const missing = 'multi-tenant isolation; sharding (lane retried twice — treat as a known gap)';
  it('threads the validator gaps into the brainer when set', () => {
    const out = BRAINER_PROMPT({ ...base, lastValidatorMissing: missing });
    expect(out).toContain('VALIDATOR — last wave left these unfilled');
    expect(out).toContain(missing);
  });
  it('renders nothing (byte-identical) when there are no validator gaps', () => {
    const none = BRAINER_PROMPT({ ...base, lastValidatorMissing: '' });
    expect(none).not.toContain('VALIDATOR — last wave left these unfilled');
    expect(none).toBe(BRAINER_PROMPT(base));
  });
});

describe('brainer — the working-derivation clause is gated on compute (A1a)', () => {
  const base = {
    wave: 1,
    query: Q,
    rubric: 'R',
    landscape: 'L',
    pursuedList: [],
    open: [],
    findings: [],
    topScores: [],
    resultSoFar: null,
    stop: 'S',
    mode: 'goal' as const,
    venues: [],
  };
  it('compute ON ⇒ instructs growing the `working` derivation chain', () => {
    const out = BRAINER_PROMPT({ ...base, compute: true });
    expect(out).toContain('grow the `working` derivation chain');
    expect(out).not.toContain('STATED UNCERTAINTY');
  });
  it('compute OFF ⇒ leave `working` empty + STATED UNCERTAINTY, never hand-roll a derivation', () => {
    const out = BRAINER_PROMPT({ ...base, compute: false });
    expect(out).toContain('STATED UNCERTAINTY');
    expect(out).toContain('never hand-roll a derivation');
    expect(out).not.toContain('grow the `working` derivation chain');
    expect(out).not.toContain('COMPUTE TO STEER'); // the steering-compute block is also gated off
  });
  it('derivationClause (compute on) instructs AUTHORING the derivation as a stored artifact', () => {
    const out = BRAINER_PROMPT({ ...base, compute: true });
    expect(out).toContain('AUTHOR the derivation once as a stored artifact');
    expect(out).toContain('return `derivation` {code, inputs}');
    expect(out).toContain('mark unevidenced inputs prior:true with a WIDE dist');
    expect(out).toContain('Fold the headline number into `working`');
  });
});

// v3 STEERING (batch 3) — the ledger/calibration/sensitivity/coverage-aware brainer clauses. Each is
// OMITTED entirely (byte-identical to the base render) when its engine-supplied input is empty/absent.
describe('brainer — v3 STEERING clauses (batch 3)', () => {
  const base = {
    wave: 1,
    query: Q,
    rubric: 'R',
    landscape: 'L',
    pursuedList: [],
    open: [],
    findings: [],
    topScores: [],
    resultSoFar: null,
    stop: 'S',
    mode: 'goal' as const,
    venues: [],
  };
  it('ledgerClause + attackClause render together once the ledger digest is non-empty', () => {
    const out = BRAINER_PROMPT({ ...base, ledger: 'c1 [tentative·clu0·pass] X — s' });
    expect(out).toContain('CLAIM LEDGER');
    expect(out).toContain('c1 [tentative·clu0·pass] X — s');
    expect(out).toContain('ids look like c12, clusters like clu2');
    expect(out).toContain('Corroboration counts independence CLUSTERS');
    expect(out).toContain('do not re-emit facts into resultSoFar.evidence');
    expect(out).toContain('kind:"attack" lane');
  });
  it('omits CLAIM LEDGER + the attack discipline (byte-identical) before any claim is ledgered', () => {
    const none = BRAINER_PROMPT({ ...base, ledger: '' });
    expect(none).not.toContain('CLAIM LEDGER');
    expect(none).not.toContain('kind:"attack" lane');
    expect(none).toBe(BRAINER_PROMPT(base));
  });
  it('calibrationClause renders only the kinds with a real observation (n>0)', () => {
    const out = BRAINER_PROMPT({
      ...base,
      calib: { gap: { n: 3, ratio: 0.8 }, attack: { n: 0, ratio: 1 } },
    });
    expect(out).toContain('CALIBRATION');
    expect(out).toContain('gap: 0.80 (3)');
    expect(out).not.toContain('attack:');
  });
  it('omits CALIBRATION (byte-identical) when every kind is unobserved or calib is absent', () => {
    const none = BRAINER_PROMPT({ ...base, calib: {} });
    expect(none).not.toContain('CALIBRATION');
    expect(none).toBe(BRAINER_PROMPT(base));
  });
  it('sensitivityClause + voiClause render once a derivation has a completed rerun', () => {
    const out = BRAINER_PROMPT({
      ...base,
      mode: 'goal',
      derivation: {
        quantiles: { p50: 100 },
        sensitivity: { cost: 0.7, volume: 0.2 },
        inputs: [
          { name: 'cost', dist: 'lognormal(mu=1,sigma=0.2)', claimIds: [1], prior: false },
          { name: 'volume', dist: 'wide', claimIds: [], prior: true },
        ],
        stale: false,
      },
    });
    expect(out).toContain('DERIVATION STATE');
    expect(out).toContain('p50=100');
    expect(out).toContain('cost: 0.70');
    expect(out).toContain('volume: 0.20 (PRIOR)');
    expect(out).toContain('a lane that pins a cost beats any topical lead');
    expect(out).toContain('STOP TEST (value-of-information)');
    expect(out).not.toContain('STALE');
  });
  it('flags STALE when the last rerun failed', () => {
    const out = BRAINER_PROMPT({
      ...base,
      derivation: { quantiles: {}, sensitivity: {}, inputs: [], stale: true },
    });
    expect(out).toContain('STALE — last rerun failed; consider re-emitting the derivation');
  });
  it('omits DERIVATION STATE / STOP TEST (byte-identical) before any rerun has completed', () => {
    const none = BRAINER_PROMPT({ ...base, derivation: undefined });
    expect(none).not.toContain('DERIVATION STATE');
    expect(none).not.toContain('STOP TEST');
    expect(none).toBe(BRAINER_PROMPT(base));
  });
  it('voiClause is goal-mode only — collect mode omits the STOP TEST even with a derivation', () => {
    const out = BRAINER_PROMPT({
      ...base,
      mode: 'collect',
      derivation: { quantiles: {}, sensitivity: {}, inputs: [], stale: false },
    });
    expect(out).toContain('DERIVATION STATE'); // sensitivity still shows
    expect(out).not.toContain('STOP TEST');
  });
  it('chaoClause renders in collect mode once chao is computed', () => {
    const out = BRAINER_PROMPT({ ...base, mode: 'collect', chao: { unseen: 4.2, coverage: 0.62 } });
    expect(out).toContain('COVERAGE');
    expect(out).toContain('~4 distinct findings remain unfound');
    expect(out).toContain('coverage ≈62%');
  });
  it('omits COVERAGE (byte-identical) in goal mode or before chao is computed', () => {
    const goalMode = BRAINER_PROMPT({ ...base, mode: 'goal', chao: { unseen: 4, coverage: 0.9 } });
    expect(goalMode).not.toContain('COVERAGE');
    const noChao = BRAINER_PROMPT({ ...base, mode: 'collect', chao: null });
    expect(noChao).not.toContain('COVERAGE');
    expect(noChao).toBe(BRAINER_PROMPT({ ...base, mode: 'collect' }));
  });
});

describe('scheduler — community vocabulary (v3 STEERING)', () => {
  const lane = { id: 1, keyword: 'x', why: 'w', note: 'n', venues };
  it('renders the COMMUNITY VOCABULARY clause when vocabulary is non-empty', () => {
    const out = SCHEDULER_PROMPT({ query: Q, lanes: [lane], vocabulary: 'biomarker (5), RCT (2)' });
    expect(out).toContain('COMMUNITY VOCABULARY');
    expect(out).toContain('biomarker (5), RCT (2)');
  });
  it('omits the clause (byte-identical) when vocabulary is empty/absent', () => {
    const none = SCHEDULER_PROMPT({ query: Q, lanes: [lane], vocabulary: '' });
    expect(none).not.toContain('COMMUNITY VOCABULARY');
    expect(none).toBe(SCHEDULER_PROMPT({ query: Q, lanes: [lane] }));
  });
});

describe('synthesiser — the derivation mention is gated on compute (A1b)', () => {
  const base = {
    mode: 'goal' as const,
    query: Q,
    landscape: 'L',
    resultSoFar, // carries working: 'cost=x'
    waveLog,
    cleanReports,
    focus: 'lead with cost',
    openRabbitHoles: ['x'],
  };
  it('compute ON (or unset) + a `working` derivation ⇒ presents it verbatim', () => {
    expect(SYNTHESISER_PROMPT({ ...base, compute: true })).toContain('present it verbatim');
    expect(SYNTHESISER_PROMPT(base)).toContain('present it verbatim'); // unset ⇒ present-when-derived default
  });
  it('compute OFF ⇒ never presents a derivation even when `working` is non-empty', () => {
    const out = SYNTHESISER_PROMPT({ ...base, compute: false });
    expect(out).not.toContain('present it verbatim');
    expect(out).not.toContain('LEADING with the computed result');
  });
});

describe('initiator — collect mode hardens breadth, not a single answer (A4)', () => {
  const base = { query: Q, resultSoFar, waveLog, landscape: 'L', openRabbitHoles: ['x'] };
  it('collect ⇒ harden BREADTH / completeness of the catalogue', () => {
    const out = INITIATOR_PROMPT({ ...base, mode: 'collect' });
    expect(out).toContain('COLLECT inventory');
    expect(out).toContain('completeness of the catalogue');
  });
  it('goal (or unset) ⇒ no collect breadth clause', () => {
    expect(INITIATOR_PROMPT({ ...base, mode: 'goal' })).not.toContain('COLLECT inventory');
    expect(INITIATOR_PROMPT(base)).not.toContain('COLLECT inventory');
  });
});

describe('judge — collect mode gates goalMet on inventory-completeness (A4)', () => {
  const base = {
    query: Q,
    resultSoFar,
    cleanReports,
    focus: 'lead with cost',
    openRabbitHoles: [],
    compute: true,
  };
  it('collect ⇒ judge goalMet as INVENTORY COMPLETENESS', () => {
    const out = JUDGE_PROMPT({ ...base, mode: 'collect' });
    expect(out).toContain('INVENTORY COMPLETENESS');
    expect(out).toContain('individually verified');
  });
  it('goal (or unset) ⇒ no inventory-completeness clause', () => {
    expect(JUDGE_PROMPT({ ...base, mode: 'goal' })).not.toContain('INVENTORY COMPLETENESS');
    expect(JUDGE_PROMPT(base)).not.toContain('INVENTORY COMPLETENESS');
  });
});

describe('judge — the finalize compute switch', () => {
  const base = {
    query: Q,
    resultSoFar,
    cleanReports,
    focus: 'lead with cost',
    openRabbitHoles: [],
  };
  it('offers the derivation path when compute is on', () => {
    const out = JUDGE_PROMPT({ ...base, compute: true });
    expect(out).toContain('A derivation may be written and run');
    expect(out).not.toContain('Derivation is off');
  });
  it('disables the derivation path when compute is off but allows an HONEST needsCompute (A1c)', () => {
    const out = JUDGE_PROMPT({ ...base, compute: false });
    expect(out).toContain('Derivation is off for this run');
    expect(out).toContain('set needsCompute false');
    // honesty path: the judge may still flag a genuinely-needed derivation as a stated limitation
    expect(out).toContain('set needsCompute true');
    expect(out).toContain('stated limitation');
  });
});

describe('judge — the leftover open-rabbit-holes input (B1-refinement)', () => {
  const base = {
    query: Q,
    resultSoFar,
    cleanReports,
    focus: 'lead with cost',
    compute: true,
  };
  it('threads the leftover open rabbit-holes in and offers to reopen the crawl', () => {
    const out = JUDGE_PROMPT({
      ...base,
      openRabbitHoles: ['[80] multi-tenant isolation — unverified'],
    });
    expect(out).toContain('LEFTOVER OPEN RABBIT-HOLES');
    expect(out).toContain('multi-tenant isolation');
    expect(out).toContain('reopen the crawl');
  });
  it('renders nothing (byte-identical) when there are no leftover rabbit-holes', () => {
    const none = JUDGE_PROMPT({ ...base, openRabbitHoles: [] });
    expect(none).not.toContain('LEFTOVER OPEN RABBIT-HOLES');
  });
});

// v3 FINALIZE (batch 4) — the ledger/nullAttacks/confidence/stop-reconcile judge clauses. Each is OMITTED
// entirely (clause-builder style) when its engine-supplied input is empty/absent.
describe('judge — v3 FINALIZE clauses (batch 4)', () => {
  const base = {
    query: Q,
    resultSoFar,
    cleanReports,
    focus: 'lead with cost',
    openRabbitHoles: [],
    compute: true,
  };
  it('ledgerClause renders the digest + the cluster-independence discipline', () => {
    const out = JUDGE_PROMPT({ ...base, ledger: 'c1 [tentative·clu0·pass] X — s' });
    expect(out).toContain('CLAIM LEDGER');
    expect(out).toContain('c1 [tentative·clu0·pass] X — s');
    expect(out).toContain('ids look like c12, clusters like clu2');
    expect(out).toContain('Corroboration counts CLUSTERS');
    expect(out).toContain('SINGLE-SOURCE however many names it wears');
  });
  it('omits CLAIM LEDGER (byte-identical) when the ledger digest is empty', () => {
    const none = JUDGE_PROMPT({ ...base, ledger: '' });
    expect(none).not.toContain('CLAIM LEDGER');
    expect(none).toBe(JUDGE_PROMPT(base));
  });
  it('nullAttacksClause renders CHALLENGED AND SURVIVED + NEVER CHALLENGED independently', () => {
    const survivedOnly = JUDGE_PROMPT({
      ...base,
      survivedAttacks: ['gap topic → c1 (queries: q1)'],
    });
    expect(survivedOnly).toContain('CHALLENGED AND SURVIVED');
    expect(survivedOnly).toContain('gap topic → c1');
    expect(survivedOnly).not.toContain('NEVER CHALLENGED');
    const neverOnly = JUDGE_PROMPT({ ...base, neverChallenged: ['c3 an untested key claim'] });
    expect(neverOnly).toContain('NEVER CHALLENGED');
    expect(neverOnly).toContain('c3 an untested key claim');
    expect(neverOnly).not.toContain('CHALLENGED AND SURVIVED');
  });
  it('omits the nullAttacks clause (byte-identical) when both lists are empty', () => {
    const none = JUDGE_PROMPT({ ...base, survivedAttacks: [], neverChallenged: [] });
    expect(none).not.toContain('CHALLENGED AND SURVIVED');
    expect(none).not.toContain('NEVER CHALLENGED');
    expect(none).toBe(JUDGE_PROMPT(base));
  });
  it('confidenceClause states the computed confidence + that the judge never sets it itself (H)', () => {
    const out = JUDGE_PROMPT({ ...base, computedConfidence: 'low' });
    expect(out).toContain('Machinery-computed confidence from evidence topology: low');
    expect(out).toContain('weigh it when judging `verificationSound`');
    expect(out).toContain('you do not set confidence yourself');
    expect(out).toContain('only the synthesiser does, and it may only lower this value');
  });
  it('omits the confidence clause (byte-identical) when absent', () => {
    expect(JUDGE_PROMPT(base)).toBe(JUDGE_PROMPT(base));
    expect(JUDGE_PROMPT({ ...base, computedConfidence: undefined })).toBe(JUDGE_PROMPT(base));
  });
  it("STOP RECONCILE: names the crawl's own stop reason and forbids silently converting it into a caveat", () => {
    const out = JUDGE_PROMPT({
      ...base,
      stop: { done: true, reason: 'one more focused wave is warranted' },
    });
    expect(out).toContain("THE CRAWL'S LAST WORD");
    expect(out).toContain('done=true');
    expect(out).toContain('one more focused wave is warranted');
    expect(out).toContain('never silently convert remaining work into a caveat');
  });
  it('omits STOP RECONCILE (byte-identical) when no stop is supplied', () => {
    const none = JUDGE_PROMPT({ ...base, stop: undefined });
    expect(none).not.toContain("THE CRAWL'S LAST WORD");
    expect(none).toBe(JUDGE_PROMPT(base));
  });
  it('the Return line names retractClaimIds', () => {
    expect(JUDGE_PROMPT(base)).toContain('retractClaimIds');
  });
});

describe('synthesiser — v3 FINALIZE clauses (batch 4)', () => {
  const base = {
    mode: 'goal' as const,
    query: Q,
    landscape: 'L',
    resultSoFar,
    waveLog,
    cleanReports,
    focus: 'lead with cost',
    openRabbitHoles: ['x'],
  };
  it('ledgerClause instructs citing [c12] ids from the digest, independence from clusters only', () => {
    const out = SYNTHESISER_PROMPT({ ...base, ledger: 'c1 [settled·clu1·pass] X — s' });
    expect(out).toContain('CLAIM LEDGER');
    expect(out).toContain('c1 [settled·clu1·pass] X — s');
    expect(out).toContain('ids look like c12, clusters like clu2');
    expect(out).toContain('Cite ledger claims inline as [c12]');
    expect(out).toContain('must be a real ledger id from the digest above');
    expect(out).toContain('independence ONLY from cluster counts');
  });
  it('omits the ledger clause (byte-identical) when the digest is empty', () => {
    const none = SYNTHESISER_PROMPT({ ...base, ledger: '' });
    expect(none).not.toContain('CLAIM LEDGER');
    expect(none).toBe(SYNTHESISER_PROMPT(base));
  });
  it('nullAttacksSummary renders the challenged-and-survived one-liners', () => {
    const out = SYNTHESISER_PROMPT({ ...base, nullAttacksSummary: ['gap topic → c1'] });
    expect(out).toContain('CHALLENGED AND SURVIVED');
    expect(out).toContain('gap topic → c1');
  });
  it('omits the nullAttacks clause (byte-identical) when empty', () => {
    const none = SYNTHESISER_PROMPT({ ...base, nullAttacksSummary: [] });
    expect(none).not.toContain('CHALLENGED AND SURVIVED');
    expect(none).toBe(SYNTHESISER_PROMPT(base));
  });
  it('confidenceClause states the computed confidence, lower-only', () => {
    const out = SYNTHESISER_PROMPT({ ...base, computedConfidence: 'medium' });
    expect(out).toContain('Machinery-computed confidence: medium');
    expect(out).toContain('may be lower with a stated reason, never higher');
  });
  it('omits the confidence clause (byte-identical) when absent', () => {
    expect(SYNTHESISER_PROMPT({ ...base, computedConfidence: undefined })).toBe(
      SYNTHESISER_PROMPT(base),
    );
  });
});

describe('initiator — v3 FINALIZE clauses (batch 4)', () => {
  const base = { query: Q, resultSoFar, waveLog, landscape: 'L', openRabbitHoles: ['x'] };
  it('ledgerClause renders the CLAIM LEDGER digest', () => {
    const out = INITIATOR_PROMPT({ ...base, ledger: 'c1 [tentative·clu0·pass] X — s' });
    expect(out).toContain('CLAIM LEDGER');
    expect(out).toContain('c1 [tentative·clu0·pass] X — s');
    expect(out).toContain('ids look like c12, clusters like clu2');
  });
  it('omits the ledger clause (byte-identical) when the digest is empty', () => {
    const none = INITIATOR_PROMPT({ ...base, ledger: '' });
    expect(none).not.toContain('CLAIM LEDGER');
    expect(none).toBe(INITIATOR_PROMPT(base));
  });
  it('sensitivityClause renders when a derivation rerun has produced a ranking', () => {
    const out = INITIATOR_PROMPT({ ...base, sensitivity: '- cost (0.70) — backed by c1 X' });
    expect(out).toContain('SENSITIVITY RANKING');
    expect(out).toContain('- cost (0.70) — backed by c1 X');
    expect(out).toContain('Prioritize hardening the claims behind the top-variance inputs');
  });
  it('omits SENSITIVITY RANKING (byte-identical) before any rerun has completed', () => {
    const none = INITIATOR_PROMPT({ ...base, sensitivity: '' });
    expect(none).not.toContain('SENSITIVITY RANKING');
    expect(none).toBe(INITIATOR_PROMPT(base));
  });
  it('the facts instruction names claimId and its binding to the ledger record', () => {
    expect(INITIATOR_PROMPT(base)).toContain('set its claimId');
  });
});

describe('refiner — v3 FINALIZE clause: THE CLAIM AS PINNED (batch 4)', () => {
  it('pinnedClause renders the claim quote + source when the fact is bound to a ledger claim', () => {
    const out = REFINE_PROMPT({
      net: NET,
      query: Q,
      fact: 'F',
      why: 'W',
      claimQuote: 'the exact verbatim span',
      claimSource: 'https://example.com/p',
    });
    expect(out).toContain('THE CLAIM AS PINNED');
    expect(out).toContain('the exact verbatim span');
    expect(out).toContain('https://example.com/p');
  });
  it('omits THE CLAIM AS PINNED (byte-identical) when the fact carries no claimId', () => {
    const none = REFINE_PROMPT({ net: NET, query: Q, fact: 'F', why: 'W' });
    expect(none).not.toContain('THE CLAIM AS PINNED');
    expect(none).toBe(REFINE_PROMPT({ net: NET, query: Q, fact: 'F', why: 'W' }));
  });
  it('the Return line names the attack-recording fields', () => {
    const out = REFINE_PROMPT({ net: NET, query: Q, fact: 'F', why: 'W' });
    expect(out).toContain('queriesTried');
    expect(out).toContain('counterFound');
    expect(out).toContain('counterNote');
  });
});

// I — debugAnalyst was blind to v3: it never named the ledger machinery an engineer should sanity-check.
describe('debugAnalyst — v3 ledger machinery sanity-check clause (finding I)', () => {
  it('names the v3 clerks + the metrics to check for anomalies', () => {
    const out = DEBUG_PROMPT({
      query: Q,
      focus: '',
      metrics: {
        mode: 'goal',
        dir: 'RR/x',
        wavesRun: 1,
        stopReason: 'brainer-done',
        scoutRabbitHoles: 0,
        prospectorVenues: 0,
        pursuedTotal: 0,
        rabbitHolesFinal: 0,
        bestOpenScore: 0,
        topScores: [],
        done: true,
        reportWritten: true,
        confidence: 'high',
        claimsTotal: 0,
        nullAttacksTotal: 0,
        chao: null,
        citationsBogus: 0,
        citationsAuditFailed: 0,
        auditCounts: { pass: 0, fail: 0, repinned: 0, unpinned: 0, pending: 0 },
        quotesRepinned: 0,
        cachePathsRejected: 0,
        venuesUnrouted: 0,
        goalMet: true,
        judgePasses: 1,
        reopenedLanes: 0,
      },
      waveLog: [],
      resultLog: [],
      highValueSources: [],
      laneRecords: [],
    });
    expect(out).toContain('claimAuditor');
    expect(out).toContain('lineageClerk');
    expect(out).toContain('rerunner');
    expect(out).toContain('metrics.claimsTotal');
    expect(out).toContain('nullAttacksTotal');
    expect(out).toContain('citationsBogus');
    expect(out).toContain('chao');
  });
});
