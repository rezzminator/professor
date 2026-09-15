// ─────────────────────────────────────────────────────────────────────────────
// Shared cross-agent fragments — imported by the per-agent modules in this folder so a
// fragment used by more than one agent lives in EXACTLY one place (never duplicated per agent).
//
// Two kinds live here:
//   • static PROMPT fragments (FINISH, WEB_ONLY) — guard clauses appended to several agents'
//     prompts. (The run-DERIVED fragments — NET, FOOTER, RUBRIC, STOP, THINKER_NOTE,
//     RESEARCHER_NOTE, COMPUTE_NOTE — are built per run on the CONFIG singleton in ../config.js.)
//   • shared StructuredOutput SCHEMA bricks — the nested sub-schemas reused across more than one
//     agent's output contract (RABBITHOLE), plus the single-source nested bricks the top-level
//     contracts compose. Each agent's TOP-LEVEL schema is co-located in its own file; these
//     reusable bricks stay here so each nesting identity has one definition. A couple (CLAIM_ITEM's
//     quote cap) render a CONFIG knob straight into their description — the schema is data too, so
//     the same "no literal outside config.ts" rule applies to it.
// ─────────────────────────────────────────────────────────────────────────────
import { CONFIG } from '../config.js';
import type { Schema } from '../types/index.js';

// ── static prompt guard clauses ──
// FINISH: the pure reducers (brainer, initiator, synthesiser) already hold the data they
// need — they MAY use a tool if it genuinely helps, but the hard rule is they FINISH: emit the
// COMPLETE StructuredOutput rather than getting lost (the wave-0 brainer once spent its whole turn
// reading this repo's own files on a self-referential query and never emitted resultSoFar/lookupNext/stop).
export const FINISH = `
The data above is enough to decide. You may consult a tool if it genuinely helps, but keep it brief — the answer does not require it. Your one required action: return the complete StructuredOutput with every required field, never a partial object.`;
// WEB_ONLY: the refine pass checks claims on the web — the local repo code is never evidence.
export const WEB_ONLY = `
Use the web only (WebSearch / mcp__harvester__fetch) to check sources — never read local files or this repo's own code; they are not evidence.`;
// EMIT: JSON-emission discipline for the agents whose StructuredOutput payload is large (readers, probes,
// merger, prospector, scheduler, brainer). Run forensics: emitters intermittently sent prose-/<parameter>-
// wrapped JSON and unescaped control characters in long string values — each a parse failure that burns a
// visible retry — and one probe collapsed its whole return into the first prose field, omitted every other
// required key, and re-sent that same shape through five schema-error retries. Schemas are null-tolerant
// for optional fields; this clause attacks the malformed-JSON and required-field-collapse classes.
export const EMIT = `
StructuredOutput discipline: its input must be ONE valid JSON object — escape every quote, newline, and backslash inside string values; no code fences, no XML/<parameter> syntax, no prose outside the JSON. Emit EVERY required field in that one call — a required array with nothing to report is [], never omitted — and never collapse your findings into a single prose field the schema splits into structured ones. Omit optional fields you have nothing for. Keep free-text values tight (one line each unless the field says otherwise) — a compact payload parses, an essay-sized one truncates and dies. When a schema error comes back naming a field, fix exactly that field and re-emit the corrected COMPLETE object — resending the same shape fails the same way.`;

