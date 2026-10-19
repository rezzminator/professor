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
      && !path.startsWith(`${join(render.home, 'source')}/`) && !path.includes('/generated/claude-agents/')))
      assert.fail(`${name}: ${stray} is a Codex role or an OpenCode agent; a GPT twin is Claude-only`);
    if (!role) {
      assert.equal(fm.role, undefined, `${name}: a general-purpose worker has no role`);
      return;
    }
    assert.equal(fm.role, role);
    // engine-sync renames every standalone occurrence of a name, quoted or not.
    const renameStandalone = (names, renameTo = (agent) => `gpt-${agent}`) => original.replace(
      new RegExp(`(^|[^A-Za-z0-9_./:-])(${names})(?![A-Za-z0-9_/-]|\\.[A-Za-z0-9])`, 'g'),
      (_, before, agent) => `${before}${renameTo(agent)}`);
    // The twin holds no effort buttons: its declaration replaces the Effort section of the manual it renames.
    const noEffortTools = 'Your effort is set by your definition; you hold no effort tools.';
    // A flight twin spawns on its own provider: shared/gpt-flights.md replaces the Provider section of both flight manuals.
    const gptProvider = 'You are a GPT agent: spawn the GPT agent of the role you need, listed as `{role} on GPT …`, wherever one exists; a Claude agent only for a role with none.';
    const onGpt = (body) => body.replace(/(^## Provider\n\n)[^\n]*(\n)/m, `$1${gptProvider}$2`);
    const renamed = role === 'flights-foreman'
      ? onGpt(renameStandalone('flights-foreman|flights-foreman-sonnet', () => 'gpt-foreman').replace(/(^## Effort\n\n)[^\n]*(\n)/m, `$1${noEffortTools}$2`))
      : role === 'flights-lander' ? onGpt(original)
      : role === 'agent-investigator' ? renameStandalone('transcript-reader|Haiku', (word) => word === 'Haiku' ? 'GPT luna' : 'gpt-transcript-reader')
      : role === 'tracer' ? renameStandalone('sub-tracer|Haiku', (word) => word === 'Haiku' ? 'GPT luna' : 'gpt-sub-tracer')
      : role === 'sub-tracer' ? renameStandalone('sub-tracer|tracer')
      : role.startsWith('reviewer') ? renameStandalone('tracer')
      : original;
    const manual = base ? renderedText(render, base) : renamed;
    assert.ok(manual, `${name}: its base ${base} was not rendered`);
    assert.ok(bodyOf(text).trimEnd().endsWith(bodyOf(manual).trimEnd()), `${name}: the rendered body is not the full ${base ?? role} manual`);
    if (role.startsWith('flights-')) {
      assert.ok(text.includes('Your parent hears your result only through your final return'), `${name}: the return law is not prepended`);
      assert.ok(text.includes(gptProvider) && !text.includes('You are a Claude agent'), `${name}: does not carry the GPT spawn rule alone`);
      assert.deepEqual(fm.visibility?.['visible-to']?.include, role === 'flights-lander' ? ['main'] : undefined,
        `${name}: a GPT lander is the main chat's alone, a GPT foreman keeps the foreman's open visibility`);
    } else if (role === 'rr') {
      // The research leads keep their diggers on Claude: no rename, and sub-rr admits each twin.
      assert.doesNotMatch(text, /gpt-sub-rr|gpt-clone-distiller/, `${name}: its diggers must remain Claude agents`);
      assert.equal(fm.visibility, undefined, `${name}: a research twin keeps rr's open visibility`);
      assert.equal(fm.tools, parseFrontmatter(base ?? role, renderedText(render, base ?? role)).tools, `${name}: tools differ from ${base ?? role}`);
      const digger = parseFrontmatter('sub-rr', renderedText(render, 'sub-rr'));
      assert.ok(digger.visibility['visible-to'].include.includes(name), `${name}: sub-rr is not visible to it`);
    } else if (role === 'agent-investigator' || role === 'transcript-reader') {
      // The investigation pair runs on GPT end to end: the twin spawns the GPT reader, never the Haiku one.
      assert.equal(fm.tools, parseFrontmatter(role, renderedText(render, role)).tools, `${name}: tools differ from ${role}`);
      const reader = parseFrontmatter('gpt-transcript-reader', renderedText(render, 'gpt-transcript-reader'));
      assert.deepEqual(reader.visibility['visible-to'].include, ['gpt-agent-investigator'], 'gpt-transcript-reader: not the GPT investigator\'s alone');
      const claudeReader = parseFrontmatter('transcript-reader', renderedText(render, 'transcript-reader'));
      assert.deepEqual(claudeReader.visibility['visible-to'].include, ['agent-investigator'], 'transcript-reader: not the Claude investigator\'s alone');
      if (role === 'agent-investigator') {
        assert.deepEqual(fm.visibility['visible-to'].include, ['main'], `${name}: a GPT investigator is the main chat's alone`);
        assert.ok(bodyOf(text).includes('`gpt-transcript-reader`'), `${name}: does not spawn gpt-transcript-reader`);
        assert.doesNotMatch(bodyOf(text), /(?<!gpt-)transcript-reader|Haiku/, `${name}: still names the Haiku reader`);
      }
    } else if (role === 'tracer' || role === 'sub-tracer') {
      // The trace tree runs on GPT end to end: the GPT tracer spawns GPT sub-tracers, which split into GPT sub-tracers.
      assert.equal(fm.tools, parseFrontmatter(role, renderedText(render, role)).tools, `${name}: tools differ from ${role}`);
      assert.ok(bodyOf(text).includes('`subagent_type: "gpt-sub-tracer"`'), `${name}: does not spawn gpt-sub-tracer`);
      assert.doesNotMatch(bodyOf(text), /(?<!gpt-)sub-tracer(?!s)|Haiku/, `${name}: still names the Haiku sub-tracer`);
      const reader = parseFrontmatter('gpt-sub-tracer', renderedText(render, 'gpt-sub-tracer'));
      assert.deepEqual(reader.visibility['visible-to'].include, ['gpt-tracer', 'gpt-sub-tracer'], 'gpt-sub-tracer: not the GPT tracers\' alone');
      const claudeReader = parseFrontmatter('sub-tracer', renderedText(render, 'sub-tracer'));
      assert.deepEqual(claudeReader.visibility['visible-to'].include, ['tracer', 'sub-tracer'], 'sub-tracer: not the Claude tracers\' alone');
      if (role === 'tracer')
        assert.deepEqual(fm.visibility['visible-to'].include, ['main', 'gpt-reviewer', 'gpt-reviewer-pro', 'gpt-foreman', 'gpt-flights-lander'],
          `${name}: visible beyond the main chat and the GPT agents that trace`);
    } else {
      // A GPT reviewer traces on GPT: its tracer runs are gpt-tracer runs, never the Claude tracer.
      assert.ok(bodyOf(text).includes('subagent_type: gpt-tracer'), `${name}: does not spawn gpt-tracer`);
      assert.doesNotMatch(bodyOf(text), /(?<![A-Za-z0-9_./:-])tracer(?![A-Za-z0-9_/-])/, `${name}: still names the Claude tracer`);
      assert.deepEqual(fm.visibility['visible-to'].include, ['main'], `${name}: a GPT reviewer is the main chat's alone`);
      const tracer = parseFrontmatter('gpt-tracer', renderedText(render, 'gpt-tracer'));
      assert.ok(tracer.visibility['visible-to'].include.includes(name), `${name}: gpt-tracer is not visible to it`);
    }
    if (role === 'flights-lander') {
      assert.deepEqual(fm.tools.split(',').map((tool) => tool.trim()),
        ['Read', 'Write', 'Edit', 'Bash', 'Glob', 'Grep', 'Agent', 'SendMessage',
         'mcp__sub-agent-compact__forward_handoff', 'mcp__sub-agent-compact__look_back'],
        `${name}: tools must match the lander's (Agent: its review dispatch; SendMessage: a critical decision to main)`);
      // The lander's children stay Claude agents, and each admits the twin beside the lander.
      for (const child of ['flights-hunter', 'flights-spec-walker', 'flights-sub-lander']) {
        assert.ok(text.includes(`subagent_type: ${child}`), `${name}: does not spawn ${child}`);
        const kid = parseFrontmatter(child, renderedText(render, child));
        assert.deepEqual(kid.visibility['visible-to'].include, ['flights-lander', name], `${child}: not visible to both landers`);
      }
      assert.doesNotMatch(text, /gpt-flights-(?:hunter|spec-walker|sub-lander)|gpt-reviewer/, `${name}: a child or reviewer was renamed`);
    }
    if (role === 'flights-foreman') {
      // A GPT foreman's foreman children are GPT: both foreman tiers render as itself.
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
    const fm = parseFrontmatter(name, text);
    const tools = (fm.tools ?? '').split(',').map((tool) => tool.trim());
    // An agent with no tools line inherits every tool, the three effort buttons among them.
    const inheritsAll = fm.tools === undefined;
    const want = effortSteered.get(name);
    if (want === undefined) {
      assert.equal(fm.effortControl, undefined, `${name}: steered by effortControl but not an approved role`);
      assert.deepEqual(tools.filter((tool) => /effort_(?:up|down|auto)$/.test(tool)), [], `${name}: holds effort buttons`);
      continue;
    }
    steered.push(name);
    assert.deepEqual(fm.effortControl, want, `${name}: effortControl`);
    for (const button of inheritsAll ? [] : effortButtons)
      assert.equal(tools.filter((tool) => tool === button).length, 1, `${name}: must hold ${button} once; tools ${fm.tools}`);
  }
  assert.deepEqual(steered.sort(), [...effortSteered.keys()].sort(), 'an approved steered role was not rendered');
  // A twin pins its own tools to drop the buttons: the rest stays its role's list.
  // A role with no tools line inherits every tool, so its twin pins a list of its own that holds no button.
  const toolsOf = (name) => parseFrontmatter(name, renderedText(render, name)).tools?.split(',').map((tool) => tool.trim());
  for (const [name, , , role] of twins.filter(([, , , role]) => effortSteered.has(role))) {
    const roleTools = toolsOf(role);
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
