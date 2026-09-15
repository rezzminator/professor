// BRAINER — the brain / global reducer. Sees the open store + pursued set + running resultSoFar; returns
// the updated resultSoFar + DELTAS (rescore / add / lookupNext / rename / drop / stop). Looks up stored
// leads OR originates new directions; code-capable (general-purpose) when compute is on so it derives its
// own steering numbers inline — no separate compute stage.
// Tier: opus — ALWAYS Opus (the global brain/reducer — measured: a Haiku brainer scored erratically +
// drifted off-goal). Effort: xhigh — re-scores the store every wave AND sets direction AND maintains
// resultSoFar; the one role where the extra reasoning budget pays back most.
import { CONFIG } from '../../config.js';
import { SCORED, LOOKUP, RESULT_SO_FAR } from '../shared.js';
import { buildBrainer, buildBrainerCompute } from './prompts.js';
import type { Agent, BrainerArgs, Schema } from '../../types/index.js';
export { buildBrainerCompute };

// COORD = the brainer's per-wave output: the updated resultSoFar + DELTAS against the engine's id-keyed open store. The engine carries
// each rabbit-hole's id + scoreHistory natively — the brainer never re-emits the whole set, it only sends what changed.
//
// Built per call by buildCoord, NOT shipped as one static shape: the platform classifier that screens
// agent spawns rejects oversized output schemas ("output schema too large to classify safely" — observed
// killing wave-0 brainers live, across models). So the optional clauses ship ONLY when the call can use
// them — `derivation` only when compute is on, `spawn` only on a wave that may spawn — and every
// description is kept terse (steering prose lives in the prompt, which already carries it). COORD (the
// full shape) stays exported as the canonical contract for types, tests, and the sandbox check.
const SPAWN: Schema = {
  type: 'object',
  properties: {
    id: {
      type: 'number',
      description: 'an OPEN rabbit-hole id for the child (or omit and give keyword+why)',
    },
    keyword: { type: 'string' },
    why: { type: 'string' },
    mandate: {
      type: 'string',
      description: 'the directive that aims the child brainer — what this one branch must chase',
    },
  },
  required: ['mandate'],
  description:
    'OPTIONAL, at most ONE per wave: spawn a focused child brainer onto a branch worth a dedicated brain — spawns are expensive. Omit when not spawning.',
};
const DERIVATION: Schema = {
  type: 'object',
  properties: {
    code: {
      type: 'string',
      description:
        'pure seeded python3: reads ONE json arg {inputName: value}, prints ONE json {quantiles:{p10,p50,p90}, sensitivity:{inputName: varianceShare 0-1}}',
    },
    inputs: {
      type: 'array',
      items: {
        type: 'object',
        properties: {
          name: { type: 'string' },
          dist: {
            type: 'string',
            description: 'the value/distribution to feed, e.g. "lognormal(mu=…, sigma=…)" or a point value',
          },
          claimIds: { type: 'array', items: { type: 'number' } },
          prior: { type: 'boolean', description: 'true = wide-prior placeholder, not evidence-backed' },
        },
        required: ['name', 'dist', 'claimIds', 'prior'],
      },
    },
  },
  required: ['code', 'inputs'],
  description:
    "OPTIONAL: author (or re-author) the run's stored derivation — the engine reruns it cheaply every wave. Re-emit only to change the code or the inputs.",
};
export function buildCoord(opts: { compute: boolean; canSpawn: boolean }): Schema {
  const properties: Record<string, Schema> = {
    resultSoFar: RESULT_SO_FAR,
    rescore: {
      type: 'array',
      items: {
        type: 'object',
        properties: { id: { type: 'number' }, score: { type: 'number' } },
        required: ['id', 'score'],
      },
      description:
        'only the open rabbit-holes whose score changes this wave; unlisted ones keep their last score. Score every "new" (unscored) one at least once.',
    },
    add: {
      type: 'array',
      items: SCORED,
      description: 'new rabbit-holes to park in the store for a later wave (the engine assigns ids)',
    },
    lookupNext: {
      type: 'array',
      items: LOOKUP,
      description:
        'the rabbit-holes to research now — each either {id} (stored) or {keyword,why,score,…} (originate-and-pursue-now). None may be already pursued; assign each its `sources` venue subset.',
    },
    rename: {
      type: 'array',
      items: {
        type: 'object',
        properties: {
          id: { type: 'number' },
          keyword: { type: 'string' },
          why: { type: 'string' },
        },
        required: ['id', 'keyword'],
      },
      description: 'relabel a rabbit-hole, keeping its id + score history',
    },
    drop: {
      type: 'array',
      items: { type: 'number' },
      description:
        'ids of dead/duplicate rabbit-holes to eliminate (a MERGE = drop the duplicate, rescore the survivor)',
    },
  };
  if (opts.canSpawn) properties.spawn = SPAWN;
  if (opts.compute) properties.derivation = DERIVATION;
  properties.stop = {
    type: 'object',
    properties: {
      done: { type: 'boolean' },
      reason: { type: 'string', description: 'one line: why done, or what is still missing' },
      lost: {
        type: 'boolean',
        description:
          'CHILD brainers only: this branch is a dead end — abandon it (no answer expected). The root brainer must never set this.',
      },
    },
    required: ['done', 'reason'],
  };
  return {
    type: 'object',
    properties,
    required: ['resultSoFar', 'rescore', 'add', 'lookupNext', 'stop'],
  };
}
export const COORD: Schema = buildCoord({ compute: true, canSpawn: true });

export const brainer: Agent<BrainerArgs> = {
  tier: CONFIG.TIER.brainer,
  effort: CONFIG.EFFORT.brainer,
  schema: COORD,
  buildPrompt: buildBrainer,
};

// BRAIN_COMPUTE = the brain finalize-compute output: the updated resultSoFar with the derivation folded into `working`.
export const BRAIN_COMPUTE: Schema = {
  type: 'object',
  properties: { resultSoFar: RESULT_SO_FAR },
  required: ['resultSoFar'],
};
