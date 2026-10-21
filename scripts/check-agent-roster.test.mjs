// Exercise the roster checker against mirrors and effective config from the same compiler.
import { cpSync, lstatSync, readdirSync, realpathSync } from "node:fs";
import { before, after, test, assert, execFileSync, spawnSync, copyFileSync, existsSync, mkdirSync, mkdtempSync,
  readFileSync, rmSync, symlinkSync, unlinkSync, writeFileSync, tmpdir, dirname, join, fileURLToPath } from "./node-test-helpers.mjs";

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
  put("home/.claude/engine-sync.json", JSON.stringify({ version: 1, codex, ...machine }));
  mkdirSync(join(root, "scripts"));
  copyFileSync(process.env.ROSTER_CHECK_SUT || join(ROOT, "scripts/check-agent-roster.mjs"), join(root, "scripts/check-agent-roster.mjs"));
  if (existsSync(join(ROOT, "scripts/agent-source-inventory.py")))
    copyFileSync(join(ROOT, "scripts/agent-source-inventory.py"), join(root, "scripts/agent-source-inventory.py"));
  const env = { ...process.env, HOME: home, ENGINE_SYNC_BIN: binary };
  for (const key of ["CLAUDE_CONFIG_DIR", "CODEX_HOME", "ENGINE_SYNC_HOME", "ENGINE_SYNC_STATE", "ENGINE_SYNC_WORKBENCH_MARKER",
    "XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME"])
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
  ["neverRegister names a root role neither runtime emits", { neverRegister: ["alpha"] }, { version: 1, codex: {} }, 1, 1],
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

// Eligibility is read from source headers, never inferred from the emitted roster.
for (const [scope, directory] of [
  ['root', '.claude/agents'], ['child', 'demo-child/.claude/agents'],
  ['nested', '.claude/agents/team'],
]) {
  for (const [name, header, capabilities, codexExtra, openCodeExtra] of [
    ['Codex excluded', 'engines:\n  codex: false', [], 0, 1],
    ['OpenCode excluded', 'engines:\n  opencode: false', [], 1, 0],
    ['both excluded', 'engines:\n  codex: false\n  opencode: false', [], 0, 0],
    ['engine settings stay eligible', 'engines:\n  codex:\n    model: gpt-6-luna\n  opencode:\n    model: openai/gpt-6-luna', [], 1, 1],
    ['unmet requirement', 'requires: [gpt-agents]', [], 0, 0],
    ['all requirements needed', 'requires: [gpt-agents, another]', ['gpt-agents'], 0, 0],
    ['requirements met and trimmed', 'requires: [" gpt-agents ", another]', [' gpt-agents ', 'another'], 1, 1],
    ['numeric requirement met', 'requires: [123]', ['123'], 1, 1],
    ['numeric requirement unmet', 'requires: [123]', [], 0, 0],
    ['boolean requirement text', 'requires: [true]', ['true'], 1, 1],
    ['no requirements', 'requires: []', [], 1, 1],
    ['requirements met but engine excluded', 'requires: [gpt-agents]\nengines:\n  codex: false\n  opencode: false', ['gpt-agents'], 0, 0],
  ]) {
    test(`source eligibility ${scope}: ${name}`, (t) => {
      const f = fixture(t, {}, { version: 1 }, { targets: { capabilities } }, {
        [`${directory}/eligible.md`]: `---\nname: eligible\ndescription: Eligibility fixture\nmodel: smart\n${header}\n---\nRead.\n`,
      });
      const result = check(f);
      assert.equal(result.status, 0, result.out);
      assert.ok(result.out.includes(`${2 + codexExtra} Codex and ${2 + (scope === 'nested' ? 0 : openCodeExtra)} OpenCode agents match source`), result.out);
    });
  }
  for (const [runtime, extension, mirror] of [
    ['codex', '.toml', '.codex/agents'], ['opencode', '.md', '.opencode/agent'],
  ]) {
    if (scope === 'nested' && runtime === 'opencode') continue;
    test(`missing eligible ${runtime} ${scope} output fails despite clean compiler`, (t) => {
      const f = fixture(t, {}, { version: 1 }, { targets: { capabilities: ['gpt-agents'] } }, {
        [`${directory}/eligible.md`]: '---\nname: eligible\ndescription: Eligible role\nmodel: smart\nrequires: [gpt-agents]\nengines:\n  codex: {}\n  opencode: {}\n---\nRead.\n',
      });
      const name = `eligible${scope === 'child' ? '-demo-child' : ''}${extension}`;
      unlinkSync(join(f.root, mirror, name));
      cleanVerdictStub(f);
      const result = check(f);
      assert.equal(result.status, 1, result.out);
      assert.ok(result.out.includes(`${runtime}: source agent missing from generated roster: ${name}`), result.out);
      assert.doesNotMatch(result.out, /agent-roster: clean/);
    });
  }
}

for (const header of ['engines:\n  codex: false\n  opencode: false', 'requires: [gpt-agents]']) {
  test(`enumerated sources can all be intentionally ineligible: ${header.split(':')[0]}`, (t) => {
    const f = fixture(t, {}, { version: 1 }, {}, {
      '.claude/agents/alpha.md': `---\nname: alpha\nmodel: smart\n${header}\n---\nRead.\n`,
      'demo-child/.claude/agents/beta.md': `---\nname: beta\nmodel: smart\n${header}\n---\nRead.\n`,
    });
    mkdirSync(join(f.root, '.codex/agents'), { recursive: true });
    mkdirSync(join(f.root, '.opencode/agent'), { recursive: true });
    const result = check(f);
    assert.equal(result.status, 0, result.out);
    assert.match(result.out, /0 Codex and 0 OpenCode agents match source/);
  });
}

for (const capabilities of [[], ['gpt-agents']]) {
  test(`five shipped local GPT registrations respect engine and capability policy: ${capabilities.length ? 'enabled' : 'disabled'}`, (t) => {
    const files = Object.fromEntries(['gpt-changelogger', 'gpt-gitter', 'gpt-pcm', 'gpt-releaser', 'gpt-themer'].map((name) => {
      const path = `.claude/agents/${name}.md`;
      return [path, readFileSync(join(ROOT, path), 'utf8')];
    }));
    const result = check(fixture(t, {}, { version: 1 }, { targets: { capabilities } }, files));
    assert.equal(result.status, 0, result.out);
    assert.ok(result.out.includes(`${2 + capabilities.length} Codex and ${2 + capabilities.length} OpenCode agents match source`), result.out);
  });
}

