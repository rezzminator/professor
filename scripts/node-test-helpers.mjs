// Shared Node primitives for script fixtures; each suite owns its setup and cleanup.
import { test as nodeTest } from "node:test";
export { before, after } from "node:test";
export { default as assert } from "node:assert/strict";
export { execFileSync, spawnSync } from "node:child_process";
export { copyFileSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, symlinkSync, unlinkSync, writeFileSync } from "node:fs";
export { tmpdir } from "node:os";
export { dirname, join } from "node:path";
export { fileURLToPath } from "node:url";

// NODE_TEST_SHARD="{i}/{n}" (dev.sh node_test_suite under NODE_TEST_SHARDS) registers every n-th
// top-level test, the i-th first, so n processes together run each test once; unset registers all.
// It is read once and dropped: a child process a test starts never inherits it. A shard left with
// no test (more shards than tests) fails its process by name: a run of nothing is never a pass.
const shard = /^([1-9]\d*)\/([1-9]\d*)$/.exec(process.env.NODE_TEST_SHARD ?? "1/1");
if (!shard || Number(shard[1]) > Number(shard[2])) {
  throw new Error(`NODE_TEST_SHARD must be {i}/{n} with 1 <= i <= n: ${process.env.NODE_TEST_SHARD}`);
}
delete process.env.NODE_TEST_SHARD;
let registered = 0;
let kept = 0;
export function test(...args) {
  if (registered++ % Number(shard[2]) !== Number(shard[1]) - 1) return undefined;
  kept++;
  return nodeTest(...args);
}
process.on("exit", () => {
  if (registered === 0 || kept > 0) return;
  console.error(`NODE_TEST_SHARD ${shard[0]} holds no test: the suite registers ${registered}`);
  process.exitCode = 1;
});
