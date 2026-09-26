// Types barrel — the one import path for every RR engine type. Consumers do
// `import type { … } from '<rel>/types/index.js'`. (Ambient Workflow globals live in ./globals.d.ts,
// auto-included via tsconfig — not re-exported here.)
export type { Schema } from './schema.js';
export type * from './domain.js';
export type * from './claims.js';
export type * from './agents.js';
export type * from './run.js';
