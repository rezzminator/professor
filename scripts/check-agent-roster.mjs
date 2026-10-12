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
const markdownNames = (dir, nested = false, ancestors = new Set()) => {
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
  return entries
    .flatMap((entry) => {
      const path = join(dir, entry.name);
      const info = entry.isSymbolicLink() ? probe(path) : entry;
      if (nested && info?.isDirectory()) return markdownNames(path, true, next);
      return info?.isFile() && entry.name.endsWith(".md") && !["README.md", "SKILL.md"].includes(entry.name)
        ? [entry.name.slice(0, -3)] : [];
    })
    .sort();
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
let modelLinesRead = 0;
for (const dir of agentSourceDirs) {
  const directory = join(ROOT, dir);
  if (!probe(directory)) continue;
  const args = ["-I", join(ROOT, "scripts/agent-source-inventory.py"), directory];
  if (dir !== "templates/global/agents") args.push("--project");
  const scan = spawnSync("python3", args, { encoding: "utf8" });
  if (scan.error || scan.status !== 0)
    compilerError(scan.error || scan.stderr || `agent source inventory exited ${scan.status}`, args);
  for (const {name, kind, model} of JSON.parse(scan.stdout)) {
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

// A role's generated name: its file's basename, plus `-{suffix}` for a child project; neverRegister
// names a base or a final name that neither runtime emits.
function expectedAgents(nested) {
  const registered = (base, name) => !POLICY.neverRegister.includes(base) && !POLICY.neverRegister.includes(name);
  const expected = markdownNames(join(ROOT, ".claude/agents"), nested).filter((agent) => registered(agent, agent));
  for (const project of projects) {
    let suffix = project;
    if (POLICY.suffixMode === "none") suffix = "";
    if (POLICY.suffixMode === "strip-prefix")
      suffix = project.startsWith(POLICY.suffixPrefix) ? project.slice(POLICY.suffixPrefix.length) : project;
    for (const agent of markdownNames(join(ROOT, project, ".claude/agents"), nested)) {
      const name = suffix ? `${agent}-${suffix.replaceAll("/", "-")}` : agent;
      if (registered(agent, name)) expected.push(name);
    }
  }
  return [...new Set(expected)].sort();
}
function compare(runtime, expected, actual, extension) {
  for (const name of expected.filter((n) => !actual.includes(n)))
    failures.push(`${runtime}: source agent missing from generated roster: ${name}${extension}`);
  for (const name of actual.filter((n) => !expected.includes(n)))
    failures.push(`${runtime}: generated agent has no source: ${name}${extension}`);
}
const codexExpected = expectedAgents(true);
const openCodeExpected = expectedAgents(false);
compare("codex", codexExpected, generatedNames(codexDir, ".toml"), ".toml");
compare("opencode", openCodeExpected, generatedNames(openCodeDir, ".md"), ".md");

for (const [runtime, expected] of [["codex", codexExpected], ["opencode", openCodeExpected]]) {
  if (!expected.length)
    failures.push(`${runtime}: the sources under .claude/agents (root and child projects) define no registered agent — the comparison had nothing to compare`);
}

if (codexExpected.includes("gitter")) {
  const path = join(ROOT, ".codex/agents/gitter.toml");
  if (
    existsSync(path) &&
    /sandbox_mode\s*=\s*"read-only"/.test(readFileSync(path, "utf8"))
  ) {
    failures.push(
      "codex: gitter.toml is registered read-only and cannot perform its source protocol",
    );
  }
}

if (openCodeExpected.includes("gitter")) {
  const path = join(ROOT, ".opencode/agent/gitter.md");
  if (existsSync(path) && !/"git \*": allow/.test(readFileSync(path, "utf8"))) {
    failures.push(
      "opencode: gitter.md lacks its per-agent git write permission",
    );
  }
}

if (failures.length) {
  for (const failure of failures) console.error(`agent-roster: ${failure}`);
  console.error(
    `agent-roster: FAIL — ${failures.length} roster/capability problem(s)`,
  );
  process.exit(1);
}

console.log(
  `agent-roster: clean — ${codexExpected.length} Codex and ${openCodeExpected.length} OpenCode agents match source`,
);
