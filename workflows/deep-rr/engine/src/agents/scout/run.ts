import { CONFIG } from '../../config.js';
import { scout, scoutMerger, scoutPlanner } from './index.js';
import { retryAgent } from '../../runtime.js';
import { lab, norm, normRef } from '../../utils/index.js';
import type { ResearchReport } from '../../engine.js';
import type {
  ClaimSeed,
  NextSource,
  Page,
  ScoutAngle,
  ScoutOut,
  ScoutPlannerOut,
  ScoutProbeResult,
  SeedLead,
  TermSeed,
} from '../../types/index.js';

// FALLBACK A — the planner died (or returned no usable angle): degrade to v2's single-scout behavior, one
// probe on the naive query itself. Exported for direct unit coverage.
export const FALLBACK_ANGLE: ScoutAngle = {
  name: 'direct',
  searchQuery: CONFIG.query,
  why: 'planner died — v2 single-scout fallback',
  lens: '',
};

// a planner angle is usable only when it carries the fields a probe actually needs.
const isUsableAngle = (a: unknown): a is ScoutAngle =>
  !!a &&
  typeof (a as ScoutAngle).name === 'string' &&
  !!(a as ScoutAngle).name &&
  typeof (a as ScoutAngle).searchQuery === 'string' &&
  !!(a as ScoutAngle).searchQuery;

// FALLBACK B — the merger died: a pure, deterministic JS merge of every surviving probe's output. Exported
// for direct unit coverage. url dedup via normRef (survivor order, first occurrence wins), capped at
// SCOUT_PAGES_CAP; claims/newTerms deduped by norm() of their quote/term; deadEnds is a plain union.
export function mechanicalMerge(survivors: { angle: ScoutAngle; out: ScoutOut }[]): ScoutOut {
  const landscape = survivors
    .map(({ angle, out }) => '«' + angle.name + '»: ' + out.landscape)
    .join('\n');
  const seenUrls = new Set<string>();
  const pages: Page[] = [];
  for (const { out } of survivors)
    for (const p of out.pages || [])
      if (p && p.url && !seenUrls.has(normRef(p.url))) {
        seenUrls.add(normRef(p.url));
        pages.push(p);
      }
  const seenQuotes = new Set<string>();
  const claims: ClaimSeed[] = [];
  for (const { out } of survivors)
    for (const c of out.claims || [])
      if (c && !seenQuotes.has(norm(c.quote))) {
        seenQuotes.add(norm(c.quote));
        claims.push(c);
      }
  const seenRefs = new Set<string>();
  const nextSources: NextSource[] = [];
  for (const { out } of survivors)
    for (const s of out.nextSources || [])
      if (s && s.ref && !seenRefs.has(normRef(s.ref))) {
        seenRefs.add(normRef(s.ref));
        nextSources.push(s);
      }
  const seenTerms = new Set<string>();
  const newTerms: TermSeed[] = [];
  for (const { out } of survivors)
    for (const t of out.newTerms || [])
      if (t && !seenTerms.has(norm(t.term))) {
        seenTerms.add(norm(t.term));
        newTerms.push(t);
      }
  const deadEnds = survivors.flatMap(({ out }) => out.deadEnds || []);
  return {
    landscape,
    pages: pages.slice(0, CONFIG.SCOUT_PAGES_CAP),
    claims,
    nextSources,
    newTerms,
    deadEnds,
  };
}

