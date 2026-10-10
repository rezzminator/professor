#!/usr/bin/env node

// Fail when a source agent silently disappears from a generated runtime roster.
// A healthy run proves source discovery completed for both Codex and OpenCode;
// missing generated directories/files are failures named by runtime and path.

import { existsSync, readFileSync, readdirSync } from "node:fs";
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
function compilerError(error) {
  console.error(`agent-roster: ERROR — ${binary} ${checkArgs.join(" ")}: ${error}`);
  process.exit(2);
}
const failures = [];
const markdownNames = (dir) => existsSync(dir)
  ? readdirSync(dir, { withFileTypes: true })
      .filter((entry) => entry.isFile() && entry.name.endsWith(".md") && !["README.md", "SKILL.md"].includes(entry.name))
      .map((entry) => entry.name.slice(0, -3)).sort()
  : [];
const generatedNames = (dir, extension) => existsSync(dir)
  ? readdirSync(dir).filter((name) => name.endsWith(extension))
      .map((name) => name.slice(0, -extension.length)).sort()
  : [];

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

// The compiler alone owns source discovery, exclusions, suffixes and orphan ownership.
// Its read-only check catches missing and extra generated roles under its effective policy.
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
const codexExpected = generatedNames(codexDir, ".toml");
const openCodeExpected = generatedNames(openCodeDir, ".md");

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
