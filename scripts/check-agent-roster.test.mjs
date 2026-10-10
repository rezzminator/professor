// Exercise the roster checker against mirrors and effective config from the same compiler.
import { before, after, test } from "node:test";
import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import { copyFileSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const ROOT = dirname(dirname(fileURLToPath(import.meta.url)));
const scratch = mkdtempSync(join(tmpdir(), "agent-roster-tests-"));
let binary = process.env.ENGINE_SYNC_BIN;
before(() => {
  if (binary) return;
  binary = join(scratch, "engine-sync");
  execFileSync("bash", ["-c", 'source "$1"; engine_sync_build "$2"', "_",
    join(ROOT, "infra/fence/engine-sync.sh"), binary], { stdio: "pipe" });
});
after(() => rmSync(scratch, { recursive: true, force: true }));

function fixture(t, codex = {}, project = { version: 1 }, machine = {}, files = {}, allowBuildFinding = false) {
  const root = mkdtempSync(join(scratch, "project-"));
  const home = join(root, "home");
  const configHome = join(home, ".config");
  const put = (path, text) => {
    mkdirSync(dirname(join(root, path)), { recursive: true });
    writeFileSync(join(root, path), text);
  };
  put("CLAUDE.md", "# Fixture\n");
  put(".claude/agents/alpha.md", "---\nname: alpha\ndescription: Fixture role\nmodel: smart\n---\nRead the fixture.\n");
  put("demo-child/CLAUDE.md", "# Child\n");
  put("demo-child/.claude/agents/beta.md", "---\nname: beta\ndescription: Fixture role\nmodel: smart\n---\nRead the child.\n");
  for (const [path, text] of Object.entries(files)) put(path, text);
  if (project !== null) put(".claude/engine-sync.json", JSON.stringify(project));
  put("home/.config/engine-sync/config.json", JSON.stringify({ version: 1, codex, ...machine }));
  mkdirSync(join(root, "scripts"));
  copyFileSync(join(ROOT, "scripts/check-agent-roster.mjs"), join(root, "scripts/check-agent-roster.mjs"));
  const env = { ...process.env, HOME: home, XDG_CONFIG_HOME: configHome, ENGINE_SYNC_BIN: binary };
  for (const key of ["CLAUDE_CONFIG_DIR", "CODEX_HOME", "ENGINE_SYNC_HOME", "ENGINE_SYNC_STATE", "ENGINE_SYNC_WORKBENCH_MARKER",
    "XDG_STATE_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME"])
    delete env[key];
  const built = spawnSync(binary, ["build", "."], { cwd: root, env, encoding: "utf8" });
  if (!allowBuildFinding) assert.equal(built.status, 0, built.stdout + built.stderr);
  t.after(() => rmSync(root, { recursive: true, force: true }));
  return { root, env, put };
}

function check({ root, env }) {
  const result = spawnSync(process.execPath, [join(root, "scripts/check-agent-roster.mjs")], {
    cwd: root, env, encoding: "utf8",
  });
  return { status: result.status, out: result.stdout + result.stderr };
}

for (const [name, codex, project, codexCount, openCodeCount, machine, files] of [
  ["machine suffixMode none with no project override", { suffixMode: "none" }, { version: 1, codex: {} }, 2, 2],
  ["machine excludeProjects with no project override", { excludeProjects: ["demo-child"] }, { version: 1, codex: {} }, 1, 1],
  ["project with no codex key uses effective defaults", {}, { version: 1 }, 2, 2],
  ["absent project config uses effective defaults", {}, null, 2, 2],
  ["machine suffixPrefix strips the project prefix", { suffixMode: "strip-prefix", suffixPrefix: "demo-" }, { version: 1, codex: {} }, 2, 2],
  ["machine projects list of none builds the root alone", { projects: [] }, { version: 1, codex: {} }, 1, 1],
  ["compiler applies exclusions to an explicit project list", { projects: ["demo-child"], excludeProjects: ["demo-child"] }, { version: 1, codex: {} }, 1, 1],
  ["compiler shares neverRegister across both runtimes", { neverRegister: ["beta"] }, { version: 1, codex: {} }, 1, 1],
  ["machine workbenchMarker child builds as its own root", {}, { version: 1, codex: {} }, 1, 1,
    { workbenchMarker: ".bench/workbench.json" }, { "demo-child/.bench/workbench.json": "{}\n" }],
  ["compiler discovers a sibling role beside SKILL.md", {}, { version: 1 }, 3, 2, {},
    { ".claude/agents/skill/SKILL.md": "---\nname: skill\ndescription: Fixture skill\n---\nUse the skill.\n",
      ".claude/agents/skill/helper.md": "---\nname: helper\ndescription: Fixture role\n---\nHelp.\n" }],
  ["README is no role; Codex reads nested roles, OpenCode the top level", {}, { version: 1, codex: {} }, 3, 2, {},
    { ".claude/agents/README.md": "# Roles\n",
      ".claude/agents/team/gamma.md": "---\nname: gamma\ndescription: Fixture role\nmodel: smart\n---\nRead the team.\n" }],
]) {
  test(name, (t) => {
    const result = check(fixture(t, codex, project, machine, files));
    assert.equal(result.status, 0, result.out);
    assert.match(result.out, new RegExp(`agent-roster: clean — ${codexCount} Codex and ${openCodeCount} OpenCode agents match source`));
  });
}

test("unrunnable binary is a named config ERROR", (t) => {
  const f = fixture(t);
  f.env.ENGINE_SYNC_BIN = join(f.root, "missing-engine-sync");
  const result = check(f);
  assert.equal(result.status, 2, result.out);
  assert.match(result.out, /agent-roster: ERROR/);
  assert.ok(result.out.includes(`${f.env.ENGINE_SYNC_BIN} check --json .`), result.out);
  assert.match(result.out, /ENOENT/);
});

test("compiler failure is a named ERROR", (t) => {
  const f = fixture(t);
  f.put(".claude/engine-sync.json", JSON.stringify({ version: 1, codex: { suffixMode: "invalid" } }));
  const result = check(f);
  assert.equal(result.status, 2, result.out);
  assert.match(result.out, /agent-roster: ERROR/);
  assert.ok(result.out.includes(`${binary} check --json .`), result.out);
  assert.match(result.out, /suffixMode/);
});

test("malformed config JSON is a named config ERROR", (t) => {
  const f = fixture(t);
  f.put("invalid-json", '#!/bin/sh\nprintf "not-json\\n"\n');
  execFileSync("chmod", ["+x", join(f.root, "invalid-json")]);
  f.env.ENGINE_SYNC_BIN = join(f.root, "invalid-json");
  const result = check(f);
  assert.equal(result.status, 2, result.out);
  assert.match(result.out, /agent-roster: ERROR/);
  assert.ok(result.out.includes(`${f.env.ENGINE_SYNC_BIN} check --json .`), result.out);
  assert.match(result.out, /JSON/);
});

test("a check that crashes is a named ERROR, not a roster mismatch", (t) => {
  const f = fixture(t);
  rmSync(join(f.root, "demo-child/.claude/agents"), { recursive: true });
  f.put("demo-child/.claude/agents", "not a directory\n");
  const result = check(f);
  assert.equal(result.status, 1, result.out);
  assert.match(result.out, /compiler:.*(not a directory|ENOTDIR)/);
});

for (const [name, stdout, exit, message] of [
  ["compiler ok true with a nonzero exit is a named ERROR",
    JSON.stringify({ ok: true, problems: [] }),
    1, /exit 1, signal none/],
  ["compiler reply without problem fields is a named ERROR",
    JSON.stringify({ ok: true }), 0, /compiler check has invalid ok or problems/],
]) {
  test(name, (t) => {
    const f = fixture(t);
    f.put("stub-engine-sync", `#!/bin/sh\nprintf '%s\\n' '${stdout}'\nexit ${exit}\n`);
    execFileSync("chmod", ["+x", join(f.root, "stub-engine-sync")]);
    f.env.ENGINE_SYNC_BIN = join(f.root, "stub-engine-sync");
    const result = check(f);
    assert.equal(result.status, 2, result.out);
    assert.ok(result.out.includes(`agent-roster: ERROR — ${f.env.ENGINE_SYNC_BIN} check --json .`), result.out);
    assert.match(result.out, message);
  });
}

test("a non-regular workbench marker is a named compiler error", (t) => {
  const f = fixture(t, {}, { version: 1 }, { workbenchMarker: ".bench/workbench.json" });
  mkdirSync(join(f.root, "demo-child/.bench/workbench.json"), { recursive: true });
  const result = check(f);
  assert.equal(result.status, 1, result.out);
  assert.match(result.out, /not a regular file/);
});

test("empty stripped suffix forwards the compiler's own finding", (t) => {
  const f = fixture(t, { suffixMode: "strip-prefix", suffixPrefix: "demo-child" }, { version: 1 }, {}, {}, true);
  mkdirSync(join(f.root, ".codex/agents"), { recursive: true });
  mkdirSync(join(f.root, ".opencode/agent"), { recursive: true });
  const result = check(f);
  assert.equal(result.status, 1, result.out);
  assert.match(result.out, /compiler:.*empty agent suffix/);
});