for (const [name, path, header, status, message] of [
  ["project from", ".claude/agents/local.md", "from: general-purpose", 5, /SOURCE REJECTED — python3 .*machine scope only/],
  ["project abstract", ".claude/agents/local.md", "abstract: true", 5, /SOURCE REJECTED — python3 .*machine scope only/],
  ["abstract retired", "templates/global/agents/layer.md", "abstract: true", 5, /SOURCE REJECTED — python3 .*abstract is retired/],
  ["quoted declaration key", "templates/global/agents/derived.md", '"from": general-purpose\nmodel: "smart"', 0, /agent-roster: clean/],
]) {
  test(`machine declaration headers: ${name}`, (t) => {
    const result = check(fixture(t, {}, {version: 1}, {}, {
      [path]: `---\nname: ${path.endsWith('layer.md') ? 'layer' : path.endsWith('derived.md') ? 'derived' : 'local'}\ndescription: Fixture role\n${header}\n---\nFixture body.\n`,
    }, true));
    assert.equal(result.status, status, result.out);
    assert.match(result.out, message);
  });
}

test("inventory and model failures retain their classification before mirror checks", async (t) => {
  for (const [name, header, missingMirrors, status, message] of [
    ["original inline comment", "model: fable # note", false, 1, /frontmatter model "fable # note" is not one bare alias/],
    ["declaration null", "from: general-purpose\nmodel: null", false, 1, /frontmatter model "null" is not one bare alias/],
    ["declaration number", "from: general-purpose\nmodel: 42", false, 1, /frontmatter model "42" is not one bare alias/],
    ["declaration list", "from: general-purpose\nmodel: [smart, fable]", false, 1, /frontmatter model "smart,fable" is not one bare alias/],
    ["model failure without mirrors", "model: fable # note", true, 3, /frontmatter model "fable # note" is not one bare alias/],
    ["no model lines without mirrors", "", true, 3, /agent model check read no model line .* the check did not run/],
  ]) {
    await t.test(name, (t) => {
      const f = fixture(t, {}, { version: 1 }, {}, {}, true);
      const declaration = header.startsWith("from:");
      const path = declaration ? "templates/global/agents/derived.md" : ".claude/agents/alpha.md";
      f.put(path, `---\nname: ${declaration ? 'derived' : 'alpha'}\ndescription: Fixture role\n${header}\n---\nRead.\n`);
      f.put("demo-child/.claude/agents/beta.md", "---\nname: beta\ndescription: Fixture role\n---\nRead.\n");
      cleanVerdictStub(f);
      if (missingMirrors) {
        rmSync(join(f.root, ".codex/agents"), { recursive: true });
        rmSync(join(f.root, ".opencode/agent"), { recursive: true });
      }
      const result = check(f);
      assert.equal(result.status, status, result.out);
      assert.match(result.out, message);
      if (missingMirrors) assert.match(result.out, /agent-roster: NOT GENERATED/);
    });
  }
  await t.test("inventory PyYAML gap", (t) => {
    const f = fixture(t);
    f.put("bin/python3", "#!/bin/sh\nprintf '%s\\n' 'GAP install (agent source inventory is missing: PyYAML)' >&2\nexit 3\n");
    execFileSync("chmod", ["+x", join(f.root, "bin/python3")]);
    f.env.PATH = `${join(f.root, "bin")}:${f.env.PATH}`;
    const result = check(f);
    assert.equal(result.status, 4, result.out);
    assert.match(result.out, /agent-roster: GAP install .*PyYAML/);
  });
});

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

test("an inventory lookup failure is a named ERROR, not a rejected source", (t) => {
  const f = fixture(t);
  symlinkSync("vanished-source.md", join(f.root, ".claude/agents/dangling.md"));
  const result = check(f);
  assert.equal(result.status, 2, result.out);
  assert.match(result.out, /agent-roster: ERROR — python3 .*agent-source-inventory: ERROR/);
  assert.match(result.out, /dangling\.md/);
});

