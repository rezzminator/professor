// Self-test for scripts/release-check.mjs over throwaway git repositories:
// per rule a passing case and a case failing with its named `FAIL {rule}` line;
// per mode an exit-2 `ERROR` case for a missing input, proving 2 ≠ 1.
// Run: node --test scripts/release-check.test.mjs

import { after, describe, test } from "node:test";
import assert from "node:assert/strict";
import { execFile, spawn } from "node:child_process";
import { promisify } from "node:util";
import {
  cpSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  readdirSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { availableParallelism, tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const SCRIPT = resolve(dirname(fileURLToPath(import.meta.url)), "release-check.mjs");

// Hermetic git: no user or system config, fixed identity and dates, and no
// fence redirection (the fixtures are not the fenced worktree).
const ENV = {
  ...process.env,
  GIT_CONFIG_GLOBAL: "/dev/null",
  GIT_CONFIG_NOSYSTEM: "1",
  GIT_AUTHOR_NAME: "Fixture",
  GIT_AUTHOR_EMAIL: "fixture@example.invalid",
  GIT_COMMITTER_NAME: "Fixture",
  GIT_COMMITTER_EMAIL: "fixture@example.invalid",
  GIT_AUTHOR_DATE: "2026-01-01T00:00:00Z",
  GIT_COMMITTER_DATE: "2026-01-01T00:00:00Z",
};
for (const k of ["PFM_DEV_REPO_GIT_DIR", "PFM_DEV_REPO_WORK_TREE", "GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE"])
  delete ENV[k];

const made = [];
after(() => {
  for (const d of made) rmSync(d, { recursive: true, force: true });
});

const tmp = () => {
  const d = mkdtempSync(join(tmpdir(), "release-check-"));
  made.push(d);
  return d;
};
const execFileAsync = promisify(execFile);
const git = async (cwd, ...args) =>
  (await execFileAsync("git", args, { cwd, env: ENV, encoding: "utf8" })).stdout.trim();
const write = (root, files) => {
  for (const [p, content] of Object.entries(files)) {
    const f = join(root, p);
    if (content === null) rmSync(f, { force: true });
    else {
      mkdirSync(dirname(f), { recursive: true });
      writeFileSync(f, content);
    }
  }
};
let emptyRepo;
const repo = async () => {
  emptyRepo ??= (async () => {
    const d = tmp();
    await git(d, "init", "-q", "-b", "main");
    return d;
  })();
  const seed = await emptyRepo;
  const d = tmp();
  cpSync(join(seed, ".git"), join(d, ".git"), { recursive: true });
  return d;
};
const commit = async (root, files, msg = "change") => {
  write(root, files);
  await git(root, "add", "-A");
  await git(root, "commit", "-q", "--allow-empty", "-m", msg);
  return await git(root, "rev-parse", "HEAD");
};

const run = async (cwd, ...args) => new Promise((resolve, reject) => {
  const child = spawn(process.execPath, [SCRIPT, ...args], { cwd, env: ENV });
  let out = "";
  let err = "";
  child.stdout.setEncoding("utf8");
  child.stderr.setEncoding("utf8");
  child.stdout.on("data", (chunk) => { out += chunk; });
  child.stderr.on("data", (chunk) => { err += chunk; });
  child.on("error", reject);
  child.on("close", (code) => resolve({ code, out, err, all: `${out}${err}` }));
  child.stdin.end();
});
const assertClean = (r, mode) => {
  assert.equal(r.code, 0, r.all);
  assert.match(r.out, new RegExp(`release-check ${mode}: clean\\n$`), r.all);
  assert.doesNotMatch(r.all, /^(FAIL|ERROR) /m, r.all);
};
const assertFail = (r, rule) => {
  assert.equal(r.code, 1, r.all);
  assert.match(r.out, new RegExp(`^FAIL ${rule}: .+ — .+$`, "m"), `expected FAIL ${rule}:\n${r.all}`);
  assert.doesNotMatch(r.all, /: clean$|^ERROR /m, r.all);
};
const assertError = (r, pattern) => {
  assert.equal(r.code, 2, r.all);
  assert.match(r.err, /^ERROR /m, r.all);
  if (pattern) assert.match(r.err, pattern, r.all);
  assert.doesNotMatch(r.all, /^FAIL |: clean$/m, r.all);
};

// A file whose head version differs from its base in n separate -U0 hunks.
const hunky = (n, tag = "x") => {
  const lines = (edit) =>
    `${Array.from({ length: 2 * n }, (_, i) => (edit && i % 2 === 0 ? `${tag}${i}` : `l${i}`)).join("\n")}\n`;
  return [lines(false), lines(true)];
};

const NOTE = `# v0.78.0 — 2026-09-25

The release lead: what an adopter gets and what to do.

#### → Stop: read Breaking before updating

## Breaking

- Global: agents/old-agent.md — the old agent is gone
#### → For: every adopter · after update · shell — run \`pfm install --yes\`

## Added

- Project: agents/bar.md — a new bar agent
- Global: skills/qux — a new qux skill

## Verification

Reviewers: 4 dispatched, 4 returned.
The gate green on three lanes.
`;

// ---------------------------------------------------------------- scope

describe("scope", { concurrency: availableParallelism() }, () => {
  test("prints the range, tiers, areas, removals and removed roles", async () => {
    const r0 = await repo();
    const base = await commit(r0, {
      "templates/global/agents/old-agent.md": "a\n",
      "templates/project/commands/pfm/gone.md": "a\n",
      ".claude/commands/flat.md": "a\n",
      "templates/global/skills/dead/SKILL.md": "a\n",
      "templates/global/skills/dead/ref.md": "b\n",
      "templates/global/skills/alive/SKILL.md": "a\n",
      "templates/global/skills/alive/extra.md": "b\n",
      "docs/BLUEPRINT.md": "public blueprint\n",
      "docs/moved.md": "one\ntwo\nthree\nfour\n",
      "pfm/cmd/pfm/install_command.go": "package main\n",
      "pfm/cmd/pfm/chat_command.go": "package main\n",
      "README.md": "readme\n",
    });
    await git(r0, "checkout", "-q", "-b", "side");
    await commit(r0, { "side.txt": "side\n" }, "side");
    await git(r0, "checkout", "-q", "main");
    await commit(r0, {
      "templates/global/agents/old-agent.md": null,
      "templates/project/commands/pfm/gone.md": null,
      ".claude/commands/flat.md": null,
      "templates/global/skills/dead/SKILL.md": null,
      "templates/global/skills/dead/ref.md": null,
      "templates/global/skills/alive/extra.md": null,
      "docs/BLUEPRINT.md": null,
      "docs/moved.md": null,
      "docs/renamed.md": "one\ntwo\nthree\nfour\n",
      "pfm/cmd/pfm/install_command.go": "package main\n\nfunc x() {}\n",
      "pfm/cmd/pfm/chat_command.go": "package main\n\nfunc y() {}\n",
      "README.md": "readme v2\n",
      "scripts/new.sh": "echo new\n",
      "releases/v0.78.0.md": "note\n",
    });
    await git(r0, "merge", "-q", "--no-ff", "side", "-m", "merge side");
    const head = await git(r0, "rev-parse", "HEAD");
    const r = await run(r0, "scope", "--base", base, "--head", "HEAD");
    assertClean(r, "scope");
    const out = r.out;
    assert.match(out, new RegExp(`^RANGE ${base.slice(0, 7)}\\.\\.${head.slice(0, 7)} COMMITS 2 FILES 14 HUNKS \\d+$`, "m"));
    for (const line of [
      "FILE D 1 tier1 public docs/BLUEPRINT.md",
      "FILE R 0 tier2 - docs/renamed.md",
      "FILE M 1 tier1 pfm-update pfm/cmd/pfm/install_command.go",
      "FILE M 1 tier2 - pfm/cmd/pfm/chat_command.go",
      "FILE M 1 tier1 public README.md",
      "FILE A 1 tier2 - releases/v0.78.0.md",
      "FILE A 1 tier1 gates scripts/new.sh",
      "FILE D 1 tier1 templates templates/global/agents/old-agent.md",
      "FILE A 1 tier2 - side.txt",
      "AREA templates 5 5 templates/ pfm/harness-prompts/",
      "AREA gates 1 1 .github/ .githooks/ scripts/ infra/check-self-hosted-manifest.sh",
      "TIER2 4 5",
      "REMOVED docs/BLUEPRINT.md",
      "REMOVED docs/moved.md",
      "REMOVED templates/global/skills/alive/extra.md",
      "REMOVED-ROLE agent old-agent",
      "REMOVED-ROLE command /flat",
      "REMOVED-ROLE command /pfm:gone",
      "REMOVED-ROLE skill dead",
    ])
      assert.ok(out.split("\n").includes(line), `missing line ${JSON.stringify(line)}:\n${out}`);
    assert.match(out, /^AREA public 2 2 .*docs\/BLUEPRINT\.md/m);
    assert.match(out, /^AREA pfm-update 1 1 .*:\(glob\)pfm\/cmd\/pfm\/install\*/m);
    assert.doesNotMatch(out, /REMOVED-ROLE skill alive/, "a skill with files left is not removed");
  });

  test("splits an area over 600 hunks by segment, then by file", async () => {
    const r0 = await repo();
    const [a0, a1] = hunky(400);
    const [b0, b1] = hunky(300);
    const [p0, p1] = hunky(200);
    const [w0, w1] = hunky(50);
    const base = await commit(r0, {
      "templates/global/a.md": a0,
      "templates/global/b.md": b0,
      "templates/project/p.md": p0,
      "pfm/harness-prompts/w/w.md": w0,
    });
    await commit(r0, {
      "templates/global/a.md": a1,
      "templates/global/b.md": b1,
      "templates/project/p.md": p1,
      "pfm/harness-prompts/w/w.md": w1,
    });
    const r = await run(r0, "scope", "--base", base, "--head", "HEAD");
    assertClean(r, "scope");
    const areas = r.out.split("\n").filter((l) => l.startsWith("AREA "));
    assert.deepEqual(areas, [
      "AREA templates-1 50 1 pfm/harness-prompts/w/",
      "AREA templates-2 400 1 templates/global/a.md",
      "AREA templates-3 300 1 templates/global/b.md",
      "AREA templates-4 200 1 templates/project/",
    ]);
    assert.ok(r.out.includes("FILE M 400 tier1 templates-2 templates/global/a.md\n"), r.out);
    assert.ok(r.out.includes("FILE M 50 tier1 templates-1 pfm/harness-prompts/w/w.md\n"), r.out);
  });

  test("splits pfm-update by package, its table's next path segment", async () => {
    const r0 = await repo();
    const [a0, a1] = hunky(400);
    const [b0, b1] = hunky(100);
    const [u0, u1] = hunky(300);
    const base = await commit(r0, {
      "pfm/internal/doctor/a.go": a0,
      "pfm/internal/doctor/b.go": b0,
      "pfm/internal/update/u.go": u0,
    });
    await commit(r0, {
      "pfm/internal/doctor/a.go": a1,
      "pfm/internal/doctor/b.go": b1,
      "pfm/internal/update/u.go": u1,
    });
    const r = await run(r0, "scope", "--base", base, "--head", "HEAD");
    assertClean(r, "scope");
    assert.deepEqual(
      r.out.split("\n").filter((l) => l.startsWith("AREA ")),
      ["AREA pfm-update-1 500 2 pfm/internal/doctor/", "AREA pfm-update-2 300 1 pfm/internal/update/"],
    );
  });

  test("a rename out of tier 1 keeps its old path's tier and area", async () => {
    const r0 = await repo();
    const base = await commit(r0, { "templates/global/agents/gone.md": "gone\nagent\nbody\n" });
    await commit(r0, { "templates/global/agents/gone.md": null, "docs/old/gone.md": "gone\nagent\nbody\n" });
    const r = await run(r0, "scope", "--base", base, "--head", "HEAD");
    assertClean(r, "scope");
    for (const line of [
      "FILE R 0 tier1 templates docs/old/gone.md",
      "AREA templates 0 1 templates/ pfm/harness-prompts/ templates/global/agents/gone.md docs/old/gone.md",
      "TIER2 0 0",
      "REMOVED templates/global/agents/gone.md",
    ])
      assert.ok(r.out.split("\n").includes(line), `missing line ${JSON.stringify(line)}:\n${r.out}`);
  });

  test("a role another root still holds at head is not removed", async () => {
    const r0 = await repo();
    const base = await commit(r0, {
      ".claude/agents/x.md": "local x\n",
      "templates/global/agents/x.md": "global x\n",
      ".claude/commands/pfm.md": "the pfm command\n",
      "templates/project/agents/gone.md": "gone\n",
    });
    await commit(r0, {
      ".claude/agents/x.md": null,
      ".claude/commands/pfm.md": null,
      ".claude/commands/other.md": "another command\n",
      "templates/global/commands/pfm.md": "the global pfm command, rewritten\n",
      "templates/project/agents/gone.md": null,
    });
    const r = await run(r0, "scope", "--base", base, "--head", "HEAD");
    assertClean(r, "scope");
    assert.deepEqual(r.out.split("\n").filter((l) => l.startsWith("REMOVED-ROLE ")), ["REMOVED-ROLE agent gone"], r.out);
  });

  test("the mcp core and mcp_serve command are pfm-update tier 1", async () => {
    const r0 = await repo();
    const base = await commit(r0, {
      "pfm/internal/mcpserv/proxy.go": "package mcpserv\n",
      "pfm/cmd/pfm/mcp_serve_command.go": "package main\n",
      "pfm/cmd/pfm/mcp_serve_test.go": "package main\n",
      "pfm/cmd/pfm/main.go": "package main\n",
    });
    await commit(r0, {
      "pfm/internal/mcpserv/proxy.go": "package mcpserv\n\nfunc x() {}\n",
      "pfm/cmd/pfm/mcp_serve_command.go": "package main\n\nfunc x() {}\n",
      "pfm/cmd/pfm/mcp_serve_test.go": "package main\n\nfunc y() {}\n",
      "pfm/cmd/pfm/main.go": "package main\n\nfunc z() {}\n",
    });
    const r = await run(r0, "scope", "--base", base, "--head", "HEAD");
    assertClean(r, "scope");
    for (const line of [
      "FILE M 1 tier1 pfm-update pfm/internal/mcpserv/proxy.go",
      "FILE M 1 tier1 pfm-update pfm/cmd/pfm/mcp_serve_command.go",
      "FILE M 1 tier1 pfm-update pfm/cmd/pfm/mcp_serve_test.go",
      "FILE M 1 tier2 - pfm/cmd/pfm/main.go",
    ])
      assert.ok(r.out.split("\n").includes(line), `missing line ${JSON.stringify(line)}:\n${r.out}`);
    assert.match(r.out, /^AREA pfm-update .*pfm\/internal\/mcpserv\/.*:\(glob\)pfm\/cmd\/pfm\/mcp_serve\*/m);
  });

  test("every pfm/internal package install and update run through is pfm-update tier 1 or named generic, and the doc table lists the same set", async () => {
    const root = resolve(dirname(SCRIPT), "..");
    // The packages install, uninstall and update import directly (non-test sources). A new import has
    // to be classified here: tier 1 when it carries install or update behavior, generic otherwise.
    const imported = new Set();
    for (const pkg of ["installer", "update"]) {
      const dir = join(root, "pfm/internal", pkg);
      const sources = readdirSync(dir).filter((n) => n.endsWith(".go") && !n.endsWith("_test.go"));
      assert.ok(sources.length > 0, `no Go source read under ${dir}`);
      for (const n of sources)
        for (const m of readFileSync(join(dir, n), "utf8").matchAll(/"[^"\n]*\/pfm\/internal\/([^"]+)"/g))
          imported.add(m[1]);
    }
    assert.ok(imported.has("enginesync") && imported.has("globallink"), `import scan lost its known imports: ${[...imported]}`);
    const generic = new Set([
      "action", "atomicfile", "clock", "cli", "deps", "engine", "fleetdb", "gather", "obs", "paths",
      "pricing", "pricing/modelcost", "reload", "restart", "schedule", "semver",
    ]);
    const script = readFileSync(SCRIPT, "utf8");
    const listed = (text) => new Set(text.split(",").map((n) => n.replace(/["\s]/g, "")).filter(Boolean));
    const tier1 = listed(script.match(/dir: "pfm\/internal\/",\s*subdirs: \[([^\]]*)\]/)?.[1] ?? "");
    assert.ok(tier1.size > 0, "no pfm/internal subdirs found in the TIER1 table");
    const unruled = [...imported].filter((n) => !tier1.has(n) && !generic.has(n));
    assert.deepEqual(unruled, [], `imported by installer or update, in neither tier 1 nor the generic list: ${unruled}`);
    const doc = readFileSync(join(root, "docs/design/release/release-check.md"), "utf8");
    const docRow = doc.match(/`pfm-update` \| `pfm\/internal\/\{([^}]*)\}\/`/)?.[1] ?? "";
    assert.deepEqual([...listed(docRow)].sort(), [...tier1].sort(), "the doc's pfm-update row differs from the script's TIER1 table");
    const r0 = await repo();
    const files = (body) => Object.fromEntries(["enginesync", "globallink", "restart"].map((n) => [`pfm/internal/${n}/x.go`, body]));
    const base = await commit(r0, files("package x\n"));
    await commit(r0, files("package x\n\nfunc y() {}\n"));
    const r = await run(r0, "scope", "--base", base, "--head", "HEAD");
    assertClean(r, "scope");
    for (const n of ["enginesync", "globallink"])
      assert.ok(r.out.split("\n").includes(`FILE M 1 tier1 pfm-update pfm/internal/${n}/x.go`), r.out);
    assert.ok(r.out.split("\n").includes("FILE M 1 tier2 - pfm/internal/restart/x.go"), r.out);
  });

  test("ERROR, not FAIL, when --base is not an ancestor of --head", async () => {
    const r0 = await repo();
    const base = await commit(r0, { "a.txt": "a\n" });
    await commit(r0, { "templates/project/agents/bar.md": "bar\n" });
    assertError(await run(r0, "scope", "--base", "HEAD", "--head", base), /--base HEAD is not an ancestor of --head [0-9a-f]{40}/);
  });

  test("ERROR, not FAIL, when a ref does not resolve", async () => {
    const r0 = await repo();
    await commit(r0, { "a.txt": "a\n" });
    assertError(await run(r0, "scope", "--base", "no-such-ref", "--head", "HEAD"), /git rev-parse --verify no-such-ref/);
    assertError(await run(r0, "scope", "--base", "HEAD"), /usage: scope needs --base and --head/);
  });
});

// ---------------------------------------------------------------- notes: grammar

describe("notes grammar N1–N8", { concurrency: availableParallelism() }, () => {
  const check = async (text) => {
    const d = tmp();
    write(d, { "v0.78.0.md": text });
    return await run(d, "notes", "v0.78.0.md");
  };
  const mutate = (from, to) => {
    assert.ok(NOTE.includes(from), `fixture lacks ${from}`);
    return NOTE.replace(from, to);
  };

  test("a note in the grammar is clean", async () => assertClean(await check(NOTE), "notes"));
  test("N1 title", async () => assertFail(await check(mutate("# v0.78.0 — 2026-09-25", "# 0.78.0 - 2026-09-25")), "N1"));
  test("N2 no lead", async () => assertFail(await check(mutate("The release lead: what an adopter gets and what to do.\n", "")), "N2"));
  test("N3 Stop without a reason", async () => assertFail(await check(mutate("#### → Stop: read Breaking before updating", "#### → Stop:")), "N3"));
  test("N3 Stop after the first ##", async () =>
    assertFail(await check(mutate("## Added\n", "## Added\n\n#### → Stop: late\n")), "N3"));
  test("N3 a lead line follows a Stop line, still before the first ##", async () => {
    const r = await check(mutate("## Breaking\n", "Another lead line.\n\n## Breaking\n"));
    assertFail(r, "N3");
    assert.match(r.out, /^FAIL N3: v0\.78\.0\.md:\d+ — a lead line follows a `#### → Stop:` line; Stop lines sit between the lead and the first `##`$/m, r.out);
  });
  test("N4 unknown section", async () => assertFail(await check(mutate("## Added", "## Misc")), "N4"));
  test("N4 order", async () => assertFail(await check(mutate("## Breaking\n", "## Fixed\n\n- Repo: x — y\n\n## Breaking\n")), "N4"));
  test("N4 empty section", async () => assertFail(await check(mutate("## Verification\n", "## Removed\n\n## Verification\n")), "N4"));
  test("N5 bad label", async () => assertFail(await check(mutate("- Project: agents/bar.md", "- Other: agents/bar.md")), "N5"));
  test("N5 prose under a category", async () => assertFail(await check(mutate("## Added\n\n", "## Added\n\nsome prose\n")), "N5"));
  test("N5 action line before any bullet of its section", async () => {
    const r = await check(mutate(
      "## Added\n\n- Project: agents/bar.md — a new bar agent\n",
      "## Added\n\n#### → For: every adopter · after update · shell — run `pfm install --yes`\n\n- Project: agents/bar.md — a new bar agent\n",
    ));
    assertFail(r, "N5");
    assert.match(r.out, /^FAIL N5: v0\.78\.0\.md:\d+ — action line before any bullet of its section$/m, r.out);
  });
  test("N6 bad timing", async () => assertFail(await check(mutate("· after update ·", "· someday ·")), "N6"));
  test("N6 action line missing a part other than timing", async () => {
    const r = await check(mutate("· shell — run", "·  — run"));
    assertFail(r, "N6");
    assert.match(r.out, /^FAIL N6: v0\.78\.0\.md:\d+ — action line does not read `#### → For: \{audience\} · \{before update\|after update\|per project\} · \{surface\} — \{action\}` with every part non-empty$/m, r.out);
  });
  test("N7 bullet under Verification", async () => assertFail(await check(`${NOTE}- a bullet\n`), "N7"));
  test("N7 action line under Verification", async () => {
    const r = await check(`${NOTE}#### → For: every adopter · after update · shell — verify\n`);
    assertFail(r, "N7");
    assert.match(r.out, /^FAIL N7: v0\.78\.0\.md:\d+ — `## Verification` holds an action line$/m, r.out);
  });
  test("N8 other heading level", async () => assertFail(await check(mutate("## Added\n\n", "## Added\n\n### Details\n")), "N8"));
  test("N8 a heading without the space after its hashes", async () => {
    const r = await check(mutate("## Added", "##Added"));
    assertFail(r, "N8");
    assert.match(r.out, /^FAIL N8: v0\.78\.0\.md:12 — a line opens with `#` but lacks the space after its hashes: ##Added$/m, r.out);
  });
  test("N8 a code fence that never closes", async () => {
    const r = await check(mutate("what to do.\n", "what to do.\n\n```sh\nopened in the lead, never closed\n"));
    assertFail(r, "N8");
    assert.match(r.out, /^FAIL N8: v0\.78\.0\.md:5 — code fence never closes$/m, r.out);
  });
  test("ERROR, not FAIL, when the note does not exist", async () =>
    assertError(await run(tmp(), "notes", "missing.md"), /note missing\.md does not exist/));
});

describe("notes --all", { concurrency: availableParallelism() }, () => {
  test("checks only notes at or above v0.78.0", async () => {
    const d = tmp();
    write(d, { "rel/v0.77.2.md": "# old grammar\n", "rel/v0.78.0.md": NOTE, "rel/other.md": "x\n" });
    const r = await run(d, "notes", "--all", "rel");
    assertClean(r, "notes");
    assert.match(r.out, /^CHECKED 1 notes at or above v0\.78\.0 in rel$/m);
    write(d, { "rel/v0.78.1.md": "# not a title\n" });
    assertFail(await run(d, "notes", "--all", "rel"), "N1");
  });
  test("binds each note's title to the version its file name carries", async () => {
    const d = tmp();
    write(d, { "rel/v0.79.0.md": NOTE });
    const r = await run(d, "notes", "--all", "rel");
    assertFail(r, "N1");
    assert.match(r.out, /^FAIL N1: rel\/v0\.79\.0\.md:1 — title version v0\.78\.0 differs from its file name v0\.79\.0\.md$/m, r.out);
  });
  test("a v*.md file name that is not v{X.Y.Z}.md", async () => {
    const d = tmp();
    write(d, { "rel/v1.md": "# whatever\n" });
    const r = await run(d, "notes", "--all", "rel");
    assertFail(r, "N1");
    assert.match(r.out, /^FAIL N1: rel\/v1\.md — file name is not v\{X\.Y\.Z\}\.md, so its grammar version cannot be placed$/m, r.out);
  });
  test("ERROR, not FAIL, when the directory does not exist", async () =>
    assertError(await run(tmp(), "notes", "--all", "nope"), /notes directory nope does not exist/));
});

// ---------------------------------------------------------------- notes: coverage

describe("notes coverage C1–C5", { concurrency: availableParallelism() }, () => {
  const fixture = async () => {
    const r0 = await repo();
    const base = await commit(r0, {
      "README.md": "r\n",
      "templates/global/agents/old-agent.md": "a\n",
      "templates/project/agents/keep.md": "k\n",
      ".claude/agents/twin.md": "local twin\n",
      "templates/global/agents/twin.md": "global twin\n",
    });
    const c1 = await commit(r0, { "templates/project/agents/bar.md": "bar\n" }, "add bar");
    const c2 = await commit(r0, { "templates/global/skills/qux/SKILL.md": "qux\n" }, "add qux");
    const c3 = await commit(r0, { "scripts/x.test.mjs": "t\n" }, "test only");
    const c4 = await commit(r0, { "templates/global/agents/old-agent.md": null }, "drop old-agent");
    const d = tmp();
    const rows = {
      c1: `${c1.slice(0, 7)}\tAdded\tProject: agents/bar.md`,
      c2: `${c2.slice(0, 7)}\tAdded\tGlobal: skills/qux`,
      c3: `${c3.slice(0, 7)}\tnone\ttest-only change`,
      c4: `${c4.slice(0, 7)}\tBreaking\tGlobal: agents/old-agent.md`,
    };
    // committed: the note is committed as the release's own commit (a
    // release-only commit, so it needs no row).
    const check = async ({ note = NOTE, rowsOver = {}, extra = [], committed = false } = {}) => {
      const all = { ...rows, ...rowsOver };
      write(d, { "coverage.tsv": `${Object.values(all).filter((x) => x !== null).join("\n")}\n` });
      let notePath = join(d, "note.md");
      if (committed) {
        await commit(r0, { "releases/v0.78.0.md": note }, "the note");
        notePath = "releases/v0.78.0.md";
      } else write(d, { "note.md": note });
      return await run(r0, "notes", notePath, "--base", base, "--head", "HEAD", "--coverage", join(d, "coverage.tsv"), ...extra);
    };
    return { r0, d, c1, c3, check };
  };

  test("a ledger naming every commit once is clean", async () => {
    assertClean(await (await fixture()).check({ committed: true }), "notes");
  });
  test("C1 missing commit", async () => assertFail(await (await fixture()).check({ rowsOver: { c3: null } }), "C1"));
  test("C1 commit twice", async () => {
    const f = await fixture();
    assertFail(await f.check({ rowsOver: { dup: `${f.c3.slice(0, 7)}\tnone\tagain` } }), "C1");
  });
  test("C1 a row names a commit outside the range", async () => {
    const f = await fixture();
    const r = await f.check({ rowsOver: { bogus: "1234567\tnone\toutside the range" } });
    assertFail(r, "C1");
    assert.match(r.out, /^FAIL C1: .* — 1234567 is not a non-merge commit of [0-9a-f]{7}\.\.[0-9a-f]{7}$/m, r.out);
  });
  test("C1 a malformed ledger row", async () => {
    const f = await fixture();
    const r = await f.check({ rowsOver: { bad: "not-a-sha" } });
    assertFail(r, "C1");
    assert.match(r.out, /^FAIL C1: .* — row is not `\{sha7\}/m, r.out);
  });
  test("C2 row names no bullet", async () => {
    const f = await fixture();
    assertFail(await f.check({ rowsOver: { c1: `${f.c1.slice(0, 7)}\tAdded\tProject: agents/nope.md` } }), "C2");
  });
  test("C3 none without a reason", async () => {
    const f = await fixture();
    assertFail(await f.check({ rowsOver: { c3: `${f.c3.slice(0, 7)}\tnone\t` } }), "C3");
  });
  test("C4 changed templates/project path named nowhere", async () => {
    const f = await fixture();
    const note = NOTE.replace("- Project: agents/bar.md — a new bar agent", "- Project: the bar agent — a new bar agent");
    assertFail(await f.check({ note, rowsOver: { c1: `${f.c1.slice(0, 7)}\tAdded\tProject: the bar agent` } }), "C4");
  });
  test("C5 added skill named nowhere", async () => {
    const f = await fixture();
    const note = NOTE.replace("- Global: skills/qux — a new qux skill", "- Global: a new skill — added");
    const r = await f.check({ note, rowsOver: { c2: null, c2b: `${(await git(f.r0, "rev-parse", "HEAD~2")).slice(0, 7)}\tAdded\tGlobal: a new skill` } });
    assertFail(r, "C5");
    assert.match(r.out, /^FAIL C5: .* — added skill qux appears nowhere in the note$/m);
  });
  test("C5 removed role named nowhere", async () => {
    const r0 = await repo();
    const base = await commit(r0, {
      "README.md": "r\n",
      "templates/global/agents/mover.md": "an agent, never mentioned\n",
    });
    const c1 = await commit(r0, { "templates/global/agents/mover.md": null }, "drop mover");
    const d = tmp();
    write(d, { "coverage.tsv": `${c1.slice(0, 7)}\tnone\tinternal cleanup\n`, "note.md": NOTE });
    const r = await run(r0, "notes", join(d, "note.md"), "--base", base, "--head", "HEAD", "--coverage", join(d, "coverage.tsv"));
    assertFail(r, "C5");
    assert.match(r.out, /^FAIL C5: .* — removed agent mover appears nowhere in the note$/m, r.out);
  });
  test("C4 matches a whole path, never inside a longer one", async () => {
    const f = await fixture();
    const named = (scope) => ({
      note: NOTE.replace("- Project: agents/bar.md — a new bar agent", `- Project: ${scope} — a new bar agent`),
      rowsOver: { c1: `${f.c1.slice(0, 7)}\tAdded\tProject: ${scope}` },
    });
    const r = await f.check(named("agents/bar.md.bak"));
    assertFail(r, "C4");
    assert.match(r.out, /^FAIL C4: .* — templates\/project\/agents\/bar\.md changed in the range; `agents\/bar\.md` appears in no bullet or action line$/m, r.out);
    assertClean(await f.check(named("templates/project/agents/bar.md")), "notes");
  });
  test("C5 does not demand a role another root still holds", async () => {
    const f = await fixture();
    const c5 = await commit(f.r0, { ".claude/agents/twin.md": null }, "drop the local twin");
    assertClean(await f.check({ rowsOver: { c5: `${c5.slice(0, 7)}\tnone\tthe local copy only` } }), "notes");
  });
  test("ERROR, not FAIL, when --base is not an ancestor of --head", async () => {
    const f = await fixture();
    write(f.d, { "note.md": NOTE, "empty.tsv": "" });
    assertError(
      await run(f.r0, "notes", join(f.d, "note.md"), "--base", "HEAD", "--head", "HEAD~4", "--coverage", join(f.d, "empty.tsv")),
      /--base HEAD is not an ancestor of --head HEAD~4/,
    );
  });
  test("ERROR, not FAIL, when the coverage file does not exist", async () => {
    const { r0 } = await fixture();
    const d = tmp();
    write(d, { "note.md": NOTE });
    assertError(
      await run(r0, "notes", join(d, "note.md"), "--base", "HEAD~4", "--head", "HEAD", "--coverage", join(d, "absent.tsv")),
      /coverage file .*absent\.tsv does not exist/,
    );
  });
  test("C1 exempts a commit touching only the release files", async () => {
    const f = await fixture();
    await commit(f.r0, { "releases/v1.0.0.md": "note\n", "CHANGELOG.md": "changelog\n", VERSION: "1.0.0\n" }, "release v1.0.0");
    assertClean(await f.check(), "notes");
  });
  test("C1 still requires a row for a commit touching a release file plus another path", async () => {
    const f = await fixture();
    await commit(f.r0, { "CHANGELOG.md": "changelog\n", "README.md": "updated readme\n" }, "changelog and readme");
    assertFail(await f.check(), "C1");
  });
});

// ---------------------------------------------------------------- notes: version and commands

describe("notes version V1–V3 and commands P1", { concurrency: availableParallelism() }, () => {
  const fixture = (over = {}) => {
    const d = tmp();
    write(d, {
      "note.md": NOTE,
      VERSION: "0.78.0\n",
      ".professor/VERSION": "0.78.0\n",
      ".professor/manifest.json": JSON.stringify({ installed_from: { version: "0.78.0" } }),
      "CHANGELOG.md": "# Changelog\n\n## Releases\n\n- [v0.78.0](releases/v0.78.0.md) — the summary\n- [v0.77.2](releases/v0.77.2.md) — older\n",
      "help.txt": "usage: pfm <command>\n\ncommands:\n  install   wire the host\n  update    update the binary\n",
      ...over,
    });
    return d;
  };
  const version = async (d, v = "0.78.0", bump = "minor") =>
    await run(d, "notes", "note.md", "--version", v, "--previous", "0.77.2", "--bump", bump);

  test("stamps, index line and bump in agreement are clean", async () => assertClean(await version(await fixture()), "notes"));
  test("V1 a stamp differs", async () => assertFail(await version(await fixture({ ".professor/VERSION": "0.77.2\n" })), "V1"));
  test("V1 the manifest stamp differs", async () => {
    const r = await version(await fixture({ ".professor/manifest.json": JSON.stringify({ installed_from: { version: "0.77.2" } }) }));
    assertFail(r, "V1");
    assert.match(r.out, /^FAIL V1: \.professor\/manifest\.json — "0\.77\.2" differs from --version 0\.78\.0$/m, r.out);
  });
  test("V2 first changelog entry", async () =>
    assertFail(await version(await fixture({ "CHANGELOG.md": "# Changelog\n\n## Releases\n\n- [v0.77.2](releases/v0.77.2.md) — older\n" })), "V2"));
  test("V2 the Releases heading holds no entry", async () => {
    const r = await version(await fixture({ "CHANGELOG.md": "# Changelog\n\n## Releases\n\n## Old\n" }));
    assertFail(r, "V2");
    assert.match(r.out, /^FAIL V2: CHANGELOG\.md:3 — `## Releases` holds no entry$/m, r.out);
  });
  test("V3 bump below the floor", async () => assertFail(await version(await fixture(), "0.77.3", "patch"), "V3"));
  test("V3 bump arithmetic differs from --version, floor satisfied", async () => {
    const r = await version(await fixture(), "0.78.0", "major");
    assertFail(r, "V3");
    assert.match(r.out, /^FAIL V3: --version — 0\.78\.0 is not 0\.77\.2 bumped by major \(1\.0\.0\)$/m, r.out);
    assert.doesNotMatch(r.out, /below the floor/, r.out);
  });
  test("ERROR, not FAIL, when VERSION does not exist", async () =>
    assertError(await version(await fixture({ VERSION: null })), /version stamp .*VERSION does not exist/));
  test("ERROR, not FAIL, on a partial argument group", async () => {
    assertError(await run(await fixture(), "notes", "note.md", "--version", "0.78.0", "--previous", "0.77.2"), /go together; missing --bump/);
    assertError(await run(await fixture(), "notes", "note.md", "--previous", "0.77.2", "--bump", "minor"), /go together; missing --version/);
  });
  test("--version alone binds N1 only", async () => {
    const d = await fixture({ VERSION: "0.1.0\n" });
    assertClean(await run(d, "notes", "note.md", "--version", "v0.78.0"), "notes");
    const r = await run(d, "notes", "note.md", "--version", "0.79.0");
    assertFail(r, "N1");
    assert.match(r.out, /^FAIL N1: note\.md:1 — title version v0\.78\.0 differs from --version 0\.79\.0$/m, r.out);
    assert.doesNotMatch(r.out, /^FAIL V/m, r.out);
  });

  test("P1 commands in the help list are clean", async () =>
    assertClean(await run(await fixture(), "notes", "note.md", "--pfm-help", "help.txt"), "notes"));
  test("P1 unknown pfm command", async () => {
    const d = await fixture({ "note.md": NOTE.replace("run `pfm install --yes`", "run `pfm frobnicate --now`") });
    assertFail(await run(d, "notes", "note.md", "--pfm-help", "help.txt"), "P1");
  });
  test("ERROR, not FAIL, when the help file does not exist", async () =>
    assertError(await run(await fixture(), "notes", "note.md", "--pfm-help", "absent.txt"), /pfm help file absent\.txt does not exist/));
  test("ERROR, not FAIL, when the help file lists no command", async () => {
    const d = await fixture({ "empty-help.txt": "usage: pfm <command>\n\nno commands here\n" });
    assertError(await run(d, "notes", "note.md", "--pfm-help", "empty-help.txt"), /pfm help file empty-help\.txt lists no command/);
  });

  test("P1 accepts a help line with one space after a padded name", async () => {
    const d = await fixture({
      "help2.txt": "usage: pfm <command>\n\ncommands:\n  uninstall remove the self-contained host integration\n",
      "note.md": NOTE.replace("run `pfm install --yes`", "run `pfm uninstall --yes`"),
    });
    assertClean(await run(d, "notes", "note.md", "--pfm-help", "help2.txt"), "notes");
  });

  test("--version and --previous each accept X.Y.Z or vX.Y.Z", async () => {
    const d = await fixture();
    assertClean(await run(d, "notes", "note.md", "--version", "0.78.0", "--previous", "v0.77.2", "--bump", "minor"), "notes");
    assertClean(await run(d, "notes", "note.md", "--version", "v0.78.0", "--previous", "0.77.2", "--bump", "minor"), "notes");
  });

  test("usage names the optional v prefix for --version and --previous", async () => {
    const r = await run(tmp(), "notes", "note.md", "--previous", "0.77.2");
    assert.match(r.err, /\[--version \[v\]X\.Y\.Z \[--previous \[v\]X\.Y\.Z --bump patch\|minor\|major\]\]/);
  });
});

// ---------------------------------------------------------------- notes: every group

describe("notes, every rule group", () => {
  test("a release commit whose note and stamps pass every group is clean", async () => {
    const r0 = await repo();
    const base = await commit(r0, { "README.md": "r\n" });
    await commit(r0, {
      "releases/v0.78.0.md": NOTE,
      VERSION: "0.78.0\n",
      ".professor/VERSION": "0.78.0\n",
      ".professor/manifest.json": JSON.stringify({ installed_from: { version: "0.78.0" } }),
      "CHANGELOG.md": "# Changelog\n\n## Releases\n\n- [v0.78.0](releases/v0.78.0.md) — the summary\n",
    }, "release v0.78.0");
    const d = tmp();
    write(d, { "empty.tsv": "", "help.txt": "commands:\n  install   wire the host\n" });
    assertClean(await run(
      r0, "notes", "releases/v0.78.0.md",
      "--base", base, "--head", "HEAD", "--coverage", join(d, "empty.tsv"),
      "--version", "0.78.0", "--previous", "0.77.2", "--bump", "minor", "--pfm-help", join(d, "help.txt"),
    ), "notes");
  });
  test("ready and --record are gone: usage errors", async () => {
    const r0 = await repo();
    assertError(await run(r0, "ready", "dir", "--worktree", r0), /unknown mode "ready"/);
    assertError(await run(r0, "notes", "note.md", "--record", "x.txt"), /has no flag --record/);
  });
});

// ---------------------------------------------------------------- preflight

describe("preflight S1, D1, T1", { concurrency: availableParallelism() }, () => {
  // A repository whose base holds a listed plugin submodule, a README naming
  // shipped commands, and a script and an agent the head commit removes.
  const fixture = async ({ tagged = true, url } = {}) => {
    const r0 = await repo();
    const plugin = join(r0, "plugins/p");
    mkdirSync(plugin, { recursive: true });
    await git(plugin, "init", "-q", "-b", "main");
    await commit(plugin, { "README.md": "p\n" }, "plugin");
    if (tagged) await git(plugin, "tag", "-a", "v1.0.0", "-m", "v1.0.0");
    const market = {
      name: "professor",
      plugins: [{ name: "p", source: { source: "git-subdir", url: url ?? plugin, path: "./plugins/p", ref: "main" } }],
    };
    const base = await commit(r0, {
      "README.md": "Run `/quality:prompt check`, `/handy` or `/reload --account 2`; see `/dev/null`.\n",
      ".claude-plugin/marketplace.json": JSON.stringify(market, null, 2),
      "templates/global/commands/quality/prompt.md": "q\n",
      "templates/global/skills/handy/SKILL.md": "h\n",
      "pfm/internal/installer/assets/reload.command.md": "r\n",
      "templates/global/agents/old-agent.md": "a\n",
      "scripts/old.sh": "echo old\n",
    });
    // The removal, and every place a removed name may stand: history and the
    // retired-name registry; plus a longer word that only starts with one.
    await commit(r0, {
      "scripts/old.sh": null,
      "templates/global/agents/old-agent.md": null,
      "releases/v0.1.0.md": "scripts/old.sh and old-agent are gone\n",
      "CHANGELOG.md": "- dropped scripts/old.sh\n",
      ".professor/RR/notes.md": "old-agent\n",
      "pfm/internal/update/retired_names.go": "// old-agent retired\n",
      "docs/live.md": "the old-agentx tool, the old-agent-sonnet role, scripts/old.sh.bak and scripts/old.shell\n",
    }, "remove old.sh and old-agent");
    const check = async (files) => {
      if (files) await commit(r0, files, "more");
      return await run(r0, "preflight", "--base", base, "--head", "HEAD");
    };
    return { r0, base, plugin, check };
  };

  test("a range whose removals no live file names, a README naming shipped commands and tagged plugins is clean", async () => {
    const r = await (await fixture()).check();
    assertClean(r, "preflight");
    for (const line of [
      "SEAMS 2 removed paths, 1 removed roles swept",
      "README 3 commands checked",
      "PLUGINS 1 checked, 0 unlistable",
    ])
      assert.ok(r.out.split("\n").includes(line), `missing line ${JSON.stringify(line)}:\n${r.out}`);
  });
  test("S1 a live file still names a removed path", async () => {
    const r = await (await fixture()).check({ "docs/run.md": "intro\nrun scripts/old.sh first\n" });
    assertFail(r, "S1");
    assert.match(r.out, /^FAIL S1: docs\/run\.md:2 — names the removed path scripts\/old\.sh$/m, r.out);
  });
  test("S1 a design doc's history section is no exemption", async () => {
    const r = await (await fixture()).check({ "docs/design/x/x.md": "## History\n\nold-agent was retired\n" });
    assertFail(r, "S1");
    assert.match(r.out, /^FAIL S1: docs\/design\/x\/x\.md:3 — names the removed agent old-agent$/m, r.out);
  });
  test("D1 the README names a command no tree ships", async () => {
    const r = await (await fixture()).check({ "README.md": "Run `/quality:prompt`.\n\nThen `/quality:gone fix`.\n" });
    assertFail(r, "D1");
    assert.match(r.out, /^FAIL D1: README\.md:3 — `\/quality:gone` names no command or skill this tree ships$/m, r.out);
  });
  test("T1 a listed plugin's gitlink is no published tag", async () => {
    const r = await (await fixture({ tagged: false })).check();
    assertFail(r, "T1");
    assert.match(r.out, /^FAIL T1: plugins\/p — plugin p's gitlink [0-9a-f]{7} is no published tag of /m, r.out);
  });
  test("T1 a listed plugin with no gitlink at head", async () => {
    const f = await fixture();
    const market = { plugins: [{ name: "q", source: { url: f.plugin, path: "plugins/q" } }] };
    const r = await f.check({ ".claude-plugin/marketplace.json": JSON.stringify(market) });
    assertFail(r, "T1");
    assert.match(r.out, /^FAIL T1: plugins\/q — plugin q has no submodule gitlink at /m, r.out);
  });
  test("ERROR, not FAIL, when a plugin's tags cannot be listed", async () => {
    const f = await fixture({ url: "/nonexistent/plugin-repo" });
    assertError(await f.check(), /ls-remote/);
  });
  test("an unlistable plugin's ERROR still names every other plugin's and S1's result", async () => {
    const f = await fixture();
    const market = {
      plugins: [
        { name: "gone", source: { url: "/nonexistent/plugin-repo", path: "plugins/p" } },
        { name: "q", source: { url: f.plugin, path: "plugins/q" } },
      ],
    };
    const r = await f.check({ ".claude-plugin/marketplace.json": JSON.stringify(market), "docs/run.md": "run scripts/old.sh\n" });
    assertError(r, /ls-remote/);
    assert.match(r.err, /plugin q has no submodule gitlink/, r.err);
    assert.match(r.err, /FAIL S1: docs\/run\.md:1 — names the removed path scripts\/old\.sh/, r.err);
  });
  test("ERROR, not FAIL, when the marketplace is absent at head", async () => {
    const f = await fixture();
    assertError(await f.check({ ".claude-plugin/marketplace.json": null }), /marketplace\.json/);
  });
  test("ERROR, not FAIL, when --base is not an ancestor of --head", async () => {
    const f = await fixture();
    assertError(await run(f.r0, "preflight", "--base", "HEAD", "--head", f.base), /is not an ancestor/);
  });
});
