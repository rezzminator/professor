#!/usr/bin/env node

// The repository no longer ships .claude/scripts/build-opencode.mjs. Keep the
// live workflow, remediation, and operator guidance surfaces on the native
// writer; name any stale deleted-writer reference anywhere in the tree, by
// file and line, except the exclusions below.

import { execFileSync } from "node:child_process";
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
const STALE_WRITER = /build-opencode\.mjs/g;

// Paths never reported: release history, the changelog, the steering inbox,
// this checker itself (its own STALE_WRITER pattern names the string), and
// the compiler's oldMarker line, which must keep naming the deleted writer
// so it recognizes legacy stamps.
const EXCLUDED_PATHS = new Set([
  "CHANGELOG.md",
  ".professor/retro.md",
  "scripts/check-opencode-writer.mjs",
]);
const isExcludedPath = (path) =>
  path.startsWith("releases/") || EXCLUDED_PATHS.has(path);
const OLD_MARKER_FILE = "pfm/internal/opencodegen/compiler.go";
const OLD_MARKER_LINE = /oldMarker\s*=/;

let scanned = 0;
let failures = 0;

const fail = (message) => {
  console.error(`opencode-writer: FAIL ${message}`);
  failures++;
};

// Inside the dev fence a linked worktree's .git points at a host path; dev.sh
// iso hands the real gitdir in as PFM_DEV_REPO_GIT_DIR + PFM_DEV_REPO_WORK_TREE
// (the contract scripts/leak-check.sh and release-check.mjs honor), applied
// only when that work tree is this checkout.
const fenceGitDir = process.env.PFM_DEV_REPO_GIT_DIR;
const fenceTree = process.env.PFM_DEV_REPO_WORK_TREE;
const gitPrefix =
  fenceGitDir && fenceTree && resolve(fenceTree) === ROOT
    ? [`--git-dir=${fenceGitDir}`, `--work-tree=${ROOT}`, "-c", `safe.directory=${ROOT}`]
    : [];

let paths = [];
try {
  const out = execFileSync(
    "git",
    [...gitPrefix, "ls-files", "--cached", "--others", "--exclude-standard", "-z"],
    { cwd: ROOT, encoding: "utf8", maxBuffer: 1 << 28 },
  );
  paths = out.split("\0").filter(Boolean);
} catch (error) {
  fail(`git ls-files — could not list the tree (${error.message}); scan did not run`);
}

for (const path of paths) {
  if (isExcludedPath(path)) continue;
  const full = resolve(ROOT, path);
  let content;
  try {
    content = readFileSync(full, "utf8");
  } catch (error) {
    // A tracked path deleted from the working tree (ENOENT) or a directory
    // entry such as a submodule (EISDIR) holds no file content to scan. Any
    // other read error is a file this scan failed to look at, never a clean one.
    if (error.code !== "ENOENT" && error.code !== "EISDIR")
      fail(`${path} — could not be read (${error.code ?? error.message}); scan incomplete`);
    continue;
  }
  scanned++;

  content.split("\n").forEach((line, index) => {
    if (path === OLD_MARKER_FILE && OLD_MARKER_LINE.test(line)) return;
    STALE_WRITER.lastIndex = 0;
    if (STALE_WRITER.test(line))
      fail(`${path}:${index + 1} — stale deleted writer reference: ${line.trim()}`);
  });
}

for (const [relativePath, nativeCommand] of SURFACES) {
  const path = resolve(ROOT, relativePath);
  let content;
  try {
    content = readFileSync(path, "utf8");
  } catch (error) {
    fail(`${relativePath} — could not be read (${error.code ?? error.message}); scan did not run`);
    continue;
  }
  if (!content.includes(nativeCommand))
    fail(`${relativePath} — missing native remediation: ${nativeCommand}`);
}

if (!scanned) fail("scan found no readable live surfaces; this is not a clean tree");

if (failures) {
  console.error(`opencode-writer: ${failures} problem(s) across ${scanned} scanned files`);
  process.exit(1);
}

console.log(`opencode-writer: clean — ${scanned} scanned files use native pfm opencode and contain no deleted-writer reference`);
