// REFINER prompts — the fact-hardening template + its assembly function. Template strings are
// module-level consts; buildRefiner only assembles/substitutes.
import { render } from '../../utils/index.js';
import { WEB_ONLY } from '../shared.js';
import type { RefineArgs } from '../../types/index.js';

const REFINE_TPL = `{{! refine — adversarially fact-check ONE load-bearing fact and return its corrected, hardened version; attack-recording, not just fact-hardening }}
Fact-check and harden this load-bearing fact for the goal "{{query}}". {{net}}
Fact: {{fact}}
Why it is load-bearing: {{why}}{{pinnedClause}}
First verify it adversarially: hunt counter-evidence, newer information, and the real numbers — actively look for where it is false, outdated, or imprecise. Record every counter-search query you actually run, verbatim, in queriesTried — the record of the attack matters as much as its outcome. Do not rubber-stamp a well-supported fact; do not manufacture doubt about one you cannot actually break. Then settle every doubt against the sources and return only the clean, corrected claim(s) — the right values, current and verified, dropping anything that does not hold. Cite sources inline.{{directiveClause}}
Return report (markdown: the hardened claim(s) for this fact), queriesTried (the exact counter-search queries you ran), counterFound (true only when a real counter-example/contradiction turned up — a completed search that found nothing is false, not a lie), counterNote (what the counter-evidence was, when counterFound; '' otherwise).{{WEB_ONLY}}
`;

export const buildRefiner = ({
  net,
  query,
  fact,
  why,
  directive,
  claimQuote,
  claimSource,
}: RefineArgs) => {
  const directiveClause = directive
    ? `\nA judge flagged the prior verification — re-check it: ${directive}`
    : '';
  const pinnedClause = claimQuote
    ? `\nTHE CLAIM AS PINNED: "${claimQuote}" — ${claimSource || ''}`
    : '';
  return render(REFINE_TPL, { net, query, fact, why, pinnedClause, directiveClause, WEB_ONLY });
};
