// Tests for .github/actions/engine-sync/action.yml — the CI compiler action.
// Run: node --test scripts/engine-sync-action.test.mjs
//
// Each case runs the action's actual YAML run script in a throwaway workspace;
// only the Go compiler build is played, by a `go` function writing a stub binary.

import { test, assert, execFileSync, spawnSync, existsSync, mkdirSync, mkdtempSync, readFileSync,
  rmSync, symlinkSync, writeFileSync, tmpdir, dirname, join, fileURLToPath } from "./node-test-helpers.mjs";

const ACTION = join(dirname(fileURLToPath(import.meta.url)), "../.github/actions/engine-sync/action.yml");
const RUNS = JSON.parse(execFileSync("python3", ["-c",
  'import json, sys, yaml; print(json.dumps([s["run"] for s in yaml.safe_load(open(sys.argv[1]))["runs"]["steps"]]))',
  ACTION], { encoding: "utf8" }));
const COMPILER = `go() {
  local output
  while [ "$#" -gt 0 ]; do
    if [ "$1" = -o ]; then output="$2"; break; fi
    shift
  done
  mkdir -p "$(dirname "$output")"
  printf '#!/usr/bin/env bash\\nexit 0\\n' > "$output"
  chmod +x "$output"
}
export -f go
`;
const ABSENT = "::error::engine-sync machine policy absent or unreadable: ";
const UNSTAGED = "::error::engine-sync machine policy could not be staged from ";

const FOREIGN = "::error::engine-sync global file exists with other content, not overwritten: ";

test("the action stages and exports the machine policy, or names the failure before any build", async (t) => {
  for (const [state, expected] of [["present", null], ["already staged", null], ["missing", ABSENT], ["symlink loop", ABSENT],
    ["staging blocked", UNSTAGED], ["another file in place", FOREIGN]]) {
    await t.test(state, (t) => {
      const root = mkdtempSync(join(tmpdir(), "ci-engine-sync-"));
      t.after(() => rmSync(root, { recursive: true, force: true }));
      const policy = join(root, "templates/global/engine-sync.json");
      const runner = join(root, "runner");
      const envFile = join(root, "github-env");
      const pathFile = join(root, "github-path");
      for (const path of [dirname(policy), join(root, "plugins/engine-sync"), runner, join(root, "home")])
        mkdirSync(path, { recursive: true });
      writeFileSync(join(root, "plugins/engine-sync/go.mod"), "module fixture\n");
      writeFileSync(envFile, "");
      writeFileSync(pathFile, "");
      const bytes = '{"version":1,"codex":{"suffixMode":"none"}}\n';
      const destination = join(root, "home/.claude/engine-sync.json");
      if (state === "symlink loop") symlinkSync("engine-sync.json", policy);
      else if (state !== "missing") writeFileSync(policy, bytes);
      if (state === "staging blocked") writeFileSync(join(root, "home/.claude"), "blocked\n");
      if (state === "already staged" || state === "another file in place") {
        mkdirSync(dirname(destination), { recursive: true });
        writeFileSync(destination, state === "already staged" ? bytes : '{"version":1}\n');
      }
      const r = spawnSync("bash", ["-c", COMPILER + "set -e\n" + RUNS.join("\n")], {
        cwd: root, env: { PATH: process.env.PATH, HOME: join(root, "home"),
          GITHUB_WORKSPACE: root, RUNNER_TEMP: runner, GITHUB_ENV: envFile, GITHUB_PATH: pathFile }, encoding: "utf8",
      });
      const output = r.stdout + r.stderr;
      if (expected === null) {
        assert.equal(r.status, 0, output);
        assert.equal(readFileSync(envFile, "utf8"),
          `ENGINE_SYNC_BIN=${runner}/engine-sync/engine-sync\n`);
        assert.equal(readFileSync(destination, "utf8"), bytes);
        assert.equal(readFileSync(pathFile, "utf8"), `${runner}/engine-sync\n`);
        return;
      }
      assert.equal(r.status, 1, output);
      assert.ok(output.includes(expected + (expected === FOREIGN ? destination : policy)), output);
      if (expected === FOREIGN) assert.equal(readFileSync(destination, "utf8"), '{"version":1}\n');
      assert.equal(readFileSync(envFile, "utf8"), "", "a failed staging exports nothing");
      assert.equal(readFileSync(pathFile, "utf8"), "", "a failed staging puts nothing on PATH");
      assert.equal(existsSync(join(runner, "engine-sync/engine-sync")), false, "the policy fails before the compiler builds");
    });
  }
});
