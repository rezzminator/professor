// BRAINER prompts — the brain's per-wave template + the clause-assembly function. Template strings
// are module-level consts; buildBrainer only assembles/substitutes the per-wave clauses.
import { CONFIG } from '../../config.js';
import { plain, render } from '../../utils/index.js';
import { EMIT, FINISH } from '../shared.js';
import type { BrainerArgs, BrainerComputeArgs } from '../../types/index.js';

const BRAINER_TPL = `{{! brainer — the brain: scores and steers rabbit-holes, keeps resultSoFar, decides done }}
You are the BRAINER — you make every decision in this research run and set its direction.
{{roleClause}}{{lastWaveClause}}
How the run works: a scout seeded the first rabbit-holes and a prospector named the source venues; then you drive each wave. For each lane you pick, a scheduler finds the highest-value sources and sequential readers read them in full (carrying a running answer across the sources), returning findings + new rabbit-holes; your per-lane \`note\` directs what the scheduler picks and what the readers extract. Then you update the running result, steer the next wave, and decide when to stop. On stop, a refinement stage hardens your findings, a judge stress-tests the answer, and a synthesiser writes the report.

The engine keeps the open rabbit-holes as an id-keyed store and carries each one's score history natively — you never re-emit the whole set, you return deltas against it.

Direction is two powers:
• LOOK UP rabbit-holes already in the store (by id) to research next.
• ORIGINATE — when the answer needs an angle, candidate, or sub-question no stored rabbit-hole covers, add it as a new directive {keyword, why, score} and a researcher will go collect it. Name a gap you can see rather than wait for one to surface; summon a candidate the scout missed — not padding. Put it in \`lookupNext\` to pursue now, or in \`add\` to park it for a later wave.

As you steer, hold three rules:
• Pivot on disproof — when a lead is fundamentally refuted, abandon it without sunk-cost and take a different road; a dead lead dropped is progress.
• Surfacing is not verifying — finding a result does not verify it. If the answer's headline rests on a claim you have not stress-tested, the judge will reject the stop, so stress-test load-bearing claims before declaring done.
• Promote serendipity — a surfaced, non-seeded candidate that out-evidences the seeded ones becomes first-class: deepen it like a seed rather than under-explore it for being off the seed list.

{{probeClause}}{{thinkerClause}}{{researcherClause}}

Wave {{wave}}. Query: "{{query}}". {{rubric}}
Scout landscape: {{landscape}}
RABBIT-HOLE STORE — open rabbit-holes (\`#id [last score or "new"] keyword — why\`); re-score up or down, a low one can resurrect:
{{open}}
ALREADY PURSUED — do not look up or re-originate these (research history):
{{pursuedList}}
Findings this wave (from the researchers' page-reading):
{{findings}}{{trajectory}}{{venuesClause}}{{languageClause}}{{calibrationClause}}{{sensitivityClause}}{{chaoClause}}

{{memoryClause}}{{ledgerClause}}
Update and return \`resultSoFar\` as the run's memory: refine \`answer\`; set \`keyClaimIds\` to the ledger ids the answer rests on; record the working \`assumptions\` the answer leans on (each {claim, basis}) and revise or retire them as evidence lands; move closed parts into \`resolved\`; keep \`openGaps\` current; record any \`tensions\` (conflicting sources); {{workingClause}}; set \`confidence\`.
Weight findings by evidence quality — funding independence, sample size, replication, stated limitations — not mere existence; let it drive both your scores and \`confidence\`.
For each headline / load-bearing finding, originate a lane to hunt failed replications, null trials, or refutations. Corroboration is what feeds the ledger's own settled/tentative computation — you never set status yourself; originate lanes that give the machinery independent clusters to count.{{attackClause}}{{computeField}}

Then return deltas against the store:
(1) \`rescore\`: [{id, score}] — only the rabbit-holes whose 0-100 score changes this wave (score every "new" one at least once); unlisted ones keep their last score. Score honestly per the rubric; a marginal one scores low. When you are setting stop.done=true this wave, skip scoring the new ones — a frontier you are closing needs no scores.
(2) \`add\`: [{keyword, why, score}] — new rabbit-holes to park in the store for a later wave (the engine assigns each an id).
(3) \`lookupNext\`: the rabbit-holes to research now — each either {id} (a stored one) or {keyword, why, score{{scoreFields}}} (one you originate and pursue now). None may be already pursued.{{assignClause}} For EVERY lookupNext lane author a \`note\`: the research directive — WHAT to find plus ranked fallbacks ("if not X, focus on Y; give both if available"). It steers both the scheduler's source pick and the reader's extraction; keep it distinct from \`why\` (your store/scoring rationale). A lane's method must be executable by a READ-ONLY reader over fetched pages (attack lanes alone get a bounded live search) — never assign per-item tracker probes, review-cadence checks, or interactive verification; reshape such a method into fetchable-source questions. Set refetch:true on a lane whose cached copy was reported CORRUPT so the scheduler bypasses the poisoned cache.
(4) \`rename\`: [{id, keyword, why?}] — relabel a rabbit-hole, keeping its id + history (optional).
(5) \`drop\`: [id, …] — eliminate a dead/duplicate rabbit-hole; a merge = drop the duplicate and rescore the survivor (optional).{{spawnClause}}
(6) \`stop\`: {done, reason}. {{stop}}{{goalClause}}{{voiClause}}{{validatorClause}}{{unsourcedClause}}${EMIT}{{FINISH}}
`;

