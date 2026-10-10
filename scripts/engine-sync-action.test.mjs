// Tests for .github/actions/engine-sync/action.yml — the CI compiler action.
// Run: node --test scripts/engine-sync-action.test.mjs
//
// Each case runs the action's actual YAML run script in a throwaway workspace;
// only the Go compiler build is played, by a `go` function writing a stub binary.

import { test } from "node:test";
import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

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

test("the action stages and exports the machine policy, or names the failure before any build", async (t) => {
  for (const [state, expected] of [["present", null], ["missing", ABSENT], ["symlink loop", ABSENT], ["staging blocked", UNSTAGED]]) {
    await t.test(state, (t) => {
      const root = mkdtempSync(join(tmpdir(), "ci-engine-sync-"));
      t.after(() => rmSync(root, { recursive: true, force: true }));
      const policy = join(root, "templates/global/engine-sync/config.json");
      const runner = join(root, "runner");
      const envFile = join(root, "github-env");
      for (const path of [dirname(policy), join(root, "plugins/engine-sync"), runner, join(root, "home")])
        mkdirSync(path, { recursive: true });
      writeFileSync(join(root, "plugins/engine-sync/go.mod"), "module fixture\n");
      writeFileSync(envFile, "");
      const bytes = '{"version":1,"codex":{"suffixMode":"none"}}\n';
      if (state === "symlink loop") symlinkSync("config.json", policy);
      else if (state !== "missing") writeFileSync(policy, bytes);
      if (state === "staging blocked") writeFileSync(join(runner, "engine-sync-config"), "blocked\n");
      const r = spawnSync("bash", ["-c", COMPILER + "set -e\n" + RUNS.join("\n")], {
        cwd: root, env: { PATH: process.env.PATH, HOME: join(root, "home"), XDG_CONFIG_HOME: join(root, "empty-config"),
          GITHUB_WORKSPACE: root, RUNNER_TEMP: runner, GITHUB_ENV: envFile }, encoding: "utf8",
      });
      const output = r.stdout + r.stderr;
      if (expected === null) {
        assert.equal(r.status, 0, output);
        assert.equal(readFileSync(envFile, "utf8"),
          `ENGINE_SYNC_BIN=${runner}/engine-sync/engine-sync\nXDG_CONFIG_HOME=${runner}/engine-sync-config\n`);
        assert.equal(readFileSync(join(runner, "engine-sync-config/engine-sync/config.json"), "utf8"), bytes);
        return;
      }
      assert.equal(r.status, 1, output);
      assert.ok(output.includes(expected + policy), output);
      assert.equal(readFileSync(envFile, "utf8"), "", "a failed staging exports nothing");
      assert.equal(existsSync(join(runner, "engine-sync/engine-sync")), false, "the policy fails before the compiler builds");
    });
  }
});
