// Self-test for scripts/release-check.mjs over throwaway git repositories:
// per rule a passing case and a case failing with its named `FAIL {rule}` line;
// per mode an exit-2 `ERROR` case for a missing input, proving 2 ≠ 1.
// Run: node --test scripts/release-check.test.mjs

import { after, describe, test } from "node:test";
import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import {
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
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
const git = (cwd, ...args) =>
  execFileSync("git", args, { cwd, env: ENV, encoding: "utf8" }).trim();
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
const repo = () => {
  const d = tmp();
  git(d, "init", "-q", "-b", "main");
  return d;
};
const commit = (root, files, msg = "change") => {
  write(root, files);
  git(root, "add", "-A");
  git(root, "commit", "-q", "--allow-empty", "-m", msg);
  return git(root, "rev-parse", "HEAD");
};

const run = (cwd, ...args) => {
  const r = spawnSync(process.execPath, [SCRIPT, ...args], { cwd, env: ENV, encoding: "utf8" });
  return { code: r.status, out: r.stdout, err: r.stderr, all: `${r.stdout}${r.stderr}` };
};
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

describe("scope", () => {
  test("prints the range, tiers, areas, removals and removed roles", () => {
    const r0 = repo();
    const base = commit(r0, {
      "templates/global/agents/old-agent.md": "a\n",
      "templates/project/commands/pfm/gone.md": "a\n",
      ".claude/commands/flat.md": "a\n",
      "templates/global/skills/dead/SKILL.md": "a\n",
      "templates/global/skills/dead/ref.md": "b\n",
      "templates/global/skills/alive/SKILL.md": "a\n",
      "templates/global/skills/alive/extra.md": "b\n",
      "docs/moved.md": "one\ntwo\nthree\nfour\n",
      "pfm/cmd/pfm/install_command.go": "package main\n",
      "pfm/cmd/pfm/chat_command.go": "package main\n",
      "README.md": "readme\n",
    });
    git(r0, "checkout", "-q", "-b", "side");
    commit(r0, { "side.txt": "side\n" }, "side");
    git(r0, "checkout", "-q", "main");
    commit(r0, {
      "templates/global/agents/old-agent.md": null,
      "templates/project/commands/pfm/gone.md": null,
      ".claude/commands/flat.md": null,
      "templates/global/skills/dead/SKILL.md": null,
      "templates/global/skills/dead/ref.md": null,
      "templates/global/skills/alive/extra.md": null,
      "docs/moved.md": null,
      "docs/renamed.md": "one\ntwo\nthree\nfour\n",
      "pfm/cmd/pfm/install_command.go": "package main\n\nfunc x() {}\n",
      "pfm/cmd/pfm/chat_command.go": "package main\n\nfunc y() {}\n",
      "README.md": "readme v2\n",
      "scripts/new.sh": "echo new\n",
      "releases/v0.78.0.md": "note\n",
    });
    git(r0, "merge", "-q", "--no-ff", "side", "-m", "merge side");
    const head = git(r0, "rev-parse", "HEAD");
    const r = run(r0, "scope", "--base", base, "--head", "HEAD");
    assertClean(r, "scope");
    const out = r.out;
    assert.match(out, new RegExp(`^RANGE ${base.slice(0, 7)}\\.\\.${head.slice(0, 7)} COMMITS 2 FILES 13 HUNKS \\d+$`, "m"));
    for (const line of [
      "FILE R 0 tier2 - docs/renamed.md",
      "FILE M 1 tier1 pfm-update pfm/cmd/pfm/install_command.go",
      "FILE M 1 tier2 - pfm/cmd/pfm/chat_command.go",
      "FILE M 1 tier1 public README.md",
      "FILE A 1 tier2 - releases/v0.78.0.md",
      "FILE A 1 tier1 gates scripts/new.sh",
      "FILE D 1 tier1 templates templates/global/agents/old-agent.md",
      "FILE A 1 tier2 - side.txt",
      "AREA templates 5 5 templates/ workflows/ pfm/harness-prompts/",
      "AREA gates 1 1 .github/ .githooks/ scripts/ infra/fence/release-rehearsal.sh infra/check-self-hosted-manifest.sh",
      "TIER2 4 5",
      "REMOVED docs/moved.md",
      "REMOVED templates/global/skills/alive/extra.md",
      "REMOVED-ROLE agent old-agent",
      "REMOVED-ROLE command /flat",
      "REMOVED-ROLE command /pfm:gone",
      "REMOVED-ROLE skill dead",
    ])
      assert.ok(out.split("\n").includes(line), `missing line ${JSON.stringify(line)}:\n${out}`);
    assert.match(out, /^AREA pfm-update 1 1 .*:\(glob\)pfm\/cmd\/pfm\/install\*/m);
    assert.doesNotMatch(out, /REMOVED-ROLE skill alive/, "a skill with files left is not removed");
  });

  test("splits an area over 600 hunks by segment, then by file", () => {
    const r0 = repo();
    const [a0, a1] = hunky(400);
    const [b0, b1] = hunky(300);
    const [p0, p1] = hunky(200);
    const [w0, w1] = hunky(50);
    const base = commit(r0, {
      "templates/global/a.md": a0,
      "templates/global/b.md": b0,
      "templates/project/p.md": p0,
      "workflows/w/w.md": w0,
    });
    commit(r0, {
      "templates/global/a.md": a1,
      "templates/global/b.md": b1,
      "templates/project/p.md": p1,
      "workflows/w/w.md": w1,
    });
    const r = run(r0, "scope", "--base", base, "--head", "HEAD");
    assertClean(r, "scope");
    const areas = r.out.split("\n").filter((l) => l.startsWith("AREA "));
    assert.deepEqual(areas, [
      "AREA templates-1 400 1 templates/global/a.md",
      "AREA templates-2 300 1 templates/global/b.md",
      "AREA templates-3 250 2 templates/project/ workflows/w/",
    ]);
    assert.ok(r.out.includes("FILE M 400 tier1 templates-1 templates/global/a.md\n"), r.out);
    assert.ok(r.out.includes("FILE M 50 tier1 templates-3 workflows/w/w.md\n"), r.out);
  });

  test("splits pfm-update by package, its table's next path segment", () => {
    const r0 = repo();
    const [a0, a1] = hunky(400);
    const [b0, b1] = hunky(100);
    const [u0, u1] = hunky(300);
    const base = commit(r0, {
      "pfm/internal/doctor/a.go": a0,
      "pfm/internal/doctor/b.go": b0,
      "pfm/internal/update/u.go": u0,
    });
    commit(r0, {
      "pfm/internal/doctor/a.go": a1,
      "pfm/internal/doctor/b.go": b1,
      "pfm/internal/update/u.go": u1,
    });
    const r = run(r0, "scope", "--base", base, "--head", "HEAD");
    assertClean(r, "scope");
    assert.deepEqual(
      r.out.split("\n").filter((l) => l.startsWith("AREA ")),
      ["AREA pfm-update-1 500 2 pfm/internal/doctor/", "AREA pfm-update-2 300 1 pfm/internal/update/"],
    );
  });

  test("a rename out of tier 1 keeps its old path's tier and area", () => {
    const r0 = repo();
    const base = commit(r0, { "templates/global/agents/gone.md": "gone\nagent\nbody\n" });
    commit(r0, { "templates/global/agents/gone.md": null, "docs/old/gone.md": "gone\nagent\nbody\n" });
    const r = run(r0, "scope", "--base", base, "--head", "HEAD");
    assertClean(r, "scope");
    for (const line of [
      "FILE R 0 tier1 templates docs/old/gone.md",
      "AREA templates 0 1 templates/ workflows/ pfm/harness-prompts/ templates/global/agents/gone.md docs/old/gone.md",
      "TIER2 0 0",
      "REMOVED templates/global/agents/gone.md",
    ])
      assert.ok(r.out.split("\n").includes(line), `missing line ${JSON.stringify(line)}:\n${r.out}`);
  });

  test("a role another root still holds at head is not removed", () => {
    const r0 = repo();
    const base = commit(r0, {
      ".claude/agents/x.md": "local x\n",
      "templates/global/agents/x.md": "global x\n",
      ".claude/commands/pfm.md": "the pfm command\n",
      "templates/project/agents/gone.md": "gone\n",
    });
    commit(r0, {
      ".claude/agents/x.md": null,
      ".claude/commands/pfm.md": null,
      ".claude/commands/pcm.md": "the pcm command, another text\n",
      "templates/global/commands/pfm.md": "the global pfm command, rewritten\n",
      "templates/project/agents/gone.md": null,
    });
    const r = run(r0, "scope", "--base", base, "--head", "HEAD");
    assertClean(r, "scope");
    assert.deepEqual(r.out.split("\n").filter((l) => l.startsWith("REMOVED-ROLE ")), ["REMOVED-ROLE agent gone"], r.out);
  });

  test("the mcp core and mcp_serve command are pfm-update tier 1", () => {
    const r0 = repo();
    const base = commit(r0, {
      "pfm/internal/mcpserv/proxy.go": "package mcpserv\n",
      "pfm/internal/harvestmcp/search_gate.go": "package harvestmcp\n",
      "pfm/cmd/pfm/mcp_serve_command.go": "package main\n",
      "pfm/cmd/pfm/mcp_serve_test.go": "package main\n",
      "pfm/cmd/pfm/main.go": "package main\n",
    });
    commit(r0, {
      "pfm/internal/mcpserv/proxy.go": "package mcpserv\n\nfunc x() {}\n",
      "pfm/internal/harvestmcp/search_gate.go": "package harvestmcp\n\nfunc x() {}\n",
      "pfm/cmd/pfm/mcp_serve_command.go": "package main\n\nfunc x() {}\n",
      "pfm/cmd/pfm/mcp_serve_test.go": "package main\n\nfunc y() {}\n",
      "pfm/cmd/pfm/main.go": "package main\n\nfunc z() {}\n",
    });
    const r = run(r0, "scope", "--base", base, "--head", "HEAD");
    assertClean(r, "scope");
    for (const line of [
      "FILE M 1 tier1 pfm-update pfm/internal/mcpserv/proxy.go",
      "FILE M 1 tier1 pfm-update pfm/internal/harvestmcp/search_gate.go",
      "FILE M 1 tier1 pfm-update pfm/cmd/pfm/mcp_serve_command.go",
      "FILE M 1 tier1 pfm-update pfm/cmd/pfm/mcp_serve_test.go",
      "FILE M 1 tier2 - pfm/cmd/pfm/main.go",
    ])
      assert.ok(r.out.split("\n").includes(line), `missing line ${JSON.stringify(line)}:\n${r.out}`);
    assert.match(r.out, /^AREA pfm-update .*pfm\/internal\/mcpserv\/.*pfm\/internal\/harvestmcp\/.*:\(glob\)pfm\/cmd\/pfm\/mcp_serve\*/m);
  });

  test("ERROR, not FAIL, when --base is not an ancestor of --head", () => {
    const r0 = repo();
    const base = commit(r0, { "a.txt": "a\n" });
    commit(r0, { "templates/project/agents/bar.md": "bar\n" });
    assertError(run(r0, "scope", "--base", "HEAD", "--head", base), /--base HEAD is not an ancestor of --head [0-9a-f]{40}/);
  });

  test("ERROR, not FAIL, when a ref does not resolve", () => {
    const r0 = repo();
    commit(r0, { "a.txt": "a\n" });
    assertError(run(r0, "scope", "--base", "no-such-ref", "--head", "HEAD"), /git rev-parse --verify no-such-ref/);
    assertError(run(r0, "scope", "--base", "HEAD"), /usage: scope needs --base and --head/);
  });
});

// ---------------------------------------------------------------- notes: grammar

describe("notes grammar N1–N8", () => {
  const check = (text) => {
    const d = tmp();
    write(d, { "v0.78.0.md": text });
    return run(d, "notes", "v0.78.0.md");
  };
  const mutate = (from, to) => {
    assert.ok(NOTE.includes(from), `fixture lacks ${from}`);
    return NOTE.replace(from, to);
  };

  test("a note in the grammar is clean", () => assertClean(check(NOTE), "notes"));
  test("N1 title", () => assertFail(check(mutate("# v0.78.0 — 2026-09-25", "# 0.78.0 - 2026-09-25")), "N1"));
  test("N2 no lead", () => assertFail(check(mutate("The release lead: what an adopter gets and what to do.\n", "")), "N2"));
  test("N3 Stop without a reason", () => assertFail(check(mutate("#### → Stop: read Breaking before updating", "#### → Stop:")), "N3"));
  test("N3 Stop after the first ##", () =>
    assertFail(check(mutate("## Added\n", "## Added\n\n#### → Stop: late\n")), "N3"));
  test("N3 a lead line follows a Stop line, still before the first ##", () => {
    const r = check(mutate("## Breaking\n", "Another lead line.\n\n## Breaking\n"));
    assertFail(r, "N3");
    assert.match(r.out, /^FAIL N3: v0\.78\.0\.md:\d+ — a lead line follows a `#### → Stop:` line; Stop lines sit between the lead and the first `##`$/m, r.out);
  });
  test("N4 unknown section", () => assertFail(check(mutate("## Added", "## Misc")), "N4"));
  test("N4 order", () => assertFail(check(mutate("## Breaking\n", "## Fixed\n\n- Repo: x — y\n\n## Breaking\n")), "N4"));
  test("N4 empty section", () => assertFail(check(mutate("## Verification\n", "## Removed\n\n## Verification\n")), "N4"));
  test("N5 bad label", () => assertFail(check(mutate("- Project: agents/bar.md", "- Other: agents/bar.md")), "N5"));
  test("N5 prose under a category", () => assertFail(check(mutate("## Added\n\n", "## Added\n\nsome prose\n")), "N5"));
  test("N5 action line before any bullet of its section", () => {
    const r = check(mutate(
      "## Added\n\n- Project: agents/bar.md — a new bar agent\n",
      "## Added\n\n#### → For: every adopter · after update · shell — run `pfm install --yes`\n\n- Project: agents/bar.md — a new bar agent\n",
    ));
    assertFail(r, "N5");
    assert.match(r.out, /^FAIL N5: v0\.78\.0\.md:\d+ — action line before any bullet of its section$/m, r.out);
  });
  test("N6 bad timing", () => assertFail(check(mutate("· after update ·", "· someday ·")), "N6"));
  test("N6 action line missing a part other than timing", () => {
    const r = check(mutate("· shell — run", "·  — run"));
    assertFail(r, "N6");
    assert.match(r.out, /^FAIL N6: v0\.78\.0\.md:\d+ — action line does not read `#### → For: \{audience\} · \{before update\|after update\|per project\} · \{surface\} — \{action\}` with every part non-empty$/m, r.out);
  });
  test("N7 bullet under Verification", () => assertFail(check(`${NOTE}- a bullet\n`), "N7"));
  test("N7 action line under Verification", () => {
    const r = check(`${NOTE}#### → For: every adopter · after update · shell — verify\n`);
    assertFail(r, "N7");
    assert.match(r.out, /^FAIL N7: v0\.78\.0\.md:\d+ — `## Verification` holds an action line$/m, r.out);
  });
  test("N8 other heading level", () => assertFail(check(mutate("## Added\n\n", "## Added\n\n### Details\n")), "N8"));
  test("N8 a heading without the space after its hashes", () => {
    const r = check(mutate("## Added", "##Added"));
    assertFail(r, "N8");
    assert.match(r.out, /^FAIL N8: v0\.78\.0\.md:12 — a line opens with `#` but lacks the space after its hashes: ##Added$/m, r.out);
  });
  test("N8 a code fence that never closes", () => {
    const r = check(mutate("what to do.\n", "what to do.\n\n```sh\nopened in the lead, never closed\n"));
    assertFail(r, "N8");
    assert.match(r.out, /^FAIL N8: v0\.78\.0\.md:5 — code fence never closes$/m, r.out);
  });
  test("ERROR, not FAIL, when the note does not exist", () =>
    assertError(run(tmp(), "notes", "missing.md"), /note missing\.md does not exist/));
});

describe("notes --all", () => {
  test("checks only notes at or above v0.78.0", () => {
    const d = tmp();
    write(d, { "rel/v0.77.2.md": "# old grammar\n", "rel/v0.78.0.md": NOTE, "rel/other.md": "x\n" });
    const r = run(d, "notes", "--all", "rel");
    assertClean(r, "notes");
    assert.match(r.out, /^CHECKED 1 notes at or above v0\.78\.0 in rel$/m);
    write(d, { "rel/v0.78.1.md": "# not a title\n" });
    assertFail(run(d, "notes", "--all", "rel"), "N1");
  });
  test("binds each note's title to the version its file name carries", () => {
    const d = tmp();
    write(d, { "rel/v0.79.0.md": NOTE });
    const r = run(d, "notes", "--all", "rel");
    assertFail(r, "N1");
    assert.match(r.out, /^FAIL N1: rel\/v0\.79\.0\.md:1 — title version v0\.78\.0 differs from its file name v0\.79\.0\.md$/m, r.out);
  });
  test("a v*.md file name that is not v{X.Y.Z}.md", () => {
    const d = tmp();
    write(d, { "rel/v1.md": "# whatever\n" });
    const r = run(d, "notes", "--all", "rel");
    assertFail(r, "N1");
    assert.match(r.out, /^FAIL N1: rel\/v1\.md — file name is not v\{X\.Y\.Z\}\.md, so its grammar version cannot be placed$/m, r.out);
  });
  test("ERROR, not FAIL, when the directory does not exist", () =>
    assertError(run(tmp(), "notes", "--all", "nope"), /notes directory nope does not exist/));
});

// ---------------------------------------------------------------- notes: coverage

describe("notes coverage C1–C5", () => {
  const fixture = () => {
    const r0 = repo();
    const base = commit(r0, {
      "README.md": "r\n",
      "templates/global/agents/old-agent.md": "a\n",
      "templates/project/agents/keep.md": "k\n",
      ".claude/agents/twin.md": "local twin\n",
      "templates/global/agents/twin.md": "global twin\n",
    });
    const c1 = commit(r0, { "templates/project/agents/bar.md": "bar\n" }, "add bar");
    const c2 = commit(r0, { "templates/global/skills/qux/SKILL.md": "qux\n" }, "add qux");
    const c3 = commit(r0, { "scripts/x.test.mjs": "t\n" }, "test only");
    const c4 = commit(r0, { "templates/global/agents/old-agent.md": null }, "drop old-agent");
    const d = tmp();
    const rows = {
      c1: `${c1.slice(0, 7)}\tAdded\tProject: agents/bar.md`,
      c2: `${c2.slice(0, 7)}\tAdded\tGlobal: skills/qux`,
      c3: `${c3.slice(0, 7)}\tnone\ttest-only change`,
      c4: `${c4.slice(0, 7)}\tBreaking\tGlobal: agents/old-agent.md`,
    };
    // committed: the note is committed as the release's own commit (a
    // release-only commit, so it needs no row) — the form --record judges.
    const check = ({ note = NOTE, rowsOver = {}, extra = [], committed = false } = {}) => {
      const all = { ...rows, ...rowsOver };
      write(d, { "coverage.tsv": `${Object.values(all).filter((x) => x !== null).join("\n")}\n` });
      let notePath = join(d, "note.md");
      if (committed) {
        commit(r0, { "releases/v0.78.0.md": note }, "the note");
        notePath = "releases/v0.78.0.md";
      } else write(d, { "note.md": note });
      return run(r0, "notes", notePath, "--base", base, "--head", "HEAD", "--coverage", join(d, "coverage.tsv"), ...extra);
    };
    return { r0, d, c1, c3, check };
  };

  test("a ledger naming every commit once is clean, and --record writes NOTES PASS with its groups", () => {
    const { r0, d, check } = fixture();
    assertClean(check({ committed: true, extra: ["--record", join(d, "notes/check.txt")] }), "notes");
    const record = readFileSync(join(d, "notes/check.txt"), "utf8").split("\n");
    assert.equal(record[0], `NOTES PASS ${git(r0, "rev-parse", "HEAD")} range`);
    assert.equal(record[1], "release-check notes: clean");
  });
  test("C1 missing commit", () => assertFail(fixture().check({ rowsOver: { c3: null } }), "C1"));
  test("C1 commit twice", () => {
    const f = fixture();
    assertFail(f.check({ rowsOver: { dup: `${f.c3.slice(0, 7)}\tnone\tagain` } }), "C1");
  });
  test("C1 a row names a commit outside the range", () => {
    const f = fixture();
    const r = f.check({ rowsOver: { bogus: "1234567\tnone\toutside the range" } });
    assertFail(r, "C1");
    assert.match(r.out, /^FAIL C1: .* — 1234567 is not a non-merge commit of [0-9a-f]{7}\.\.[0-9a-f]{7}$/m, r.out);
  });
  test("C1 a malformed ledger row", () => {
    const f = fixture();
    const r = f.check({ rowsOver: { bad: "not-a-sha" } });
    assertFail(r, "C1");
    assert.match(r.out, /^FAIL C1: .* — row is not `\{sha7\}/m, r.out);
  });
  test("C2 row names no bullet", () => {
    const f = fixture();
    assertFail(f.check({ rowsOver: { c1: `${f.c1.slice(0, 7)}\tAdded\tProject: agents/nope.md` } }), "C2");
  });
  test("C3 none without a reason, and --record writes NOTES FAIL", () => {
    const f = fixture();
    const r = f.check({ committed: true, rowsOver: { c3: `${f.c3.slice(0, 7)}\tnone\t` }, extra: ["--record", join(f.d, "check.txt")] });
    assertFail(r, "C3");
    assert.match(readFileSync(join(f.d, "check.txt"), "utf8"), /^NOTES FAIL [0-9a-f]{40} range\nFAIL C3: /);
  });
  test("C4 changed templates/project path named nowhere", () => {
    const f = fixture();
    const note = NOTE.replace("- Project: agents/bar.md — a new bar agent", "- Project: the bar agent — a new bar agent");
    assertFail(f.check({ note, rowsOver: { c1: `${f.c1.slice(0, 7)}\tAdded\tProject: the bar agent` } }), "C4");
  });
  test("C5 added skill named nowhere", () => {
    const f = fixture();
    const note = NOTE.replace("- Global: skills/qux — a new qux skill", "- Global: a new skill — added");
    const r = f.check({ note, rowsOver: { c2: null, c2b: `${git(f.r0, "rev-parse", "HEAD~2").slice(0, 7)}\tAdded\tGlobal: a new skill` } });
    assertFail(r, "C5");
    assert.match(r.out, /^FAIL C5: .* — added skill qux appears nowhere in the note$/m);
  });
  test("C5 removed role named nowhere", () => {
    const r0 = repo();
    const base = commit(r0, {
      "README.md": "r\n",
      "templates/global/agents/mover.md": "an agent, never mentioned\n",
    });
    const c1 = commit(r0, { "templates/global/agents/mover.md": null }, "drop mover");
    const d = tmp();
    write(d, { "coverage.tsv": `${c1.slice(0, 7)}\tnone\tinternal cleanup\n`, "note.md": NOTE });
    const r = run(r0, "notes", join(d, "note.md"), "--base", base, "--head", "HEAD", "--coverage", join(d, "coverage.tsv"));
    assertFail(r, "C5");
    assert.match(r.out, /^FAIL C5: .* — removed agent mover appears nowhere in the note$/m, r.out);
  });
  test("C4 matches a whole path, never inside a longer one", () => {
    const f = fixture();
    const named = (scope) => ({
      note: NOTE.replace("- Project: agents/bar.md — a new bar agent", `- Project: ${scope} — a new bar agent`),
      rowsOver: { c1: `${f.c1.slice(0, 7)}\tAdded\tProject: ${scope}` },
    });
    const r = f.check(named("agents/bar.md.bak"));
    assertFail(r, "C4");
    assert.match(r.out, /^FAIL C4: .* — templates\/project\/agents\/bar\.md changed in the range; `agents\/bar\.md` appears in no bullet or action line$/m, r.out);
    assertClean(f.check(named("templates/project/agents/bar.md")), "notes");
  });
  test("C5 does not demand a role another root still holds", () => {
    const f = fixture();
    const c5 = commit(f.r0, { ".claude/agents/twin.md": null }, "drop the local twin");
    assertClean(f.check({ rowsOver: { c5: `${c5.slice(0, 7)}\tnone\tthe local copy only` } }), "notes");
  });
  test("ERROR, not FAIL, when --base is not an ancestor of --head", () => {
    const f = fixture();
    write(f.d, { "note.md": NOTE, "empty.tsv": "" });
    assertError(
      run(f.r0, "notes", join(f.d, "note.md"), "--base", "HEAD", "--head", "HEAD~4", "--coverage", join(f.d, "empty.tsv")),
      /--base HEAD is not an ancestor of --head HEAD~4/,
    );
  });
  test("ERROR, not FAIL, when the coverage file does not exist", () => {
    const { r0 } = fixture();
    const d = tmp();
    write(d, { "note.md": NOTE });
    assertError(
      run(r0, "notes", join(d, "note.md"), "--base", "HEAD~4", "--head", "HEAD", "--coverage", join(d, "absent.tsv")),
      /coverage file .*absent\.tsv does not exist/,
    );
  });
  test("C1 exempts a commit touching only the release files", () => {
    const f = fixture();
    commit(f.r0, { "releases/v1.0.0.md": "note\n", "CHANGELOG.md": "changelog\n", VERSION: "1.0.0\n" }, "release v1.0.0");
    assertClean(f.check(), "notes");
  });
  test("C1 still requires a row for a commit touching a release file plus another path", () => {
    const f = fixture();
    commit(f.r0, { "CHANGELOG.md": "changelog\n", "README.md": "updated readme\n" }, "changelog and readme");
    assertFail(f.check(), "C1");
  });
});

// ---------------------------------------------------------------- notes: version and commands

describe("notes version V1–V3 and commands P1", () => {
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
  const version = (d, v = "0.78.0", bump = "minor") =>
    run(d, "notes", "note.md", "--version", v, "--previous", "0.77.2", "--bump", bump);

  test("stamps, index line and bump in agreement are clean", () => assertClean(version(fixture()), "notes"));
  test("V1 a stamp differs", () => assertFail(version(fixture({ ".professor/VERSION": "0.77.2\n" })), "V1"));
  test("V1 the manifest stamp differs", () => {
    const r = version(fixture({ ".professor/manifest.json": JSON.stringify({ installed_from: { version: "0.77.2" } }) }));
    assertFail(r, "V1");
    assert.match(r.out, /^FAIL V1: \.professor\/manifest\.json — "0\.77\.2" differs from --version 0\.78\.0$/m, r.out);
  });
  test("V2 first changelog entry", () =>
    assertFail(version(fixture({ "CHANGELOG.md": "# Changelog\n\n## Releases\n\n- [v0.77.2](releases/v0.77.2.md) — older\n" })), "V2"));
  test("V2 the Releases heading holds no entry", () => {
    const r = version(fixture({ "CHANGELOG.md": "# Changelog\n\n## Releases\n\n## Old\n" }));
    assertFail(r, "V2");
    assert.match(r.out, /^FAIL V2: CHANGELOG\.md:3 — `## Releases` holds no entry$/m, r.out);
  });
  test("V3 bump below the floor", () => assertFail(version(fixture(), "0.77.3", "patch"), "V3"));
  test("V3 bump arithmetic differs from --version, floor satisfied", () => {
    const r = version(fixture(), "0.78.0", "major");
    assertFail(r, "V3");
    assert.match(r.out, /^FAIL V3: --version — 0\.78\.0 is not 0\.77\.2 bumped by major \(1\.0\.0\)$/m, r.out);
    assert.doesNotMatch(r.out, /below the floor/, r.out);
  });
  test("ERROR, not FAIL, when VERSION does not exist", () =>
    assertError(version(fixture({ VERSION: null })), /version stamp .*VERSION does not exist/));
  test("ERROR, not FAIL, on a partial argument group", () => {
    assertError(run(fixture(), "notes", "note.md", "--version", "0.78.0", "--previous", "0.77.2"), /go together; missing --bump/);
    assertError(run(fixture(), "notes", "note.md", "--previous", "0.77.2", "--bump", "minor"), /go together; missing --version/);
  });
  test("--version alone binds N1 only", () => {
    const d = fixture({ VERSION: "0.1.0\n" });
    assertClean(run(d, "notes", "note.md", "--version", "v0.78.0"), "notes");
    const r = run(d, "notes", "note.md", "--version", "0.79.0");
    assertFail(r, "N1");
    assert.match(r.out, /^FAIL N1: note\.md:1 — title version v0\.78\.0 differs from --version 0\.79\.0$/m, r.out);
    assert.doesNotMatch(r.out, /^FAIL V/m, r.out);
  });

  test("P1 commands in the help list are clean", () =>
    assertClean(run(fixture(), "notes", "note.md", "--pfm-help", "help.txt"), "notes"));
  test("P1 unknown pfm command", () => {
    const d = fixture({ "note.md": NOTE.replace("run `pfm install --yes`", "run `pfm frobnicate --now`") });
    assertFail(run(d, "notes", "note.md", "--pfm-help", "help.txt"), "P1");
  });
  test("ERROR, not FAIL, when the help file does not exist", () =>
    assertError(run(fixture(), "notes", "note.md", "--pfm-help", "absent.txt"), /pfm help file absent\.txt does not exist/));
  test("ERROR, not FAIL, when the help file lists no command", () => {
    const d = fixture({ "empty-help.txt": "usage: pfm <command>\n\nno commands here\n" });
    assertError(run(d, "notes", "note.md", "--pfm-help", "empty-help.txt"), /pfm help file empty-help\.txt lists no command/);
  });

  test("P1 accepts a help line with one space after a padded name", () => {
    const d = fixture({
      "help2.txt": "usage: pfm <command>\n\ncommands:\n  uninstall remove the self-contained host integration\n",
      "note.md": NOTE.replace("run `pfm install --yes`", "run `pfm uninstall --yes`"),
    });
    assertClean(run(d, "notes", "note.md", "--pfm-help", "help2.txt"), "notes");
  });

  test("--version and --previous each accept X.Y.Z or vX.Y.Z", () => {
    const d = fixture();
    assertClean(run(d, "notes", "note.md", "--version", "0.78.0", "--previous", "v0.77.2", "--bump", "minor"), "notes");
    assertClean(run(d, "notes", "note.md", "--version", "v0.78.0", "--previous", "0.77.2", "--bump", "minor"), "notes");
  });

  test("usage names the optional v prefix for --version and --previous", () => {
    const r = run(tmp(), "notes", "note.md", "--previous", "0.77.2");
    assert.match(r.err, /\[--version \[v\]X\.Y\.Z \[--previous \[v\]X\.Y\.Z --bump patch\|minor\|major\]\]/);
  });
});

// ---------------------------------------------------------------- notes: --record

describe("notes --record", () => {
  // A release commit whose note and stamps pass every rule group.
  const fixture = (committedNote = NOTE) => {
    const r0 = repo();
    const base = commit(r0, { "README.md": "r\n" });
    const head = commit(r0, {
      "releases/v0.78.0.md": committedNote,
      VERSION: "0.78.0\n",
      ".professor/VERSION": "0.78.0\n",
      ".professor/manifest.json": JSON.stringify({ installed_from: { version: "0.78.0" } }),
      "CHANGELOG.md": "# Changelog\n\n## Releases\n\n- [v0.78.0](releases/v0.78.0.md) — the summary\n",
    }, "release v0.78.0");
    const d = tmp();
    write(d, { "empty.tsv": "", "help.txt": "commands:\n  install   wire the host\n" });
    const rec = join(d, "notes/check.txt");
    const every = [
      "--base", base, "--head", "HEAD", "--coverage", join(d, "empty.tsv"),
      "--version", "0.78.0", "--previous", "0.77.2", "--bump", "minor", "--pfm-help", join(d, "help.txt"),
    ];
    const notes = (...extra) => run(r0, "notes", "releases/v0.78.0.md", "--record", rec, ...extra);
    const line1 = () => readFileSync(rec, "utf8").split("\n")[0];
    return { r0, head, d, rec, every, notes, line1 };
  };

  test("records NOTES PASS {head} and the rule groups that ran", () => {
    const f = fixture();
    assertClean(f.notes(...f.every), "notes");
    assert.equal(f.line1(), `NOTES PASS ${f.head} range version commands`);
    assertClean(f.notes(), "notes");
    assert.equal(f.line1(), `NOTES PASS ${f.head}`);
  });
  test("ERROR, recorded NOTES ERROR, when the note differs from HEAD", () => {
    const f = fixture("# broken title\n");
    write(f.r0, { "releases/v0.78.0.md": NOTE });
    assertError(f.notes(...f.every), /releases\/v0\.78\.0\.md differs from HEAD — commit the note and stamps before recording/);
    assert.equal(f.line1(), `NOTES ERROR ${f.head}`);
  });
  test("ERROR when a stamp differs from HEAD", () => {
    const f = fixture();
    write(f.r0, { VERSION: "0.79.0\n" });
    assertError(f.notes(), /VERSION differs from HEAD/);
    assert.equal(f.line1(), `NOTES ERROR ${f.head}`);
  });
  test("a usage error once --record is known still records NOTES ERROR", () => {
    const f = fixture();
    write(f.d, { "notes/check.txt": `NOTES PASS ${f.head} range version commands\n` });
    assertError(f.notes("--previous", "0.77.2"), /go together/);
    assert.match(f.line1(), /^NOTES ERROR /);
    write(f.d, { "notes/check.txt": `NOTES PASS ${f.head} range version commands\n` });
    assertError(run(f.r0, "notes", "releases/v0.78.0.md", "--record", f.rec, "--bogus"), /has no flag --bogus/);
    assert.match(f.line1(), /^NOTES ERROR /);
  });
  test("a record that cannot be written is reported beside the original error", () => {
    const f = fixture();
    write(f.d, { blocker: "a file, not a directory\n" });
    const r = run(f.r0, "notes", "missing.md", "--record", join(f.d, "blocker/check.txt"));
    assertError(r, /note missing\.md does not exist/);
    assert.match(r.err, /--record .*blocker\/check\.txt could not be written/, r.all);
  });
});

// ---------------------------------------------------------------- ready

describe("ready R1–R6", () => {
  const fixture = () => {
    const wt = repo();
    const head = commit(wt, {
      VERSION: "0.78.0\n",
      "CHANGELOG.md": "# Changelog\n",
      "templates/global/agents/a.md": "a\n",
      "docs/x.md": "x\n",
      "releases/v0.78.0.md": NOTE,
    });
    const dir = tmp();
    write(dir, {
      "scope.md": `RANGE 0000000..${head.slice(0, 7)} COMMITS 1 FILES 1 HUNKS 1\nAREA templates 1 1 templates/ workflows/ pfm/harness-prompts/\nTIER2 0 0\n`,
      "review/templates.md": "### F1 — a finding\nstatus: resolved @abc1234\n",
      "review/sandbox-templates/fixture.md": "status: open — a sandbox file, not a report\n",
      "review/HEAD": `${head}\n`,
      "gate/candidate-templates.log": `ok\nEXIT 0 @${head}\n`,
      "gate/candidate-pfm.log": `ok\nEXIT 0 @${head}\n`,
      "gate/candidate-e2e.log": `ok\nEXIT 0 @${head}\n`,
      "rehearsal.md": `REHEARSAL CLEAN ${head} round 1\n`,
      "rehearsal/main-1/result.json": JSON.stringify({ verdict: "CLEAN" }),
      "rehearsal/behind-1/result.json": JSON.stringify({ verdict: "CLEAN" }),
      "notes/check.txt": `NOTES PASS ${head} range version commands\n`,
    });
    const ready = (...flags) => run(wt, "ready", dir, "--worktree", wt, ...flags);
    // A later commit; the phases whose verdicts the caller names move to it.
    const advance = (files, phases = []) => {
      const next = commit(wt, files, "later");
      if (phases.includes("notes")) write(dir, { "notes/check.txt": `NOTES PASS ${next} range version commands\n` });
      if (phases.includes("rehearse")) write(dir, { "rehearsal.md": `REHEARSAL CLEAN ${next} round 2\n`, "rehearsal/main-2/result.json": '{"verdict":"CLEAN"}', "rehearsal/behind-2/result.json": '{"verdict":"CLEAN"}' });
      return next;
    };
    return { wt, dir, head, ready, advance };
  };
  const assertRerun = (r, phase) => assert.match(r.out, new RegExp(`^RERUN ${phase}$`, "m"), r.all);

  test("every verdict at HEAD: --stamp writes READY, then the unstamped form is clean", () => {
    const f = fixture();
    const r = f.ready("--stamp");
    assertClean(r, "ready");
    assert.match(readFileSync(join(f.dir, "READY"), "utf8"), new RegExp(`^${f.head} v0\\.78\\.0 \\d{4}-\\d{2}-\\d{2}\\n$`));
    assertClean(f.ready(), "ready");
  });
  const neither = (line, value) =>
    new RegExp(`^FAIL R1: review/seams\\.md:${line} — status "${value}" is neither resolved @\\{sha\\} nor waived$`, "m");
  for (const [name, report, expected] of [
    ["status: open", "S1 · a seam\nstatus: open\n", neither(2, "open")],
    ["Status: Open", "S1 · a seam\nStatus: Open\n", neither(2, "Open")],
    ["status: **open**", "S1 · a seam\nstatus: **open**\n", neither(2, "open")],
    ["a finding with no status line", "F1 · a finding\nstatus: resolved @abc1234\n\nF2: a second finding\nevidence only\n", /^FAIL R1: review\/seams\.md:4 — finding F2 carries no status$/m],
    ["an empty report", "  \n\n", /^FAIL R1: review\/seams\.md — report is empty$/m],
  ])
    test(`R1 ${name}`, () => {
      const f = fixture();
      write(f.dir, { "review/seams.md": report });
      const r = f.ready("--stamp");
      assertFail(r, "R1");
      assert.match(r.out, expected, r.out);
      assertRerun(r, "REVIEW");
    });
  test("R1 resolved and waived findings, in markup, pass", () => {
    const f = fixture();
    write(f.dir, {
      "review/seams.md": "- **S1** · a seam\n  `status: resolved @abc1234`\n\n## P2 | a slow path\n_Status: Waived — accepted for this release_\n",
    });
    assertClean(f.ready("--stamp"), "ready");
  });
  test("R1 no report", () => {
    const f = fixture();
    write(f.dir, { "review/templates.md": null });
    assertFail(f.ready("--stamp"), "R1");
  });
  test("R1 scope.md absent", () => {
    const f = fixture();
    write(f.dir, { "scope.md": null });
    const r = f.ready("--stamp");
    assertFail(r, "R1");
    assert.match(r.out, /^FAIL R1: scope\.md — absent/m, r.out);
  });
  test("R1 an AREA of scope.md without its report", () => {
    const f = fixture();
    write(f.dir, { "scope.md": "AREA templates 1 1 templates/\nAREA gates 1 1 scripts/\n" });
    const r = f.ready("--stamp");
    assertFail(r, "R1");
    assert.match(r.out, /^FAIL R1: review\/gates\.md — no report for AREA gates$/m, r.out);
    assertRerun(r, "REVIEW");
  });
  test("R2 tier-1 change since the review", () => {
    const f = fixture();
    f.advance({ "templates/global/agents/a.md": "a2\n" }, ["notes", "rehearse"]);
    const r = f.ready("--stamp");
    assertFail(r, "R2");
    assertRerun(r, "REVIEW");
  });
  test("R2 review/HEAD does not name a commit", () => {
    const f = fixture();
    write(f.dir, { "review/HEAD": "not-a-commit\n" });
    const r = f.ready("--stamp");
    assertFail(r, "R2");
    assert.match(r.out, /^FAIL R2: review\/HEAD — not-a-commit is not a commit of the worktree$/m, r.out);
    assertRerun(r, "REVIEW");
  });
  test("R3 new red lane", () => {
    const f = fixture();
    write(f.dir, { "gate/candidate-e2e.log": `boom\nEXIT 1 @${f.head}\n` });
    const r = f.ready("--stamp");
    assertFail(r, "R3");
    assertRerun(r, "GATE");
  });
  test("R3 a lane with no candidate log at all", () => {
    const f = fixture();
    write(f.dir, { "gate/candidate-pfm.log": null });
    const r = f.ready("--stamp");
    assertFail(r, "R3");
    assert.match(r.out, /^FAIL R3: gate\/candidate-pfm\.log — absent$/m, r.out);
    assertRerun(r, "GATE");
  });
  test("R3 red on stable too is inherited", () => {
    const f = fixture();
    write(f.dir, { "gate/candidate-e2e.log": `boom\nEXIT 1 @${f.head}\n`, "gate/stable-e2e.log": "boom\nEXIT 1 @0000000\n" });
    assertClean(f.ready("--stamp"), "ready");
  });
  test("R3 carries over a commit touching only release files", () => {
    const f = fixture();
    f.advance({ "CHANGELOG.md": "# Changelog\n\n## Releases\n" }, ["notes", "rehearse"]);
    assertClean(f.ready("--stamp"), "ready");
  });
  test("R3 does not carry over a commit outside the release files", () => {
    const f = fixture();
    f.advance({ "docs/x.md": "x2\n" }, ["notes", "rehearse"]);
    const r = f.ready("--stamp");
    assertFail(r, "R3");
    assert.doesNotMatch(r.out, /^FAIL R[1245]/m, r.out);
  });
  test("R4 a machine not CLEAN", () => {
    const f = fixture();
    write(f.dir, { "rehearsal/behind-1/result.json": JSON.stringify({ verdict: "FRICTION" }) });
    const r = f.ready("--stamp");
    assertFail(r, "R4");
    assertRerun(r, "REHEARSE");
  });
  test("R4 carries over a change inside ## Verification", () => {
    const f = fixture();
    f.advance({ "releases/v0.78.0.md": NOTE.replace("The gate green on three lanes.\n", "The gate green on three lanes at the release commit.\nRehearsal: two machines, one round.\n") }, ["notes"]);
    assertClean(f.ready("--stamp"), "ready");
  });
  test("R4 does not carry over a deletion outside ## Verification", () => {
    const f = fixture();
    f.advance({ "releases/v0.78.0.md": NOTE.replace("- Global: skills/qux — a new qux skill\n", "") }, ["notes"]);
    const r = f.ready("--stamp");
    assertFail(r, "R4");
    assert.match(r.out, /^FAIL R4: releases\/v0\.78\.0\.md:\d+ — @@ /m, r.out);
  });
  test("R4 a non-note path changes since the rehearsed commit", () => {
    const f = fixture();
    const next = f.advance({ "docs/x.md": "x2\n" }, ["notes"]);
    write(f.dir, {
      "gate/candidate-templates.log": `ok\nEXIT 0 @${next}\n`,
      "gate/candidate-pfm.log": `ok\nEXIT 0 @${next}\n`,
      "gate/candidate-e2e.log": `ok\nEXIT 0 @${next}\n`,
    });
    const r = f.ready("--stamp");
    assertFail(r, "R4");
    assert.doesNotMatch(r.out, /^FAIL R[123]/m, r.out);
    assert.match(r.out, /^FAIL R4: docs\/x\.md — changed since the rehearsed [0-9a-f]{7}; only releases\/v0\.78\.0\.md § Verification carries the rehearsal over$/m, r.out);
    assertRerun(r, "REHEARSE");
  });
  test("R4 rehearsal.md line one is malformed", () => {
    const f = fixture();
    write(f.dir, { "rehearsal.md": "not the right format\n" });
    const r = f.ready("--stamp");
    assertFail(r, "R4");
    assert.match(r.out, /^FAIL R4: rehearsal\.md — line one is not `REHEARSAL CLEAN \{sha\} round \{n\}`: "not the right format"$/m, r.out);
    assertRerun(r, "REHEARSE");
  });
  const RULED_NOTE = NOTE.replace("The gate green on three lanes.\n", "The gate green on three lanes.\nREHEARSAL RULED round 7 — the owner ruled to ship without a CLEAN rehearsal.\n");
  const ruled = (f, sha, ruling = " — the owner ruled to ship without a CLEAN rehearsal") =>
    write(f.dir, {
      "rehearsal.md": `REHEARSAL RULED ${sha} round 7${ruling}\n`,
      "rehearsal/main-7/result.json": JSON.stringify({ verdict: "FRICTION" }),
      "rehearsal/behind-7/result.json": JSON.stringify({ verdict: "FRICTION" }),
    });
  test("R4 RULED with the ruling in ## Verification passes without CLEAN verdicts", () => {
    const f = fixture();
    f.advance({ "releases/v0.78.0.md": RULED_NOTE }, ["notes"]);
    ruled(f, f.head);
    assertClean(f.ready("--stamp"), "ready");
  });
  test("R4 RULED without the ruling in ## Verification", () => {
    const f = fixture();
    ruled(f, f.head);
    const r = f.ready("--stamp");
    assertFail(r, "R4");
    assert.match(r.out, /^FAIL R4: releases\/v0\.78\.0\.md — `## Verification` holds no line with `REHEARSAL RULED` and round 7; the ruling ships in the note$/m, r.out);
    assertRerun(r, "REHEARSE");
  });
  test("R4 RULED against a note with no ## Verification section", () => {
    const f = fixture();
    f.advance({ "releases/v0.78.0.md": NOTE.replace(/\n## Verification\n[\s\S]*$/, "\n") }, ["notes"]);
    ruled(f, f.head);
    const r = f.ready("--stamp");
    assertFail(r, "R4");
    assert.match(r.out, /^FAIL R4: releases\/v0\.78\.0\.md — holds no `## Verification` section; the ruling ships in it$/m, r.out);
  });
  test("R4 RULED line with a hyphen instead of an em dash", () => {
    const f = fixture();
    f.advance({ "releases/v0.78.0.md": RULED_NOTE }, ["notes"]);
    ruled(f, f.head, " - the owner ruled to ship");
    const r = f.ready("--stamp");
    assertFail(r, "R4");
    assert.match(r.out, /^FAIL R4: rehearsal\.md — line one is not `REHEARSAL RULED \{sha\} round \{n\} — \{ruling\}`: "REHEARSAL RULED [0-9a-f]+ round 7 - the owner ruled to ship"$/m, r.out);
  });
  test("R4 RULED with empty ruling text", () => {
    const f = fixture();
    f.advance({ "releases/v0.78.0.md": RULED_NOTE }, ["notes"]);
    ruled(f, f.head, " — ");
    const r = f.ready("--stamp");
    assertFail(r, "R4");
    assert.match(r.out, /^FAIL R4: rehearsal\.md — `REHEARSAL RULED \{sha\} round \{n\} — \{ruling\}` carries no ruling text$/m, r.out);
  });
  test("R4 RULED at a stale sha with a change outside ## Verification", () => {
    const f = fixture();
    const next = f.advance({ "docs/x.md": "x2\n", "releases/v0.78.0.md": RULED_NOTE }, ["notes"]);
    write(f.dir, {
      "gate/candidate-templates.log": `ok\nEXIT 0 @${next}\n`,
      "gate/candidate-pfm.log": `ok\nEXIT 0 @${next}\n`,
      "gate/candidate-e2e.log": `ok\nEXIT 0 @${next}\n`,
    });
    ruled(f, f.head);
    const r = f.ready("--stamp");
    assertFail(r, "R4");
    assert.doesNotMatch(r.out, /^FAIL R[123]/m, r.out);
    assert.doesNotMatch(r.out, /^FAIL R4: (rehearsal\.md|releases\/)/m, r.out);
    assert.match(r.out, /^FAIL R4: docs\/x\.md — changed since the rehearsed [0-9a-f]{7}; only releases\/v0\.78\.0\.md § Verification carries the rehearsal over$/m, r.out);
  });
  test("R5 notes check not PASS at HEAD", () => {
    const f = fixture();
    write(f.dir, { "notes/check.txt": `NOTES FAIL ${f.head}\n` });
    const r = f.ready("--stamp");
    assertFail(r, "R5");
    assertRerun(r, "NOTES");
  });
  test("R5 a record that ran without every rule group", () => {
    const f = fixture();
    write(f.dir, { "notes/check.txt": `NOTES PASS ${f.head}\n` });
    const r = f.ready("--stamp");
    assertFail(r, "R5");
    assertRerun(r, "NOTES");
  });
  test("R5 an uncommitted change to a tracked file", () => {
    const f = fixture();
    write(f.wt, { "docs/x.md": "edited, not committed\n" });
    const r = f.ready("--stamp");
    assertFail(r, "R5");
    assert.match(r.out, /^FAIL R5: .* — uncommitted tracked change: M docs\/x\.md$/m, r.out);
    assert.doesNotMatch(r.out, /^FAIL R[12346]/m, r.out);
  });
  test("R5 HEAD's VERSION is not X.Y.Z, and nothing is stamped", () => {
    const f = fixture();
    f.advance({ VERSION: "0.79.0-alpha\n" }, ["notes", "rehearse"]);
    const r = f.ready("--stamp");
    assertFail(r, "R5");
    assert.match(r.out, /^FAIL R5: VERSION — HEAD's VERSION "0\.79\.0-alpha" is not X\.Y\.Z$/m, r.out);
    assert.doesNotMatch(r.out, /STAMPED READY/, r.out);
  });
  test("R5 the note HEAD's VERSION names is absent at HEAD", () => {
    const f = fixture();
    f.advance({ VERSION: "0.79.0\n" }, ["notes", "rehearse"]);
    const r = f.ready("--stamp");
    assertFail(r, "R5");
    assert.match(r.out, /^FAIL R5: releases\/v0\.79\.0\.md — absent at HEAD/m, r.out);
  });
  test("R6 READY absent without --stamp", () => {
    const f = fixture();
    const r = f.ready();
    assertFail(r, "R6");
    assertRerun(r, "READY");
  });
  test("R6 READY names a commit other than HEAD", () => {
    const f = fixture();
    write(f.dir, { READY: "0000000 v0.78.0 2026-01-01\n" });
    const r = f.ready();
    assertFail(r, "R6");
    assert.match(r.out, /^FAIL R6: READY — names 0000000, not HEAD [0-9a-f]{40}$/m, r.out);
    assertRerun(r, "READY");
  });
  test("ERROR, not FAIL, when the release directory does not exist", () => {
    const f = fixture();
    assertError(run(f.wt, "ready", join(f.dir, "absent"), "--worktree", f.wt), /release directory .*absent does not exist/);
    assertError(run(f.wt, "ready", f.dir), /usage: ready needs --worktree/);
  });
});