export const buildBrainer = ({
  wave,
  query,
  rubric,
  landscape,
  pursuedList,
  open,
  findings,
  topScores,
  resultSoFar,
  stop,
  mode,
  venues,
  languageGuidance,
  lastValidatorMissing,
  unsourced,
  compute,
  computeNote,
  thinkerNote,
  researcherNote,
  isChild,
  parentName,
  mandate,
  trail,
  canSpawn,
  lastWave,
  ledger,
  calib,
  derivation,
  chao,
}: BrainerArgs) => {
  // brainer-tree role: a CHILD drives ONE branch and may abandon it; the ROOT carries the whole run and never can.
  const roleClause = isChild
    ? `\nYou are a CHILD brainer: ${parentName || 'a parent'} spawned you to drive ONE branch — ${mandate || 'your mandate'} — split from the path ${trail || '(root)'}. Pursue that branch deep on the store + memory you inherited. If it proves a dead end, abandon it: set stop.lost=true with a one-line reason and you are done (no answer expected). You carry only this branch, not the whole run.`
    : `\nYou are the ROOT brainer: you carry the whole run to a real answer — stop.lost is not yours to set.`;
  const lastWaveClause = lastWave
    ? `\nLAST WAVE — the run is wrapping up: consolidate your answer into resultSoFar and set stop.done=true. Request no new lookupNext; research is closing.`
    : '';
  // spawn is offered ONLY while spawning is still permitted (caps not hit) — don't tempt a capped-out brainer.
  const spawnClause = canSpawn
    ? `\n(5b) \`spawn\` (at most ONE this wave): when the goal holds two or more INDEPENDENT investigations — separate evidence bases, sub-questions that do not inform each other — hand one to a focused child brainer THIS wave instead of carrying both in your single line: emit \`spawn\` {id (or keyword+why), mandate}. The child inherits a clean copy of your store + memory, aimed by the mandate; you drop that branch and steer the rest. Reserve a spawn for a branch substantial enough to run on its own — not a single lane — but when the run genuinely splits in two, spawn rather than interleave.`
    : `\n(spawn is unavailable this wave — the parallel-brainer cap is reached; do not emit one.)`;
  const thinkerClause = thinkerNote ? '\n\n' + thinkerNote : '';
  const validatorClause = lastValidatorMissing
    ? `\nVALIDATOR — last wave left these unfilled; re-pursue the reopened lanes or originate new ones to close them: ${lastValidatorMissing}`
    : '';
  const unsourcedClause = unsourced
    ? '\nSCHEDULER REPORT — refs/venues the last wave could NOT source, substituted, or flagged: ' + unsourced + '\nA substituted venue is NOT covered — re-route it, set refetch, or record the gap explicitly in resultSoFar.openGaps.'
    : '';
  const researcherClause = researcherNote ? '\n' + researcherNote : '';
  const trajectory = topScores.length
    ? `
TOP-PICK SCORE TRAJECTORY by wave (calibrated 0-100): ${plain(topScores)}
A steadily declining trajectory means high-value rabbit-holes are drying up — read it as convergence.`
    : '';
  const goalClause =
    mode === 'goal'
      ? `
Goal mode: if the goal is already well answered and the best remaining rabbit-hole adds only marginal value (a declining trajectory is strong evidence), set stop.done=true rather than chase diminishing returns.`
      : '';
  const venuesClause =
    venues && venues.length
      ? `
SOURCE VENUES (from the prospector) — give each lookupNext pick the subset whose source fits its lane, in its \`sources\`, so its researcher searches the right places first:
${plain(venues)}
A venue suffixed '⚠ never assigned' has had NO lane yet — either route a lane to it this wave or name the waiver in stop.reason when you declare done.`
      : '';
  const memoryClause =
    wave === 0
      ? `RESULT SO FAR — the run's living MEMORY. Start it this wave: capture the answer as it stands plus the load-bearing evidence behind it.`
      : `RESULT SO FAR — the run's living MEMORY, carried wave to wave. Prior version:
${plain(resultSoFar)}`;
  const languageClause =
    languageGuidance && languageGuidance.trim()
      ? `
Some of this topic's strongest literature is non-English. Guidance: ${languageGuidance}. Deliberately route some lanes to the non-English venues above, giving each its native venue(s) in \`sources\` — rather than defaulting every lane to English.`
      : '';
  const probeClause = `Before you decide, hunt for coverage gaps — a candidate, sub-question, or angle the goal needs that no lane has touched — and probe them yourself with WebSearch / mcp__harvester__fetch, as many as you need, to fill them; fold what you find into resultSoFar and originate the missing rabbit-holes into \`lookupNext\`. Beyond gap-filling, leave the heavy digging to the lane readers.`;
  const scoreFields = ', sources, note';
  const assignClause = venues && venues.length ? ' Assign each its `sources` venue subset.' : '';
  // workingClause — gated on compute exactly as computeField is. compute OFF ⇒ the brainer must NOT hand-roll a
  // derivation: leave `working` empty and treat unknowns as STATED UNCERTAINTY (only an EXPLICIT compute:false
  // turns it off, so the prompt-only callers — which omit compute — keep the derive-when-needed default).
  const workingClause =
    compute === false
      ? `leave \`working\` empty and treat any value you cannot source as STATED UNCERTAINTY in the answer — never hand-roll a derivation`
      : `for build-the-answer / estimate questions grow the \`working\` derivation chain (else '')`;
  // derivationClause (compute on) — replaces v2's inline COMPUTE TO STEER: redirect from deriving-inline to
  // AUTHORING the derivation once as a stored artifact the engine reruns cheaply every wave (kept in the
  // `computeField` slot/variable name — same template placeholder, same compute gate as before).
  const computeField = compute
    ? `

When the answer must be BUILT (an estimate, a synthesis with arithmetic), AUTHOR the derivation once as a stored artifact: return \`derivation\` {code, inputs} — pure seeded python3, reads one JSON arg of input values, prints {quantiles, sensitivity}. Name each input, tie it to the ledger claims backing it (claimIds), and mark unevidenced inputs prior:true with a WIDE dist — never a fake point value. The engine reruns it cheaply every wave and shows you the sensitivity; re-emit \`derivation\` only to change code or inputs. Fold the headline number into \`working\`.${computeNote ? '\n\n' + computeNote : ''}`
    : '';
  // ledgerClause — the claim ledger digest (v3 STEERING); omitted entirely before any claim is ledgered.
  const ledgerClause = ledger
    ? `\nCLAIM LEDGER — the run's evidence, quote-pinned + audited by machinery (each line: c12 [status·clu2·audit] claim = value — ids look like c12, clusters like clu2):\n${ledger}\nCorroboration counts independence CLUSTERS, not sources — two claims in one cluster are ONE voice. Reference claims by their NUMBER (the digits in its c12 id) in \`keyClaimIds\` (the ids the answer rests on). Evidence lives HERE now — do not re-emit facts into resultSoFar.evidence.`
    : '';
  // attackClause — gated on the SAME ledger presence: nothing to challenge before the first claim lands.
  const attackClause = ledger
    ? ` For every keyClaim still \`tentative\` that has never been challenged (no attack lane, no nullAttack), originate ONE kind:"attack" lane hunting its strongest realistic counter-evidence, NAMING the target's c-id in the lane's note (the machinery links the attack outcome to that claim) — a claim that has survived attack outranks one nobody questioned.`
    : '';
  // calibrationClause — only kinds with a real observation (n>0) render; an all-neutral table teaches nothing.
  const calibEntries = Object.entries(calib || {}).filter(([, v]) => v && v.n > 0);
  const calibrationClause = calibEntries.length
    ? `\nCALIBRATION — your lead kinds, predicted vs realized yield this run (ratio<1 = you overestimate this kind): ${calibEntries.map(([k, v]) => k + ': ' + v.ratio.toFixed(2) + ' (' + v.n + ')').join(', ')}. The engine already weights selection by these; let them temper your scores too.`
    : '';
  // sensitivityClause + voiClause — gated on a derivation with at least one completed rerun (lastRun);
  // a freshly-authored, not-yet-run derivation has no sensitivity to show.
  const priorNames = new Set(
    (derivation ? derivation.inputs : []).filter((i) => i.prior).map((i) => i.name),
  );
  const sensEntries = derivation ? Object.entries(derivation.sensitivity || {}) : [];
  const topInput = sensEntries.length ? sensEntries.reduce((a, b) => (b[1] > a[1] ? b : a))[0] : '';
  const staleFlag =
    derivation && derivation.stale
      ? ' (STALE — last rerun failed; consider re-emitting the derivation.)'
      : '';
  const sensitivityClause = derivation
    ? `\nDERIVATION STATE — current quantiles: ${Object.entries(derivation.quantiles || {})
        .map(([k, v]) => k + '=' + v)
        .join(', ')}. Variance shares by input: ${sensEntries
        .map(([k, v]) => k + ': ' + v.toFixed(2) + (priorNames.has(k) ? ' (PRIOR)' : ''))
        .join(
          ', ',
        )} (inputs marked PRIOR are placeholders awaiting evidence). Chase the inputs that dominate the variance: a lane that pins a ${topInput} beats any topical lead.${staleFlag}`
    : '';
  const voiClause =
    mode === 'goal' && derivation
      ? `\nSTOP TEST (value-of-information): when the goal is answered AND no open rabbit-hole targets a derivation input with variance share > ${CONFIG.VOI_SENS_THRESHOLD}, further waves cannot materially change the answer — set stop.done=true.`
      : '';
  // chaoClause — collect mode only, once the first collect-mode wave has computed a coverage estimate.
  const chaoClause =
    mode === 'collect' && chao
      ? `\nCOVERAGE — statistical estimate from the claim ledger: ~${Math.round(chao.unseen)} distinct findings remain unfound (coverage ≈${Math.round(chao.coverage * 100)}%). Read it as the inventory's completeness, not a feeling.`
      : '';
  return render(BRAINER_TPL, {
    roleClause,
    lastWaveClause,
    spawnClause,
    probeClause,
    thinkerClause,
    researcherClause,
    wave,
    query,
    rubric,
    landscape,
    open: plain(open),
    pursuedList: plain(pursuedList),
    findings: plain(findings),
    trajectory,
    venuesClause,
    languageClause,
    calibrationClause,
    sensitivityClause,
    chaoClause,
    memoryClause,
    ledgerClause,
    attackClause,
    scoreFields,
    assignClause,
    workingClause,
    stop,
    goalClause,
    voiClause,
    validatorClause,
    unsourcedClause,
    computeField,
    FINISH,
  });
};

