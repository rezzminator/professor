// INITIATOR — opens the Finalize phase. Reads the final resultSoFar and shapes the finish to the query:
// names the load-bearing facts to harden and sets the report focus. Tier: opus (synthesis/planning).
// Effort: xhigh.
import { CONFIG } from '../../config.js';
import { buildInitiator } from './prompts.js';
import type { Agent, InitiatorArgs, Schema } from '../../types/index.js';

// FINALIZE schemas. The INITIATOR plans the finish (which facts to harden, the report focus); a Sonnet REFINE pass adversarially
// fact-checks each load-bearing fact and returns its corrected claim; an Opus JUDGE then judges the hardened answer.
export const INITIATOR: Schema = {
  type: 'object',
  properties: {
    refinement: {
      type: 'object',
      properties: {
        facts: {
          type: 'array',
          items: {
            type: 'object',
            properties: {
              fact: {
                type: 'string',
                description:
                  'a group of related load-bearing facts the answer rests on (state the whole cluster)',
              },
              why: {
                type: 'string',
                description: 'why this group is load-bearing — what breaks if it is wrong',
              },
              claimId: {
                type: 'number',
                description:
                  'ledger claim id this fact corresponds to, when one exists — hardening then updates that claim record',
              },
            },
            required: ['fact', 'why'],
          },
          description:
            'load-bearing facts to harden, aggressively grouped into a few items — bundle facts that share sources or stand or fall together into one; cover all that would change the answer if wrong, skip soft restatements',
        },
      },
      required: ['facts'],
    },
    synthesiser: {
      type: 'object',
      properties: {
        focus: {
          type: 'string',
          description:
            'a note to the report writer on what to emphasize / the shape the answer should take',
        },
      },
      required: ['focus'],
    },
  },
  required: ['refinement', 'synthesiser'],
};

export const initiator: Agent<InitiatorArgs> = {
  tier: CONFIG.TIER.initiator,
  effort: CONFIG.EFFORT.initiator,
  schema: INITIATOR,
  buildPrompt: buildInitiator,
};
