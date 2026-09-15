// SYNTHESISER prompts — the report-writer template + its assembly function. Template strings are
// module-level consts; buildSynthesiser only assembles/substitutes the compute-mention clauses.
import { plain, render } from '../../utils/index.js';
import { FINISH } from '../shared.js';
import type { SynthesiserArgs } from '../../types/index.js';

const SYNTHESISER_TPL = `{{! synthesiser — writes the final multi-section cited report }}
Write the final research report (mode={{mode}}) for: "{{query}}".{{focusClause}}{{thinkerClause}}
Work from: the run's accumulated RESULT (the brainer's living memory — answer, working derivation, keyClaimIds, resolved, open gaps, tensions), the hardened facts (each adversarially fact-checked + source-corrected), not raw findings.
Lean on the hardened facts as the source of truth: drop anything they leave unsupported and use the corrected value wherever they revised one.{{computeMention}} Cite sources inline where they matter.
Scout landscape: {{landscape}}
Run result so far (the answer as it ended + its keyClaimIds + the \`working\` derivation):
{{resultSoFar}}
Per-wave log (what each wave pursued + where the answer stood — for the §2 narrative):
{{waveLog}}
Hardened facts (the corrected claims):
{{cleanReports}}
Top remaining open rabbit-holes (for Open questions):
{{openRabbitHoles}}{{ledgerClause}}{{nullAttacksClause}}{{confidenceClause}}
Write \`report\` as markdown with exactly these sections in order: (1) Prompt — the goal; (2) Research waves — per wave: what was pursued and how the answer sharpened (from the per-wave log); (3) Scout landscape; (4) Findings — the synthesized answer, {{computeLeading}}weaving each hardened fact in with its corrected value; (5) Assumptions — the working assumptions the answer leans on (from resultSoFar.assumptions), each with its basis, flagging any that is load-bearing but unconfirmed; (6) Verdict + overall confidence; (7) Plan — concrete operator actions; (8) Open questions. Also return verdict (1-3 sentences), confidence, plan (array of action strings), openQuestions (array).{{FINISH}}
`;

export const buildSynthesiser = ({
  mode,
  query,
  landscape,
  resultSoFar,
  waveLog,
  cleanReports,
  focus,
  openRabbitHoles,
  compute,
  thinkerNote,
  ledger,
  nullAttacksSummary,
  computedConfidence,
}: SynthesiserArgs) => {
  // the brain folds any finalize derivation into resultSoFar.working — present that as the quantitative result.
  // Key this on CONFIG.compute, NOT merely on a non-empty `working`: with compute OFF a derivation must NEVER be
  // presented even if one leaked into resultSoFar (only an EXPLICIT compute:false suppresses it, so prompt-only
  // callers that omit compute keep the present-when-derived default).
  const hasCompute =
    compute !== false && !!(resultSoFar && resultSoFar.working && resultSoFar.working.trim());
  const thinkerClause = thinkerNote ? '\n\n' + thinkerNote : '';
  const focusClause = focus
    ? `
Emphasis from the finalize director: ${focus}`
    : '';
  const computeMention = hasCompute
    ? ' The `working` field holds the computed derivation (the calculated answer with error bars) — present it verbatim, do not re-derive or second-guess it.'
    : '';
  const computeLeading = hasCompute
    ? 'LEADING with the computed result + its error bars from `working` and showing the derivation, then '
    : '';
  // ledgerClause — cite the ledger inline (v3 FINALIZE): every [cN] the report writes must be a REAL id from
  // this digest; independence is labeled ONLY from cluster counts, never from how many distinctly-named
  // sources happen to appear.
  const ledgerClause = ledger
    ? `
CLAIM LEDGER (ids look like c12, clusters like clu2: c12 [status·clu2·audit] claim = value):
${ledger}
Cite ledger claims inline as [c12] wherever a load-bearing fact appears — every [c12]-style marker must be a real ledger id from the digest above (same c12 notation); label independence ONLY from cluster counts, never from distinct-sounding source names.
Never cite a claim whose audit field reads 'fail' — its quote could not be verified against its source; the engine strips such markers from the shipped report.`
    : '';
  const nullAttacksClause =
    nullAttacksSummary && nullAttacksSummary.length
      ? `
CHALLENGED AND SURVIVED (counter-searched, nothing found): ${plain(nullAttacksSummary)}`
      : '';
  const confidenceClause = computedConfidence
    ? `
Machinery-computed confidence: ${computedConfidence} — your returned confidence may be lower with a stated reason, never higher.`
    : '';
  return render(SYNTHESISER_TPL, {
    mode,
    query,
    focusClause,
    thinkerClause,
    computeMention,
    landscape,
    resultSoFar: plain(resultSoFar),
    waveLog: plain(waveLog),
    cleanReports: plain(cleanReports),
    openRabbitHoles: plain(openRabbitHoles),
    ledgerClause,
    nullAttacksClause,
    confidenceClause,
    computeLeading,
    FINISH,
  });
};
