// RERUNNER — re-executes the stored derivation artifact (a pure, seeded Python script) with the run's
// current inputs. Tier: haiku (bounded, mechanical re-execution — never repairs the artifact). Effort: low.
// Dies or errors → lastRun stays stale; the caller keeps the last good run and tells the brainer it is stale.
import { CONFIG } from '../../config.js';
import { buildRerunner } from './prompts.js';
import type { Agent, RerunnerArgs, Schema } from '../../types/index.js';

export const RERUN: Schema = {
  type: 'object',
  properties: {
    ok: { type: 'boolean' },
    quantiles: {
      type: 'object',
      additionalProperties: { type: 'number' },
      description: "the script's printed quantiles, verbatim",
    },
    sensitivity: {
      type: 'object',
      additionalProperties: { type: 'number' },
      description: "the script's printed variance-share sensitivity, verbatim",
    },
    note: { type: 'string', description: 'the error message when ok is false' },
  },
  required: ['ok'],
};

export const rerunner: Agent<RerunnerArgs> = {
  tier: CONFIG.TIER.rerunner,
  effort: CONFIG.EFFORT.rerunner,
  schema: RERUN,
  buildPrompt: buildRerunner,
};
