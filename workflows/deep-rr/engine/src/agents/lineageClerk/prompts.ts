// LINEAGE CLERK prompts — the batched entity-canonicalization template + its assembly function. Template
// strings are module-level consts; buildLineageClerk only assembles/substitutes the items + known keys.
// No FINISH here: the clerk carries no tools (a plain subagent) — there is no tool-use rabbit hole to guard against.
import { plain, render } from '../../utils/index.js';
import type { LineageClerkArgs } from '../../types/index.js';

const LINEAGE_TPL = `{{! lineageClerk — canonicalize provenance entities so JS can union-find independence clusters }}
You are the LINEAGE CLERK. Canonicalize each new claim's provenance entities into stable keys so independence clusters can be computed mechanically downstream — this IS the whole job: the SAME real-world entity spelled differently must map to the SAME key ("Pfizer Inc." and "Pfizer" both → funder:pfizer).
New claims (\`#id source | entities\`):
{{items}}
Known canonical keys already in use this run (reuse one of these EXACTLY whenever a claim's entity is the same real-world thing, however it is spelled):
{{knownKeys}}
For each claim return its canonical keys: lowercase, kebab-ish, prefixed by entity type — author:j-smith, funder:pfizer, dataset:gaia-dr3, venue:apj. Only emit a key for an entity actually present on that claim; skip absent/unknown entities entirely (never invent one to fill a slot).
Return links: one {id, keys} per claim.
`;

export const buildLineageClerk = ({ items, knownKeys }: LineageClerkArgs) =>
  render(LINEAGE_TPL, { items: plain(items), knownKeys: plain(knownKeys) });
