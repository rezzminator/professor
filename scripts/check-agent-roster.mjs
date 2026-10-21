#!/usr/bin/env node

// Fail when a source agent silently disappears from a generated runtime roster.
// Two independent judges, both must pass: engine-sync's own read-only check, and
// this script's comparison of the roles the Claude sources define (.claude/agents
// of the root and of each child project) with the files generated for Codex
// (.codex/agents/*.toml) and OpenCode (.opencode/agent/*.md). The comparison reads
// engine-sync's EFFECTIVE POLICY (project selection, suffix mode, neverRegister,
// workbench marker) from `config show --json` and walks the sources itself, so a
// source-discovery defect in the writer that its own check agrees with still fails
// here. Missing generated directories/files are failures named by runtime and path.

import { existsSync, lstatSync, readFileSync, readdirSync, realpathSync, statSync } from "node:fs";
import { spawnSync } from "node:child_process";
import { dirname, join, resolve } from "node:path";
import { homedir } from "node:os";
import { fileURLToPath } from "node:url";

const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const binary = process.env.ENGINE_SYNC_BIN || "engine-sync";
// A check that crashed never looked: an unexpected throw is an ERROR (exit 2),
// never the roster mismatch exit 1 reports.
process.on("uncaughtException", (error) => {
  console.error(`agent-roster: ERROR — the check could not run: ${error.stack || error}`);
  process.exit(2);
});
const checkArgs = ["check", "--json", "."];
const configArgs = ["config", "show", "--json", "."];
function compilerError(error, args = checkArgs) {
  console.error(`agent-roster: ERROR — ${binary} ${args.join(" ")}: ${error}`);
  process.exit(2);
}
const failures = [];
function reportFailures() {
  for (const failure of failures) console.error(`agent-roster: ${failure}`);
  if (failures.length)
    console.error(`agent-roster: FAIL — ${failures.length} roster/capability problem(s)`);
}
// A path lookup. Only ENOENT and ENOTDIR mean the path is absent (null); any other error (permission,
// a symlink loop) is a lookup that failed, recorded by name and never read as absence.
const probe = (path, link = false) => {
  try { return link ? lstatSync(path) : statSync(path); }
  catch (error) {
    if (!["ENOENT", "ENOTDIR"].includes(error.code)) failures.push(`cannot stat ${path}: ${error.code || error.message}`);
    return null;
  }
};
// Agent sources as the compilers read them: README.md and SKILL.md are never an agent; Codex walks
// nested directories and names a role by its file's basename, OpenCode reads the top level only.
const markdownSources = (dir, nested = false, ancestors = new Set()) => {
  if (!probe(dir)) return [];
  let physical;
  try { physical = realpathSync(dir); }
  catch (error) {
    failures.push(`cannot resolve agent source directory ${dir}: ${error.code || error.message}`);
    return [];
  }
  if (ancestors.has(physical)) {
    failures.push(`cyclic agent source directory ${dir}`);
    return [];
  }
  const next = new Set([...ancestors, physical]);
  let entries;
  try { entries = readdirSync(dir, { withFileTypes: true }); }
  catch (error) {
    // A source directory that cannot be read is a named failure, never an empty roster.
    failures.push(`cannot read agent source directory ${dir}: ${error.code || error.message}`);
    return [];
  }
  const headers = new Map(sourceInventory(dir).map((row) => [row.name, row]));
  return entries
    .flatMap((entry) => {
      const path = join(dir, entry.name);
      const info = entry.isSymbolicLink() ? probe(path) : entry;
      if (nested && info?.isDirectory()) return markdownSources(path, true, next);
      if (!info?.isFile() || !entry.name.endsWith(".md") || ["README.md", "SKILL.md"].includes(entry.name)) return [];
      const name = entry.name.slice(0, -3);
      const header = headers.get(name);
      if (!header) throw new Error(`agent source inventory omitted ${path}`);
      return [header];
    })
    .sort((a, b) => a.name.localeCompare(b.name));
};
const generatedNames = (dir, extension) => existsSync(dir)
  ? readdirSync(dir).filter((name) => name.endsWith(extension))
      .map((name) => name.slice(0, -extension.length)).sort()
  : [];

