// Tests for scripts/check-opencode-writer.mjs — the native-writer surface check.
// Run: node --test scripts/check-opencode-writer.test.mjs
//
// Each case copies the checker beside the five native-remediation surfaces,
// then runs it and reads the exit code and the named lines.

import { test } from "node:test";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { copyFileSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const CHECKER = join(dirname(fileURLToPath(import.meta.url)), "check-opencode-writer.mjs");
const SURFACES = {
  ".github/workflows/verify.yml": "run: go run ./cmd/pfm opencode build ..\n",
  ".github/workflows/plugin-scan.yml": "run: go run ./cmd/pfm opencode build ..\n",
  "scripts/check-agent-roster.mjs": "// go run ./cmd/pfm opencode build ..\n",
  "INSTALL.md": "Run `pfm opencode build .`\n",
  ".gitignore": "# regenerate with pfm opencode build\n",
};

function tree(files, { omit = [] } = {}) {
  const root = mkdtempSync(join(tmpdir(), "opencode-writer-"));
  for (const [path, body] of Object.entries({ ...SURFACES, ...files })) {
    if (omit.includes(path)) continue;
    mkdirSync(dirname(join(root, path)), { recursive: true });
    writeFileSync(join(root, path), body);
  }
  copyFileSync(CHECKER, join(root, "scripts", "check-opencode-writer.mjs"));
  return root;
}

function check(root) {
  const r = spawnSync(process.execPath, [join(root, "scripts", "check-opencode-writer.mjs")], {
    cwd: root,
    encoding: "utf8",
  });
  return { status: r.status, out: r.stdout + r.stderr };
}

test("a clean tree exits 0 and names five live surfaces", (t) => {
  const root = tree({ "docs/a.md": "ordinary note\n" });
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const r = check(root);
  assert.equal(r.status, 0, r.out);
  assert.match(r.out, /opencode-writer: clean — 5 live surfaces name native pfm opencode/);
});

test("a surface without its native command fails by name", (t) => {
  const root = tree({ "INSTALL.md": "Install from the current build.\n" });
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const r = check(root);
  assert.equal(r.status, 1, r.out);
  assert.match(r.out, /INSTALL\.md — missing native remediation: pfm opencode build \./);
});

test("a missing surface fails as unreadable, never clean", (t) => {
  const root = tree({}, { omit: ["INSTALL.md"] });
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const r = check(root);
  assert.equal(r.status, 1, r.out);
  assert.match(r.out, /INSTALL\.md — could not be read/);
  assert.doesNotMatch(r.out, /opencode-writer: clean/);
});