// ── shared schema bricks (declaration order respects nesting) ──
export const RABBITHOLE: Schema = {
  type: 'object',
  properties: {
    keyword: { type: 'string' },
    why: { type: 'string' },
    kind: {
      type: 'string',
      enum: ['gap', 'attack', 'entity'],
      description:
        "gap = an unexplained-gap search phrased in the SOURCE'S OWN terminology; attack = the single strongest realistic counter-evidence search for a claim this content supports; entity = follow an author/venue/dataset that keeps recurring",
    },
  },
  required: ['keyword', 'why'],
};
export const SCORED: Schema = {
  type: 'object',
  properties: {
    keyword: { type: 'string' },
    why: { type: 'string' },
    score: { type: 'number' },
    kind: {
      type: 'string',
      enum: ['gap', 'attack', 'entity', 'origin'],
      description: 'the lead channel; set "attack" for a counter-evidence lane',
    },
  },
  required: ['keyword', 'why', 'score'],
};
// CLAIM_ITEM = the load-bearing fact a reader/scout pins to a verbatim quote (pre-ledger — the engine
// assigns id/cluster/audit/status when it enters bs.claims). The base shape every claim-emitting
// agent shares; CLAIM_ITEM_STANCE adds `stance` for the researcher, which alone gets a digest of
// existing claims to bear on (the scout's wave-0 pages have no digest yet).
export const CLAIM_ITEM: Schema = {
  type: 'object',
  properties: {
    claim: { type: 'string', description: 'one load-bearing fact, in one line' },
    value: {
      type: ['string', 'null'],
      description: 'the number/quantity, when the claim is quantitative',
    },
    quote: {
      type: 'string',
      description: `a VERBATIM span, copied exactly and CONTIGUOUSLY from the source, of at most ${CONFIG.QUOTE_MAX_CHARS} characters that carries the claim — one unbroken span, NEVER separate fragments stitched with an ellipsis (a spliced quote fails the mechanical audit and the claim dies)`,
    },
    source: { type: 'string', description: 'the url or DOI this quote is from' },
    // every optional provenance field is null-tolerant (run forensics: haiku readers emit null for an
    // entity the page does not show, and a hard 'must be string' fails the whole payload) — the engine
    // null-scrubs at ingest (utils scrubEntities), so tolerance here costs nothing downstream.
    entities: {
      type: ['object', 'null'],
      properties: {
        authors: { type: ['array', 'null'], items: { type: ['string', 'null'] } },
        funder: { type: ['string', 'null'] },
        dataset: { type: ['string', 'null'] },
        venue: { type: ['string', 'null'] },
      },
      description:
        "the source's provenance — only what is visibly stated, never inferred; omit what is absent",
    },
    cachePath: {
      type: ['string', 'null'],
      description:
        'local cache file path this quote can be verified against, ONLY as reported by the fetch tool — never invented',
    },
  },
  required: ['claim', 'quote', 'source'],
};
export const CLAIM_ITEM_STANCE: Schema = {
  type: 'object',
  properties: {
    ...CLAIM_ITEM.properties,
    stance: {
      type: ['object', 'null'],
      properties: {
        target: {
          // number preferred; a string ('c12') validates and the engine's ingest coercion pulls the
          // digit-run out — a hard number-only type made every prose target a schema-mismatch retry.
          type: ['number', 'string'],
          description:
            "the NUMBER from the existing KEY CLAIM's c-id in the digest (e.g. c12 → target: 12) this claim bears on — a bare number, never prose",
        },
        kind: { type: 'string', enum: ['supports', 'attacks'] },
      },
      required: ['target', 'kind'], // a stance without a target is unlinkable — run forensics: prose targets were silently dropped and the attack graph never formed
      description:
        'ONLY when this claim directly bears on one of the KEY CLAIMS listed in the digest',
    },
  },
  required: ['claim', 'quote', 'source'],
};
// TERM_SEED = one community term of art a reader/scout surfaces (pre-ledger — `uses` is engine-owned).
export const TERM_SEED: Schema = {
  type: 'object',
  properties: { term: { type: 'string' }, gloss: { type: ['string', 'null'] } },
  required: ['term'],
};
export const PAGE: Schema = {
  type: 'object',
  properties: {
    url: { type: 'string' },
    summary: { type: 'string' },
    rabbitHoles: { type: 'array', items: RABBITHOLE },
  },
  required: ['url', 'summary', 'rabbitHoles'],
};
// LOOKUP = one item in the brainer's `lookupNext` (research NOW): EITHER {id} (an existing open rabbit-hole) OR {keyword,why,score,…}
// (originate-and-pursue-now). All fields optional so both shapes validate; `sources` are the prospector venues the brainer
// assigns to THIS lane (its researcher searches them first).
export const LOOKUP: Schema = {
  type: 'object',
  properties: {
    id: {
      type: 'number',
      description: 'id of an existing open rabbit-hole — use this OR the keyword fields, not both',
    },
    keyword: { type: 'string' },
    why: { type: 'string' },
    score: { type: 'number' },
    sources: {
      type: 'array',
      items: { type: 'string' },
      description:
        'subset of prospector venue identifiers (exact `source` strings) best suited to THIS rabbit-hole; empty if none fit',
    },
    note: {
      type: 'string',
      description:
        'WHAT to find + ranked fallbacks — steers the scheduler and the reader; distinct from `why`',
    },
    ref: {
      type: 'string',
      description:
        'a concrete URL/DOI to fetch DIRECTLY (a followed citation) instead of WebSearching',
    },
    kind: {
      type: 'string',
      enum: ['gap', 'attack', 'entity', 'origin'],
      description: 'the lead channel; set "attack" for a counter-evidence lane',
    },
    refetch: {
      type: 'boolean',
      description: 'cached copy corrupted — fetch fresh',
    },
  },
};
// resultSoFar = the run's living MEMORY, carried wave to wave. The brainer maintains it; refinement gets the FINAL one only.
export const RESULT_SO_FAR: Schema = {
  type: 'object',
  properties: {
    answer: {
      type: 'string',
      description: 'the best current answer to the goal, as it stands this wave',
    },
    keyClaimIds: {
      type: 'array',
      items: { type: 'number' },
      description: 'the ledger claim ids the answer currently rests on (load-bearing)',
    },
    assumptions: {
      type: 'array',
      items: {
        type: 'object',
        properties: {
          claim: {
            type: 'string',
            description: 'a working assumption the answer currently leans on',
          },
          basis: { type: 'string', description: 'what it rests on, and how firm that is' },
        },
        required: ['claim', 'basis'],
      },
      description:
        'working assumptions the answer leans on (each {claim, basis}); revise or retire them as evidence lands',
    },
    resolved: { type: 'array', items: { type: 'string' }, description: 'sub-questions now closed' },
    openGaps: { type: 'array', items: { type: 'string' }, description: 'what is still missing' },
    tensions: {
      type: 'array',
      items: { type: 'string' },
      description: 'conflicting sources / unresolved contradictions',
    },
    working: {
      type: 'string',
      description:
        "for build-the-answer / estimate questions, the growing derivation chain; '' for non-derivation questions",
    },
    confidence: { type: 'string' },
  },
  required: ['answer', 'keyClaimIds', 'resolved', 'openGaps', 'tensions', 'working', 'confidence'],
};
