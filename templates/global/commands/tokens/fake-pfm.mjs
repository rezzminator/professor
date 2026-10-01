// fake-pfm.mjs — the stand-in pfm the tokens test files share (pricing.test.mjs,
// token-audit.test.mjs); not a test file itself.
import fs from "node:fs";
import path from "node:path";

// A stand-in pfm: a shell script running `body`, written into a fresh directory under `root`.
export function fakePfm(root, body) {
  const p = path.join(fs.mkdtempSync(path.join(root, "pfm-")), "pfm");
  fs.writeFileSync(p, `#!/bin/sh\n${body}\n`);
  fs.chmodSync(p, 0o755);
  return p;
}