test("an inventory that crashes with a traceback is a named ERROR, not a rejected source", (t) => {
  const f = fixture(t);
  f.put("scripts/agent-source-inventory.py", "raise RuntimeError('invented inventory crash')\n");
  const result = check(f);
  assert.equal(result.status, 2, result.out);
  assert.match(result.out, /agent-roster: ERROR — python3 [^\n]*Traceback[\s\S]*invented inventory crash/);
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

// The judge is never the thing being judged: a stub that reports its own check clean (as an
// engine-sync with a source-discovery defect would) while every other verb is the real binary.
function cleanVerdictStub(f) {
  f.put("clean-engine-sync", `#!/bin/sh\nif [ "$1" = check ]; then printf '%s\\n' '{"ok":true,"problems":[]}'; exit 0; fi\nexec "${binary}" "$@"\n`);
  execFileSync("chmod", ["+x", join(f.root, "clean-engine-sync")]);
  f.env.ENGINE_SYNC_BIN = join(f.root, "clean-engine-sync");
}

for (const [runtime, corrupt] of [
  ['opencode', '---\nname: gpt-gitter\npermission:\n  bash:\n    "git *": deny\n---\nThe body says "git *": allow, but the permission denies it.\n'],
  ...['read-only', 'workspace-write', 'unknown'].map((mode) => ['codex', `name = "gpt-gitter"\nsandbox_mode = "${mode}"\n`]),
]) {
  test(`structured writer permission corruption fails despite clean compiler: ${runtime} ${corrupt}`, (t) => {
    const source = '---\nname: gpt-gitter\ndescription: Writer fixture\nmodel: smart\nengines:\n  codex:\n    sandbox: inherit\n  opencode:\n    permission:\n      bash:\n        "git *": allow\n---\nWrite git.\n';
    const f = fixture(t, {}, { version: 1 }, {}, { '.claude/agents/gpt-gitter.md': source });
    f.put(runtime === 'codex' ? '.codex/agents/gpt-gitter.toml' : '.opencode/agent/gpt-gitter.md', corrupt);
    cleanVerdictStub(f);
    const result = check(f);
    assert.equal(result.status, 1, result.out);
    assert.match(result.out, new RegExp(`${runtime}: gpt-gitter`));
  });
}

test("a role with no generated Codex twin fails although engine-sync's check reports clean", (t) => {
  const f = fixture(t);
  unlinkSync(join(f.root, ".codex/agents/alpha.toml"));
  cleanVerdictStub(f);
  const result = check(f);
  assert.equal(result.status, 1, result.out);
  assert.match(result.out, /agent-roster: codex: source agent missing from generated roster: alpha\.toml/);
  assert.doesNotMatch(result.out, /agent-roster: clean/);
});

test("a role with no generated OpenCode twin fails although engine-sync's check reports clean", (t) => {
  const f = fixture(t);
  unlinkSync(join(f.root, ".opencode/agent/beta-demo-child.md"));
  cleanVerdictStub(f);
  const result = check(f);
  assert.equal(result.status, 1, result.out);
  assert.match(result.out, /agent-roster: opencode: source agent missing from generated roster: beta-demo-child\.md/);
});

test("a generated role with no source fails although engine-sync's check reports clean", (t) => {
  const f = fixture(t);
  f.put(".opencode/agent/ghost.md", "ghost\n");
  f.put(".codex/agents/ghost.toml", "ghost\n");
  cleanVerdictStub(f);
  const result = check(f);
  assert.equal(result.status, 1, result.out);
  assert.match(result.out, /agent-roster: codex: generated agent has no source: ghost\.toml/);
  assert.match(result.out, /agent-roster: opencode: generated agent has no source: ghost\.md/);
});

test("sources that define no agent name what the comparison could not do", (t) => {
  const f = fixture(t);
  rmSync(join(f.root, ".claude/agents"), { recursive: true });
  rmSync(join(f.root, "demo-child/.claude/agents"), { recursive: true });
  mkdirSync(join(f.root, ".claude/agents"), { recursive: true });
  mkdirSync(join(f.root, "demo-child/.claude/agents"), { recursive: true });
  rmSync(join(f.root, ".codex/agents"), { recursive: true });
  rmSync(join(f.root, ".opencode/agent"), { recursive: true });
  mkdirSync(join(f.root, ".codex/agents"), { recursive: true });
  mkdirSync(join(f.root, ".opencode/agent"), { recursive: true });
  cleanVerdictStub(f);
  const result = check(f);
  assert.equal(result.status, 1, result.out);
  assert.match(result.out, /codex: the sources under \.claude\/agents \(root and child projects\) define no registered agent/);
  assert.match(result.out, /opencode: the sources under \.claude\/agents \(root and child projects\) define no registered agent/);
});

for (const writer of ['gitter', 'gpt-gitter']) {
  const source = `---\nname: ${writer}\ndescription: Fixture Git writer\nmodel: smart\ntools: Read, Write, Bash\nengines:\n  codex:\n    sandbox: inherit\n  opencode:\n    permission:\n      bash:\n        "git *": allow\n---\nPerform the assigned Git phase.\n`;
  test(`${writer} emitted permissions satisfy the local roster check`, (t) => {
    const f = fixture(t, {}, { version: 1 }, {}, { [`.claude/agents/${writer}.md`]: source });
    const result = check(f);
    assert.equal(result.status, 0, result.out);
    assert.match(result.out, /agent-roster: clean/);
  });
  test(`${writer} read-only Codex artifact fails despite a clean compiler verdict`, (t) => {
    const f = fixture(t, {}, { version: 1 }, {}, { [`.claude/agents/${writer}.md`]: source });
    f.put(`.codex/agents/${writer}.toml`, 'sandbox_mode = "read-only"\n');
    cleanVerdictStub(f);
    const result = check(f);
    assert.equal(result.status, 1, result.out);
    assert.ok(result.out.includes(`codex: ${writer}.toml sandbox override differs from its source protocol (expected inherit)`), result.out);
    assert.doesNotMatch(result.out, /agent-roster: clean/);
  });
  test(`${writer} missing OpenCode Git permission fails despite a clean compiler verdict`, (t) => {
    const f = fixture(t, {}, { version: 1 }, {}, { [`.claude/agents/${writer}.md`]: source });
    f.put(`.opencode/agent/${writer}.md`, '---\npermission:\n  bash:\n    "git *": deny\n---\nRead only.\n');
    cleanVerdictStub(f);
    const result = check(f);
    assert.equal(result.status, 1, result.out);
    assert.ok(result.out.includes(`opencode: ${writer}.md lacks its source per-agent git write permission`), result.out);
    assert.doesNotMatch(result.out, /agent-roster: clean/);
  });
}

// Only ENOENT and ENOTDIR mean a path is absent. Any other lookup error (neither is a permission error
// root would bypass: a symlink loop and an over-long name fail for every user) is a named failure.
test("a child whose CLAUDE.md lookup fails is a named failure, not an absent project", (t) => {
  const f = fixture(t);
  unlinkSync(join(f.root, "demo-child/CLAUDE.md"));
  symlinkSync("CLAUDE.md", join(f.root, "demo-child/CLAUDE.md"));
  cleanVerdictStub(f);
  const result = check(f);
  assert.equal(result.status, 1, result.out);
  assert.match(result.out, /agent-roster: cannot stat .*demo-child\/CLAUDE\.md: ELOOP/);
  assert.doesNotMatch(result.out, /agent-roster: clean/);
});

test("a workbench marker lookup that fails is a named failure, not an absent marker", (t) => {
  const f = fixture(t);
  mkdirSync(join(f.root, "demo-child/.bench"));
  f.put("home/.claude/engine-sync.json", JSON.stringify({ version: 1, workbenchMarker: `.bench/${"x".repeat(300)}` }));
  cleanVerdictStub(f);
  const result = check(f);
  assert.equal(result.status, 1, result.out);
  assert.match(result.out, /agent-roster: cannot stat .*demo-child\/\.bench\/x+: ENAMETOOLONG/);
  assert.doesNotMatch(result.out, /agent-roster: clean/);
});

test("a missing CLAUDE.md or workbench marker is plain absence, not a failure", (t) => {
  const f = fixture(t, {}, { version: 1 }, { workbenchMarker: ".bench/workbench.json" });
  const result = check(f);
  assert.equal(result.status, 0, result.out);
  assert.doesNotMatch(result.out, /cannot stat/);
});

// The compiler follows directory symlinks; the independent reader must agree
// from filesystem evidence, not from the compiler's reported source roster.
test("a symlinked source directory is independently enumerated", (t) => {
  const f = fixture(t, {}, { version: 1 }, {}, {
    "shared/gamma.md": "---\nname: gamma\ndescription: Fixture role\nmodel: smart\n---\nRead the shared role.\n",
  });
  symlinkSync("../../shared", join(f.root, ".claude/agents/shared"));
  const built = spawnSync(binary, ["build", "."], { cwd: f.root, env: f.env, encoding: "utf8" });
  assert.equal(built.status, 0, built.stdout + built.stderr);
  const result = check(f);
  assert.equal(result.status, 0, result.out);
  assert.match(result.out, /3 Codex and 2 OpenCode agents match source/);
});

for (const sourceDir of [".claude/agents", "demo-child/.claude/agents"]) {
  test(`a cyclic agent source directory fails both compiler and roster (${sourceDir})`, (t) => {
    const f = fixture(t);
    symlinkSync(".", join(f.root, sourceDir, "cycle"));
    const built = spawnSync(binary, ["build", "."], { cwd: f.root, env: f.env, encoding: "utf8" });
    // Codex skips the cycle with a warning, but OpenCode promotes the same warning to a problem.
    assert.equal(built.status, 1, built.stdout + built.stderr);
    assert.match(built.stdout + built.stderr, /command source symlink cycle: skip cyclic command directory .*\/cycle/);
    const result = check(f);
    assert.equal(result.status, 1, result.out);
    assert.match(result.out, /agent-roster: compiler: .*command source symlink cycle: skip cyclic command directory .*\/cycle/);
    assert.match(result.out, /agent-roster: cyclic agent source directory .*\/cycle/);
  });
}

// The GPT twins are derived agents: their machine-scoped .md declarations name their base, engine-sync renders
// them, and a host that opts in with --capability gpt-agents links them. The properties below are therefore
// pinned against what engine-sync RENDERS from the shipped declaration, never against a source file.
const CAPABILITY = 'gpt-agents';
const gitterEngines = {
  codex: { model: 'gpt-6-luna', sandbox: 'inherit' },
  opencode: { model: 'openai/gpt-6.1-sol-fast', permission: { bash: { 'git *': 'allow' } } },
};
const twins = [
  ['gpt-foreman', 'gpt-6.1-sol', 'high', 'flights-foreman'],
  ['gpt-flights-lander', 'gpt-6.1-sol', 'high', 'flights-lander'],
  ['gpt-reviewer', 'gpt-6.1-sol', 'high', 'reviewer'],
  ['gpt-reviewer-pro', 'gpt-6.1-sol', 'high', 'reviewer-pro'],
  ['gpt-rr', 'gpt-6.1-sol', 'low', 'rr'],
  ['gpt-rr-pro', 'gpt-6.1-sol', 'medium', 'rr', 'rr-pro'],
  ['gpt-rr-pro-max', 'gpt-6.1-sol', 'medium', 'rr', 'rr-pro-max'],
  ['gpt-agent-investigator', 'gpt-6.1-sol', 'high', 'agent-investigator'],
  ['gpt-transcript-reader', 'gpt-6-luna', 'medium', 'transcript-reader'],
  ['gpt-tracer', 'gpt-6.1-sol', 'high', 'tracer'],
  ['gpt-sub-tracer', 'gpt-6-luna', 'medium', 'sub-tracer'],
  ['gpt-clone-distiller', 'gpt-6.1-sol', 'high', 'clone-distiller'],
  ['gpt-collector', 'gpt-6.1-sol', 'high', 'collector'],
  ['gpt-conflict-resolver', 'gpt-6.1-sol', 'high', 'conflict-resolver'],
  ['gpt-documenter', 'gpt-6.1-sol', 'high', 'documenter'],
  ['gpt-pcm', 'gpt-6.1-sol', 'high'],
  ['gpt-flights-hunter', 'gpt-6.1-sol', 'high', 'flights-hunter'],
  ['gpt-flights-spec-walker', 'gpt-6.1-sol', 'high', 'flights-spec-walker'],
  ['gpt-flights-sub-lander', 'gpt-6.1-sol', 'high', 'flights-sub-lander'],
  ['gpt-gitter', 'gpt-6.1-sol', 'high', 'gitter'],
  ['gpt-imaginator', 'gpt-6.1-sol', 'high', 'imaginator'],
  ['gpt-sub-rr', 'gpt-6.1-sol', 'high', 'sub-rr'],
  ['gpt-reviewer-nt', 'gpt-6.1-sol', 'high', 'reviewer', 'reviewer-nt'],
  ['sol', 'gpt-6.1-sol', 'high'],
  ['luna', 'gpt-6-luna', 'medium'],
  ['astra', 'gpt-6-astra', 'high'],
];
const otherVariants = ['flights-foreman-sonnet', 'rr-pro', 'rr-pro-max', 'reviewer-nt'];
const configDir = '${CLAUDE_CONFIG_DIR:-$HOME/.claude}';
const renderErrors = /SECTION-DRIFT|SECTION-MISSING|RENAME-UNUSED|UNSET-MISSING|FRONTMATTER-INVALID/;

// `engine-sync global` over the shipped declaration, into a jail home and Codex home: the real machine
// policy, the real sources, nothing of the host's. Rendered once per capability set.
const rendered = new Map();
function renderShipped(capabilities) {
  const key = capabilities.join(',');
  if (rendered.has(key)) return rendered.get(key);
  const home = mkdtempSync(join(scratch, 'shipped-'));
  const sourceRoot = join(home, 'source');
  mkdirSync(sourceRoot);
  cpSync(join(ROOT, 'templates'), join(sourceRoot, 'templates'), { recursive: true });
  mkdirSync(join(home, '.claude'), { recursive: true });
  mkdirSync(join(home, '.codex'), { recursive: true });
  copyFileSync(join(sourceRoot, 'templates/global/engine-sync.json'), join(home, '.claude/engine-sync.json'));
  const env = { ...process.env, HOME: home };
  for (const name of ['XDG_CONFIG_HOME', 'XDG_DATA_HOME', 'XDG_STATE_HOME', 'ENGINE_SYNC_HOME', 'ENGINE_SYNC_STATE', 'CODEX_HOME',
    'CLAUDE_CONFIG_DIR']) delete env[name];
  const flags = capabilities.flatMap((name) => ['--capability', name]);
  const run = (args) => {
    const result = spawnSync(binary, args, { env, encoding: 'utf8', timeout: 120000 });
    return { status: result.status, out: result.stdout + result.stderr };
  };
  const global = run(['global', '--home', home, '--source-repo', sourceRoot, '--claude-config-dir', join(home, '.claude'),
    '--codex-home', join(home, '.codex'), ...flags]);
  const doctor = run(['doctor', '--machine', '--home', home, '--source-repo', sourceRoot, '--claude-config-dir', join(home, '.claude'),
    '--codex-home', join(home, '.codex'), ...flags]);
  const render = { home, global, doctor };
  rendered.set(key, render);
  return render;
}
const renderedPath = (render, name) => join(render.home, '.claude/agents', `${name}.md`);
// The linked file's text, or null when engine-sync linked nothing at that name.
function renderedText(render, name) {
  const path = renderedPath(render, name);
  try { lstatSync(path); } catch (error) { if (error.code === 'ENOENT') return null; throw error; }
  return readFileSync(path, 'utf8');
}
function parseFrontmatter(name, text) {
  const parsed = spawnSync('python3', ['-I', '-c',
    'import json,sys,yaml; print(json.dumps(yaml.safe_load(sys.stdin.read().split("---", 2)[1])))'],
  { input: text, encoding: 'utf8' });
  assert.equal(parsed.status, 0, `${name}: frontmatter parser failed: ${parsed.error ?? parsed.stderr}`);
  return JSON.parse(parsed.stdout);
}
const bodyOf = (text) => text.split('---\n').slice(2).join('---\n').replace(/^\n+/, '');
const filesNamed = (dir, name) => {
  if (!existsSync(dir)) return [];
  return readdirSync(dir, { withFileTypes: true, recursive: true })
    .filter((entry) => entry.isFile() && [`${name}.md`, `${name}.toml`].includes(entry.name))
    .map((entry) => join(entry.parentPath ?? entry.path, entry.name));
};

test('the shipped variants render with zero errors with and without the gpt-agents capability', () => {
  for (const capabilities of [[CAPABILITY], []]) {
    const render = renderShipped(capabilities);
    const label = capabilities.length ? `--capability ${CAPABILITY}` : 'no capability';
    assert.equal(render.global.status, 0, `engine-sync global (${label}): ${render.global.out}`);
    assert.doesNotMatch(render.global.out, renderErrors, `engine-sync global (${label})`);
    assert.doesNotMatch(render.doctor.out, /SECTION-DRIFT/, `engine-sync doctor --machine (${label})`);
    for (const name of otherVariants) {
      assert.ok(renderedText(render, name), `${label}: ${name} was not rendered`);
      assert.equal(realpathSync(renderedPath(render, name)),
        join(render.home, 'source/templates/generated/agents', `${name}.md`),
        `${label}: ${name} must be rendered into the copied source tree`);
    }
    const gitter = parseFrontmatter('gitter', readFileSync(join(render.home, '.config/opencode/agents/gitter.md'), 'utf8'));
    assert.equal(gitter.permission.bash['git *'], 'allow', `${label}: OpenCode gitter must retain Git write permission`);
  }
});

test('only exact gitter and gpt-gitter mirrors retain Git write permissions', () => {
  const render = renderShipped([CAPABILITY]);
  const dir = join(render.home, '.config/opencode/agents');
  const allowed = [];
  for (const file of readdirSync(dir).filter(file => file.endsWith('.md'))) {
    const fm = parseFrontmatter(file, readFileSync(join(dir, file), 'utf8'));
    if (fm.permission?.bash?.['git *'] === 'allow') allowed.push(file.slice(0, -3));
  }
  assert.deepEqual(allowed.sort(), ['gitter', 'gpt-gitter']);
  for (const name of ['gitter', 'gpt-gitter']) {
    const codex = readFileSync(join(render.home, '.codex/agents', `${name}.toml`), 'utf8');
    assert.doesNotMatch(codex, /^sandbox_mode\s*=/m, `${name}: sandbox inherit must emit no sandbox_mode`);
  }
});

test('the gpt-agents capability decides whether the GPT twins are linked', () => {
  const on = renderShipped([CAPABILITY]);
  const off = renderShipped([]);
  for (const [name] of twins) {
    assert.ok(renderedText(on, name), `${name}: not rendered with --capability ${CAPABILITY}`);
    assert.equal(renderedText(off, name), null, `${name}: linked without the capability`);
    // The doctor row: `ok agent SKIPPED all {name} {path} (requires {capability} unmet)`.
    assert.match(off.doctor.out, new RegExp(`agent SKIPPED all ${name} \\S+ \\(requires ${CAPABILITY} unmet\\)`),
      `${name}: not reported SKIPPED`);
    assert.doesNotMatch(on.doctor.out, new RegExp(`agent SKIPPED all ${name} `), `${name}: SKIPPED although the capability is declared`);
  }
});

test('gpt-pcm registers the project PCM owner with its runtime skills and fail-closed GPT routing', () => {
  const render = renderShipped([CAPABILITY]);
  const text = renderedText(render, 'gpt-pcm');
  assert.ok(text, 'gpt-pcm: the rendered registration is missing');
  const fm = parseFrontmatter('gpt-pcm', text);
  const project = parseFrontmatter('project gpt-pcm', readFileSync(join(ROOT, 'templates/project/agents/gpt-pcm.md'), 'utf8'));
  for (const key of ['tools', 'skills', 'model', 'effort', 'requires', 'engines', 'autoCompact'])
    assert.deepEqual(fm[key], project[key], `gpt-pcm: project registration ${key} drifted`);
  assert.equal(realpathSync(renderedPath(render, 'gpt-pcm')),
    join(render.home, 'source/templates/generated/agents/gpt-pcm.md'));
  assert.match(text, /Read this repository's `\.claude\/agents\/pcm\.md` in full before acting/);
  assert.match(text, /execute its protocol, including skills, checks, refusals and report format/);
  assert.match(text, /BLOCKED: project pcm manual unreadable/);
  assert.match(text, /BLOCKED: GPT role unavailable: \{role\}/);
  assert.match(text, /`general-purpose` and `Explore` use `sol`/);
  assert.match(text, /manual's model/);
  assert.match(text, /Registry discovery requires a new session or user-authorized reload/);
  assert.match(text, /Git writes remain with the authorized Git writer/);
  for (const caller of ['gpt-documenter', 'gpt-foreman'])
    assert.ok(renderedText(render, caller).includes('`gpt-pcm`'), `${caller}: PCM ownership route missing`);
});

// A fifth field names the rendered variant a twin derives from when it is not the role's own manual.
for (const [name, model, effort, role, base] of twins) {
  test(`${name} renders its runtime and portable role contract`, () => {
    const render = renderShipped([CAPABILITY]);
    const text = renderedText(render, name);
    assert.ok(text, `${name}: the rendered agent is missing`);
    const fm = parseFrontmatter(name, text);
    assert.equal(fm.name, name);
    assert.equal(fm.model, `${model}[1m]`);
    assert.equal(fm.effort, effort);
    assert.deepEqual(fm.engines, role === 'gitter' ? gitterEngines : { codex: false, opencode: false });
    assert.deepEqual(fm.requires, [CAPABILITY]);
    assert.deepEqual(fm.autoCompact, { forceAt: '230k', nudgeFrom: '190k', nudgeEvery: '20k' });
    // The cap is 15 KiB for a twin of the general-purpose worker; a twin of a role manual may grow by the
    // renames and the return law (2 KiB) over the larger of 15 KiB and the manual it derives from.
    const original = role ? readFileSync(join(ROOT, 'templates/global/agents', `${role}.md`), 'utf8') : '';
    const cap = role ? Math.max(15 * 1024, Buffer.byteLength(original)) + 2 * 1024 : 15 * 1024;
    assert.ok(Buffer.byteLength(text) <= cap, `${name}: ${Buffer.byteLength(text)} bytes exceeds the agent size cap ${cap}`);
    assert.doesNotMatch(text, /\/(Users|home|root)\/|\/private\//, `${name}: carries a machine-absolute path`);
    assert.ok(!text.includes(render.home), `${name}: carries the render's home directory`);
    for (const stray of (role === 'gitter' ? [] : filesNamed(render.home, name)).filter((path) => !path.startsWith(join(render.home, '.claude/agents'))
      && !path.startsWith(`${join(render.home, 'source')}/`) && !path.includes('/generated/claude-agents/')))
      assert.fail(`${name}: ${stray} is a Codex role or an OpenCode agent; a GPT twin is Claude-only`);
    if (!role) {
      assert.equal(fm.role, undefined, `${name}: a general-purpose worker has no role`);
      return;
    }
    assert.equal(fm.role, role);
    // Expected routes are independent of the declaration being judged. Paths and skill names stay canonical.
    const routes = {
      'flights-foreman': { 'flights-foreman': 'gpt-foreman', 'flights-foreman-sonnet': 'gpt-foreman', documenter: 'gpt-documenter', pcm: 'gpt-pcm' },
      'flights-lander': Object.fromEntries(['flights-hunter', 'flights-spec-walker', 'flights-sub-lander'].map(n => [n, `gpt-${n}`])),
      rr: { 'sub-rr': 'gpt-sub-rr', 'clone-distiller': 'gpt-clone-distiller' },
      'rr-pro': { 'sub-rr': 'gpt-sub-rr', 'clone-distiller': 'gpt-clone-distiller' },
      'rr-pro-max': { 'sub-rr': 'gpt-sub-rr', 'clone-distiller': 'gpt-clone-distiller' },
      'agent-investigator': { 'transcript-reader': 'gpt-transcript-reader', Haiku: 'GPT luna' },
      tracer: { 'sub-tracer': 'gpt-sub-tracer', Haiku: 'GPT luna' },
      'sub-tracer': { 'sub-tracer': 'gpt-sub-tracer', tracer: 'gpt-tracer' },
      reviewer: { tracer: 'gpt-tracer' }, 'reviewer-pro': { tracer: 'gpt-tracer' },
      'clone-distiller': { tracer: 'gpt-tracer' },
      documenter: { documenter: 'gpt-documenter', pcm: 'gpt-pcm' },
      gitter: { 'conflict-resolver': 'gpt-conflict-resolver' },
    }[base ?? role] ?? {};
    const canonical = renderedText(render, base ?? role);
    assert.ok(canonical, `${name}: missing canonical rendered manual`);
    const canonicalFm = parseFrontmatter(base ?? role, canonical);
    const expectedTools = canonicalFm.tools?.split(',').map(t => t.trim()).filter(t => !t.startsWith('mcp__auto-effort-control__'));
    if (expectedTools) assert.deepEqual(fm.tools?.split(',').map(t => t.trim()), expectedTools, `${name}: non-effort tools drifted`);
    else assert.ok(fm.tools && !fm.tools.includes('mcp__auto-effort-control__'), `${name}: inherited all tools instead of pinning a GPT-safe list`);
    for (const key of ['skills', 'hooks']) assert.deepEqual(fm[key], canonicalFm[key], `${name}: runtime ${key} drifted`);
    let manual = bodyOf(canonical);
    if (Object.keys(routes).length) manual = manual.replace(
      new RegExp(`(^|[^A-Za-z0-9_./:-])(${Object.keys(routes).join('|')})(?![A-Za-z0-9_/-]|\\.[A-Za-z0-9])`, 'g'),
      (_, before, agent) => `${before}${routes[agent]}`);
    const noEffortTools = 'Your effort is set by your definition; you hold no effort tools.';
    if (role === 'flights-foreman' || role === 'documenter')
      manual = manual.replace(/(^## Effort\n\n)[^\n]*(\n)/m, `$1${noEffortTools}$2`);
    const gptProvider = 'Select only the registered GPT equivalent of each required role; both foreman tiers use `gpt-foreman`. Omit the Agent `model` override and pass GPT-only routing in every child brief. If unavailable or inaccessible, return `BLOCKED: GPT role unavailable: {role}`; never substitute an Anthropic agent or skip the step.';
    if (role.startsWith('flights-') && ['flights-foreman', 'flights-lander'].includes(role))
      manual = manual.replace(/(^## Provider\n\n)[^\n]*(\n)/m, `$1${gptProvider}$2`);
    assert.ok(bodyOf(text).trimEnd().endsWith(manual.trimEnd()), `${name}: the full canonical manual was not preserved with only declared route/effort/provider changes`);
    assert.ok(text.includes('BLOCKED: GPT role unavailable: {role}'), `${name}: missing fail-closed provider routing`);
    if (role === 'rr') {
      assert.ok(text.includes('subagent_type: "gpt-sub-rr"'), `${name}: web digger is not GPT`);
      if (base) assert.ok(text.includes('`gpt-clone-distiller`'), `${name}: repository digger is not GPT`);
      const kid = parseFrontmatter('gpt-sub-rr', renderedText(render, 'gpt-sub-rr'));
      assert.deepEqual(kid.visibility['visible-to'].include, ['gpt-rr', 'gpt-rr-pro', 'gpt-rr-pro-max']);
    }
    if (role === 'flights-lander') {
      assert.deepEqual(fm.tools.split(',').map((tool) => tool.trim()),
        ['Read', 'Write', 'Edit', 'Bash', 'Glob', 'Grep', 'Agent', 'SendMessage',
         'mcp__sub-agent-compact__forward_handoff', 'mcp__sub-agent-compact__look_back'],
        `${name}: tools must match the lander's (Agent: its review dispatch; SendMessage: a critical decision to main)`);
      for (const child of ['flights-hunter', 'flights-spec-walker', 'flights-sub-lander']) {
        assert.ok(text.includes(`subagent_type: gpt-${child}`), `${name}: does not spawn GPT ${child}`);
        const kid = parseFrontmatter(`gpt-${child}`, renderedText(render, `gpt-${child}`));
        assert.deepEqual(kid.visibility['visible-to'].include, [name], `${child}: not the GPT lander's alone`);
      }
    }
    if (role === 'flights-foreman') {
      // A GPT foreman's foreman children are GPT: both foreman tiers render as itself.
      assert.ok(text.includes('`gpt-documenter`') && text.includes('`gpt-pcm`'), `${name}: documenter/pcm routes are not GPT`);
      assert.ok(text.includes('`gpt-foreman` when the path is clear'), `${name}: its split line does not name gpt-foreman`);
      assert.doesNotMatch(text, /flights-foreman-sonnet|gpt-foreman-sonnet|gpt-flights-fore/, `${name}: a Claude foreman tier or the old GPT name appears`);
    }
  });
}

// auto-effort-control steers an agent whose frontmatter carries `effortControl`, and only a steered agent may press
// its buttons: exactly these Claude roles hold both; every other rendered agent, each GPT twin, pcm and releaser neither.
const effortButtons = ['effort_up', 'effort_down', 'effort_auto'].map((tool) => `mcp__auto-effort-control__${tool}`);
const foremanEffort = { floor: 'medium', ceiling: 'xhigh' };
const effortSteered = new Map([
  ['flights-foreman', foremanEffort], ['flights-foreman-sonnet', foremanEffort],
  ['reviewer', true], ['reviewer-pro', true], ['reviewer-nt', true], ['documenter', true],
]);
function assertEffortPermission(name, text) {
  const fm = parseFrontmatter(name, text);
  const tools = (fm.tools ?? '').split(',').map((tool) => tool.trim());
  const inheritsAll = fm.tools === undefined || tools.includes('*');
  const denied = new Set(fm.disallowedTools ?? []);
  const held = effortButtons.filter((button) => (inheritsAll || tools.includes(button)) && !denied.has(button));
  const want = effortSteered.get(name);
  if (want === undefined) {
    assert.equal(fm.effortControl, undefined, `${name}: steered by effortControl but not an approved role`);
    assert.deepEqual(held, [], `${name}: holds effort buttons`);
    return false;
  }
  assert.deepEqual(fm.effortControl, want, `${name}: effortControl`);
  assert.deepEqual(held, effortButtons, `${name}: must hold every effort button`);
  for (const button of inheritsAll ? [] : effortButtons)
    assert.equal(tools.filter((tool) => tool === button).length, 1, `${name}: must hold ${button} once; tools ${fm.tools}`);
  return true;
}

test('effort permission gate rejects an unapproved role inheriting every tool', () => {
  assert.throws(() => assertEffortPermission('unapproved', '---\nname: unapproved\n---\nRead.\n'), /holds effort buttons/);
  assert.throws(() => assertEffortPermission('unapproved', '---\nname: unapproved\ntools: "*"\n---\nRead.\n'), /holds effort buttons/);
  assert.equal(assertEffortPermission('unapproved', '---\nname: unapproved\ntools: Read, Bash\n---\nRead.\n'), false);
});

test('effort permission gate evaluates native exclusions without reducing inherited capabilities', () => {
  const exclusions = `disallowedTools: ${JSON.stringify(effortButtons)}\n`;
  for (const tools of ['', 'tools: "*"\n', `tools: Read, Bash, ${effortButtons.join(', ')}\n`]) {
    assert.equal(assertEffortPermission('unapproved', `---\nname: unapproved\n${tools}${exclusions}---\nRead.\n`), false);
  }
  for (const surviving of effortButtons) {
    const denied = effortButtons.filter((button) => button !== surviving);
    assert.throws(() => assertEffortPermission('unapproved', `---\nname: unapproved\ndisallowedTools: ${JSON.stringify(denied)}\n---\nRead.\n`));
  }
  assert.throws(() => assertEffortPermission('reviewer', `---\nname: reviewer\neffortControl: true\ndisallowedTools: [${effortButtons[0]}]\n---\nReview.\n`));
  const render = renderShipped([CAPABILITY]);
  for (const name of ['astra', 'sol', 'luna']) {
    const fm = parseFrontmatter(name, renderedText(render, name));
    assert.equal(fm.tools, undefined, `${name}: unrelated inherited capabilities narrowed`);
    assert.equal(fm.effortControl, undefined, `${name}: must not be automatically steered`);
    assert.deepEqual(fm.disallowedTools, effortButtons, `${name}: exclude exactly the effort buttons`);
  }
});

test('effort buttons ride exactly the steered Claude roles, never a GPT twin, pcm or releaser', () => {
  const render = renderShipped([CAPABILITY]);
  const dir = join(render.home, '.claude/agents');
  const agents = [
    ...readdirSync(dir).filter((file) => file.endsWith('.md')).map((file) => [file.slice(0, -3), readFileSync(join(dir, file), 'utf8')]),
    ...['.claude/agents/pcm.md', '.claude/agents/releaser.md', 'templates/project/agents/pcm.md']
      .map((path) => [path, readFileSync(join(ROOT, path), 'utf8')]),
  ];
  for (const [name] of twins)
    assert.ok(agents.some(([agent]) => agent === name), `${name}: not rendered, so its lack of buttons is unproven`);
  const steered = [];
  for (const [name, text] of agents) {
    if (assertEffortPermission(name, text)) steered.push(name);
  }
  assert.deepEqual(steered.sort(), [...effortSteered.keys()].sort(), 'an approved steered role was not rendered');
  // A twin pins its own tools to drop the buttons: the rest stays its role's list.
  // A role with no tools line inherits every tool, so its twin pins a list of its own that holds no button.
  const toolsOf = (name) => parseFrontmatter(name, renderedText(render, name)).tools?.split(',').map((tool) => tool.trim());
  for (const [name, , , role, base] of twins.filter(([, , , role]) => effortSteered.has(role))) {
    const roleTools = toolsOf(base ?? role);
    if (roleTools === undefined) {
      assert.ok(toolsOf(name)?.length > 0, `${name}: ${role} lists no tools, so the twin must pin its own list`);
      assert.deepEqual(toolsOf(name).filter((tool) => effortButtons.includes(tool)), [], `${name}: holds effort buttons`);
    } else
      assert.deepEqual(toolsOf(name), roleTools.filter((tool) => !effortButtons.includes(tool)), `${name}: tools drifted from ${role}`);
  }
});

// sub-agent-compact registers `compact` only while forwardHandoffTool is off, and Professor runs it on:
// a fixed tools list naming the plugin names forward_handoff and look_back, never the unregistered compact.
test('every shipped agent naming sub-agent-compact holds forward_handoff and look_back, never compact', () => {
  const dirs = ['templates/global/agents', 'templates/generated/agents', 'templates/project/agents', '.claude/agents'];
  let handing = 0;
  for (const dir of dirs.filter((relative) => existsSync(join(ROOT, relative)))) {
    for (const file of readdirSync(join(ROOT, dir)).filter((entry) => entry.endsWith('.md'))) {
      const tools = readFileSync(join(ROOT, dir, file), 'utf8').match(/^tools:(.*)$/m)?.[1] ?? '';
      if (!tools.includes('mcp__sub-agent-compact__')) continue;
      handing++;
      assert.ok(!tools.includes('mcp__sub-agent-compact__compact'), `${dir}/${file}: names the unregistered compact tool`);
      for (const tool of ['mcp__sub-agent-compact__forward_handoff', 'mcp__sub-agent-compact__look_back'])
        assert.ok(tools.includes(tool), `${dir}/${file}: names sub-agent-compact but not ${tool}`);
    }
  }
  assert.ok(handing > 0, 'no shipped agent names a sub-agent-compact tool: the scan read nothing');
});

// Project roles cannot use machine inheritance. The loader keeps the canonical body on disk;
// registration must carry every runtime tool/skill/hook, never source role metadata.
const projectTwins = ['changelogger', 'gitter', 'pcm', 'releaser', 'themer'];
test('project GPT wrappers preserve runtime metadata and close independent provider doors', () => {
  for (const role of projectTwins) {
    const source = readFileSync(join(ROOT, '.claude/agents', `${role}.md`), 'utf8');
    const text = readFileSync(join(ROOT, '.claude/agents', `gpt-${role}.md`), 'utf8');
    const canonical = parseFrontmatter(role, source);
    const twin = parseFrontmatter(`gpt-${role}`, text);
    assert.equal(twin.name, `gpt-${role}`);
    assert.equal(twin.model, 'gpt-6.1-sol[1m]');
    assert.equal(twin.effort, 'high');
    for (const key of ['tools', 'skills', 'hooks']) assert.deepEqual(twin[key], canonical[key], `${role}: runtime ${key} drift`);
    assert.equal(twin.from, undefined); assert.equal(twin.role, undefined);
    assert.deepEqual(twin.engines, role === 'gitter' ? gitterEngines : { codex: false, opencode: false });
    assert.ok(text.includes(`.claude/agents/${role}.md`) && text.includes('in full before acting'), `${role}: missing full manual loader`);
    assert.ok(text.includes('BLOCKED: GPT role unavailable: {role}') && text.includes('Omit the Agent `model` override'), `${role}: missing GPT-only routing/refusal`);
    assert.ok(text.includes('new session or user-authorized reload'), `${role}: assumes registration hot reload`);
    if (role === 'pcm') {
      assert.ok(text.includes('Explore') && text.includes('`sol`'), 'pcm: Explore needs its independent GPT route');
      assert.ok(text.includes('model: haiku') && text.includes('explicit model overrides'), 'pcm: the separate haiku door is open');
    }
    if (role === 'gitter') {
      assert.deepEqual(twin.visibility.agents.include, ['gpt-conflict-resolver']);
      assert.ok(text.includes('GitGuard') && text.includes('BLOCKED: gpt-gitter native GitGuard identity unproven'));
      assert.ok(text.includes('worktree prune -n') && text.includes('"$HOME/.local/bin/pfm" internal git-guard'), 'gitter: GitGuard admission lacks its runnable proof');
    }
    if (role === 'changelogger') assert.deepEqual(twin.visibility['visible-to'].include, ['gpt-releaser']);
    if (['pcm', 'gitter'].includes(role))
      assert.equal(text, readFileSync(join(ROOT, 'templates/project/agents', `gpt-${role}.md`), 'utf8'), `${role}: shipped wrapper differs`);
  }
});

test('GPT helper visibility admits every mapped caller', () => {
  const render = renderShipped([CAPABILITY]);
  for (const [child, callers] of [
    ['gpt-conflict-resolver', ['gpt-gitter']],
    ['gpt-tracer', ['main', 'gpt-reviewer', 'gpt-reviewer-pro', 'gpt-foreman', 'gpt-flights-lander', 'gpt-clone-distiller', 'gpt-changelogger']],
    ['gpt-reviewer', ['main', 'gpt-releaser']],
  ]) {
    const fm = parseFrontmatter(child, renderedText(render, child));
    assert.deepEqual(fm.visibility['visible-to'].include, callers, `${child}: caller visibility`);
  }
  const git = parseFrontmatter('gpt-gitter', renderedText(render, 'gpt-gitter'));
  assert.deepEqual(git.visibility.agents.include, ['gpt-conflict-resolver']);
  assert.ok(renderedText(render, 'gpt-gitter').includes('.claude/agents/gitter.md'), 'global gitter lost project delegation');
});

test('canonical investigation and trace visibility stays separate from GPT helpers', () => {
  const render = renderShipped([CAPABILITY]);
  for (const [child, callers] of [
    ['transcript-reader', ['agent-investigator']], ['gpt-transcript-reader', ['gpt-agent-investigator']],
    ['sub-tracer', ['tracer', 'sub-tracer']], ['gpt-sub-tracer', ['gpt-tracer', 'gpt-sub-tracer']],
  ]) {
    const text = renderedText(render, child);
    assert.ok(text, `${child}: no rendered helper`);
    assert.deepEqual(parseFrontmatter(child, text).visibility['visible-to'].include, callers);
  }
});