// Claude Code sends an agent's frontmatter `model:` value to the API verbatim —
// an inline `# comment` included — so `fable # …` is a 404 model_not_found.
// Every agent source's model value is one bare alias or id.
const agentSourceDirs = [
  ".claude/agents",
  "templates/global/agents",
  "templates/project/agents",
];
const inventoryCache = new Map();
function sourceInventory(directory) {
  if (inventoryCache.has(directory)) return inventoryCache.get(directory);
  const args = ["-I", join(ROOT, "scripts/agent-source-inventory.py"), directory];
  if (directory !== join(ROOT, "templates/global/agents")) args.push("--project");
  const scan = spawnSync("python3", args, { encoding: "utf8" });
  // The inventory's exit 3 is a toolchain gap; roster exit 3 is only absent mirrors.
  if (scan.status === 3) {
    console.error(`agent-roster: ${scan.stderr.trim()}`);
    process.exit(4);
  }
  if (scan.error || scan.status !== 0) {
    // Only the producer's validation verdict rejects a source; lookup failures and crashes are errors.
    const rejected = !scan.error && scan.status === 1 && scan.stderr.startsWith("agent-source-inventory: REJECTED —");
    const context = scan.error?.stack || scan.stderr.trim() || `exit ${scan.status}, signal ${scan.signal || "none"}`;
    console.error(`agent-roster: ${rejected ? "SOURCE REJECTED" : "ERROR"} — python3 ${args.join(" ")}: ${context}`);
    process.exit(rejected ? 5 : 2);
  }
  const rows = JSON.parse(scan.stdout);
  // Reuse the inventory's structured frontmatter reader for registration names
  // and the tools-derived sandbox default, rather than parsing YAML as prose.
  const metadata = spawnSync("python3", ["-I", "-c", [
    "import json,pathlib,runpy,sys",
    "read = runpy.run_path(sys.argv[1])['frontmatter']",
    "print(json.dumps([read(pathlib.Path(path))[0] for path in json.load(sys.stdin)]))",
  ].join("\n"), join(ROOT, "scripts/agent-source-inventory.py")], {
    input: JSON.stringify(rows.map((row) => row.path)), encoding: "utf8",
  });
  if (metadata.error || metadata.status !== 0)
    throw new Error(`cannot read source protocols in ${directory}: ${metadata.error || metadata.stderr.trim() || `exit ${metadata.status}`}`);
  const headers = JSON.parse(metadata.stdout);
  for (const [index, row] of rows.entries()) {
    const header = headers[index];
    row.base = typeof header.name === "string" && header.name.trim() ? header.name.trim() : row.name;
    row.tools = header.tools;
  }
  inventoryCache.set(directory, rows);
  return rows;
}
let modelLinesRead = 0;
for (const dir of agentSourceDirs) {
  const directory = join(ROOT, dir);
  if (!probe(directory)) continue;
  for (const {name, kind, model} of sourceInventory(directory)) {
    const text = readFileSync(join(directory, `${name}.md`), "utf8");
    const front = text.startsWith("---\n") ? text.slice(4, text.indexOf("\n---", 4)) : "";
    const line = front.split("\n").find((l) => /^model:/.test(l));
    if (!line) continue;
    modelLinesRead++;
    // Declarations are parsed and re-emitted by the compiler; Claude never reads their YAML quotes.
    const value = kind !== "original" ? model : line.slice("model:".length).trim();
    if (typeof value !== "string" || !/^[A-Za-z0-9._\[\]-]+$/.test(value))
      failures.push(
        `${dir}/${name}.md: frontmatter model "${value}" is not one bare alias — the harness sends it to the API verbatim`,
      );
  }
}
if (!modelLinesRead)
  failures.push(
    `agent model check read no model line in ${agentSourceDirs.join(", ")} — the check did not run`,
  );

