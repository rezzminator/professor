// DEBUG ANALYST — last phase, opt-in (arg.debug). Consolidates the run's diagnostics corner by corner
// (incl. prospector→researcher venue utilization + any arg.debugPrompt question) into one _debug.md.
// Tier: opus (diagnostic synthesis). Effort: high.
import { CONFIG } from '../../config.js';
import { buildDebugAnalyst } from './prompts.js';
import type { Agent, DebugAnalystArgs, Schema } from '../../types/index.js';

export const DIAG: Schema = {
  type: 'object',
  properties: {
    diagnosis: {
      type: 'string',
      description: 'the full corner-by-corner debug consolidation + analysis as markdown',
    },
  },
  required: ['diagnosis'],
};

export const debugAnalyst: Agent<DebugAnalystArgs> = {
  tier: CONFIG.TIER.debugAnalyst,
  effort: CONFIG.EFFORT.debugAnalyst,
  schema: DIAG,
  buildPrompt: buildDebugAnalyst,
};
