// RERUNNER dispatch — reads bs.derivation (null ⇒ nothing stored yet, no agent) and re-executes its code
// with the CURRENT inputs (bs.derivation.inputs, passed verbatim — the engine keeps them current across
// waves). Degrades to null on a dead agent or ok:false; the caller keeps the last lastRun and marks it stale.
import { CONFIG } from '../../config.js';
import { rerunner } from './index.js';
import { retryAgent } from '../../runtime.js';
import type { BrainerState } from '../../brainerState.js';
import type { RerunnerOut } from '../../types/index.js';

export async function runRerunner(
  bs: BrainerState,
  phaseName: string,
): Promise<{ quantiles: Record<string, number>; sensitivity: Record<string, number> } | null> {
  if (!bs.derivation) return null;
  const inputsJson = JSON.stringify(bs.derivation.inputs);
  const out = await retryAgent<RerunnerOut>(
    rerunner.buildPrompt({ code: bs.derivation.code, inputsJson }),
    {
      label: 'rerun-w' + bs.wave,
      phase: phaseName,
      model: rerunner.tier,
      effort: rerunner.effort,
      agentType: CONFIG.GENERAL_PURPOSE,
      schema: rerunner.schema,
    },
  );
  if (!out || !out.ok) return null;
  return { quantiles: out.quantiles || {}, sensitivity: out.sensitivity || {} };
}