// Generated mirrors are never tracked (see .gitignore); a fresh clone has
// neither directory until its compiler runs. That is the pre-generate state,
// not a roster mismatch — reported by name, distinct exit code, so a
// pipeline that forgot to generate fails loudly instead of drowning in a
// wall of "source agent missing" lines for every role.
const codexDir = join(ROOT, ".codex/agents");
const openCodeDir = join(ROOT, ".opencode/agent");
const missingDirs = [
  !existsSync(codexDir) && "codex (.codex/agents/)",
  !existsSync(openCodeDir) && "opencode (.opencode/agent/)",
].filter(Boolean);
if (missingDirs.length) {
  reportFailures();
  console.error(
    `agent-roster: NOT GENERATED — ${missingDirs.join(", ")} absent; run: engine-sync build .`,
  );
  process.exit(3);
}

// Judge 1 — engine-sync's own read-only check under its effective policy.
const result = spawnSync(binary, checkArgs, { cwd: ROOT, encoding: "utf8", timeout: 30000 });
if (result.error) compilerError(result.error.message);
let checked;
try { checked = JSON.parse(result.stdout); }
catch (error) { compilerError(`invalid JSON: ${error.message}; ${result.stderr.trim()}`); }
if (typeof checked?.ok !== "boolean" || !Array.isArray(checked.problems) ||
    !checked.problems.every((problem) => typeof problem === "string"))
  compilerError("compiler check has invalid ok or problems fields");
if (checked.error) compilerError(checked.error);
if ((result.status === 0) !== checked.ok || (result.status !== 0 && result.status !== 1))
  compilerError(`exit ${result.status}, signal ${result.signal || "none"}; ${result.stderr.trim()}`);
if (!checked.ok) {
  if (!checked.problems.length) compilerError("compiler check failed without a named problem");
  failures.push(...checked.problems.map((problem) => `compiler: ${problem}`));
}

// Judge 2 — the sources against the generated files. Only the POLICY comes from engine-sync.
const configResult = spawnSync(binary, configArgs, { cwd: ROOT, encoding: "utf8", timeout: 30000 });
if (configResult.error) compilerError(configResult.error.message, configArgs);
let effective;
try { effective = JSON.parse(configResult.stdout); }
catch (error) { compilerError(`invalid JSON: ${error.message}; ${configResult.stderr.trim()}`, configArgs); }
if (effective?.ok !== true)
  compilerError(effective?.error || configResult.stderr.trim() || "config show did not report ok: true", configArgs);
if (configResult.status !== 0)
  compilerError(`exit ${configResult.status}, signal ${configResult.signal || "none"}; ${configResult.stderr.trim()}`, configArgs);
const POLICY = effective.config?.codex;
const WORKBENCH_MARKER = effective.config?.workbenchMarker;
const stringList = (value) => Array.isArray(value) && value.every((name) => typeof name === "string");
if (!stringList(POLICY?.excludeProjects) || !stringList(POLICY.neverRegister) ||
    (POLICY.projects !== null && !stringList(POLICY.projects)) ||
    typeof POLICY.suffixMode !== "string" || typeof POLICY.suffixPrefix !== "string" ||
    typeof WORKBENCH_MARKER !== "string")
  compilerError("effective config has invalid codex projects, excludeProjects, neverRegister, suffixMode, suffixPrefix or workbenchMarker fields", configArgs);

// Child projects as engine-sync discovers them (plugins/engine-sync internal/source/discover.go
// Projects): an explicit codex.projects list replaces the root scan; a
// child holding the workbench marker builds as its own root; a child needs a regular CLAUDE.md.
const hasOrientation = (dir) => probe(join(dir, "CLAUDE.md"))?.isFile() === true;
const isWorkbench = (dir) => {
  if (!WORKBENCH_MARKER) return false;
  let current = dir;
  for (const part of WORKBENCH_MARKER.split("/")) {
    current = join(current, part);
    const found = probe(current, true);
    if (!found || found.isSymbolicLink()) return false;
  }
  return true;
};
// excludeProjects applies to the explicit list and to the root scan alike (source.Excluded).
const excluded = (name) => POLICY.excludeProjects.some((value) =>
  value === name || name.startsWith(`${value}/`) || `/${name}`.includes(`/${value}/`));
