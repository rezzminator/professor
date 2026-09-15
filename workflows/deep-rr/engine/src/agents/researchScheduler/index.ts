// RESEARCH SCHEDULER — inserted AFTER the brainer picks the wave's lanes (resolveLookupNext), BEFORE the
// readers spawn. It owns discovery: per brainer lane (the rabbit-hole + its steering `note`), it picks the
// HIGHEST-VALUE sources — MULTIPLE per lane, no cap — by batching ALL lane searches in one parallel round,
// then sizing every candidate via mcp__harvester__fetch size_only (returns {size, path, chars}) in a second
// parallel round; it returns the chosen sources grouped per lane id. Code (engine.ts) then bin-packs each
// lane's content into RESEARCHER_TOKEN_BUDGET reader-units and spawns the sequential per-lane reader threads.
// Tier: sonnet — judging source value + driving batched tool I/O is a mid-weight job, above a worker but below
// the Opus brain. Effort: high. (Both read from the central CONFIG.TIER/EFFORT maps.)
import { CONFIG } from '../../config.js';
import { buildResearchScheduler } from './prompts.js';
import type { Agent, ResearchSchedulerArgs, Schema } from '../../types/index.js';

// SCHEDULE — the scheduler's output: the chosen, sized sources grouped per lane id. The engine re-confirms
// each `size` against the budget and computes the char windows itself (the Sonnet's numbers are not trusted).
export const SCHEDULE: Schema = {
  type: 'object',
  properties: {
    lanes: {
      type: 'array',
      items: {
        type: 'object',
        properties: {
          id: { type: 'number', description: 'the input lane id these sources belong to' },
          sources: {
            type: 'array',
            items: {
              type: 'object',
              properties: {
                source: {
                  type: 'string',
                  description: 'the exact url or DOI of the chosen source',
                },
                path: {
                  type: 'string',
                  description: 'the local cache file path returned by the size_only fetch',
                },
                size: {
                  type: 'number',
                  description: 'the source size in tokens (the size_only count)',
                },
                chars: { type: 'number', description: 'the source size in raw characters' },
              },
              required: ['source', 'path', 'size', 'chars'],
            },
            description:
              'the highest-value sources chosen for this lane (multiple, no cap); empty only when every candidate failed',
          },
          venuesServed: {
            type: 'array',
            items: { type: 'string' },
            description:
              "the subset of this lane's ASSIGNED venue source strings its chosen sources actually come from; [] when none",
          },
          unsourced: {
            type: 'array',
            items: {
              type: 'object',
              properties: { ref: { type: 'string' }, reason: { type: 'string' } },
              required: ['ref', 'reason'],
            },
            description:
              'directive-named refs/venues that could NOT be sourced — reported honestly, never silently substituted',
          },
        },
        required: ['id', 'sources'],
      },
    },
  },
  required: ['lanes'],
};

export const researchScheduler: Agent<ResearchSchedulerArgs> = {
  tier: CONFIG.TIER.researchScheduler,
  effort: CONFIG.EFFORT.researchScheduler,
  schema: SCHEDULE,
  buildPrompt: buildResearchScheduler,
};
