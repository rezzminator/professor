// verify.js — the stale-bundle guard. Rebuilds ../workflow.js to a disposable temp path (reusing
// build.js's exact `runBuild` pipeline) and byte-diffs the result against the COMMITTED ../workflow.js.
// A mismatch means src/ was edited without rebuilding + committing the bundle — exactly the drift this
// repo's "commit source + the regenerated bundle together" convention depends on humans remembering.
// `npm test`'s `pretest` runs this first, so the full suite can never pass silently against a stale
// bundle. See build.js's header comment for why this beats "verify only inside build's own finishing
// step" (that would only catch staleness at build time, not at the `npm test` gate that actually
// precedes a commit).

import { readFileSync, mkdtempSync, rmSync } from 'node:fs';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
import { tmpdir } from 'node:os';
import { runBuild } from './build.js';

const HERE = dirname(fileURLToPath(import.meta.url));
const COMMITTED = join(HERE, '..', 'workflow.js');

const fail = (msg) => {
  console.error('✗ verify failed — ' + msg);
  process.exit(1);
};

const scratchDir = mkdtempSync(join(tmpdir(), 'rr-verify-'));
try {
  const fresh = runBuild(join(scratchDir, 'workflow.js'));
  let committed;
  try {
    committed = readFileSync(COMMITTED, 'utf8');
  } catch (e) {
    fail(COMMITTED + ' does not exist — run `npm run build` first.\n' + e.message);
  }
  if (fresh !== committed)
    fail(
      '../workflow.js is STALE — it does not match a fresh rebuild of engine/src/.\n' +
        '  Run `npm run build` in engine/ and commit the regenerated ../workflow.js alongside your source change.',
    );
  console.log('✓ ../workflow.js matches a fresh rebuild of engine/src/ — not stale');
} finally {
  rmSync(scratchDir, { recursive: true, force: true });
}