const candidates = POLICY.projects !== null
  ? POLICY.projects.filter((name) => name !== ".")
  : readdirSync(ROOT, { withFileTypes: true })
      .filter((entry) => entry.isDirectory() && ![".claude", ".codex"].includes(entry.name))
      .map((entry) => entry.name);
const projects = [...new Set(candidates)]
  .filter((name) => !excluded(name) && !isWorkbench(join(ROOT, name)) && hasOrientation(join(ROOT, name)))
  .sort();

// Capabilities are host-only: config show omits targets, and this checker supplies
// no --capability flags. Read the same fixed global path as config.LoadTargets;
// an unreadable/malformed policy is an error, never an empty capability set.
const hostPolicyPath = join(homedir(), ".claude/engine-sync.json");
const hostPolicy = probe(hostPolicyPath) ? JSON.parse(readFileSync(hostPolicyPath, "utf8")) : {};
const configuredCapabilities = hostPolicy.targets?.capabilities ?? [];
if (!stringList(configuredCapabilities) || configuredCapabilities.some((name) => !name.trim()))
  throw new Error(`${hostPolicyPath}: targets.capabilities must contain nonempty capability names`);
const capabilities = new Set(configuredCapabilities.map((name) => name.trim()));
function eligible(source, runtime) {
  const { engines, requires } = source;
  if (!engines || typeof engines !== "object" || Array.isArray(engines) ||
      !stringList(requires) || requires.some((name) => !name.trim()))
    throw new Error(`${source.path}: invalid engines/requires eligibility metadata`);
  const engine = engines[runtime];
  if (engine !== undefined && engine !== false && (!engine || typeof engine !== "object" || Array.isArray(engine)))
    throw new Error(`${source.path}: engines.${runtime} must be settings or false`);
  return engine !== false && requires.every((name) => capabilities.has(name.trim()));
}

// One registration walk supplies both roster names and each artifact's OWN source.
// Match source.RoleName/DeclaredName: a declared name (else basename), child suffix,
// then neverRegister by base or final name. Never discard a collision with a Set/map overwrite.
const sourceCounts = new Map();
function expectedAgents(runtime, nested) {
  const registered = (base, name) => !POLICY.neverRegister.includes(base) && !POLICY.neverRegister.includes(name);
  const registrations = [];
  let defined = 0;
  for (const project of [".", ...projects]) {
    let suffix = project === "." ? "" : project;
    if (POLICY.suffixMode === "none") suffix = "";
    if (project !== "." && POLICY.suffixMode === "strip-prefix")
      suffix = project.startsWith(POLICY.suffixPrefix) ? project.slice(POLICY.suffixPrefix.length) : project;
    const sources = markdownSources(join(ROOT, project, ".claude/agents"), nested);
    defined += sources.length;
    for (const source of sources) {
      if (project !== "." && !suffix && POLICY.suffixMode !== "none") {
        failures.push(`${runtime}: ${source.path}: project ${project} has an empty agent suffix under ${POLICY.suffixMode} policy`);
        continue;
      }
      const name = suffix ? `${source.base}-${suffix.replaceAll("/", "-")}` : source.base;
      if (registered(source.base, name)) registrations.push({ name, source });
    }
  }
  // Both compilers reserve names BEFORE eligibility, using paths.FoldName
  // (Unicode NFC + case fold). Python supplies full case folding, not JS lowercasing.
  const folded = spawnSync("python3", ["-I", "-c",
    "import json,sys,unicodedata; print(json.dumps([unicodedata.normalize('NFC', name).casefold() for name in json.load(sys.stdin)]))"], {
    input: JSON.stringify(registrations.map(({ name }) => name)), encoding: "utf8",
  });
  if (folded.error || folded.status !== 0)
    throw new Error(`cannot compare ${runtime} registration names: ${folded.error || folded.stderr.trim() || `exit ${folded.status}`}`);
  const keys = JSON.parse(folded.stdout);
  const seen = new Map();
  const expected = [];
  for (const [index, registration] of registrations.entries()) {
    const { name, source } = registration;
    const previous = seen.get(keys[index]);
    if (previous) {
      failures.push(`${runtime}: conflicting role name ${name} from ${previous.path} and ${source.path}`);
      continue;
    }
    seen.set(keys[index], source);
    if (eligible(source, runtime)) expected.push(registration);
  }
  sourceCounts.set(runtime, defined);
  return expected;
}
function compare(runtime, expected, actual, extension) {
  for (const name of expected.filter((n) => !actual.includes(n)))
    failures.push(`${runtime}: source agent missing from generated roster: ${name}${extension}`);
  for (const name of actual.filter((n) => !expected.includes(n)))
    failures.push(`${runtime}: generated agent has no source: ${name}${extension}`);
}
const codexRegistrations = expectedAgents("codex", true);
const openCodeRegistrations = expectedAgents("opencode", false);
const codexExpected = codexRegistrations.map(({ name }) => name).sort();
const openCodeExpected = openCodeRegistrations.map(({ name }) => name).sort();
compare("codex", codexExpected, generatedNames(codexDir, ".toml"), ".toml");
compare("opencode", openCodeExpected, generatedNames(openCodeDir, ".md"), ".md");

