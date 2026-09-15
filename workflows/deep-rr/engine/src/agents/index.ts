// Agents barrel — re-exports the per-agent modules so the engine (and tests) import from one path.
// Each agent is a DIRECTORY: `<agent>/index.ts` holds the agent object (tier, effort, schema, buildPrompt)
// + its StructuredOutput schema literal, and `<agent>/prompts.ts` holds the template consts + assembly
// function. Genuinely shared fragments (FINISH, WEB_ONLY, and the reusable schema bricks) live in ./shared.ts.
//
// NOTE: the bundler (build.js) concatenates the agent modules directly (see ORDER) and strips this
// barrel's re-exports — at runtime every agent const already lives in the flat scope. This file only
// serves the dev/test import path.
export { scout, scoutPlanner, scoutMerger } from './scout/index.js';
export { runScout } from './scout/run.js';
export { prospector } from './prospector/index.js';
export { brainer, BRAIN_COMPUTE, buildBrainerCompute } from './brainer/index.js';
export { validator } from './validator/index.js';
export { researchScheduler } from './researchScheduler/index.js';
export { researcher } from './researcher/index.js';
export { initiator } from './initiator/index.js';
export { refiner } from './refiner/index.js';
export { judge } from './judge/index.js';
export { synthesiser } from './synthesiser/index.js';
export { debugAnalyst } from './debugAnalyst/index.js';
// v3 ledger clerks — each a bounded, mechanical, batched-per-wave job; every one degrades to a named null path.
export { claimAuditor } from './claimAuditor/index.js';
export { runClaimAuditor } from './claimAuditor/run.js';
export { lineageClerk } from './lineageClerk/index.js';
export { runLineageClerk } from './lineageClerk/run.js';
export { rerunner } from './rerunner/index.js';
export { runRerunner } from './rerunner/run.js';
