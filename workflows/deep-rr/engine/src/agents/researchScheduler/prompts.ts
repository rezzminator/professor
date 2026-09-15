// RESEARCH SCHEDULER prompts — the discovery template + its assembly function. Template strings are
// module-level consts; buildResearchScheduler only assembles/substitutes the per-wave clauses.
import { EMIT } from '../shared.js';
import { render } from '../../utils/index.js';
import type { ResearchSchedulerArgs, SchedulerLaneInput } from '../../types/index.js';

const SCHEDULER_TPL = `{{! researchScheduler — discovery: per lane, find + size the highest-value sources, grouped per lane }}
You are the RESEARCH SCHEDULER — you own source discovery for this wave. For each lane below, find the HIGHEST-VALUE sources to read — as MANY as genuinely add value, no cap. The readers only read what you return; they do not search.
TOP GOAL: "{{query}}".
Tools (load any missing via ToolSearch): WebSearch; mcp__harvester__search; mcp__harvester__findWorks — resolves a work/DOI to its open-access full text; mcp__harvester__fetch — fetches + caches a url/DOI. Built-in WebFetch is denied; fetch only through Harvester.
{{venueLegend}}LANES — each carries a rabbit-hole, the brainer's directive \`note\` (WHAT to find + ranked fallbacks), and the venues to prefer:
{{lanes}}
Work in TWO batched rounds — never one-source-at-a-time round-trips:
1. DISCOVER — run ALL lanes' searches in ONE parallel batch (WebSearch / mcp__harvester__search / findWorks). Prefer each lane's assigned venues; let its \`note\` decide which results serve it. A lane carrying a concrete ref takes that ref as a source directly — no search needed for it.
2. SIZE — call mcp__harvester__fetch with size_only:true on EVERY candidate across all lanes in ONE parallel batch. With size_only it fetches + caches the full text and returns {size in tokens, path to the cache file, chars} and NO body. Drop any candidate that failed or came back walled/thin and pick another from the same lane.
SANITY — after sizing, compare the batch: two DIFFERENT urls returning identical {size, chars} is a cache-poisoning signature — treat both as failed and replace them.
For each lane, return its chosen sources as {source (the exact url or DOI), path (the cache path from size_only), size (tokens), chars}. Group them under the lane's id. A lane may return several sources; return an empty list for a lane only when every candidate failed.{{translateClause}}{{researcherClause}}{{vocabClause}}{{corruptClause}}
Return \`lanes\`: one entry per input lane id, each {id, sources:[{source, path, size, chars}], venuesServed:[...], unsourced:[{ref, reason}]}. venuesServed is the subset of THIS lane's ASSIGNED venues (the legend entries' exact source strings) its chosen sources actually come from — [] when none. unsourced lists every ref/DOI/venue the lane's directive or brief NAMED that could not be fetched, each {ref, reason} — omit the field entirely when everything named was sourced. A lane whose PRIORITY venue yielded nothing must say so in unsourced (reason e.g. "venue unfetchable") — never silently substitute a lower tier for it. Use the sizes you measured — never invent them.${EMIT}
`;

// laneLine — renders one LANES entry. `legend` maps a venue's exact source string → its VENUE LEGEND number
// (built once per prompt in buildResearchScheduler, over the deduped venue set); a lane's venues render as
// just their legend numbers ("venues: 2, 5") instead of repeating each venue's full ~700-char description.
const laneLine = (l: SchedulerLaneInput, legend: Map<string, number>): string =>
  '#' +
  l.id +
  ' ' +
  l.keyword +
  ' — ' +
  l.why +
  '\n  directive: ' +
  (l.note && l.note.trim() ? l.note : '(none — use the rabbit-hole + goal)') +
  '\n  venues: ' +
  ((l.venues || [])
    .map((v) => legend.get(v.source))
    .filter((n): n is number => n !== undefined)
    .join(', ') || '(none — general search)') +
  (l.ref ? '\n  ref (fetch directly): ' + l.ref : '') +
  (l.kind === 'attack'
    ? '\n  ⚔ ATTACK lane — mandatory sources: the CURRENT product/changelog/pricing/news surface of EVERY prime suspect the directive names, not just pages about the claim.'
    : '') +
  (l.refetch
    ? "\n  REFETCH — this lane's cached copy is corrupted: fetch FRESH (cache-busting URL variant, a mirror, or archive.org) and NEVER return an already-cached path for it."
    : '');

export const buildResearchScheduler = ({
  query,
  lanes,
  researcherNote,
  vocabulary,
  corruptCache,
}: ResearchSchedulerArgs) => {
  const anyLang = lanes.some((l) => (l.venues || []).some((v) => v.lang));
  const translateClause = anyLang
    ? `
For a lane routed to a non-English venue (tagged [zh], [ja], …), translate its query terms into that language, search the native venue, and choose the native-language sources — the readers translate the content back to English.`
    : '';
  const researcherClause = researcherNote ? '\n' + researcherNote : '';
  // vocabClause — the field's own terms of art (v3 STEERING), so venue queries speak the community's
  // language rather than the operator's; omitted entirely when the vocabulary is still empty.
  const vocabClause = vocabulary
    ? `
COMMUNITY VOCABULARY — the field's own terms of art (usage counts); phrase venue queries in THESE terms, not the operator's wording, where they fit: ${vocabulary}`
    : '';
  // corruptClause — known-poisoned cache paths (readers flagged them CORRUPT this run); omitted entirely
  // when nothing has been flagged yet.
  const corruptClause =
    corruptCache && corruptCache.length
      ? `
CORRUPTED CACHE — known-poisoned cache paths; NEVER return any of these as a source path (fetch fresh or substitute another source):
` + corruptCache.map((p) => '- ' + p).join('\n')
      : '';
  // VENUE LEGEND — dedupe the venues across every lane by v.source (first occurrence wins), then number
  // them once; laneLine looks each lane's venues up in this map instead of repeating full descriptions.
  const legendVenues: SchedulerLaneInput['venues'] = [];
  const seen = new Set<string>();
  for (const l of lanes)
    for (const v of l.venues || [])
      if (!seen.has(v.source)) {
        seen.add(v.source);
        legendVenues.push(v);
      }
  const legend = new Map(legendVenues.map((v, i) => [v.source, i + 1]));
  const venueLegend = legendVenues.length
    ? "VENUE LEGEND — the prospector's venues, referenced by number below:\n" +
      legendVenues
        .map(
          (v, i) =>
            i +
            1 +
            '. ' +
            v.source +
            (v.lang ? ' [' + v.lang + ']' : '') +
            (v.goodFor ? ' (' + v.goodFor + ')' : ''),
        )
        .join('\n') +
      '\n\n'
    : '';
  return render(SCHEDULER_TPL, {
    query,
    lanes: lanes.map((l) => laneLine(l, legend)).join('\n') || '(no lanes)',
    venueLegend,
    translateClause,
    researcherClause,
    vocabClause,
    corruptClause,
  });
};
