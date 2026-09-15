import { CONFIG } from '../../config.js';
import { brainer, buildCoord, BRAIN_COMPUTE, buildBrainerCompute } from './index.js';
import { retryAgent } from '../../runtime.js';
import { ledgerLines, openLine, venuesWithYieldWarn } from '../../utils/index.js';
import type { BrainerState } from '../../brainerState.js';
import type { BrainComputeOut, CleanReport, Coord, Finding } from '../../types/index.js';

// the single Opus BRAINER — the brain / global reducer. Sees the open store + pursued set + running resultSoFar; returns the updated
// resultSoFar + DELTAS (rescore / add / lookupNext / rename / drop / stop). Can LOOK UP stored leads OR ORIGINATE new directions; code-capable
// (general-purpose) when compute is on, so it can derive its own steering numbers inline — no separate compute stage.
export async function runBrainer(
  bs: BrainerState,
  wave: number,
  findings: Finding[],
  phaseName: string = CONFIG.PHASE.crawl,
  ctx?: { canSpawn?: boolean; lastWave?: boolean },
): Promise<Coord | null> {
  const open = bs.rabbitHoles.map(openLine);
  // v3 STEERING — the ledger digest + calibration/sensitivity/coverage state, computed here (pure reads off
  // bs) and handed to buildBrainer, which decides per-clause whether there is anything to say.
  const ledger = ledgerLines(bs, CONFIG.BRAINER_LEDGER_CAP);
  const derivation =
    bs.derivation && bs.derivation.lastRun
      ? {
          quantiles: bs.derivation.lastRun.quantiles,
          sensitivity: bs.derivation.lastRun.sensitivity,
          inputs: bs.derivation.inputs,
          stale: !!bs.derivationStale,
        }
      : undefined;
  return retryAgent<Coord>(
    brainer.buildPrompt({
      wave,
      query: CONFIG.query,
      rubric: CONFIG.RUBRIC,
      landscape: bs.scout!.landscape,
      pursuedList: bs.pursuedList,
      open,
      findings,
      topScores: bs.topScores,
      resultSoFar: bs.resultSoFar,
      stop: CONFIG.STOP,
      mode: CONFIG.mode,
      venues: venuesWithYieldWarn(bs.highValueSources, bs.venueStats, wave),
      languageGuidance: bs.languageGuidance,
      lastValidatorMissing: bs.lastValidatorMissing,
      unsourced: bs.lastUnsourced,
      compute: CONFIG.compute,
      computeNote: CONFIG.COMPUTE_NOTE,
      thinkerNote: CONFIG.THINKER_NOTE,
      researcherNote: CONFIG.RESEARCHER_NOTE,
      ledger,
      calib: bs.yieldCalib,
      derivation,
      chao: bs.chao,
      // brainer-tree context — identity off the brainer, per-wave permissions off ctx (both inert in single-brainer runs)
      isChild: !bs.isRoot,
      parentName: bs.parentName || undefined,
      mandate: bs.mandate || undefined,
      trail: bs.trail || undefined,
      canSpawn: ctx ? ctx.canSpawn : false,
      lastWave: ctx ? ctx.lastWave : false,
    }),
    {
      label: 'brainer-' + (bs.isRoot ? '' : bs.name + '-') + 'w' + wave,
      phase: phaseName,
      model: brainer.tier,
      effort: brainer.effort,
      // pruned per call — optional clauses inflate the schema past the spawn classifier's size limit
      schema: buildCoord({ compute: CONFIG.compute, canSpawn: !!(ctx && ctx.canSpawn) }),
      agentType: CONFIG.compute ? CONFIG.GENERAL_PURPOSE : undefined,
    },
  );
}

// brain FINALIZE-COMPUTE — the brain (code-capable) derives the answer on the hardened facts, per the judge directive.
// Pure: returns the BrainComputeOut; the engine folds out.resultSoFar back into bs.
export async function runBrainerCompute(
  bs: BrainerState,
  hardenedFacts: CleanReport[],
  directive: string,
  reason: string,
  pass: number,
): Promise<BrainComputeOut | null> {
  return retryAgent<BrainComputeOut>(
    buildBrainerCompute({
      query: CONFIG.query,
      resultSoFar: bs.resultSoFar,
      hardenedFacts,
      directive,
      reason,
      computeNote: CONFIG.COMPUTE_NOTE,
      thinkerNote: CONFIG.THINKER_NOTE,
    }),
    {
      label: 'brain-compute-' + pass,
      phase: CONFIG.PHASE.finalize,
      model: brainer.tier,
      effort: brainer.effort,
      agentType: CONFIG.GENERAL_PURPOSE,
      schema: BRAIN_COMPUTE,
    },
  );
}