// SCOUT SWARM (wave-0 seed): scoutPlanner decomposes the query + proposes 3..SCOUT_PROBES search angles →
// a `scout` probe sweeps each angle in parallel (broad WebSearch → fetch with the rabbit-hole footer,
// scoped to its angle) → scoutMerger folds every surviving probe into the FINAL ScoutOut, naming the
// tensions between angles. Each stage degrades to a named JS fallback; only a total probe wipeout is
// fatal (unchanged from v2's single-scout semantics). Sets rr.scout + rr.scoutRabbitHoles and returns the
// seed leads (the engine seeds the open store from them) — signature/return/assignment unchanged from v2.
export async function runScout(rr: ResearchReport): Promise<SeedLead[]> {
  phase(CONFIG.PHASE.scout);

  // ── stage 1: scoutPlanner ──
  log('· scout planner DISPATCH · ' + scoutPlanner.tier);
  const planner = await retryAgent<ScoutPlannerOut>(
    scoutPlanner.buildPrompt({
      query: CONFIG.query,
      mode: CONFIG.mode,
      net: CONFIG.NET,
      researcherNote: CONFIG.RESEARCHER_NOTE,
    }),
    {
      label: 'scout-planner',
      phase: CONFIG.PHASE.scout,
      model: scoutPlanner.tier,
      effort: scoutPlanner.effort,
      agentType: CONFIG.GENERAL_PURPOSE,
      schema: scoutPlanner.schema,
    },
  );
  const usableAngles =
    planner && Array.isArray(planner.angles) ? planner.angles.filter(isUsableAngle) : [];
  const plannerFellBack = !planner || !usableAngles.length;
  const angles: ScoutAngle[] = plannerFellBack
    ? [FALLBACK_ANGLE]
    : usableAngles.slice(0, CONFIG.SCOUT_PROBES);
  const decomposition = (planner && planner.decomposition) || '';
  if (plannerFellBack) log('  ⚠ scout planner died → v2 single-scout fallback (angle: direct)');
  log('· scout planner · ' + angles.length + ' angles');

  // ── stage 2: the probe swarm — one `scout` call per angle, in parallel ──
  const probeResults = await parallel(
    angles.map((angle, i) => async () => {
      const out = await retryAgent<ScoutOut>(
        scout.buildPrompt({
          query: CONFIG.query,
          net: CONFIG.NET,
          footer: CONFIG.FOOTER,
          angleName: angle.name,
          angleWhy: angle.why,
          angleLens: angle.lens || '',
          searchQuery: angle.searchQuery,
          index: i + 1,
          total: angles.length,
          researcherNote: CONFIG.RESEARCHER_NOTE,
        }),
        {
          label: 'scout-probe:' + lab(angle.name),
          phase: CONFIG.PHASE.scout,
          model: scout.tier,
          effort: scout.effort,
          agentType: CONFIG.GENERAL_PURPOSE,
          schema: scout.schema,
        },
      );
      return { angle, out };
    }),
  );
  const survivors = probeResults.filter((r): r is { angle: ScoutAngle; out: ScoutOut } => !!(r && r.out));
  survivors.forEach(({ angle, out }) =>
    log('· scout probe «' + angle.name + '» RETURN · pages=' + out.pages.length),
  );
  probeResults
    .filter((r) => !r.out)
    .forEach(({ angle }) => log('  ✗ scout probe «' + angle.name + '» DIED'));
  if (!survivors.length) {
    log('✗ scout DIED');
    throw new Error('scout died');
  }

  // ── stage 3: scoutMerger ──
  const mergerProbes: ScoutProbeResult[] = survivors.map(({ angle, out }) => ({
    name: angle.name,
    landscape: out.landscape,
    pages: out.pages || [],
    claims: out.claims || [],
    nextSources: out.nextSources || [],
    newTerms: out.newTerms || [],
    deadEnds: out.deadEnds || [],
  }));
  const merged = await retryAgent<ScoutOut>(
    scoutMerger.buildPrompt({ query: CONFIG.query, decomposition, probes: mergerProbes }),
    {
      label: 'scout-merger',
      phase: CONFIG.PHASE.scout,
      model: scoutMerger.tier,
      effort: scoutMerger.effort,
      schema: scoutMerger.schema,
    },
  );
  const mergerFellBack = !merged;
  const scoutOut: ScoutOut = merged || mechanicalMerge(survivors);
  const fallbackLabel =
    [plannerFellBack && 'planner', mergerFellBack && 'merger'].filter(Boolean).join('+') || 'none';
  log(
    '· scout merged · pages=' +
      scoutOut.pages.length +
      ' · claims=' +
      (scoutOut.claims || []).length +
      ' (fallback: ' +
      fallbackLabel +
      ')',
  );

  rr.scout = scoutOut;
  const scoutRabbitHoles: SeedLead[] = scoutOut.pages.flatMap((p) =>
    (p.rabbitHoles || []).map((l) => ({
      keyword: l.keyword,
      why: l.why,
      path: [] as string[],
      kind: 'seed' as const,
    })),
  ); // PATH: scout rabbit-holes descend directly from the goal
  rr.scoutRabbitHoles = scoutRabbitHoles;
  log(
    '· scout RETURN · pages=' +
      scoutOut.pages.length +
      ' · rabbit-holes=' +
      scoutRabbitHoles.length +
      ' · claims=' +
      (scoutOut.claims || []).length +
      ' · newTerms=' +
      (scoutOut.newTerms || []).length +
      ' · deadEnds=' +
      (scoutOut.deadEnds || []).length,
  );
  scoutOut.pages.forEach((p, i) =>
    log(
      '    source ' + (i + 1) + ' · rabbit-holes=' + (p.rabbitHoles || []).length + ' · ' + p.url,
    ),
  );
  return scoutRabbitHoles;
}