for (const [runtime, expected] of [["codex", codexExpected], ["opencode", openCodeExpected]]) {
  if (!expected.length && sourceCounts.get(runtime) === 0)
    failures.push(`${runtime}: the sources under .claude/agents (root and child projects) define no registered agent — the comparison had nothing to compare`);
}

function artifactPermissions(path, runtime) {
  const args = ["-I", join(ROOT, "scripts/agent-source-inventory.py"), path, "--artifact", runtime];
  const parsed = spawnSync("python3", args, { encoding: "utf8" });
  if (parsed.error || parsed.status !== 0)
    throw new Error(`cannot read structured ${runtime} permissions in ${path}: ${parsed.error || parsed.stderr.trim() || `exit ${parsed.status}`}`);
  return JSON.parse(parsed.stdout);
}
for (const [runtime, registrations] of [["codex", codexRegistrations], ["opencode", openCodeRegistrations]]) {
  for (const { name, source } of registrations) {
    if (!["gitter", "gpt-gitter"].includes(source.base)) continue;
    if (runtime === "codex") {
      const path = join(codexDir, `${name}.toml`);
      const configured = source.engines.codex?.sandbox?.trim();
      const tools = (Array.isArray(source.tools) ? source.tools.join(",") : source.tools ?? "").trim();
      // codexRoleSettings: an explicit pin wins; inherit is no override;
      // otherwise a nonempty tools list with no write tool defaults to read-only.
      const readOnly = tools && !tools.split(",").some((tool) =>
        ["Write", "Edit", "MultiEdit", "NotebookEdit"].includes(tool.trim()));
      const expected = configured === "inherit" ? null : configured || (readOnly ? "read-only" : null);
      if (existsSync(path) && artifactPermissions(path, runtime).sandbox !== expected)
        failures.push(`codex: ${name}.toml sandbox override differs from its source protocol (expected ${expected ?? "inherit"}) — ${source.path}`);
    } else {
      const path = join(openCodeDir, `${name}.md`);
      const expected = source.engines.opencode?.permission ?? null;
      // Serialize parsed data, never instruction text. Keep mapping order:
      // OpenCode uses the LAST matching permission rule, so deep equality that
      // ignores object-key order would accept a Git-denying rule permutation.
      if (existsSync(path) && JSON.stringify(artifactPermissions(path, runtime).permission) !== JSON.stringify(expected))
        failures.push(`opencode: ${name}.md lacks its source per-agent git write permission — ${source.path}`);
    }
  }
}

if (failures.length) {
  reportFailures();
  process.exit(1);
}

console.log(
  `agent-roster: clean — ${codexExpected.length} Codex and ${openCodeExpected.length} OpenCode agents match source`,
);
