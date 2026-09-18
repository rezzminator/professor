#!/usr/bin/env node

// The repository no longer ships .claude/scripts/build-opencode.mjs. Keep the
// live workflow, remediation, and operator guidance surfaces on the native
// writer and name any stale deleted-writer reference by file and line.

import { readFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const SURFACES = new Map([
  [".github/workflows/verify.yml", "go run ./cmd/pfm opencode build .."],
  [".github/workflows/plugin-scan.yml", "go run ./cmd/pfm opencode build .."],
  ["scripts/check-agent-roster.mjs", "go run ./cmd/pfm opencode build .."],
  ["INSTALL.md", "pfm opencode build ."],
  [".gitignore", "pfm opencode build"],
]);
const STALE_WRITER = /build-opencode\.mjs/g;
let scanned = 0;
let failures = 0;

const fail = (message) => {
  console.error(`opencode-writer: FAIL ${message}`);
  failures++;
};

for (const [relativePath, nativeCommand] of SURFACES) {
  const path = join(ROOT, relativePath);
  let content;
  try {
    content = readFileSync(path, "utf8");
  } catch (error) {
    fail(`${relativePath} — could not be read (${error.code ?? error.message}); scan did not run`);
    continue;
  }
  scanned++;

  content.split("\n").forEach((line, index) => {
    STALE_WRITER.lastIndex = 0;
    if (STALE_WRITER.test(line))
      fail(`${relativePath}:${index + 1} — stale deleted writer reference: ${line.trim()}`);
  });
  if (!content.includes(nativeCommand))
    fail(`${relativePath} — missing native remediation: ${nativeCommand}`);
}

if (scanned !== SURFACES.size)
  fail(`scan incomplete — read ${scanned}/${SURFACES.size} assigned live surfaces`);
if (!scanned) fail("scan found no readable live surfaces; this is not a clean tree");

if (failures) {
  console.error(`opencode-writer: ${failures} problem(s) across ${scanned}/${SURFACES.size} live surfaces`);
  process.exit(1);
}

console.log(`opencode-writer: clean — ${scanned} live surfaces use native pfm opencode and contain no deleted-writer reference`);