// brain FINALIZE-COMPUTE — the brainer re-invoked (code-capable, full resultSoFar) to DERIVE the final answer
// on the hardened facts, on the judge's directive. Transplants the old compute chain's rigor: fact-check the
// input numbers, write + run a short script for the arithmetic, propagate error bars, self-check.
const BRAIN_COMPUTE_TPL = `{{! brain-compute — the brain derives the final answer on the hardened facts, with rigor + error bars }}
You are the BRAINER, now DERIVING the final answer for: "{{query}}". The judge ruled the answer still needs this derivation — build it, do not restate facts.
Judge directive: {{directive}}
Judge reasoning: {{reason}}
Hardened facts (adversarially fact-checked + source-corrected — your input numbers):
{{hardenedFacts}}
The run's accumulated RESULT (your answer + the half-built \`working\` derivation to finish):
{{resultSoFar}}
Derive with rigor:
- first fact-check your input numbers: verify each against a current primary source (WebSearch / mcp__harvester__fetch) and correct any that is stale, wrong, or imprecise before computing — a derivation is only as sound as its inputs;
- assemble the verified inputs with their units;
- write and run a short script for any non-trivial arithmetic — load Bash + Write via ToolSearch if absent, run python (or node) — compute, do not estimate;
- propagate the input uncertainties into an explicit ± error range;
- adversarially check your own work: re-derive a second way or sanity-check against an anchor, and fix any unit / formula / arithmetic slip.{{noteClause}}{{thinkerClause}}
Return the updated \`resultSoFar\`: fold the completed derivation into \`working\` (the verified inputs, the steps, the numbers, the ± result, the self-check), put the headline computed result in \`answer\`, and keep \`keyClaimIds\` / \`resolved\` / \`openGaps\` / \`tensions\` / \`confidence\` current — \`keyClaimIds\` are the ledger claim ids the answer rests on; the deprecated \`evidence\` array is never populated.{{FINISH}}
`;

export const buildBrainerCompute = ({
  query,
  resultSoFar,
  hardenedFacts,
  directive,
  reason,
  computeNote,
  thinkerNote,
}: BrainerComputeArgs) => {
  const noteClause = computeNote ? '\n' + computeNote : '';
  const thinkerClause = thinkerNote ? '\n\n' + thinkerNote : '';
  return render(BRAIN_COMPUTE_TPL, {
    query,
    resultSoFar: plain(resultSoFar),
    hardenedFacts: plain(hardenedFacts),
    directive: directive || '(derive the answer the goal needs)',
    reason: reason || '',
    noteClause,
    thinkerClause,
    FINISH,
  });
};
