// RESEARCHER — a lane READER: it reads its ASSIGNED cache slice(s) from disk (via code) and digests them
// into the lane's running answer. The scheduler owns discovery (which sources, sized to disk); code bin-packs
// each lane's content into RESEARCHER_TOKEN_BUDGET reader-units and runs ONE sequential thread per lane,
// handing the running answer forward across all its reads. Tier: haiku — the read-from-disk + digest is a
// BOUNDED worker task (the scheduler already chose + fetched the sources; a SONNET researcher crashed the
// vector-DB run). Effort: medium. (Both read from the central CONFIG.TIER/EFFORT maps.) The reader runs as a
// code-capable general-purpose agent so it can read its char window off disk + resolve a wall.
import { CONFIG } from '../../config.js';
import { CLAIM_ITEM_STANCE, RABBITHOLE, TERM_SEED } from '../shared.js';
import { buildResearcher } from './prompts.js';
import type { Agent, ResearcherArgs, Schema } from '../../types/index.js';

export const RESEARCH: Schema = {
  type: 'object',
  properties: {
    runningAnswer: {
      type: 'string',
      description:
        'the accumulated answer for this lane: merge what you found in your slice INTO the prior answer (or begin it if you are reader 1), kept a coherent whole — the next reader continues it and the brainer reads the final one',
    },
    rabbitHoles: { type: 'array', items: RABBITHOLE },
    nextSources: {
      type: 'array',
      maxItems: 5,
      items: {
        type: 'object',
        properties: {
          ref: {
            type: 'string',
            description: 'an exact url or DOI the content points to, worth fetching directly',
          },
          why: { type: 'string', description: 'one line on why following it advances the goal' },
          // expect/target are advisory (the engine seeds only ref/why into the store) — null-tolerant
          // and un-enumed so a loose value can never fail the whole reader payload into a retry.
          expect: {
            type: ['string', 'null'],
            description:
              'support | attack | neutral — whether following it is expected to SUPPORT or ATTACK `target`',
          },
          target: {
            type: ['number', 'string', 'null'],
            description: 'id of the existing claim this source is expected to support or attack',
          },
        },
        required: ['ref', 'why'],
      },
      description:
        "up to 5 of the content's highest-value outbound citations/links as concrete fetch targets — a later lane fetches each directly",
    },
    claims: {
      type: 'array',
      items: CLAIM_ITEM_STANCE,
      description:
        'load-bearing facts this slice carries — each pinned to a verbatim quote; only facts the answer could rest on, never a transcript of everything read',
    },
    newTerms: {
      type: 'array',
      items: TERM_SEED,
      description:
        "the community's terms of art this slice uses that the digest/query does not — empty when the slice speaks our vocabulary",
    },
    surprise: {
      type: ['string', 'null'],
      description:
        'one line naming the contradiction — set ONLY when this slice contradicts one of the KEY CLAIMS in the digest',
    },
    deadEnds: { type: 'array', items: { type: 'string' } },
  },
  // The channel fields are REQUIRED so a reader consciously reports zero — an empty array is fine and
  // normal on a thin slice, but a MISSING field is indistinguishable from a silently dropped one. Run
  // forensics caught a degraded lane that returned ONLY runningAnswer, omitting claims/deadEnds entirely,
  // so the engine's lane-reopen machinery never fired. newTerms/surprise stay optional (TS ReaderOut keeps
  // its optionals — the engine's `|| []` guards still apply).
  required: ['runningAnswer', 'claims', 'rabbitHoles', 'deadEnds'],
};

export const researcher: Agent<ResearcherArgs> = {
  tier: CONFIG.TIER.researcher,
  effort: CONFIG.EFFORT.researcher,
  schema: RESEARCH,
  buildPrompt: buildResearcher,
};
