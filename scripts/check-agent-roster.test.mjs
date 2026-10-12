// Exercise the roster checker against mirrors and effective config from the same compiler.
import { lstatSync, readdirSync } from "node:fs";
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
  copyFileSync(process.env.ROSTER_CHECK_SUT || join(ROOT, "scripts/check-agent-roster.mjs"), join(root, "scripts/check-agent-roster.mjs"));
  if (existsSync(join(ROOT, "scripts/agent-source-inventory.py")))
    copyFileSync(join(ROOT, "scripts/agent-source-inventory.py"), join(root, "scripts/agent-source-inventory.py"));
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

for (const [name, path, header, status, message] of [
  ["project from", ".claude/agents/local.md", "from: general-purpose", 2, /machine scope only/],
  ["project abstract", ".claude/agents/local.md", "abstract: true", 2, /machine scope only/],
  ["abstract quoted model", "templates/global/agents/layer.md", 'abstract: true\nmodel: "smart"', 0, /agent-roster: clean/],
  ["abstract model hygiene", "templates/global/agents/layer.md", 'abstract: true\nmodel: "smart invalid"', 1, /not one bare alias/],
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

// The judge is never the thing being judged: a stub that reports its own check clean (as an
// engine-sync with a source-discovery defect would) while every other verb is the real binary.
function cleanVerdictStub(f) {
  f.put("clean-engine-sync", `#!/bin/sh\nif [ "$1" = check ]; then printf '%s\\n' '{"ok":true,"problems":[]}'; exit 0; fi\nexec "${binary}" "$@"\n`);
  execFileSync("chmod", ["+x", join(f.root, "clean-engine-sync")]);
  f.env.ENGINE_SYNC_BIN = join(f.root, "clean-engine-sync");
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
  f.put("home/.config/engine-sync/config.json", JSON.stringify({ version: 1, workbenchMarker: `.bench/${"x".repeat(300)}` }));
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
const twins = [
  ['gpt-foreman', 'gpt-6.1-sol', 'high', 'flights-foreman'],
  ['gpt-foreman-pro', 'gpt-6.1-sol', 'xhigh', 'flights-foreman'],
  ['gpt-lander', 'gpt-6.1-sol', 'high', 'flights-lander'],
  ['gpt-executor', 'gpt-6-luna', 'high', 'flights-mechanical-executor'],
  ['gpt-reviewer', 'gpt-6.1-sol', 'high', 'reviewer'],
  ['gpt-reviewer-pro', 'gpt-6.1-sol', 'high', 'reviewer-pro'],
  ['sol', 'gpt-6.1-sol', 'high'],
  ['luna', 'gpt-6-luna', 'medium'],
  ['terra', 'gpt-5.6-terra', 'high'],
  ['astra', 'gpt-6-astra', 'high'],
];
const otherVariants = ['foreman-pro', 'rr-pro', 'rr-pro-max', 'reviewer-nt'];
const configDir = '${CLAUDE_CONFIG_DIR:-$HOME/.claude}';
const renderErrors = /SECTION-DRIFT|SECTION-MISSING|RENAME-UNUSED|UNSET-MISSING|FRONTMATTER-INVALID/;

// `engine-sync global` over the shipped declaration, into a jail home and Codex home: the real machine
// policy, the real sources, nothing of the host's. Rendered once per capability set.
const rendered = new Map();
function renderShipped(capabilities) {
  const key = capabilities.join(',');
  if (rendered.has(key)) return rendered.get(key);
  const home = mkdtempSync(join(scratch, 'shipped-'));
  mkdirSync(join(home, '.claude'), { recursive: true });
  mkdirSync(join(home, '.codex'), { recursive: true });
  mkdirSync(join(home, '.config/engine-sync'), { recursive: true });
  copyFileSync(join(ROOT, 'templates/global/engine-sync/config.json'), join(home, '.config/engine-sync/config.json'));
  const env = { ...process.env, HOME: home };
  for (const name of ['XDG_CONFIG_HOME', 'XDG_DATA_HOME', 'XDG_STATE_HOME', 'ENGINE_SYNC_HOME', 'ENGINE_SYNC_STATE', 'CODEX_HOME',
    'CLAUDE_CONFIG_DIR']) delete env[name];
  const flags = capabilities.flatMap((name) => ['--capability', name]);
  const run = (args) => {
    const result = spawnSync(binary, args, { env, encoding: 'utf8', timeout: 120000 });
    return { status: result.status, out: result.stdout + result.stderr };
  };
  const global = run(['global', '--home', home, '--source-repo', ROOT, '--claude-config-dir', join(home, '.claude'),
    '--codex-home', join(home, '.codex'), ...flags]);
  const doctor = run(['doctor', '--machine', '--home', home, '--source-repo', ROOT, '--claude-config-dir', join(home, '.claude'),
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
    for (const name of otherVariants)
      assert.ok(renderedText(render, name), `${label}: ${name} was not rendered`);
  }
});

test('the gpt-agents capability decides whether the ten GPT twins are linked', () => {
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

for (const [name, model, effort, role] of twins) {
  test(`${name} renders its runtime and portable role contract`, () => {
    const render = renderShipped([CAPABILITY]);
    const text = renderedText(render, name);
    assert.ok(text, `${name}: the rendered agent is missing`);
    const fm = parseFrontmatter(name, text);
    assert.equal(fm.name, name);
    assert.equal(fm.model, `${model}[1m]`);
    assert.equal(fm.effort, effort);
    assert.deepEqual(fm.engines, { codex: false, opencode: false });
    assert.deepEqual(fm.requires, [CAPABILITY]);
    assert.deepEqual(fm.autoCompact, { forceAt: '230k', nudgeFrom: '190k', nudgeEvery: '20k' });
    // The cap is 15 KiB for a twin of the general-purpose worker; a twin of a role manual may grow by the
    // renames and the return law (2 KiB) over the larger of 15 KiB and the manual it derives from.
    const original = role ? readFileSync(join(ROOT, 'templates/global/agents', `${role}.md`), 'utf8') : '';
    const cap = role ? Math.max(15 * 1024, Buffer.byteLength(original)) + 2 * 1024 : 15 * 1024;
    assert.ok(Buffer.byteLength(text) <= cap, `${name}: ${Buffer.byteLength(text)} bytes exceeds the agent size cap ${cap}`);
    assert.doesNotMatch(text, /\/(Users|home|root)\/|\/private\//, `${name}: carries a machine-absolute path`);
    assert.ok(!text.includes(render.home), `${name}: carries the render's home directory`);
    for (const stray of filesNamed(render.home, name).filter((path) => !path.startsWith(join(render.home, '.claude/agents'))
      && !path.includes('/generated/claude-agents/')))
      assert.fail(`${name}: ${stray} is a Codex role or an OpenCode agent; a GPT twin is Claude-only`);
    if (!role) {
      assert.equal(fm.role, undefined, `${name}: a general-purpose worker has no role`);
      return;
    }
    assert.equal(fm.role, role);
    const renamed = name.startsWith('gpt-foreman')
      ? original.replaceAll('"flights-foreman"', '"gpt-foreman"').replaceAll('"flights-mechanical-executor"', '"gpt-executor"')
        .replaceAll('"flights-lander"', '"gpt-lander"')
      : role === 'flights-lander'
        ? original.replace(/(^|[^A-Za-z0-9_./:-])(reviewer-pro|reviewer)(?![A-Za-z0-9_/-]|\.[A-Za-z0-9])/g,
          (_, before, agent) => `${before}gpt-${agent}`)
        : original;
    assert.ok(bodyOf(text).trimEnd().endsWith(bodyOf(renamed).trimEnd()), `${name}: the rendered body is not the full ${role} manual`);
    if (role.startsWith('flights-')) {
      assert.ok(text.includes('your parent hears from you only by that return'), `${name}: the return law is not prepended`);
      assert.ok(text.includes('run.md, briefs, returns/*.md, gate.md, red/*'), `${name}: the return law lost its file list`);
    } else {
      assert.doesNotMatch(text, /gpt-(?:sub-)?tracer/, `${name}: its code readers must remain Claude tracers`);
      assert.deepEqual(fm.visibility['visible-to'].include, ['main', 'gpt-lander']);
    }
    if (role === 'flights-lander') {
      assert.ok(fm.tools.split(',').map((tool) => tool.trim()).includes('Agent'),
        `${name}: lander manual requires Agent for review dispatch`);
      for (const reviewer of ['gpt-reviewer', 'gpt-reviewer-pro'])
        assert.ok(text.includes(reviewer), `${name}: review dispatch did not rename ${reviewer}`);
    }
    if (role === 'flights-foreman') {
      for (const spawned of ['gpt-foreman', 'gpt-executor', 'gpt-lander'])
        assert.ok(text.includes(`subagent_type: "${spawned}"`), `${name}: rename to ${spawned} not applied`);
      assert.doesNotMatch(text, /subagent_type: "flights-/, `${name}: a flights-* spawn survived the renames`);
      const commands = (event) => fm.hooks[event].flatMap((entry) => entry.hooks.map((hook) => hook.command));
      assert.deepEqual(commands('PreToolUse'), ['pfm internal orchestrator-wait',
        `python3 -I "${configDir}/hooks/gpt-return-watch.py"`]);
      assert.deepEqual(commands('Stop'), [`python3 -I "${configDir}/hooks/gpt-agent-stop.py" foreman`]);
    }
  });
}
