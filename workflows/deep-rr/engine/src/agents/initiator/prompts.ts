// INITIATOR prompts — the finalize-planner template + its assembly function. Template strings are
// module-level consts; buildInitiator only substitutes the operator-steering clause.
import { plain, render } from '../../utils/index.js';
import { FINISH } from '../shared.js';
import type { InitiatorArgs } from '../../types/index.js';

const INITIATOR_TPL = `{{! initiator — plans the finalize pipeline, shaping the finish to this query }}
You direct the FINALIZE phase for: "{{query}}". The research is done; below is everything it gathered. Shape the finishing pipeline to fit this query, then return the plan.{{modeClause}}
The finish runs in two parts, and you set how each starts:
1. REFINEMENT — one refine agent per item adversarially fact-checks that group of load-bearing facts and returns them corrected and hardened. You decide the grouping. (A judge then evaluates the hardened answer and may trigger a derivation or a re-check; you do not plan that.)
2. SYNTHESIS — writes the final report from the hardened, judged answer. You give it a focus note.
The run's accumulated RESULT (the brainer's living memory — answer, the \`working\` derivation, keyClaimIds, gaps, tensions):
{{resultSoFar}}
Per-wave log:
{{waveLog}}
Scout landscape: {{landscape}}
Top open rabbit-holes left unpursued:
{{openRabbitHoles}}{{ledgerClause}}{{sensitivityClause}}
Return:
- refinement.facts[] — the load-bearing facts to harden, aggressively grouped: bundle facts that share sources or stand or fall together into ONE item (each {fact, why, claimId?}); prefer a few broad groups over many atomic facts. Cover every fact that would change the answer if wrong; skip soft restatements. Where a fact corresponds to a ledger claim, set its claimId — hardening then updates that claim's record.
- synthesiser.focus — one note on what the report must emphasize / the shape the answer should take.{{thinkerClause}}{{FINISH}}
`;

export const buildInitiator = ({
  query,
  resultSoFar,
  waveLog,
  landscape,
  openRabbitHoles,
  mode,
  thinkerNote,
  ledger,
  sensitivity,
}: InitiatorArgs) => {
  const thinkerClause = thinkerNote ? '\n\n' + thinkerNote : '';
  // collect mode ⇒ harden the BREADTH (coverage of the landscape), not the shape of a single answer.
  const modeClause =
    mode === 'collect'
      ? ' This run was a COLLECT inventory, not a single-answer goal — harden BREADTH: the key claims and the major sub-areas that span the landscape, and set the report focus to completeness of the catalogue rather than the shape of one answer.'
      : '';
  // ledgerClause — the ledger-fed initiator (v3 FINALIZE): names of already-pinned claims so facts can bind
  // to them via claimId instead of restating them.
  const ledgerClause = ledger
    ? `
CLAIM LEDGER — the run's evidence (ids look like c12, clusters like clu2: c12 [status·clu2·audit] claim = value):
${ledger}`
    : '';
  // sensitivityClause — SENSITIVITY RANKING (v3 FINALIZE): once a derivation has a completed rerun, prioritize
  // hardening the claims behind the inputs that dominate the variance — a lane wasted on a low-variance input
  // cannot move the answer.
  const sensitivityClause = sensitivity
    ? `
SENSITIVITY RANKING — derivation inputs by variance share (with their backing claims):
${sensitivity}
Prioritize hardening the claims behind the top-variance inputs.`
    : '';
  return render(INITIATOR_TPL, {
    query,
    resultSoFar: plain(resultSoFar),
    waveLog: plain(waveLog),
    landscape,
    openRabbitHoles: plain(openRabbitHoles),
    modeClause,
    ledgerClause,
    sensitivityClause,
    thinkerClause,
    FINISH,
  });
};
