// Shared Node primitives for script fixtures; each suite owns its setup and cleanup.
export { before, after, test } from "node:test";
export { default as assert } from "node:assert/strict";
export { execFileSync, spawnSync } from "node:child_process";
export { copyFileSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, symlinkSync, unlinkSync, writeFileSync } from "node:fs";
export { tmpdir } from "node:os";
export { dirname, join } from "node:path";
export { fileURLToPath } from "node:url";
