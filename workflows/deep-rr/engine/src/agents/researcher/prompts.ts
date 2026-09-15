// RESEARCHER prompts — the lane-READER template + its assembly function. Template strings are module-level
// consts; buildResearcher only assembles/substitutes. The reader reads its assigned char window(s) from the
// cache files on disk (via code) and digests them into the running answer — it does NOT search the web
// (the scheduler owns discovery). Prompt order: task + which slice to read FIRST; the "answer so far"
// handoff LAST; "reader i of N" stated once.
import { CONFIG } from '../../config.js';
import { EMIT } from '../shared.js';
import { render } from '../../utils/index.js';
import type { ReadSlice, ResearcherArgs } from '../../types/index.js';

const RESEARCHER_TPL = `{{! researcher — a lane reader: reads its assigned cache slice(s) from disk via code, then digests into the running answer }}
You are reader {{readerIndex}} of {{readerCount}} on one research lane — read your assigned slice and digest it. You do NOT search the web: the sources are already chosen and fetched to the local cache. Tools (load any via ToolSearch): Bash (read the cache files); mcp__harvester__fetch + mcp__harvester__findWorks (resolve a wall to its open-access full text); mcp__harvester__fetchImage (to view an image, request it via mcp__harvester__fetchImage for a local path, then read it).
TOP GOAL: "{{query}}".
TRAIL (top goal → … → this lane): {{trail}}.
This lane: "{{keyword}}" (why it matters: {{why}}).
DIRECTIVE — what to extract, with ranked fallbacks: {{note}}{{claimSection}}
READ NOW — your assigned slice(s), from the local cache on disk (already fetched; never re-fetch). First confirm each file exists and is non-empty (e.g. wc -c PATH); then read each char window with code, e.g. python3 -c "print(open('PATH',encoding='utf-8',errors='replace').read()[OFFSET:OFFSET+LIMIT])" — if the output is truncated, read it in sub-parts (OFFSET, OFFSET+step, … to OFFSET+LIMIT) until the whole window is consumed. Use this code path for every read — the Read tool errors out above ~25k tokens on a big cache file; if you do reach for Read, page it with offset/limit, never request the whole file:
{{reads}}
HONEST READ — if an assigned file is MISSING, empty, or unreadable, do NOT invent its content: record it in deadEnds, leave the running answer unchanged (an honest gap, never a fabricated summary), and the engine will reopen the lane. A confident wrong summary is worse than an admitted empty read. If a cached file's CONTENT is corrupted — spam, a different page than its URL promises, garbled or foreign-language filler — record it in deadEnds as "CORRUPT: <cachePath> — <one line why>"; the engine quarantines that cache path and routes a fresh fetch.
Extract everything that serves the DIRECTIVE and the top goal — facts, numbers, and for any trial or study its funding source, conflicts of interest, sample size, and key limitations. You may read images to understand the content. If the content is in another language, translate your findings to English, keeping each cited source's original-language title alongside the translation.
Extract each load-bearing fact as a claim: {claim (one sentence), value (the number/verdict if any), quote (VERBATIM from the content, ≤${CONFIG.QUOTE_MAX_CHARS} chars — ONE CONTIGUOUS unbroken span that carries the fact; NEVER stitch fragments with an ellipsis, a spliced quote fails the mechanical audit and the claim dies), source (url/DOI), cachePath (the cache file you read it in), entities (authors/funder/dataset/venue when visible)}. A claim without its verbatim quote is worthless — no quote, no claim.{{attackClause}}{{wallClause}}
Return: runningAnswer (extend the prior answer with what you found, kept a coherent whole — or begin it if you are reader 1); rabbitHoles (new gap searches the content raises, {keyword, why}); nextSources (up to 5 of the content's top outbound citations/links, each {ref: exact url or DOI, why, expect: support/attack/neutral, target: the claim id it bears on}); claims (each load-bearing fact pinned to a verbatim quote, this read's cachePath, its source, and entities when visible); newTerms (the community's terms of art this slice uses that we don't, {term, gloss}); surprise (one line, ONLY when this slice contradicts a KEY CLAIM above); deadEnds (any slice that was missing/empty/garbled/walled). <<{{footer}}>>${EMIT}{{researcherClause}}{{priorClause}}
`;

const readLine = (r: ReadSlice, i: number): string =>
  i +
  1 +
  '. ' +
  r.source +
  ' — read ' +
  r.cachePath +
  ' chars [' +
  r.offset +
  ', ' +
  (r.offset + r.limit) +
  ') (open the file, slice [' +
  r.offset +
  ':' +
  (r.offset + r.limit) +
  '])';

export const buildResearcher = ({
  query,
  trail,
  keyword,
  why,
  note,
  footer,
  reads,
  readerIndex,
  readerCount,
  priorAnswer,
  claimDigest,
  laneKind,
  researcherNote,
}: ResearcherArgs) => {
  const wallClause = `
If your assigned content is a paywall, stub, or too thin for the directive, extract its DOI/identifier and call mcp__harvester__fetch — it resolves DOIs — or mcp__harvester__findWorks to fetch the open-access full text to the cache, then read THAT from disk; do not return an empty answer. This is scoped to resolving THIS source — do not open a general web search.`;
  const researcherClause = researcherNote ? '\n' + researcherNote : '';
  const priorClause = priorAnswer
    ? `
ANSWER SO FAR — the running answer from the earlier readers on this lane; extend and correct it, do not restate it wholesale:
${priorAnswer}`
    : '';
  // claimSection — the KEY CLAIMS digest a stance can target; omitted entirely (byte-identical to no ledger yet)
  // when the ledger is empty, rather than padding with a placeholder line.
  const claimSection = claimDigest
    ? `
KEY CLAIMS SO FAR (\`c12 claim\` — ids look like c12) — when your content SUPPORTS or ATTACKS one of these, say so via \`stance\` {target: the NUMBER from its c12 id}; a contradiction is also your Surprise note:
${claimDigest}`
    : '';
  // attackClause — an ATTACK-kind lane (RabbitHole.kind, set when the brainer originates a counter-evidence
  // search) exists to try to break a claim: its primary output is counter-evidence, never manufactured doubt.
  const attackClause =
    laneKind === 'attack'
      ? " This is an ATTACK lane: your PRIMARY output is counter-evidence — claims with stance {target, kind:'attacks'} against the target claim, or an honest empty claims list when you find none; never manufacture doubt. Attack lanes ALONE may search beyond their assigned slices: before concluding the claim holds, run up to 3 WebSearch / mcp__harvester__fetch probes against the CURRENT product/changelog/news surface of every prime suspect the DIRECTIVE names — absence from your cached slices is not absence in the world."
      : '';
  return render(RESEARCHER_TPL, {
    readerIndex,
    readerCount,
    query,
    trail,
    keyword,
    why,
    note: note || '(no directive — extract what best serves the lane + goal)',
    claimSection,
    reads: (reads || []).map(readLine).join('\n') || '(no slice assigned)',
    wallClause,
    attackClause,
    footer,
    researcherClause,
    priorClause,
  });
};
