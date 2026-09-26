// SCOUT SWARM — the wave-0 seed, now three stages instead of one broad sweep: scoutPlanner (sonnet, high)
// runs 1-2 quick WebSearches to ground the landscape then decomposes the query + proposes 3..SCOUT_PROBES
// search angles; a swarm of `scout` probes (haiku, medium — UNCHANGED tier: the page reading stays the
// fixed haiku reader's job, only its scope narrows to one angle) each sweep one angle; scoutMerger (sonnet,
// high, no tools) folds every surviving probe into the final ScoutOut, naming the tensions between angles.
// Every stage degrades to a named JS fallback — see run.ts.
import { CONFIG } from '../../config.js';
import { CLAIM_ITEM, PAGE, TERM_SEED } from '../shared.js';
import { buildScout, buildScoutMerger, buildScoutPlanner } from './prompts.js';
import type {
  Agent,
  ScoutArgs,
  ScoutMergerArgs,
  ScoutPlannerArgs,
  Schema,
} from '../../types/index.js';

// ── stage 1 schema — the planner's decomposition + proposed angles ──
export const SCOUT_ANGLE: Schema = {
  type: 'object',
  properties: {
    name: {
      type: 'string',
      description: 'short angle label, e.g. "direct", "skeptic", "recent"',
    },
    searchQuery: {
      type: 'string',
      description: 'a concrete, distinct search query for THIS angle — never a reworded sibling',
    },
    why: { type: 'string', description: 'why this angle matters for the query' },
    lens: {
      type: 'string',
      description: 'the interpretive lens the probe should read its sources through',
    },
  },
  required: ['name', 'searchQuery', 'why'],
};

export const SCOUT_PLANNER: Schema = {
  type: 'object',
  properties: {
    decomposition: { type: 'string', description: "the query's distinct axes, one line each" },
    angles: {
      type: 'array',
      items: SCOUT_ANGLE,
      description: `3..${CONFIG.SCOUT_PROBES} distinct, non-overlapping search angles for the probe swarm`,
    },
  },
  required: ['decomposition', 'angles'],
};

export const scoutPlanner: Agent<ScoutPlannerArgs> = {
  tier: CONFIG.TIER.scoutPlanner,
  effort: CONFIG.EFFORT.scoutPlanner,
  schema: SCOUT_PLANNER,
  buildPrompt: buildScoutPlanner,
};

// ── stage 2 schema — one probe's return. UNCHANGED shape from v2's single scout: field-compatible so
// scoutMerger (and both JS fallbacks) can reuse this exact schema for the FINAL ScoutOut too. ──
export const SCOUT: Schema = {
  type: 'object',
  properties: {
    landscape: {
      type: 'string',
      // shared by probe (2-3 sentences) and merger (one paragraph) — the description binds the
      // INVARIANT both share: run forensics caught a probe packing its entire return in here,
      // omitting every other required key, and burning five identical schema-error retries.
      description:
        'the summary narrative ONLY — facts belong in claims[] and page detail in pages[]; never pack the whole return into this field',
    },
    pages: { type: 'array', items: PAGE },
    deadEnds: { type: 'array', items: { type: 'string' } },
    claims: {
      type: 'array',
      items: CLAIM_ITEM,
      description:
        "union of the fetched pages' load-bearing facts, each pinned to a verbatim quote — no digest exists yet, so never carries `stance`",
    },
    nextSources: {
      type: 'array',
      items: {
        type: 'object',
        properties: {
          ref: {
            type: 'string',
            description: 'an exact url or DOI a fetched page points to, worth fetching directly',
          },
          why: { type: 'string', description: 'one line on why following it matters' },
        },
        required: ['ref', 'why'],
      },
      description:
        "union of the fetched pages' highest-value outbound citations — no ledger exists yet, so never carries expect/target",
    },
    newTerms: {
      type: 'array',
      items: TERM_SEED,
      description: "union of the fetched pages' community terms of art that we did not use",
    },
  },
  required: ['landscape', 'pages'],
};

export const scout: Agent<ScoutArgs> = {
  tier: CONFIG.TIER.scout,
  effort: CONFIG.EFFORT.scout,
  schema: SCOUT,
  buildPrompt: buildScout,
};

// ── stage 3 — scoutMerger: folds every probe's SCOUT-shaped output into ONE final SCOUT-shaped output. ──
export const scoutMerger: Agent<ScoutMergerArgs> = {
  tier: CONFIG.TIER.scoutMerger,
  effort: CONFIG.EFFORT.scoutMerger,
  schema: SCOUT, // same shape as a probe's output — the merger folds N of them into one
  buildPrompt: buildScoutMerger,
};
