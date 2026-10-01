#!/usr/bin/env node

// Keep the live workflow, remediation, and operator-guidance surfaces on the
// native writer (pfm opencode build).

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const SURFACES = new Map([
  [".github/workflows/verify.yml", "go run ./cmd/pfm opencode build .."],
  [".github/workflows/plugin-scan.yml", "go run ./cmd/pfm opencode build .."],
  ["scripts/check-agent-roster.mjs", "go run ./cmd/pfm opencode build .."],
  ["INSTALL.md", "pfm opencode build ."],
  [".gitignore", "pfm opencode build"],
]);
let checked = 0;
let failures = 0;

const fail = (message) => {
  console.error(`opencode-writer: FAIL ${message}`);
  failures++;
};

for (const [relativePath, nativeCommand] of SURFACES) {
  const path = resolve(ROOT, relativePath);
  let content;
  try {
    content = readFileSync(path, "utf8");
  } catch (error) {
    fail(`${relativePath} — could not be read (${error.code ?? error.message})`);
    continue;
  }
  checked++;
  if (!content.includes(nativeCommand))
    fail(`${relativePath} — missing native remediation: ${nativeCommand}`);
}

if (!checked) fail("read no live surface; this is not a clean tree");

if (failures) {
  console.error(`opencode-writer: ${failures} problem(s) across ${checked} live surfaces`);
  process.exit(1);
}

console.log(`opencode-writer: clean — ${checked} live surfaces name native pfm opencode`);
