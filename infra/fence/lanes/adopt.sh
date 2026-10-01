#!/usr/bin/env bash
# Deterministic adoption of an invented local node project; no engine turn.
set -euo pipefail
[ "${PFM_DEV_FENCE:-}" = 1 ] || { echo 'adopt: requires the fence' >&2; exit 1; }
export PATH="$HOME/.local/bin:$PATH"
step='create project'
trap 'rc=$?; echo "adopt: $step failed at line $LINENO (exit $rc) — output above" >&2; exit 1' ERR
DIR=/work/express
if [ -f "$DIR/.professor/baseline.json" ] && grep -qx 'professor: install' <<<"$(git -C "$DIR" log --format=%s)"; then
  echo 'adopt: express already adopted'; exit 0
fi
mkdir -p "$DIR/test"
cd "$DIR"
if [ ! -d .git ]; then
  git init -q -b develop
  printf '{"name":"lane-express-fixture","private":true,"scripts":{"test":"node --test"}}\n' >package.json
  printf "const test = require('node:test');\nconst assert = require('node:assert/strict');\ntest('fixture arithmetic', () => assert.equal(2 + 2, 4));\n" >test/fixture.test.js
  git add package.json test
  git -c user.name=demo -c user.email=demo@example.invalid commit -qm init
fi
step='pfm init'
[ -f .professor/baseline.json ] || pfm init .
step='fill registered tokens'
python3 - <<'PY'
import json
import re
from pathlib import Path

registry = re.split(r'^## Runtime metavariables', Path('/worktree/docs/PLACEHOLDERS.md').read_text(), flags=re.M)[0]
tokens = {name: 'lane-fixture' for name in re.findall(r'\{([A-Z][A-Z0-9_]+)\}', registry)}
tokens.update({
    'PROJECT_NAME': 'Express Fixture', 'PROJECT_TAGLINE': 'Invented lane project',
    'PROJECT_DOMAIN': 'express.lane.invalid', 'BLUEPRINT_REPO': 'fixture/blueprint',
    'GH_USER': 'fixture', 'BLUEPRINT_CLONE_PATH': '/worktree', 'PROJECT_ROSTER': 'express',
    'PROJECT': 'express', 'PROJECT_ROLE': 'fixture', 'PROJECT_STACK': 'Node.js',
    'PROJECT_PKG_MGR': 'npm', 'PROJECT_TEST_RUNNER': 'node --test', 'PROJECT_PORT': '3000',
    'PROJECT_BUILD_CMD': 'node --check test/fixture.test.js',
    'PROJECT_TYPECHECK': 'node --check test/fixture.test.js', 'PROJECT_FORMAT': ':',
    'PROJECT_LINT': 'node --check test/fixture.test.js', 'PROJECT_INSTALL_CMD': ':',
    'PROJECT_RUN_CMD': 'node --test', 'PROJECT_ENV_FILES': '-',
    'HEALTH_PROBE': ':', 'ENV_FILE_PROVISION': ':', 'ENV_BOOTSTRAP': ':',
    'POST_INSTALL_HOOKS': ':', 'STATUS_EXTRA_PROBES': ':', 'DEV_PROCESS_PATTERN': 'lane-fixture',
    'DEV_PREREQS': ':', 'PORT_DEFAULTS': 'EXPRESS_PORT=3000',
    'SEED_PROJECT': '-', 'MIGRATIONS_DIR': '-', 'ROSTER_DOC_PATHS': 'docs/express',
    'PROJECT_TYPING_RULES': 'Use node assertions.', 'CODEX_MODEL': 'lane-fixture',
    'CODEX_MODEL_SMART': 'lane-fixture', 'CODEX_MODEL_MECHANICAL': 'lane-fixture',
    'CODEX_MODEL_COLLECTOR': 'lane-fixture', 'CODEX_REASONING_EFFORT': 'medium',
})
Path('.professor/manifest.json').write_text(json.dumps({
    'tokens': tokens, 'interview': {'tech_commands': {'express': {'test': 'node --test'}}}
}, indent=2) + '\n')
PY
step='pfm init --render'
pfm init --render .
step='project roster'
python3 - <<'PY'
import re
from pathlib import Path
path = Path('.claude/scripts/dev.sh')
text, count = re.subn(r'(?m)^PROJECTS=\([\s\S]*?\)', 'PROJECTS=(express)', path.read_text(), count=1)
if count != 1:
    raise SystemExit('adopt: PROJECTS roster not found')
path.write_text(text)
PY
step='project update report'
if report="$(pfm doctor --project-updates 2>&1)"; then rc=0; else rc=$?; fi
printf '%s\n' "$report"
[ "$rc" -le 1 ] || { echo "adopt: $step failed (exit $rc) — output above" >&2; exit 1; }
mapfile -t ignored < <(printf '%s\n' "$report" | awk '$1 == "NEW" {innew=1; next} /^  [^ ]/ {innew=0} innew && /^    [^ ]/ {print $1}')
step='ignore unscaffolded templates'
[ "${#ignored[@]}" -eq 0 ] || pfm update ignore "${ignored[@]}"
step='scaffolded markdown pins'
jq -e '[.files | keys[] | select(endswith(".md"))] | length >= 4' .professor/baseline.json >/dev/null
step='Codex build'
pfm codex build .
step='clean project updates'
report="$(pfm doctor --project-updates 2>&1)"
printf '%s\n' "$report"
[ "$(printf '%s\n' "$report" | tail -1)" = clean ]
step='professor install commit'
git add .
git -c user.name=demo -c user.email=demo@example.invalid commit -qm 'professor: install'
echo 'adopt: express adopted'
