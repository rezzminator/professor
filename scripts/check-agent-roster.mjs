#!/usr/bin/env node

// Fail when a source agent silently disappears from a generated runtime roster.
// A healthy run proves source discovery completed for both Codex and OpenCode;
// missing generated directories/files are failures named by runtime and path.

import { existsSync, lstatSync, readFileSync, readdirSync, statSync } from "node:fs";
import { spawnSync } from "node:child_process";
import { basename, dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const binary = process.env.ENGINE_SYNC_BIN || "engine-sync";
// A check that crashed never looked: an unexpected throw is an ERROR (exit 2),
// never the roster mismatch exit 1 reports.
process.on("uncaughtException", (error) => {
  console.error(`agent-roster: ERROR — the check could not run: ${error.stack || error}`);
  process.exit(2);
});
const configArgs = ["config", "show", "--json", "."];
function configError(error) {
  console.error(`agent-roster: ERROR — ${binary} ${configArgs.join(" ")}: ${error}`);
  process.exit(2);
}
const result = spawnSync(binary, configArgs, {
  cwd: ROOT, encoding: "utf8", timeout: 30000,
});
if (result.error) configError(result.error.message);
let effective;
try {
  effective = JSON.parse(result.stdout);
} catch (error) {
  configError(`invalid JSON: ${error.message}; ${result.stderr.trim()}`);
}
if (effective?.ok !== true)
  configError(effective?.error || result.stderr.trim() || "config show did not report ok: true");
if (result.status !== 0)
  configError(`exit ${result.status}, signal ${result.signal || "none"}; ${result.stderr.trim()}`);
const CODEX_CONFIG = effective.config?.codex;
const WORKBENCH_MARKER = effective.config?.workbenchMarker;
const stringList = (value) => Array.isArray(value) && value.every((name) => typeof name === "string");
if (!stringList(CODEX_CONFIG?.excludeProjects) || !stringList(CODEX_CONFIG.neverRegister) ||
    (CODEX_CONFIG.projects !== null && !stringList(CODEX_CONFIG.projects)) ||
    typeof CODEX_CONFIG.suffixMode !== "string" || typeof CODEX_CONFIG.suffixPrefix !== "string" ||
    typeof WORKBENCH_MARKER !== "string")
  configError("effective config has invalid codex projects, excludeProjects, neverRegister, suffixMode, suffixPrefix or workbenchMarker fields");
const failures = [];

// Agent sources as the compilers read them: README.md is never an agent;
// Codex walks nested directories and names a role by its file's basename,
// OpenCode reads the top level only.
const markdownNames = (dir, nested = false) =>
  existsSync(dir)
    ? readdirSync(dir, { withFileTypes: true })
        .flatMap((entry) =>
          nested && entry.isDirectory()
            ? markdownNames(join(dir, entry.name), true)
            : entry.name.endsWith(".md") && !["README.md", "SKILL.md"].includes(entry.name)
              ? [entry.name.slice(0, -3)]
              : [],
        )
        .sort()
    : [];

const generatedNames = (dir, extension) =>
  existsSync(dir)
    ? readdirSync(dir)
        .filter((name) => name.endsWith(extension))
        .map((name) => name.slice(0, -extension.length))
        .sort()
    : [];

// Child projects as engine-sync discovers them (internal/source/discover.go
// Projects): an explicit codex.projects list wins over the root scan and its
// excludeProjects; a child holding the workbench marker builds as its own root.
const hasOrientation = (dir) => {
  try {
    return statSync(join(dir, "CLAUDE.md")).isFile();
  } catch {
    return false;
  }
};
const isWorkbench = (dir) => {
  if (!WORKBENCH_MARKER) return false;
  let current = dir;
  for (const part of WORKBENCH_MARKER.split("/")) {
    current = join(current, part);
    try {
      if (lstatSync(current).isSymbolicLink()) return false;
    } catch {
      return false;
    }
  }
  return true;
};
const candidates = CODEX_CONFIG.projects !== null
  ? CODEX_CONFIG.projects.filter((name) => name !== ".")
  : readdirSync(ROOT, { withFileTypes: true })
      .filter((entry) => entry.isDirectory() && ![".claude", ".codex"].includes(entry.name) &&
        !CODEX_CONFIG.excludeProjects.includes(entry.name))
      .map((entry) => entry.name);
const projects = [...new Set(candidates)]
  .filter((name) => !isWorkbench(join(ROOT, name)) && hasOrientation(join(ROOT, name)))
  .sort();

function expectedAgents(nested, suffixMode, suffixPrefix = "", neverRegister = []) {
  const expected = markdownNames(join(ROOT, ".claude/agents"), nested)
    .filter((agent) => !neverRegister.includes(agent));
  for (const project of projects) {
    for (const agent of markdownNames(join(ROOT, project, ".claude/agents"), nested)) {
      let suffix = project;
      if (suffixMode === "none") suffix = "";
      if (suffixMode === "strip-prefix")
        suffix = project.startsWith(suffixPrefix)
          ? project.slice(suffixPrefix.length)
          : project;
      const name = suffix ? `${agent}-${suffix}` : agent;
      if (!neverRegister.includes(agent) && !neverRegister.includes(name)) expected.push(name);
    }
  }
  return [...new Set(expected)].sort();
}

function compare(runtime, expected, actual, extension) {
  const missing = expected.filter((name) => !actual.includes(name));
  const extra = actual.filter((name) => !expected.includes(name));
  for (const name of missing)
    failures.push(
      `${runtime}: source agent missing from generated roster: ${name}${extension}`,
    );
  for (const name of extra)
    failures.push(
      `${runtime}: generated agent has no source: ${name}${extension}`,
    );
}

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

const codexExpected = expectedAgents(
  true,
  CODEX_CONFIG.suffixMode,
  CODEX_CONFIG.suffixPrefix,
  CODEX_CONFIG.neverRegister,
);
const codexActual = generatedNames(codexDir, ".toml");
compare("codex", codexExpected, codexActual, ".toml");

const openCodeExpected = expectedAgents(false, "project");
const openCodeActual = generatedNames(openCodeDir, ".md");
compare("opencode", openCodeExpected, openCodeActual, ".md");

for (const runtime of ["codex", "opencode"]) {
  const expected = runtime === "codex" ? codexExpected : openCodeExpected;
  if (!expected.length)
    failures.push(
      `${runtime}: source discovery returned an empty agent roster`,
    );
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
  for (const name of markdownNames(join(ROOT, dir))) {
    const text = readFileSync(join(ROOT, dir, `${name}.md`), "utf8");
    const front = text.startsWith("---\n") ? text.slice(4, text.indexOf("\n---", 4)) : "";
    const line = front.split("\n").find((l) => /^model:/.test(l));
    if (!line) continue;
    modelLinesRead++;
    const value = line.slice("model:".length).trim();
    if (!/^[A-Za-z0-9._\[\]-]+$/.test(value))
      failures.push(
        `${dir}/${name}.md: frontmatter model "${value}" is not one bare alias — the harness sends it to the API verbatim`,
      );
  }
}
if (!modelLinesRead)
  failures.push(
    `agent model check read no model line in ${agentSourceDirs.join(", ")} — the check did not run`,
  );

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
