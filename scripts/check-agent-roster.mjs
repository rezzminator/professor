#!/usr/bin/env node

// Fail when a source agent silently disappears from a generated runtime roster.
// A healthy run proves source discovery completed for both Codex and OpenCode;
// missing generated directories/files are failures named by runtime and path.

import { existsSync, readFileSync, readdirSync } from "node:fs";
import { basename, dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const CODEX_CONFIG = JSON.parse(
  readFileSync(join(ROOT, ".claude/codex-build.json"), "utf8"),
);
const failures = [];

const markdownNames = (dir) =>
  existsSync(dir)
    ? readdirSync(dir)
        .filter((name) => name.endsWith(".md"))
        .map((name) => name.slice(0, -3))
        .sort()
    : [];

const generatedNames = (dir, extension) =>
  existsSync(dir)
    ? readdirSync(dir)
        .filter((name) => name.endsWith(extension))
        .map((name) => name.slice(0, -extension.length))
        .sort()
    : [];

const projects = readdirSync(ROOT, { withFileTypes: true })
  .filter(
    (entry) =>
      entry.isDirectory() && existsSync(join(ROOT, entry.name, "CLAUDE.md")),
  )
  .map((entry) => entry.name)
  .sort();

const rootAgents = markdownNames(join(ROOT, ".claude/agents"));

function expectedAgents(excludedProjects, suffixMode, suffixPrefix = "") {
  const expected = [...rootAgents];
  for (const project of projects) {
    if (excludedProjects.includes(project)) continue;
    for (const agent of markdownNames(join(ROOT, project, ".claude/agents"))) {
      let suffix = project;
      if (suffixMode === "none") suffix = "";
      if (suffixMode === "strip-prefix")
        suffix = project.startsWith(suffixPrefix)
          ? project.slice(suffixPrefix.length)
          : project;
      expected.push(suffix ? `${agent}-${suffix}` : agent);
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
    `agent-roster: NOT GENERATED — ${missingDirs.join(", ")} absent; run: cd pfm && go run ./cmd/pfm codex build .. && go run ./cmd/pfm opencode build ..`,
  );
  process.exit(3);
}

const codexExpected = expectedAgents(
  CODEX_CONFIG.excludeProjects ?? [],
  CODEX_CONFIG.suffixMode ?? "project",
  CODEX_CONFIG.suffixPrefix ?? "",
);
const codexActual = generatedNames(codexDir, ".toml");
compare("codex", codexExpected, codexActual, ".toml");

const openCodeExpected = expectedAgents(["templates"], "project");
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
