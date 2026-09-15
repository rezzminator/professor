// PROSPECTOR prompts — the venue-naming template + its assembly function. Template strings are
// module-level consts; buildProspector only assembles/substitutes.
import { plain, render } from '../../utils/index.js';
import { EMIT, WEB_ONLY } from '../shared.js';
import type { ProspectorArgs } from '../../types/index.js';

const PROSPECTOR_TPL = `{{! prospector — names the high-value authoritative source venues for the topic }}
Goal: "{{query}}". Scout landscape: {{landscape}}
Sources the scout already opened:
{{sources}}
Name the 6-8 highest-value, authoritative source venues for this goal — where primary, expert, or rigorous information on the topic actually lives. The right set is domain-specific (GPU serving → arXiv/USENIX/MLSys/SemiAnalysis/r/LocalLLaMA; a stock → SEC EDGAR/earnings calls/Bloomberg; weather → NOAA/ECMWF).
Span what is relevant here: primary research (papers/preprints + where they live for this field), official docs, standards bodies/regulators, authoritative datasets/benchmarks, deep practitioner/industry analysis, high-signal community venues. Exclude generic SEO blogs.
Assess where this subject is most actively researched. When a non-English literature is genuinely significant for this topic — a disease studied mostly in China/Japan, a field led by Russian or Korean groups — name the high-value native venues for those languages (CNKI/Wanfang → Chinese, J-STAGE/ICHUSHI → Japanese, SciELO/LILACS → Spanish/Portuguese, eLibrary.ru → Russian, KoreaMed → Korean), each with how to query it, and set languageGuidance: one line telling the brainer which languages to cover and why. For an English-dominated topic, return only English venues and languageGuidance "".
Where the same concept is indexed under other names (older or alternate terms, regional spellings), fold those synonyms into the venues' search guidance so English-indexed work filed under a different name is still found.
For each venue: source (venue + how to reach/search it, e.g. "arXiv (site:arxiv.org)"), goodFor (the sub-questions it is best for — specific enough for the downstream brainer to match each research lane to the right venue), and lang (its language as an ISO-ish code like zh/ja/es/ru/ko — omit for English).
Run WebSearch (one or more queries) to discover and verify the actual highest-value venues — confirm each exists and is authoritative (memory alone misses recent venues). Return highValueSources (6-8, lang-tagged when non-English), languageGuidance ("" when the topic is English-dominated), and a brief reasoning naming what you searched.${EMIT}{{thinkerClause}}{{researcherClause}}{{WEB_ONLY}}
`;

export const buildProspector = ({
  query,
  landscape,
  sources,
  thinkerNote,
  researcherNote,
}: ProspectorArgs) => {
  const thinkerClause = thinkerNote ? '\n\n' + thinkerNote : '';
  const researcherClause = researcherNote ? '\n' + researcherNote : '';
  return render(PROSPECTOR_TPL, {
    query,
    landscape,
    sources: plain(sources),
    thinkerClause,
    researcherClause,
    WEB_ONLY,
  });
};
