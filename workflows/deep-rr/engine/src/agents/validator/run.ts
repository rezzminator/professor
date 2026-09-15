import { CONFIG } from '../../config.js';
import { validator } from './index.js';
import { retryAgent } from '../../runtime.js';
import type { BrainerState } from '../../brainerState.js';
import type { ValidatorFinding, ValidatorOut, ValidatorRequest } from '../../types/index.js';

// VALIDATOR — the per-wave coverage gate (distinct from the terminal judge). Given the wave's lookupNext
// requests + each lane's intro + which lanes died, it rules whether each request was fulfilled and what is still missing.
export async function runValidator(
  bs: BrainerState,
  wave: number,
  requests: ValidatorRequest[],
  findings: ValidatorFinding[],
  nullLanes: string[],
): Promise<ValidatorOut | null> {
  return retryAgent<ValidatorOut>(
    validator.buildPrompt({ query: CONFIG.query, requests, findings, nullLanes }),
    {
      label: 'validator-w' + wave,
      phase: CONFIG.PHASE.crawl,
      model: validator.tier,
      effort: validator.effort,
      schema: validator.schema,
    },
  );
}
