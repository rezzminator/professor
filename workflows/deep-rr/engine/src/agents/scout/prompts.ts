// SCOUT SWARM prompts — three templates + their assembly functions: scoutPlanner (senses the landscape,
// decomposes the query, proposes search angles) → scout (one PROBE per angle) → scoutMerger (folds every
// probe into the final ScoutOut). Template strings are module-level consts; each build* fn only
// assembles/substitutes — it holds no template text itself.
import { CONFIG } from '../../config.js';
import { EMIT } from '../shared.js';
import { plain, render } from '../../utils/index.js';
import type { ScoutArgs, ScoutMergerArgs, ScoutPlannerArgs } from '../../types/index.js';

// ── stage 1: scoutPlanner — ground the angle vocabulary in real search results before proposing anything;
// decompose the query into its distinct axes; propose the probe swarm's search angles ──
const SCOUT_PLANNER_TPL = `{{! scoutPlanner — grounds the angle vocabulary in real search results, decomposes the query, proposes the probe swarm's angles }}
Query: "{{query}}" (mode: {{mode}}). {{net}}
Step 1 — run 1-2 quick WebSearches to sense the landscape's lay. The angle vocabulary below MUST come from what these searches actually surface — never from imagination.
Step 2 — return decomposition: the query's distinct axes, one line each — everything that must ALL be understood to answer it.
Step 3 — return angles: 3 to ${CONFIG.SCOUT_PROBES} distinct search angles for a swarm of scout probes, each {name, searchQuery, why, lens}. searchQuery is a concrete, distinct query for THIS angle — never a reworded restatement of another angle's. No two angles may overlap: each must be built to surface pages the others will not.
MANDATORY whenever it applies to this query:
(a) a DIRECT angle — the query as asked, straight up.
(b) a SKEPTIC angle — counter-evidence: criticism, limitations, failed replications, debunking phrasing.
(c) a RECENT angle — the last ~12 months of developments.
Fill any remaining slots with whichever of these fit THIS query best: datasets/benchmarks, practitioner communities, regulatory/official sources, industry analyses, adjacent-field framings.{{researcherClause}}
`;

export const buildScoutPlanner = ({ query, mode, net, researcherNote }: ScoutPlannerArgs) => {
  const researcherClause = researcherNote ? '\n' + researcherNote : '';
  return render(SCOUT_PLANNER_TPL, { query, mode, net, researcherClause });
};

// ── stage 2: scout — one PROBE of the swarm, scoped to a single angle. Unchanged reading substrate from
// v2's single scout (one WebSearch → fetch ≤N sources → the same footer discipline); only the SCOPE
// narrows from "the whole topic" to "this one angle". ──
const SCOUT_TPL = `{{! scout — one probe of the swarm, scoped to a single angle: sweeps it and seeds its rabbit-holes }}
You are scout probe {{index}} of {{total}}, on the angle «{{angleName}}» — {{angleWhy}}. Lens: {{angleLens}}. {{net}}
Step 1 — run WebSearch with: "{{searchQuery}}". You may refine it ONCE if the results are off-angle — stay on THIS angle, do not wander onto another probe's.
Step 2 — pick the up-to-${CONFIG.SCOUT_PROBE_SOURCES} most relevant sources FOR THIS ANGLE and fetch each via mcp__harvester__fetch — built-in WebFetch is denied. For each fetched page, first surface the key facts about "{{query}}" as this angle reveals them, then apply this instruction: <<{{footer}}>> Record the local cache path the fetch tool reports as EVERY claim's cachePath — a claim without its cachePath can never be mechanically verified and stays permanently unpinned; never invent one when the tool did not report it. Skip the footer's Surprise section — no prior claims exist yet.
Step 3 — return ALL SIX fields (an array with nothing to report is [], never omitted): landscape (2-3 sentences on what THIS ANGLE revealed — not the whole topic, and NEVER your findings packed into prose: facts go in claims[], page detail in pages[].summary); pages[] (each: url, 2-3 sentence summary, rabbitHoles[] copied from the page's "Rabbit holes" section as {keyword, why}); nextSources[] union of the pages' "Next sources" sections, each {ref, why}; claims[] union of the pages' "Claims" sections, each pinned to a verbatim quote — a claim without its verbatim quote is worthless, no quote no claim; newTerms[] union of the pages' "New terms" sections; deadEnds[] for any source that timed out, was parked, or was off-topic — do not invent rabbit-holes for those. If every source is dead/unreachable, still return a valid result: landscape from your search, pages [], the dead sources in deadEnds.${EMIT}{{researcherClause}}
`;

export const buildScout = ({
  query,
  net,
  footer,
  angleName,
  angleWhy,
  angleLens,
  searchQuery,
  index,
  total,
  researcherNote,
}: ScoutArgs) => {
  const researcherClause = researcherNote ? '\n' + researcherNote : '';
  return render(SCOUT_TPL, {
    query,
    net,
    footer,
    angleName,
    angleWhy,
    angleLens,
    searchQuery,
    index,
    total,
    researcherClause,
  });
};

// ── stage 3: scoutMerger — folds every surviving probe's output into ONE final ScoutOut (same schema).
// No tools: a plain subagent reducing material the probes already gathered — there is no tool-use rabbit
// hole to guard against (mirrors lineageClerk: no FINISH clause). ──
const SCOUT_MERGER_TPL = `{{! scoutMerger — folds every angle probe's output into the final ScoutOut, naming the tensions between angles }}
Query: "{{query}}".
Decomposition (the query's distinct axes): {{decomposition}}
Every surviving probe's output (angle name, what it revealed, its pages/claims/newTerms/deadEnds):
{{probes}}
Return the FINAL scout result:
landscape: ONE rich paragraph weaving every angle together — and NAME THE TENSIONS between them (where, say, the SKEPTIC angle contradicts the DIRECT angle). That tension is the single most valuable seed the next stage can receive — surface it explicitly, never bury it.
pages: the union of every probe's pages, deduped by url, keeping the strongest up to ${CONFIG.SCOUT_PAGES_CAP} — carry each kept page's summary and rabbitHoles EXACTLY as its probe wrote them; you are SELECTING, not rewriting.
nextSources: the union of every probe's nextSources, deduped by ref.
claims: the union of every probe's claims, dropping exact duplicates (the same quote reported by more than one probe).
newTerms: the union, deduped by term.
deadEnds: the union.
Only use what the probes actually returned — never invent a page, claim, or term no probe reported.${EMIT}
`;

export const buildScoutMerger = ({ query, decomposition, probes }: ScoutMergerArgs) =>
  render(SCOUT_MERGER_TPL, { query, decomposition, probes: plain(probes) });
