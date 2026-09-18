#!/usr/bin/env bash
# A.sh — lane A, the adopter: the express install the root image adopted
# (`pfm init` + the interview, infra/demo/adopt.sh), walked depth-first from the
# scaffold roster through the baseline pin, the whole `pfm update` flow against
# a SECOND blueprint clone inside the container, the self-update preflight and
# body, the Codex mirror, the guard hook driven by a real chat, express's own
# suite, the release-notice refresh, and the cross-lane "fleet unchanged by an
# update" seam. Runs INSIDE a lane container (run.sh), never on a host.
#
#   run.sh --lanes A             solo, from a fresh root (the root must have
#                                adopted express: --no-adopt roots have no adopter,
#                                and the prelude then runs adopt.sh — one seat,
#                                ~10-15 min — before the first beat)
#   run.sh                       in the sequence, after M
#
# Every beat asserts from pfm's OWN report (`pfm update check`'s lines and exit
# code, a verb's stdout/stderr, a file pfm wrote, `pfm ls --tsv`) or from the
# pane and the transcript the harness itself wrote — never from a model's prose.
# The two model turns (A.11) are stimuli: the evidence is the file on disk, the
# hook's deny text, and the recompiled AGENTS.md.
#
# The second blueprint — $HOME/blueprint-b — is a real git clone of the mounted
# repository's common git dir, checked out at the mounted worktree's HEAD with
# its uncommitted template state applied, so its templates are byte-identical to
# the store express was scaffolded from: `pfm update check` reads `clean` against
# it BEFORE any drift is provoked, and every `review: git -C … diff A..B` line
# pfm prints is runnable there. Express is pointed at it through the one door
# pfm resolves a store by (`.professor/manifest.json` → interview.blueprint_clone_path,
# internal/professor/store.go ResolveStore) and pointed back after each beat.
#
# Cost: no model turn outside A.11 (two short turns on one Claude seat) and the
# prelude's `need`s (E1's chat re-opened when it is gone; adopt.sh when the root
# never adopted express). A.07 builds pfm four times inside the container (two
# reproducible builds per `pfm update`) — minutes, no seat.
#
# BROKEN STATE: the prelude aborts the lane by name when the container carries
# no pfm install, no fence git environment (PFM_DEV_REPO_GIT_DIR), no adopted
# express it could make, or no second blueprint it could clone; every mutating
# beat restores what it changed and asserts the restore against a `git status`
# snapshot of express taken at lane start — a restore that did not converge is
# its own ✗ line, never silence; each ✗ carries the exit code and the first line
# of the output that contradicted the assertion.
set -uo pipefail
export PATH="$HOME/.local/bin:$PATH"
export IS_SANDBOX=1 # root fence: Claude Code refuses the bypass flag under root without it
cd /tmp 2>/dev/null || true

LANES_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
# shellcheck source=lib.sh
. "$LANES_DIR/lib.sh"

EXPRESS="${A_EXPRESS:-/work/express}"
CHAT="${A_CHAT:-A_MAIN}"
E1_CHAT="${E1_CHAT:-E1_MAIN}"
E1_CWD="${E1_CWD:-/work/orbit}"
CONFIG="$HOME/.config/pfm/pfm.config.json"
MANAGED="$HOME/.local/share/pfm/install"
BLUEPRINT="$HOME/.professor"
B="$HOME/blueprint-b"
WORKTREE=/worktree
BK=/tmp/lane-a-backup
SEAT="$(printf '%s\n' $LANE_SEATS | awk -F: '/^cc:/ { print $2; exit }')"
[ -n "$SEAT" ] || SEAT=1
PORT="$(jq -r '.mcp.http.port // 18377' "$CONFIG" 2>/dev/null || echo 18377)"

lane_begin A

# ── prelude: what this lane needs, made when it is missing, no-op otherwise ──
[ -f "$CONFIG" ] || lane_abort "no pfm config at $CONFIG — the root image was not built by lanes/root.sh"
jq -e --argjson want "$SEAT" '.accounts[] | select(.id == $want)' "$CONFIG" >/dev/null 2>&1 ||
  lane_abort "seat cc:$SEAT is not configured in $CONFIG (accounts: $(jq -c '[.accounts[].id]' "$CONFIG"))"
SEAT_DIR="$(jq -r --argjson want "$SEAT" '.accounts[] | select(.id == $want) | .configDir' "$CONFIG")"
case "$SEAT_DIR" in "~"*) SEAT_DIR="$HOME${SEAT_DIR#\~}" ;; esac
# The blueprint's git state is read through the fence's git environment
# (container.sh sets it; internal/paths DevRepoGitDir consumes it): /worktree's
# own .git file names a host path no process in here can resolve.
[ -n "${PFM_DEV_REPO_GIT_DIR:-}" ] && [ -d "$PFM_DEV_REPO_GIT_DIR" ] ||
  lane_abort "PFM_DEV_REPO_GIT_DIR is unset or not a directory — this is not a lane container (lanes/container.sh sets it), so the blueprint's git state cannot be read"
# Run from the mounted tree so every pathspec is worktree-relative, the way
# scripts/leak-check.sh and dev.sh drive the same git environment.
wt_git() { (cd "$WORKTREE" && git --git-dir="$PFM_DEV_REPO_GIT_DIR" --work-tree="$WORKTREE" -c "safe.directory=$WORKTREE" "$@"); }
WT_SHA="$(wt_git rev-parse --short HEAD 2>&1)" ||
  lane_abort "the mounted worktree's HEAD could not be read through $PFM_DEV_REPO_GIT_DIR: $(one_line "$WT_SHA")"

need "the blueprint clone at $BLUEPRINT" "[ -e '$BLUEPRINT' ]" "ln -s $WORKTREE '$BLUEPRINT'" ||
  lane_abort "no blueprint clone — pfm resolves every store from it"
need "the managed install root $MANAGED" "[ -d '$MANAGED' ] && [ -s '$MANAGED/source-repo' ]" \
  "(cd '$BLUEPRINT' && pfm install --yes)" ||
  lane_abort "pfm install has never completed in this container (no $MANAGED/source-repo marker)"
need "the pfm MCP daemon on :$PORT" \
  "[ \"\$(curl -s -o /dev/null -w '%{http_code}' -m 2 http://127.0.0.1:$PORT/mcp/chat)\" != 000 ]" \
  "bash $WORKTREE/infra/demo/daemon.sh" ||
  lane_abort "the chat MCP daemon never answered on :$PORT — no chat can call a chat_* tool"
need "express adopted at $EXPRESS (pfm init + the interview, 'professor: install' committed)" \
  "[ -f '$EXPRESS/.professor/baseline.json' ] && git -C '$EXPRESS' log --oneline 2>/dev/null | grep -q 'professor: install'" \
  "bash $WORKTREE/infra/demo/adopt.sh" ||
  lane_abort "no adopted express at $EXPRESS and adopt.sh could not make one — nothing for the adopter lane to assert against"

# make_blueprint_b — the second blueprint: a clone of the mounted repository's
# common git dir at the worktree's HEAD, plus the worktree's uncommitted state
# under the hash paths, committed, so B's templates are byte-identical to the
# store express was scaffolded from and B carries every SHA express pinned.
make_blueprint_b() {
  local head common f
  head="$(wt_git rev-parse HEAD)" || return 1
  common="${PFM_DEV_REPO_GIT_DIR%%/worktrees/*}"
  # The mount is owned by the host's uid and this shell is root: git refuses a
  # "dubious" repository unless it is declared safe — for the clone AND for every
  # later fetch pfm update runs against it (safe.directory is global-only).
  git config --global --get-all safe.directory 2>/dev/null | grep -qxF "$common" ||
    git config --global --add safe.directory "$common" || return 1
  rm -rf "$B"
  git clone -q "$common" "$B" || return 1
  git -C "$B" checkout -q --detach "$head" || return 1
  wt_git diff HEAD --binary -- templates VERSION docs/SETUP.md >/tmp/lane-a-dirty.patch || return 1
  if [ -s /tmp/lane-a-dirty.patch ]; then git -C "$B" apply /tmp/lane-a-dirty.patch || return 1; fi
  for f in $(wt_git ls-files -o --exclude-standard -- templates); do
    mkdir -p "$B/$(dirname "$f")" && cp -p "$WORKTREE/$f" "$B/$f" || return 1
  done
  if [ -n "$(git -C "$B" status --porcelain)" ]; then
    git -C "$B" add -A && git -C "$B" commit -q -m "lane A: the mounted worktree's uncommitted template state" || return 1
  fi
  return 0
}
need "a second blueprint clone at $B (clean, with a VERSION)" \
  "[ -f '$B/VERSION' ] && [ -d '$B/templates/project' ] && [ -z \"\$(git -C '$B' status --porcelain 2>&1)\" ]" \
  "make_blueprint_b" ||
  lane_abort "the second blueprint could not be cloned at $B — the update flow has nothing to drift against"
B_BASE="$(git -C "$B" rev-parse HEAD)"
B_BASE_SHORT="$(git -C "$B" rev-parse --short HEAD)"

# E1's chat, the cross-lane state A.15 asserts against: E1.sh open_main's own
# line (E1.25 ends it, so in the sequence it is gone by the time A runs).
need "the working directory $E1_CWD" "[ -d '$E1_CWD/.git' ]" \
  "mkdir -p '$E1_CWD' && git -C '$E1_CWD' init -q && git -C '$E1_CWD' commit -q --allow-empty -m lane" ||
  lane_abort "no working directory for E1's chat to live in ($E1_CWD)"
open_e1_main() {
  pfm chat new --name "$E1_CHAT" --engine cc --account "$SEAT" --cwd "$E1_CWD" --await --timeout 300 \
    "You are $E1_CHAT, the chat an automated Tier B lane drives. Reply with one word: ready. Then wait and do exactly what each next message says, nothing more." 2>&1
}
need "E1's chat $E1_CHAT alive" "live_chat '$E1_CHAT'" "open_e1_main" ||
  _lane_say "A need: $E1_CHAT could not be opened — A.15 will report blocked, not ✗"

mkdir -p "$BK"
STORE_T="$BLUEPRINT/templates"
SOURCE_REPO="$(cat "$MANAGED/source-repo")"
express_status() { git -C "$EXPRESS" status --porcelain --untracked-files=all 2>&1 | grep -v ' tmp/' | sort; }
EXPRESS_STATUS0="$(express_status)"
_lane_log_only "A · express git status at lane start: $(printf '%s\n' "$EXPRESS_STATUS0" | grep -c .) line(s)"

# restore_check — the express tree reads exactly as it did at lane start.
# Prints the differing status lines; returns 1 when any.
restore_check() {
  local now
  now="$(express_status)"
  [ "$now" = "$EXPRESS_STATUS0" ] && return 0
  diff <(printf '%s\n' "$EXPRESS_STATUS0") <(printf '%s\n' "$now") | grep '^[<>]' | head -5
  return 1
}
# count_of <report> <status> — the count column of one status row of the human report
count_of() { printf '%s\n' "$1" | awk -v s="$2" '$1 == s && NF == 2 { print $2; exit }'; }
BASELINE="$EXPRESS/.professor/baseline.json"
MANIFEST="$EXPRESS/.professor/manifest.json"
MANIFEST_MADE=0
# point_express_at <store-root> — the manifest door pfm resolves a store by
point_express_at() {
  if [ -f "$MANIFEST" ]; then
    [ -f "$BK/manifest.json" ] || cp -p "$MANIFEST" "$BK/manifest.json"
    jq --arg p "$1" '.interview = ((.interview // {}) + {blueprint_clone_path: $p})' "$BK/manifest.json" \
      >"$MANIFEST.tmp" && mv "$MANIFEST.tmp" "$MANIFEST"
  else
    MANIFEST_MADE=1
    printf '{"interview":{"blueprint_clone_path":"%s"}}\n' "$1" >"$MANIFEST"
  fi
}
unpoint_express() {
  if [ -f "$BK/manifest.json" ]; then
    mv "$BK/manifest.json" "$MANIFEST"
  elif [ "$MANIFEST_MADE" -eq 1 ]; then
    rm -f "$MANIFEST"
    MANIFEST_MADE=0
  fi
}
# express_pins — "local<TAB>template<TAB>pinnedSha" for every .md pin whose local
# exists and whose template is a regular file in B, sorted by local.
express_pins() {
  local local_path template sha
  jq -r '.files | to_entries | sort_by(.key) | .[] | select(.value.template | endswith(".md")) |
    "\(.key)\t\(.value.template)\t\(.value.pinnedSha)"' "$BASELINE" 2>/dev/null |
    while IFS=$'\t' read -r local_path template sha; do
      [ -f "$EXPRESS/$local_path" ] && [ -f "$B/templates/$template" ] && printf '%s\t%s\t%s\n' "$local_path" "$template" "$sha"
    done
}
# provoke_drift — two templates edited upstream, one added, one removed, one
# local deleted; express pointed at B. Sets D_L1..D_S4, D_SHA; D_WHY on failure.
D_WHY="" D_SHA="" D_L1="" D_T1="" D_S1="" D_L2="" D_T2="" D_S2="" D_L3="" D_T3="" D_L4="" D_T4=""
NEW_T="project/commands/lane-a-new.md"
NEW_L=".claude/commands/lane-a-new.md"
provoke_drift() {
  local pins n
  D_WHY=""
  pins="$(express_pins | head -4)"
  n="$(printf '%s\n' "$pins" | grep -c .)"
  if [ "$n" -ne 4 ]; then
    D_WHY="only $n .md pin(s) in $BASELINE have both a local file and a template in $B (four are needed: two UPDATED, one GONE-UPSTREAM, one LOCAL-DELETED)"
    return 1
  fi
  D_L1="$(printf '%s\n' "$pins" | awk -F'\t' 'NR == 1 { print $1 }')"; D_T1="$(printf '%s\n' "$pins" | awk -F'\t' 'NR == 1 { print $2 }')"; D_S1="$(printf '%s\n' "$pins" | awk -F'\t' 'NR == 1 { print $3 }')"
  D_L2="$(printf '%s\n' "$pins" | awk -F'\t' 'NR == 2 { print $1 }')"; D_T2="$(printf '%s\n' "$pins" | awk -F'\t' 'NR == 2 { print $2 }')"; D_S2="$(printf '%s\n' "$pins" | awk -F'\t' 'NR == 2 { print $3 }')"
  D_L3="$(printf '%s\n' "$pins" | awk -F'\t' 'NR == 3 { print $1 }')"; D_T3="$(printf '%s\n' "$pins" | awk -F'\t' 'NR == 3 { print $2 }')"
  D_L4="$(printf '%s\n' "$pins" | awk -F'\t' 'NR == 4 { print $1 }')"; D_T4="$(printf '%s\n' "$pins" | awk -F'\t' 'NR == 4 { print $2 }')"
  cp -p "$BASELINE" "$BK/baseline.json" || { D_WHY="could not back up $BASELINE"; return 1; }
  point_express_at "$B"
  printf '\nLane A probe: UPDATED %s\n' "$LANE_STAMP" >>"$B/templates/$D_T1"
  printf '\nLane A probe: UPDATED %s\n' "$LANE_STAMP" >>"$B/templates/$D_T2"
  printf -- '---\nname: lane-a-new\ndescription: a template lane A adds upstream to provoke NEW\n---\n\nLane A probe: NEW\n' >"$B/templates/$NEW_T"
  rm -f "$B/templates/$D_T3"
  git -C "$B" add -A && git -C "$B" commit -q -m "lane A: drift" || { D_WHY="the drift could not be committed in $B"; return 1; }
  D_SHA="$(git -C "$B" rev-parse --short HEAD)"
  mkdir -p "$BK/local-deleted" && cp -p "$EXPRESS/$D_L4" "$BK/local-deleted/file" && rm -f "$EXPRESS/$D_L4" ||
    { D_WHY="the local $D_L4 could not be backed up and removed"; return 1; }
  return 0
}
# restore_drift — express and B back to their pre-drift state. Prints what did
# not converge; returns 1 when anything did not.
restore_drift() {
  local bad=""
  [ -f "$BK/baseline.json" ] && { cp -p "$BK/baseline.json" "$BASELINE" || bad="$bad baseline.json not restored;"; }
  unpoint_express
  [ -f "$BK/local-deleted/file" ] && [ -n "$D_L4" ] && { cp -p "$BK/local-deleted/file" "$EXPRESS/$D_L4" || bad="$bad $D_L4 not restored;"; }
  rm -f "$EXPRESS/$NEW_L"
  git -C "$B" reset -q --hard "$B_BASE" && git -C "$B" clean -fdq || bad="$bad $B could not be reset to $B_BASE_SHORT;"
  rm -rf "$BK/local-deleted" "$BK/baseline.json"
  printf '%s' "$bad"
  [ -z "$bad" ]
}
in_express() { (cd "$EXPRESS" && "$@" 2>&1); }

# ─── A.01 — the scaffold roster from pfm init ───────────────────────────────

beat A.01-scaffold P1 P2 P3 P4 P5 P6 P7 P8 P9 P10 P11 P32
spends none
INIT_DIR="$HOME/lane-a-init"
rm -rf "$INIT_DIR"
mkdir -p "$INIT_DIR"
bad=""
# The expected count comes from the store itself: the eleven init mappings
# (internal/professor/scaffold.go initTemplatePaths), agents minus per-project.
expected=0
for single in CLAUDE.md settings.json rumdl-policy.toml; do
  [ -f "$STORE_T/project/$single" ] && expected=$((expected + 1))
done
expected=$((expected + $(find "$STORE_T/project/commands" "$STORE_T/project/scripts" "$STORE_T/project/skills" \
  "$STORE_T/project/epics" "$STORE_T/project/codex" "$STORE_T/project/docs-commands" "$STORE_T/project/docs-agents" \
  -type f 2>/dev/null | wc -l | tr -d ' ')))
expected=$((expected + $(find "$STORE_T/project/agents" -type f -not -path "$STORE_T/project/agents/per-project/*" 2>/dev/null | wc -l | tr -d ' ')))
out="$(cd "$INIT_DIR" && pfm init . 2>&1)"
rc=$?
[ "$rc" -eq 0 ] || bad="$bad pfm init exited $rc ($(one_line "$out"));"
printf '%s\n' "$out" | grep -qxF "initialized $INIT_DIR from $SOURCE_REPO" ||
  bad="$bad no 'initialized $INIT_DIR from $SOURCE_REPO' line ($(one_line "$out" | cut -c1-160));"
printf '%s\n' "$out" | grep -qxF "deployed $expected project files; baseline: $INIT_DIR/.professor/baseline.json" ||
  bad="$bad want 'deployed $expected project files; baseline: …' (store has $expected mapped files), got: $(printf '%s\n' "$out" | grep '^deployed' | head -1);"
printf '%s\n' "$out" | grep -qF "follow $SOURCE_REPO/docs/SETUP.md § Install interview" ||
  bad="$bad the handoff line does not name $SOURCE_REPO/docs/SETUP.md;"
pinned="$(jq '.files | length' "$INIT_DIR/.professor/baseline.json" 2>/dev/null)"
[ "$pinned" = "$expected" ] || bad="$bad baseline.json pins ${pinned:-<unreadable>} file(s), want $expected;"
for local_path in CLAUDE.md .claude/settings.json .rumdl.toml .claude/agents/gitter.md; do
  [ -f "$INIT_DIR/$local_path" ] || bad="$bad $local_path not deployed;"
done
for dir in .claude/commands .claude/scripts .claude/skills docs/epics .codex docs/commands docs/agents; do
  [ -d "$INIT_DIR/$dir" ] || bad="$bad $dir/ not deployed;"
done
[ -e "$INIT_DIR/.claude/agents/per-project" ] && bad="$bad .claude/agents/per-project was deployed (P5: only gitter.md ships);"
grep -q 'notify.sh' "$INIT_DIR/.claude/settings.json" 2>/dev/null && grep -q 'format-md.sh' "$INIT_DIR/.claude/settings.json" 2>/dev/null ||
  bad="$bad .claude/settings.json does not ship the notify.sh/format-md.sh hooks (P2);"
[ -x "$INIT_DIR/.claude/scripts/dev.sh" ] || bad="$bad .claude/scripts/dev.sh lost its executable mode (P6);"
# P32: [dir] [--force] — a re-init names every collision; --force overwrites.
again="$(pfm init "$INIT_DIR" 2>&1)"
again_rc=$?
conflicts="$(printf '%s\n' "$again" | grep -c '^CONFLICT .*: exists$')"
[ "$again_rc" -eq 0 ] || bad="$bad a re-init exited $again_rc ($(one_line "$again"));"
[ "$conflicts" -eq "$expected" ] || bad="$bad a re-init named $conflicts CONFLICT line(s), want one per file ($expected);"
printf '%s\n' "$again" | grep -q '^deployed 0 project files' || bad="$bad a re-init did not report 'deployed 0 project files';"
repinned="$(jq '.files | length' "$INIT_DIR/.professor/baseline.json" 2>/dev/null)"
forced="$(pfm init "$INIT_DIR" --force 2>&1)"
forced_rc=$?
[ "$forced_rc" -eq 0 ] || bad="$bad pfm init --force exited $forced_rc ($(one_line "$forced"));"
printf '%s\n' "$forced" | grep -q '^CONFLICT' && bad="$bad --force still printed CONFLICT lines;"
printf '%s\n' "$forced" | grep -q "^deployed $expected project files" || bad="$bad --force did not redeploy all $expected files;"
pfm init "$INIT_DIR" extra >/dev/null 2>&1
[ $? -eq 2 ] || bad="$bad pfm init with two positionals did not exit 2 (usage);"
if [ -n "$bad" ]; then fail "$bad"; else
  pass "pfm init deployed $expected files into the eleven roster targets (per-project skipped, hooks and exec modes shipped); re-init named $conflicts CONFLICTs and left ${repinned:-?} pin(s) in baseline.json (observed: a re-init without --force rewrites the baseline to the files it deployed, i.e. none); --force redeployed all"
fi

# ─── A.02 — Phase-2 territory is never deployed by pfm ──────────────────────

beat A.02-phase2-never-deployed P12 P13 P14 P15
spends none
if requires A.01-scaffold; then
  bad=""
  for never in .claude/agents/per-project/developer.md .claude/agents/per-project/qa.md .claude/agents/developer.md .claude/agents/qa.md \
    .claude/settings-global.json settings-global.json .claude/skills/host-gh/SKILL.md .claude/skills/host-glab/SKILL.md; do
    [ -e "$INIT_DIR/$never" ] && bad="$bad $never exists after a bare pfm init;"
  done
  for tmpl in project/per-project/CLAUDE.md project/agents/per-project/developer.md project/agents/per-project/qa.md project/settings-global.json; do
    [ -f "$STORE_T/$tmpl" ] || bad="$bad the store has no $tmpl (the never-deployed template this beat asserts on is gone);"
    jq -e --arg t "$tmpl" '.files | to_entries[] | select(.value.template == $t)' "$INIT_DIR/.professor/baseline.json" >/dev/null 2>&1 &&
      bad="$bad $tmpl is PINNED by a bare pfm init;"
  done
  [ -z "$(find "$STORE_T/project/skills" -path '*host-gh*' -o -path '*host-glab*' 2>/dev/null | head -1)" ] ||
    bad="$bad the store carries a host-gh/host-glab skill template — P15 says it is Phase-2 generated, never shipped;"
  # The proof pfm itself gives: on a bare init those templates are exactly the
  # unpinned, un-ignored ones — `pfm update check` lists each as NEW.
  chk="$(cd "$INIT_DIR" && pfm update check 2>&1)"
  chk_rc=$?
  [ "$chk_rc" -eq 3 ] || bad="$bad pfm update check in the bare init exited $chk_rc, want 3 (the never-deployed templates are NEW);"
  for tmpl in project/per-project/CLAUDE.md project/agents/per-project/developer.md project/agents/per-project/qa.md project/settings-global.json; do
    printf '%s\n' "$chk" | grep -qF "    $tmpl — adopt: copy/adapt it locally, then pfm update pin --template $tmpl <local> — or ignore" ||
      bad="$bad check does not list $tmpl as NEW with its adopt line;"
  done
  unmapped="$(find "$STORE_T/project" -type f 2>/dev/null | wc -l | tr -d ' ')"
  unmapped=$((unmapped - expected))
  [ "$(count_of "$chk" NEW)" = "$unmapped" ] ||
    bad="$bad check counts NEW $(count_of "$chk" NEW), want $unmapped (every store file outside the init mapping);"
  if [ -n "$bad" ]; then fail "$bad"; else
    pass "a bare pfm init deployed none of per-project/CLAUDE.md, agents/per-project/{developer,qa}.md, settings-global.json or a host git-bridge skill; check lists exactly those $unmapped unmapped templates as NEW (exit 3)"
  fi
fi
rm -rf "$INIT_DIR"

# ─── A.03 — the baseline pin ────────────────────────────────────────────────

beat A.03-baseline-pin P16
spends none
bad=""
if [ ! -f "$BASELINE" ]; then
  fail "no $BASELINE — express carries no pin file"
else
  [ "$(jq -r '.version' "$BASELINE" 2>/dev/null)" = 1 ] || bad="$bad version is '$(jq -r .version "$BASELINE" 2>&1)', want 1;"
  bp_sha="$(jq -r '.blueprint.sha // ""' "$BASELINE")"
  bp_ver="$(jq -r '.blueprint.version // ""' "$BASELINE")"
  [ -n "$bp_sha" ] && [ "$bp_sha" != self-hosted@unknown ] || bad="$bad blueprint.sha is '${bp_sha:-<empty>}' — the store's git state was not pinned;"
  [ "$bp_ver" = "$(tr -d '[:space:]' <"$BLUEPRINT/VERSION")" ] || bad="$bad blueprint.version '$bp_ver' is not the store's VERSION '$(tr -d '[:space:]' <"$BLUEPRINT/VERSION")';"
  n_files="$(jq '.files | length' "$BASELINE")"
  [ "$n_files" -gt 0 ] || bad="$bad files{} is empty;"
  jq -e '.files | to_entries | all(.value.template | startswith("project/"))' "$BASELINE" >/dev/null || bad="$bad a pin's template is not under project/;"
  jq -e '.files | to_entries | all(.value.templateHash | test("^sha256:[0-9a-f]{64}$"))' "$BASELINE" >/dev/null || bad="$bad a pin's templateHash is not sha256:<64 hex>;"
  jq -e '.files | to_entries | all((.value.pinnedSha | length) > 0 and (.value.pinnedAt | test("^[0-9]{4}-[0-9]{2}-[0-9]{2}$")))' "$BASELINE" >/dev/null ||
    bad="$bad a pin lacks pinnedSha or a YYYY-MM-DD pinnedAt;"
  jq -e '(.ignored // []) | all(type == "string")' "$BASELINE" >/dev/null || bad="$bad ignored[] carries a non-string;"
  # Cross-checked against the live store: every pinned hash is the sha256 of the
  # template as it sits in the blueprint — the check's `current` promise.
  mismatched=0
  while IFS=$'\t' read -r template hash; do
    [ -f "$STORE_T/$template" ] || { mismatched=$((mismatched + 1)); continue; }
    [ "sha256:$(sha256sum "$STORE_T/$template" | cut -d' ' -f1)" = "$hash" ] || mismatched=$((mismatched + 1))
  done < <(jq -r '.files[] | "\(.template)\t\(.templateHash)"' "$BASELINE")
  [ "$mismatched" -eq 0 ] || bad="$bad $mismatched pinned template hash(es) do not match the store at $STORE_T (the interview's own pin step must end clean);"
  if [ -n "$bad" ]; then fail "$bad"; else
    pass "$n_files pins, blueprint $bp_ver@$bp_sha, every pin template/templateHash/pinnedSha/pinnedAt well-formed and every hash equal to the live store's ($(jq '(.ignored // []) | length' "$BASELINE") ignored)"
  fi
fi

# ─── A.04 — pfm update check: clean, then every status class ────────────────

beat A.04-update-check P17 P18 P19 P20 P21 P22 P23
spends none
expect-log 'baseline.json not found'
bad=""
clean0="$(in_express pfm update check)"
clean0_rc=$?
if [ "$clean0_rc" -ne 0 ] || [ "$(printf '%s\n' "$clean0" | tail -1)" != clean ]; then
  fail "express is not clean before any drift: pfm update check exited $clean0_rc — $(one_line "$(printf '%s\n' "$clean0" | grep -vE '^  (current|ignored) ' | head -6)")"
else
  n_pins="$(jq '.files | length' "$BASELINE")"
  n_ign="$(jq '(.ignored // []) | length' "$BASELINE")"
  point_express_at "$B"
  cleanB="$(in_express pfm update check)"
  cleanB_rc=$?
  [ "$cleanB_rc" -eq 0 ] && [ "$(printf '%s\n' "$cleanB" | tail -1)" = clean ] ||
    bad="$bad pointed at the identical second blueprint $B, check exited $cleanB_rc: $(one_line "$(printf '%s\n' "$cleanB" | grep -vE '^  (current|ignored) ' | head -4)");"
  printf '%s\n' "$cleanB" | grep -qF "blueprint $(jq -r .blueprint.sha "$BASELINE") → $B_BASE_SHORT" ||
    bad="$bad the header does not read 'blueprint <pinned> → $B_BASE_SHORT' through the manifest door: $(printf '%s\n' "$cleanB" | head -1);"
  unpoint_express
  if ! provoke_drift; then
    bad="$bad the drift could not be provoked: $D_WHY;"
  else
    rep="$(in_express pfm update check)"
    rep_rc=$?
    [ "$rep_rc" -eq 3 ] || bad="$bad with 5 review items check exited $rep_rc, want 3;"
    [ "$(count_of "$rep" current)" = "$((n_pins - 4))" ] || bad="$bad current $(count_of "$rep" current), want $((n_pins - 4)) (P17);"
    [ "$(count_of "$rep" ignored)" = "$n_ign" ] || bad="$bad ignored $(count_of "$rep" ignored), want $n_ign from the baseline (P18);"
    [ "$(count_of "$rep" UPDATED)" = 2 ] || bad="$bad UPDATED $(count_of "$rep" UPDATED), want 2 (P19);"
    [ "$(count_of "$rep" NEW)" = 1 ] || bad="$bad NEW $(count_of "$rep" NEW), want 1 (P20);"
    [ "$(count_of "$rep" GONE-UPSTREAM)" = 1 ] || bad="$bad GONE-UPSTREAM $(count_of "$rep" GONE-UPSTREAM), want 1 (P21);"
    [ "$(count_of "$rep" LOCAL-DELETED)" = 1 ] || bad="$bad LOCAL-DELETED $(count_of "$rep" LOCAL-DELETED), want 1 (P22);"
    printf '%s\n' "$rep" | grep -qxF "    $D_L1   $D_T1  pinned @$D_S1" || bad="$bad no UPDATED row '$D_L1   $D_T1  pinned @$D_S1';"
    printf '%s\n' "$rep" | grep -qxF "    $D_L2   $D_T2  pinned @$D_S2" || bad="$bad no UPDATED row '$D_L2   $D_T2  pinned @$D_S2';"
    review="git -C $B diff $D_S1..$D_SHA -- templates/$D_T1"
    printf '%s\n' "$rep" | grep -qxF "      review: $review" || bad="$bad no 'review: $review' line;"
    printf '%s\n' "$rep" | grep -qxF "      then apply by hand and: pfm update pin $D_L1" || bad="$bad no 'then apply by hand and: pfm update pin $D_L1' line;"
    review_out="$($review 2>&1)"
    review_rc=$?
    [ "$review_rc" -eq 0 ] && printf '%s' "$review_out" | grep -qF 'Lane A probe: UPDATED' ||
      bad="$bad the printed review command is not runnable or shows no delta (exit $review_rc): $(one_line "$review_out" | cut -c1-160);"
    printf '%s\n' "$rep" | grep -qxF "    $NEW_T — adopt: copy/adapt it locally, then pfm update pin --template $NEW_T <local> — or ignore" ||
      bad="$bad no NEW row with the adopt instruction for $NEW_T;"
    printf '%s\n' "$rep" | grep -qxF "    $D_L3   $D_T3 — local file is YOURS now — keep it and pfm update drop $D_L3, or delete both" ||
      bad="$bad no GONE-UPSTREAM row for $D_L3;"
    printf '%s\n' "$rep" | grep -qxF "    $D_L4   $D_T4 — pfm update drop $D_L4 to forget, or restore the file" ||
      bad="$bad no LOCAL-DELETED row for $D_L4;"
    printf '%s\n' "$rep" | grep -qxF "REVIEW REQUIRED — 5 items; nothing was written." || bad="$bad no 'REVIEW REQUIRED — 5 items; nothing was written.' terminal;"
    js="$(in_express pfm update check --json)"
    js_rc=$?
    [ "$js_rc" -eq 3 ] || bad="$bad --json exited $js_rc, want 3;"
    printf '%s' "$js" | jq -e --argjson c "$((n_pins - 4))" --argjson i "$n_ign" \
      '.counts.current == $c and .counts.ignored == $i and .counts.UPDATED == 2 and .counts.NEW == 1 and .counts["GONE-UPSTREAM"] == 1 and .counts["LOCAL-DELETED"] == 1 and .reviewRequired == 5 and .terminal == "REVIEW REQUIRED — 5 items" and (.items | length) == 5' >/dev/null 2>&1 ||
      bad="$bad --json counts/terminal disagree with the human report: $(one_line "$js" | cut -c1-200);"
    # P23: 0 clean · 3 review · 1 failed (no baseline) · 2 usage
    nob="$(mktemp -d /tmp/lane-a-nobaseline.XXXXXX)"
    nob_out="$(pfm update check --root "$nob" 2>&1)"
    nob_rc=$?
    [ "$nob_rc" -eq 1 ] || bad="$bad check with no baseline exited $nob_rc, want 1;"
    printf '%s\n' "$nob_out" | grep -qxF 'FAILED — .professor/baseline.json not found — pfm update adopt pins an existing install; pfm init scaffolds a new one' ||
      bad="$bad the no-baseline terminal is not the named FAILED line: $(one_line "$nob_out");"
    rmdir "$nob"
    in_express pfm update check extra-positional >/dev/null 2>&1
    [ $? -eq 2 ] || bad="$bad check with a positional did not exit 2 (usage);"
  fi
  left="$(restore_drift)" || bad="$bad restore did not converge: $left;"
  if ! after="$(restore_check)"; then bad="$bad express differs from its lane-start state after restore: $(one_line "$after");"; fi
  final="$(in_express pfm update check)"
  final_rc=$?
  [ "$final_rc" -eq 0 ] && [ "$(printf '%s\n' "$final" | tail -1)" = clean ] || bad="$bad after restore check exited $final_rc, not clean;"
  if [ -n "$bad" ]; then fail "$bad"; else
    pass "clean (0) through ~/.professor and through the manifest door to $B; UPDATED×2/NEW/GONE-UPSTREAM/LOCAL-DELETED each reported with its instruction, the review diff command runnable and showing the delta; --json agrees; exit 3 on review, 1 with the named FAILED line on no baseline, 2 on usage; restored to clean"
  fi
fi

# ─── A.05 — the update verbs ────────────────────────────────────────────────

beat A.05-update-verbs P24 P25 P26 P27 P28 P29
spends none
expect-log 'has no pin'
expect-log 'already has a pin'
expect-log 'does not exist upstream'
expect-log 'is pinned by'
expect-log 'is not ignored'
bad=""
if ! provoke_drift; then
  fail "the drift the verbs act on could not be provoked: $D_WHY"
else
  step() { # step <what> <want-rc> <want-substring> <cmd…> — appends to $bad on a miss
    local what="$1" want_rc="$2" want="$3" out rc
    shift 3
    out="$(in_express "$@")"
    rc=$?
    [ "$rc" -eq "$want_rc" ] || bad="$bad $what exited $rc, want $want_rc ($(one_line "$out"));"
    [ -z "$want" ] || printf '%s' "$out" | grep -qF -- "$want" || bad="$bad $what did not report '$want': $(one_line "$out");"
  }
  step "pin $D_L1" 0 "pinned 1 file(s) at $D_SHA" pfm update pin "$D_L1"
  step "pin --all (one UPDATED left)" 0 "pinned 1 file(s) at $D_SHA" pfm update pin --all
  step "pin of a local with no pin" 1 "pfm update pin: lane-a-no-such-local has no pin; use --template for a new adoption" pfm update pin lane-a-no-such-local
  cp "$B/templates/$NEW_T" "$EXPRESS/$NEW_L" || bad="$bad the NEW template could not be copied locally;"
  step "pin --template (adopt NEW)" 0 "pinned 1 file(s) at $D_SHA" pfm update pin --template "$NEW_T" "$NEW_L"
  step "pin --template twice" 1 "pfm update pin: $NEW_L already has a pin" pfm update pin --template "$NEW_T" "$NEW_L"
  step "drop $D_L3 (GONE-UPSTREAM)" 0 "dropped 1 pin(s)" pfm update drop "$D_L3"
  step "drop $D_L3 again" 1 "pfm update drop: $D_L3 has no pin" pfm update drop "$D_L3"
  step "drop with no local" 2 "" pfm update drop
  step "drop $D_L4 (LOCAL-DELETED)" 0 "dropped 1 pin(s)" pfm update drop "$D_L4"
  step "ignore a template gone upstream" 1 "pfm update ignore: $D_T3 does not exist upstream" pfm update ignore "$D_T3"
  step "ignore a pinned template" 1 "pfm update ignore: $D_T1 is pinned by $D_L1; pfm update drop $D_L1 first" pfm update ignore "$D_T1"
  step "ignore $D_T4" 0 "ignored 1 template(s)" pfm update ignore "$D_T4"
  step "ignore $D_T4 again" 0 "ignored 0 template(s) (1 already ignored)" pfm update ignore "$D_T4"
  step "ignore --undo $D_T4" 0 "un-ignored 1 template(s)" pfm update ignore --undo "$D_T4"
  step "ignore --undo twice" 1 "pfm update ignore: $D_T4 is not ignored" pfm update ignore --undo "$D_T4"
  step "ignore $D_T4 (final)" 0 "ignored 1 template(s)" pfm update ignore "$D_T4"
  jq -e --arg t "$D_T4" '.ignored | index($t) != null' "$BASELINE" >/dev/null 2>&1 || bad="$bad baseline.json ignored[] does not carry $D_T4 after ignore;"
  jq -e --arg l "$NEW_L" --arg t "$NEW_T" '.files[$l].template == $t' "$BASELINE" >/dev/null 2>&1 || bad="$bad baseline.json does not pin $NEW_L to $NEW_T after pin --template;"
  jq -e --arg l "$D_L3" '.files[$l] == null' "$BASELINE" >/dev/null 2>&1 || bad="$bad baseline.json still pins $D_L3 after drop;"
  [ "$(jq -r .blueprint.sha "$BASELINE")" = "$D_SHA" ] || bad="$bad baseline.json blueprint.sha is $(jq -r .blueprint.sha "$BASELINE") after pinning at $D_SHA;"
  fin="$(in_express pfm update check)"
  fin_rc=$?
  [ "$fin_rc" -eq 0 ] && [ "$(printf '%s\n' "$fin" | tail -1)" = clean ] ||
    bad="$bad after pin/pin --all/pin --template/drop/ignore the check is not clean (exit $fin_rc): $(one_line "$(printf '%s\n' "$fin" | grep -vE '^  (current|ignored) ' | head -4)");"
  left="$(restore_drift)" || bad="$bad restore did not converge: $left;"
  if ! after="$(restore_check)"; then bad="$bad express differs from its lane-start state after restore: $(one_line "$after");"; fi
  final="$(in_express pfm update check)"
  [ $? -eq 0 ] && [ "$(printf '%s\n' "$final" | tail -1)" = clean ] || bad="$bad after restore check is not clean;"
  if [ -n "$bad" ]; then fail "$bad"; else
    pass "pin <local>, pin --all, pin --template (NEW adopted; a second adoption refused), drop (a pin forgotten; no pin and no argument refused by name), ignore (gone-upstream and pinned templates refused by name; already-ignored counted), ignore --undo (a non-ignored refused) — the drift ended clean and express was restored"
  fi
fi

# ─── A.06 — pfm update adopt on an install that predates init ───────────────

beat A.06-update-adopt P30 P31
spends none
expect-log 'baseline.json not found'
expect-log 'resolve --at'
bad=""
ADOPT_DIR="$HOME/lane-a-adopt"
rm -rf "$ADOPT_DIR"
mkdir -p "$ADOPT_DIR"
if ! (cd "$EXPRESS" && tar --exclude=./node_modules --exclude=./.git --exclude=./tmp -cf - . 2>/dev/null) | tar -C "$ADOPT_DIR" -xf - 2>/dev/null; then
  fail "a copy of express could not be made at $ADOPT_DIR"
else
  rm -f "$ADOPT_DIR/.professor/baseline.json"
  if [ -f "$ADOPT_DIR/.professor/manifest.json" ]; then
    jq --arg p "$B" '.interview = ((.interview // {}) + {blueprint_clone_path: $p})' "$ADOPT_DIR/.professor/manifest.json" \
      >"$ADOPT_DIR/.professor/manifest.json.tmp" && mv "$ADOPT_DIR/.professor/manifest.json.tmp" "$ADOPT_DIR/.professor/manifest.json"
  else
    mkdir -p "$ADOPT_DIR/.professor"
    printf '{"interview":{"blueprint_clone_path":"%s"}}\n' "$B" >"$ADOPT_DIR/.professor/manifest.json"
  fi
  pre="$(cd "$ADOPT_DIR" && pfm update check 2>&1)"
  pre_rc=$?
  [ "$pre_rc" -eq 1 ] && printf '%s\n' "$pre" | grep -q 'baseline.json not found' ||
    bad="$bad before adopt, check exited $pre_rc without the named 'baseline.json not found' terminal: $(one_line "$pre");"
  ad="$(pfm update adopt --root "$ADOPT_DIR" 2>&1)"
  ad_rc=$?
  [ "$ad_rc" -eq 0 ] || bad="$bad pfm update adopt --root exited $ad_rc ($(one_line "$ad"));"
  printf '%s\n' "$ad" | grep -qxF "professor: $ADOPT_DIR  blueprint $B" || bad="$bad no 'professor: $ADOPT_DIR  blueprint $B' line;"
  adopted="$(printf '%s\n' "$ad" | sed -n 's/^adopted \([0-9]*\) file(s) at \(.*\)$/\1 \2/p' | head -1)"
  n_adopted="${adopted%% *}"
  at_sha="${adopted#* }"
  [ -n "$n_adopted" ] && [ "$n_adopted" -gt 0 ] || bad="$bad no 'adopted N file(s) at <sha>' line with N > 0: $(one_line "$ad");"
  [ "$at_sha" = "$B_BASE_SHORT" ] || bad="$bad adopted at '$at_sha', want the store HEAD $B_BASE_SHORT;"
  absent="$(printf '%s\n' "$ad" | awk '$1 == "absent" { print $2; exit }')"
  printf '%s\n' "$ad" | grep -qE '^  kept +0 +\(already pinned, untouched\)$' || bad="$bad no 'kept 0 (already pinned, untouched)' row;"
  printf '%s\n' "$ad" | grep -qE '^  absent +[0-9]+ +\(mapped template, no local file — check reports NEW\)$' || bad="$bad no 'absent N (mapped template, no local file — check reports NEW)' row;"
  printf '%s\n' "$ad" | grep -qxF 'next: pfm update check' || bad="$bad no 'next: pfm update check' line;"
  [ "$(jq '.files | length' "$ADOPT_DIR/.professor/baseline.json" 2>/dev/null)" = "$n_adopted" ] ||
    bad="$bad baseline.json pins $(jq '.files | length' "$ADOPT_DIR/.professor/baseline.json" 2>/dev/null) file(s), want the adopted $n_adopted;"
  [ "$(jq -r '.blueprint.sha' "$ADOPT_DIR/.professor/baseline.json" 2>/dev/null)" = "$B_BASE_SHORT" ] || bad="$bad baseline.json blueprint.sha is not $B_BASE_SHORT;"
  post="$(cd "$ADOPT_DIR" && pfm update check 2>&1)"
  post_rc=$?
  want_rc=0
  [ "${absent:-0}" -gt 0 ] && want_rc=3
  [ "$post_rc" -eq "$want_rc" ] || bad="$bad after adopt check exited $post_rc, want $want_rc ($absent absent → NEW);"
  [ "$(count_of "$post" current)" = "$n_adopted" ] || bad="$bad after adopt current $(count_of "$post" current), want $n_adopted;"
  [ "$(count_of "$post" NEW)" = "${absent:-0}" ] || bad="$bad after adopt NEW $(count_of "$post" NEW), want the $absent absent template(s);"
  for s in UPDATED GONE-UPSTREAM LOCAL-DELETED; do
    [ "$(count_of "$post" "$s")" = 0 ] || bad="$bad after adopt $s $(count_of "$post" "$s"), want 0;"
  done
  again="$(cd "$ADOPT_DIR" && pfm update adopt 2>&1)"
  again_rc=$?
  [ "$again_rc" -eq 0 ] && printf '%s\n' "$again" | grep -qxF "adopted 0 file(s); blueprint pin unchanged ($B_BASE_SHORT)" &&
    printf '%s\n' "$again" | grep -qE "^  kept +$n_adopted +\(already pinned, untouched\)$" ||
    bad="$bad a second adopt (exit $again_rc) did not report 'adopted 0 file(s); blueprint pin unchanged ($B_BASE_SHORT)' with kept $n_adopted: $(one_line "$again");"
  # P31: --at REF pins against `git show REF:` bytes — the same commit here, so
  # the pin lands at REF's short sha with REF's VERSION and absent-at-ref 0.
  rm -f "$ADOPT_DIR/.professor/baseline.json"
  at="$(cd "$ADOPT_DIR" && pfm update adopt --at "$B_BASE" 2>&1)"
  at_rc=$?
  [ "$at_rc" -eq 0 ] || bad="$bad adopt --at $B_BASE_SHORT exited $at_rc ($(one_line "$at"));"
  printf '%s\n' "$at" | grep -qxF "adopted $n_adopted file(s) at $B_BASE_SHORT" || bad="$bad --at did not report 'adopted $n_adopted file(s) at $B_BASE_SHORT': $(one_line "$at");"
  printf '%s\n' "$at" | grep -qE "^  absent-at-ref +0 +\(template did not exist at $B_BASE; check reports NEW\)$" || bad="$bad --at printed no 'absent-at-ref 0 (template did not exist at $B_BASE; check reports NEW)' row;"
  [ "$(jq -r '.blueprint.version' "$ADOPT_DIR/.professor/baseline.json" 2>/dev/null)" = "$(git -C "$B" show "$B_BASE:VERSION" | tr -d '[:space:]')" ] ||
    bad="$bad --at pinned blueprint.version '$(jq -r .blueprint.version "$ADOPT_DIR/.professor/baseline.json" 2>/dev/null)', want git show $B_BASE_SHORT:VERSION;"
  badref="$(cd "$ADOPT_DIR" && pfm update adopt --at lane-a-no-such-ref 2>&1)"
  badref_rc=$?
  [ "$badref_rc" -eq 1 ] && printf '%s' "$badref" | grep -qF 'pfm update adopt: resolve --at lane-a-no-such-ref' ||
    bad="$bad --at an unknown ref exited $badref_rc without naming 'resolve --at lane-a-no-such-ref': $(one_line "$badref");"
  rm -rf "$ADOPT_DIR"
  if [ -n "$bad" ]; then fail "$bad"; else
    pass "a copy of express stripped of its baseline: check FAILED by name (1) → adopt --root pinned $n_adopted file(s) at $B_BASE_SHORT ($absent absent → NEW, nothing UPDATED/GONE/LOCAL-DELETED), a second adopt kept all $n_adopted, --at $B_BASE_SHORT pinned against that ref's bytes and VERSION, an unknown ref refused by name"
  fi
fi

# ─── A.07 — bare pfm update: preflight refusals, the rebuild, the rollback ──

beat A.07-self-update P33
spends none
expect-log 'invalid target tag'
expect-log 'is not present after fetch'
expect-log 'refuse dirty worktree'
expect-log 'replace owned binary'
bad=""
PFM_BIN="$HOME/.local/bin/pfm"
LEDGER="$MANAGED/binary-ownership.json"
version_before="$(pfm version 2>&1)"
marker_before="$(cat "$MANAGED/source-repo" 2>/dev/null)"
if [ ! -s "$LEDGER" ]; then
  fail "no binary ownership ledger at $LEDGER — pfm update refuses to replace anything without it, so the self-update cannot be exercised"
else
  cp -p "$LEDGER" "$BK/binary-ownership.json"
  i=0
  for owned in $(jq -r '.paths[]' "$LEDGER"); do
    cp -p "$owned" "$BK/pfm-owned-$i" || bad="$bad owned binary $owned could not be backed up;"
    i=$((i + 1))
  done
  # Preflight refusals, each before anything is staged or replaced.
  o="$(pfm update --repo "$B" --to v0 2>&1)"; rc=$?
  [ "$rc" -eq 1 ] && printf '%s' "$o" | grep -qF 'invalid target tag "v0" (expected vMAJOR.MINOR.PATCH)' ||
    bad="$bad --to v0 exited $rc without 'invalid target tag' ($(one_line "$o"));"
  o="$(pfm update --repo "$B" --to v999.999.999 2>&1)"; rc=$?
  [ "$rc" -eq 1 ] && printf '%s' "$o" | grep -qF 'target tag "v999.999.999" is not present after fetch' ||
    bad="$bad --to v999.999.999 exited $rc without 'is not present after fetch' ($(one_line "$o"));"
  LANE_TAG=v99.0.0
  git -C "$B" tag -f "$LANE_TAG" "$B_BASE" >/dev/null 2>&1 || bad="$bad the lane tag $LANE_TAG could not be created in $B;"
  : >"$B/lane-a-dirty"
  o="$(pfm update --repo "$B" --to "$LANE_TAG" 2>&1)"; rc=$?
  rm -f "$B/lane-a-dirty"
  [ "$rc" -eq 1 ] && printf '%s' "$o" | grep -qF 'refuse dirty worktree; commit or stash changes before update' ||
    bad="$bad a dirty --repo exited $rc without 'refuse dirty worktree' ($(one_line "$o"));"
  if [ -n "$bad" ]; then
    fail "preflight:$bad — the rebuild was not attempted"
  else
    # The body: two reproducible builds of the tagged source, the owned binary
    # replaced, the candidate's install --yes, doctor before and after, the
    # post-update template report for express.
    up="$(pfm update --repo "$B" --to "$LANE_TAG" --skip-harvest --root "$EXPRESS" 2>&1)"
    up_rc=$?
    [ "$up_rc" -eq 0 ] || bad="$bad pfm update --to $LANE_TAG exited $up_rc: $(one_line "$(printf '%s\n' "$up" | grep -E 'pfm update:|doctor' | tail -3)");"
    printf '%s\n' "$up" | grep -qE '^doctor after update: warnings=[0-9]+ \(before update: [0-9]+\)$' ||
      bad="$bad no 'doctor after update: warnings=N (before update: M)' line;"
    printf '%s\n' "$up" | grep -qxF "updated $LANE_TAG from $B" || bad="$bad no 'updated $LANE_TAG from $B' line;"
    [ "$(printf '%s\n' "$up" | tail -1)" = clean ] || bad="$bad the post-update report for --root $EXPRESS did not end 'clean': $(printf '%s\n' "$up" | tail -1);"
    [ "$(pfm version 2>&1)" = "pfm $LANE_TAG" ] || bad="$bad after the update pfm version reads '$(pfm version 2>&1)', want 'pfm $LANE_TAG';"
    [ "$(cat "$MANAGED/source-repo" 2>/dev/null)" = "$B" ] || bad="$bad the source-repo marker reads '$(cat "$MANAGED/source-repo" 2>/dev/null)' after updating from $B;"
    # Rollback on failure: a second owned path that EXISTS (so its backup is
    # taken) but sits on the read-only mount makes the replacement fail AFTER
    # the real binary was swapped; pfm must put it back, re-run the previous
    # installer, doctor it, and say so.
    if [ "$up_rc" -eq 0 ]; then
      jq --arg p "$WORKTREE/VERSION" '.paths += [$p]' "$LEDGER" >"$LEDGER.tmp" && mv "$LEDGER.tmp" "$LEDGER"
      rb="$(pfm update --repo "$B" --to "$LANE_TAG" --skip-harvest 2>&1)"
      rb_rc=$?
      [ "$rb_rc" -eq 1 ] || bad="$bad the update with an unwritable owned path exited $rb_rc, want 1;"
      printf '%s' "$rb" | grep -qF "replace owned binary $WORKTREE/VERSION" || bad="$bad the failure did not name the unwritable owned path $WORKTREE/VERSION;"
      [ "$(tr -d '[:space:]' <"$WORKTREE/VERSION")" = "$(tr -d '[:space:]' <"$B/VERSION")" ] || bad="$bad $WORKTREE/VERSION was altered by the failed replacement;"
      printf '%s' "$rb" | grep -qF "pfm update: rolled back $PFM_BIN" || bad="$bad no 'rolled back $PFM_BIN' line — the swapped binary was not reported restored;"
      printf '%s' "$rb" | grep -qF 'rolled back update-owned changes' || bad="$bad the terminal does not say 'rolled back update-owned changes': $(one_line "$(printf '%s\n' "$rb" | grep 'pfm update:' | tail -1)");"
      [ "$(pfm version 2>&1)" = "pfm $LANE_TAG" ] || bad="$bad after the rollback pfm version reads '$(pfm version 2>&1)', want the pre-attempt 'pfm $LANE_TAG';"
    else
      bad="$bad rollback-on-failure not exercised because the update itself failed;"
    fi
    # Restore: the tree-built binaries, the ledger, the marker and the fan-out
    # (an install from the real blueprint), the daemon (it ran a replaced binary).
    cp -p "$BK/binary-ownership.json" "$LEDGER"
    i=0
    for owned in $(jq -r '.paths[]' "$BK/binary-ownership.json"); do
      cp -p "$BK/pfm-owned-$i" "$owned" || bad="$bad $owned could not be restored from the backup;"
      i=$((i + 1))
    done
    # From the directory the marker named at lane start: install records the
    # clone by the cwd it was run from, so the marker comes back byte-identical.
    ri="$( (cd "$marker_before" && pfm install --yes 2>&1) )"
    ri_rc=$?
    [ "$ri_rc" -eq 0 ] || bad="$bad the restoring pfm install --yes from $marker_before exited $ri_rc ($(one_line "$(printf '%s\n' "$ri" | tail -3)"));"
    bash "$WORKTREE/infra/demo/daemon.sh" >/dev/null 2>&1 || bad="$bad daemon.sh could not converge the MCP daemon after the binary swap;"
    git -C "$B" tag -d "$LANE_TAG" >/dev/null 2>&1
    git -C "$B" config --unset core.hooksPath >/dev/null 2>&1
    [ "$(pfm version 2>&1)" = "$version_before" ] || bad="$bad after restore pfm version reads '$(pfm version 2>&1)', want '$version_before';"
    [ "$(cat "$MANAGED/source-repo" 2>/dev/null)" = "$marker_before" ] || bad="$bad after restore the source-repo marker reads '$(cat "$MANAGED/source-repo" 2>/dev/null)', want '$marker_before';"
    pfm doctor >/dev/null 2>&1; doc_rc=$?
    [ "$doc_rc" -le 1 ] || bad="$bad pfm doctor exits $doc_rc after the restore (want 0 or 1);"
    if [ -n "$bad" ]; then fail "$bad"; else
      pass "preflight refused v0, an absent tag and a dirty clone by name; the update to $LANE_TAG rebuilt twice, swapped the owned binary, doctored before/after ($(printf '%s\n' "$up" | grep '^doctor after update' | head -1)) and reported express clean; an unwritable owned path made the next update roll the binary back and say so (exit 1); the tree binary, ledger, marker and daemon were restored ($version_before, doctor exit $doc_rc)"
    fi
  fi
fi

# ─── A.08 — the Codex mirror: build and check, and check's own broken state ─

beat A.08-codex-mirror P34 P35
spends none
expect-log 'STALE'
bad=""
chk="$(in_express pfm codex check .)"
chk_rc=$?
[ "$chk_rc" -eq 0 ] && [ "$(printf '%s\n' "$chk" | tail -1)" = 'CODEX CHECK PASS' ] ||
  bad="$bad pfm codex check exited $chk_rc without 'CODEX CHECK PASS' ($(one_line "$(printf '%s\n' "$chk" | grep 'pfm codex' | head -2)"));"
agents_hash="$(sha256sum "$EXPRESS/AGENTS.md" 2>/dev/null | cut -d' ' -f1)"
[ -n "$agents_hash" ] || bad="$bad no $EXPRESS/AGENTS.md to compile into;"
bld="$(in_express pfm codex build .)"
bld_rc=$?
[ "$bld_rc" -eq 0 ] && [ "$(printf '%s\n' "$bld" | tail -1)" = 'CODEX BUILD PASS' ] ||
  bad="$bad pfm codex build exited $bld_rc without 'CODEX BUILD PASS' ($(one_line "$bld"));"
[ "$(sha256sum "$EXPRESS/AGENTS.md" 2>/dev/null | cut -d' ' -f1)" = "$agents_hash" ] ||
  bad="$bad a build over a PASSing mirror changed AGENTS.md — build and check disagree about what is compiled;"
# The broken state: a hand edit to a compiled file is STALE to check (exit 1)
# and build puts it back.
printf '\nLane A probe: stale %s\n' "$LANE_STAMP" >>"$EXPRESS/AGENTS.md"
stale="$(in_express pfm codex check .)"
stale_rc=$?
[ "$stale_rc" -eq 1 ] || bad="$bad check over a hand-edited AGENTS.md exited $stale_rc, want 1;"
printf '%s' "$stale" | grep -qF "pfm codex: STALE $EXPRESS/AGENTS.md" || bad="$bad check did not name 'STALE $EXPRESS/AGENTS.md': $(one_line "$stale");"
printf '%s\n' "$stale" | grep -qxF 'CODEX CHECK PASS' && bad="$bad check printed CODEX CHECK PASS over a stale mirror;"
rebuilt="$(in_express pfm codex build .)"
rebuilt_rc=$?
[ "$rebuilt_rc" -eq 0 ] || bad="$bad the repairing build exited $rebuilt_rc ($(one_line "$rebuilt"));"
[ "$(sha256sum "$EXPRESS/AGENTS.md" 2>/dev/null | cut -d' ' -f1)" = "$agents_hash" ] || bad="$bad build did not restore AGENTS.md to its compiled content;"
in_express pfm codex check . >/dev/null 2>&1 || bad="$bad check does not PASS after the repairing build;"
if ! after="$(restore_check)"; then bad="$bad express differs from its lane-start state: $(one_line "$after");"; fi
if [ -n "$bad" ]; then fail "$bad"; else
  pass "CODEX CHECK PASS and CODEX BUILD PASS (build idempotent over AGENTS.md); a hand edit to AGENTS.md is 'STALE' to check (exit 1) and the next build puts the compiled content back"
fi

# ─── A.09 — the global Codex agent mirror ───────────────────────────────────

beat A.09-codex-agents P36
spends none
bad=""
n_md="$(find "$BLUEPRINT/templates/global/agents" -maxdepth 1 -name '*.md' 2>/dev/null | wc -l | tr -d ' ')"
[ "$n_md" -gt 0 ] || bad="$bad no templates/global/agents/*.md in $BLUEPRINT to compile;"
first="$(pfm codex agents 2>/tmp/lane-a-codex-agents.err)"
first_rc=$?
_lane_log_only "   A.09: first run printed $(printf '%s\n' "$first" | grep -c .) line(s); $(printf '%s\n' "$first" | grep -cE '^(missing|copy|wrong-target) ') link(s) were not yet correct"
second="$(pfm codex agents 2>>/tmp/lane-a-codex-agents.err)"
second_rc=$?
[ "$first_rc" -eq 0 ] && [ "$second_rc" -eq 0 ] || bad="$bad pfm codex agents exited $first_rc then $second_rc ($(one_line "$(cat /tmp/lane-a-codex-agents.err)"));"
[ "$(printf '%s\n' "$second" | tail -1)" = 'CODEX AGENTS PASS' ] || bad="$bad no CODEX AGENTS PASS terminal: $(one_line "$second" | cut -c1-160);"
n_clean="$(printf '%s\n' "$second" | grep -c '\.toml: [0-9]* B, parses clean$')"
[ "$n_clean" -eq "$n_md" ] || bad="$bad $n_clean '.toml: N B, parses clean' line(s) for $n_md agent source(s);"
not_correct="$(printf '%s\n' "$second" | grep -E '^(missing|copy|wrong-target|conflict) ' | head -3)"
[ -z "$not_correct" ] || bad="$bad on the second run a registry link is still not 'correct': $(one_line "$not_correct");"
[ -s /tmp/lane-a-codex-agents.err ] && bad="$bad problem line(s) on stderr: $(one_line "$(cat /tmp/lane-a-codex-agents.err)" | cut -c1-200);"
for src in "$BLUEPRINT"/templates/global/agents/*.md; do
  name="$(basename "$src" .md)"
  link="$HOME/.codex/agents/$name.toml"
  [ -L "$link" ] || { bad="$bad $link is not a symlink;"; continue; }
  [ "$(readlink -f "$link")" = "$(readlink -f "$BLUEPRINT/templates/global/agents/$name.toml")" ] ||
    bad="$bad $link → $(readlink "$link"), not the compiled twin beside $src;"
done
rm -f /tmp/lane-a-codex-agents.err
if [ -n "$bad" ]; then fail "$bad"; else
  pass "$n_md global agents compiled to parsing .toml twins, every registry link 'correct' on the second run, $HOME/.codex/agents/*.toml → the twins beside their sources, no problem on stderr"
fi

# ─── A.10 — a blueprint reached through a symlink (P10.1, update side) ──────

beat A.10-symlinked-blueprint I37
spends none
bad=""
[ -L "$BLUEPRINT" ] || bad="$bad $BLUEPRINT is not a symlink — the default store is not the linked shape this beat asserts;"
via_home="$(in_express pfm update check)"
via_home_rc=$?
[ "$via_home_rc" -eq 0 ] || bad="$bad check through the linked $BLUEPRINT exited $via_home_rc ($(one_line "$via_home"));"
printf '%s\n' "$via_home" | grep -qF "→ $WT_SHA" ||
  bad="$bad the store sha resolved through $BLUEPRINT → $WORKTREE is not the worktree's HEAD $WT_SHA (symlink + fence git): $(printf '%s\n' "$via_home" | head -1);"
printf '%s' "$via_home" | grep -qE 'self-hosted@unknown|UNREADABLE' && bad="$bad the linked store reads as self-hosted@unknown or UNREADABLE;"
LINK="$HOME/lane-a-blueprint-link"
rm -f "$LINK"
ln -s "$B" "$LINK"
point_express_at "$B"
direct="$(in_express pfm update check)"
direct_rc=$?
point_express_at "$LINK"
linked="$(in_express pfm update check)"
linked_rc=$?
unpoint_express
rm -f "$LINK"
[ "$direct_rc" -eq 0 ] && [ "$linked_rc" -eq 0 ] || bad="$bad check exited $direct_rc via $B and $linked_rc via the link $LINK;"
printf '%s\n' "$linked" | grep -qF "→ $B_BASE_SHORT" || bad="$bad through the link the store sha is not $B_BASE_SHORT: $(printf '%s\n' "$linked" | head -1);"
[ "$(printf '%s\n' "$direct" | tail -n +2)" = "$(printf '%s\n' "$linked" | tail -n +2)" ] ||
  bad="$bad the report through the link differs from the report through the real path beyond the header: $(one_line "$(diff <(printf '%s\n' "$direct") <(printf '%s\n' "$linked") | head -3)");"
if ! after="$(restore_check)"; then bad="$bad express differs from its lane-start state: $(one_line "$after");"; fi
if [ -n "$bad" ]; then fail "$bad"; else
  pass "the default store $BLUEPRINT is a link and resolves the worktree's HEAD $WT_SHA; $B reached through $LINK reports the same clean check with the same store sha $B_BASE_SHORT"
fi

# ─── A.11 — the guard hook, driven by a real chat in express ────────────────

open_main() {
  pfm chat new --name "$CHAT" --engine cc --account "$SEAT" --cwd "$EXPRESS" --await --timeout 300 \
    "You are $CHAT, the chat an automated Tier B lane drives inside this repository. Reply with one word: ready. Then wait and do exactly what each next message says, nothing more." 2>&1
}
lane_reopen 'open_main'

beat A.11-guard-hook
spends "cc:$SEAT"
target "$CHAT"
bad=""
# Hooks rooted at $CLAUDE_PROJECT_DIR and present (the spec's static half).
hooks="$(jq -r '.hooks | to_entries[] | .value[] | .hooks[] | select(.type == "command") | .command' "$EXPRESS/.claude/settings.json" 2>&1)"
[ -n "$hooks" ] || bad="$bad $EXPRESS/.claude/settings.json enumerates no command hook;"
while IFS= read -r cmd; do
  [ -n "$cmd" ] || continue
  case "$cmd" in '$CLAUDE_PROJECT_DIR/'*) ;; *) bad="$bad hook '$cmd' is not rooted at \$CLAUDE_PROJECT_DIR;"; continue ;; esac
  script="${cmd#\$CLAUDE_PROJECT_DIR/}"
  script="${script%% *}"
  [ -x "$EXPRESS/$script" ] || bad="$bad hook script $script is absent or not executable in express;"
done <<EOF
$hooks
EOF
printf '%s' "$hooks" | grep -q 'pfm-guard.sh' || bad="$bad no pfm-guard.sh PreToolUse hook wired;"
printf '%s' "$hooks" | grep -q 'codex-sync.sh sync' || bad="$bad no codex-sync.sh Stop hook wired;"
GUARD_FILE=".claude/agents/gitter.md"
ROOT_FILE="CLAUDE.md"
if [ -n "$bad" ]; then
  fail "the express hook wiring is not the shape the guard needs:$bad"
elif [ ! -f "$EXPRESS/$GUARD_FILE" ] || [ ! -f "$EXPRESS/$ROOT_FILE" ]; then
  fail "express has no $GUARD_FILE or no $ROOT_FILE to edit"
else
  if live_chat "$CHAT"; then
    _lane_log_only "   A.11: $CHAT was already live — reused"
  else
    o="$(open_main)"
    live_chat "$CHAT" || bad="$bad $CHAT could not be opened in $EXPRESS: $(one_line "$o");"
  fi
  if [ -n "$bad" ]; then fail "$bad"; else
    target_live "$CHAT"
    sid="$(live_field "$CHAT" 2)"
    ACTIVE="$EXPRESS/tmp/professor_pfm_active.$sid"
    QUALITY="$EXPRESS/tmp/professor_quality_loaded.$sid"
    rm -f "$ACTIVE" "$QUALITY"
    guard_before="$(sha256sum "$EXPRESS/$GUARD_FILE" | cut -d' ' -f1)"
    agents_before="$(sha256sum "$EXPRESS/AGENTS.md" 2>/dev/null | cut -d' ' -f1)"
    transcript="$(find -L "$SEAT_DIR/projects" -name "$sid.jsonl" 2>/dev/null | head -1)"
    # 1. Without the stamp: the Edit is DENIED, the file unchanged.
    pfm chat inject --allow-unsigned "$CHAT" \
      "Using the Edit tool and ONLY the Edit tool, append this exact line to the end of the file $GUARD_FILE in this repository: Lane A probe: LANE-A-DENY. Make exactly one Edit attempt. If it is denied or fails, do not retry, do not follow any unlock instructions, and do not use Bash, Write or any other tool to change any file. Whatever happened, finish by replying with exactly one word: EDIT-ONE-DONE" >/dev/null 2>&1 ||
      bad="$bad the deny stimulus could not be injected;"
    wait_last "$CHAT" EDIT-ONE-DONE 300 || bad="$bad no EDIT-ONE-DONE in 300s (${LANE_WAIT_WHY:-no wait reason});"
    deny_needle='infra edits route through /pcm'
    deny_seen=""
    pane "$CHAT" | grep -qF "$deny_needle" && deny_seen="pane"
    [ -n "$transcript" ] && grep -qF "$deny_needle" "$transcript" 2>/dev/null && deny_seen="${deny_seen:+$deny_seen+}transcript"
    guard_after="$(sha256sum "$EXPRESS/$GUARD_FILE" | cut -d' ' -f1)"
    if [ "$guard_after" != "$guard_before" ]; then
      if [ -f "$ACTIVE" ]; then
        bad="$bad $GUARD_FILE CHANGED without the lane's stamp — the model opened the gate itself ($ACTIVE exists) rather than the guard letting it through;"
      else
        bad="$bad $GUARD_FILE CHANGED with no /pcm stamp present — the guard did not deny the Edit (or the model wrote outside the Edit tool: read the transcript);"
      fi
    elif [ -z "$deny_seen" ]; then
      bad="$bad $GUARD_FILE is unchanged but the deny text '$deny_needle' is neither on the pane nor in the transcript ${transcript:-<no transcript file found under $SEAT_DIR/projects>} — the guard ran silently or the model never attempted the Edit;"
    fi
    # 2. With the stamp (the deny message's own unlock, written by the lane for
    #    this session id): the same Edit is ALLOWED, and the Stop hook recompiles.
    mkdir -p "$EXPRESS/tmp"
    date +%s >"$ACTIVE"
    date +%s >"$QUALITY"
    pfm chat inject --allow-unsigned "$CHAT" \
      "Using the Edit tool and ONLY the Edit tool, make exactly two edits: append the line Lane A probe: LANE-A-ALLOW-AGENT to the end of $GUARD_FILE, and append the line Lane A probe: LANE-A-ALLOW-ROOT to the end of $ROOT_FILE. If an edit is denied, do not retry and do not use any other tool. Then reply with exactly one word: EDIT-TWO-DONE" >/dev/null 2>&1 ||
      bad="$bad the allow stimulus could not be injected;"
    wait_last "$CHAT" EDIT-TWO-DONE 300 || bad="$bad no EDIT-TWO-DONE in 300s (${LANE_WAIT_WHY:-no wait reason});"
    grep -qF 'LANE-A-ALLOW-AGENT' "$EXPRESS/$GUARD_FILE" || bad="$bad with both markers fresh the Edit of $GUARD_FILE did not land;"
    grep -qF 'LANE-A-ALLOW-ROOT' "$EXPRESS/$ROOT_FILE" || bad="$bad with both markers fresh the Edit of $ROOT_FILE did not land;"
    if ! wait_for 180 "grep -qF LANE-A-ALLOW-ROOT '$EXPRESS/AGENTS.md'"; then
      bad="$bad the Stop hook did not recompile AGENTS.md from the edited CLAUDE.md within 180s (${LANE_WAIT_WHY:-no wait reason}); AGENTS.md hash $(sha256sum "$EXPRESS/AGENTS.md" 2>/dev/null | cut -d' ' -f1 | cut -c1-12) vs before ${agents_before:0:12};"
    fi
    sleep 5 # the Stop hook clears its flag right after the check that follows the build
    [ -f "$EXPRESS/tmp/professor_codex_dirty" ] && bad="$bad tmp/professor_codex_dirty is still set after the turn — codex-sync.sh sync did not clear it (build or check failed on the pane);"
    # Restore express: the committed files back, the mirror rebuilt from them,
    # the session markers gone.
    git -C "$EXPRESS" checkout -q -- "$GUARD_FILE" "$ROOT_FILE" 2>/dev/null || bad="$bad git checkout of $GUARD_FILE/$ROOT_FILE failed;"
    in_express pfm codex build . >/dev/null || bad="$bad the mirror could not be rebuilt after the restore;"
    rm -f "$ACTIVE" "$QUALITY" "$EXPRESS/tmp/professor_codex_dirty"
    if ! after="$(restore_check)"; then bad="$bad express differs from its lane-start state after restore: $(one_line "$after");"; fi
    if [ -n "$bad" ]; then fail "$bad"; else
      pass "without the stamp the Edit of $GUARD_FILE was denied ($deny_seen carried '$deny_needle', file unchanged); with tmp/professor_pfm_active.<sid> + professor_quality_loaded.<sid> fresh both edits landed and the Stop hook recompiled AGENTS.md with the CLAUDE.md line; express restored"
    fi
  fi
fi

# ─── A.12 — express's own dev.sh and suite ──────────────────────────────────

beat A.12-dev-suite
spends none
bad=""
DEV_SH="$EXPRESS/.claude/scripts/dev.sh"
if [ ! -x "$DEV_SH" ]; then
  fail "no executable $DEV_SH in express (the scaffold ships it; the interview fills its roster)"
else
  st="$(in_express bash .claude/scripts/dev.sh status)"
  st_rc=$?
  if printf '%s' "$st" | grep -q 'PROJECTS roster is empty'; then
    bad="$bad dev.sh status exited $st_rc: the interview left the PROJECTS roster empty ('PROJECTS roster is empty — SETUP fills one entry per server-bearing roster project');"
  else
    [ "$st_rc" -eq 0 ] || bad="$bad dev.sh status exited $st_rc ($(one_line "$st" | cut -c1-200));"
    printf '%s' "$st" | grep -q 'Dev server status' || bad="$bad dev.sh status printed no 'Dev server status' header;"
    if ! printf '%s' "$st" | grep -qx 'NO_SERVERS=true' && ! { printf '%s' "$st" | grep -qx -- '---REPORT---' && printf '%s' "$st" | grep -qx -- '---END---'; }; then
      bad="$bad dev.sh status printed neither NO_SERVERS=true nor a ---REPORT---/---END--- block: $(one_line "$st" | cut -c1-200);"
    fi
  fi
  # `test` is not a dev.sh mode: the script refuses it by name with its usage
  # line (exit 1), and the suite runs by the interview's own test command.
  tm="$(in_express bash .claude/scripts/dev.sh test)"
  tm_rc=$?
  [ "$tm_rc" -eq 1 ] && printf '%s' "$tm" | grep -q '^Usage: .*{up|kill|restart' ||
    bad="$bad dev.sh test exited $tm_rc without the named Usage refusal ($(one_line "$tm" | cut -c1-160));"
  test_cmd="$(jq -r '[.interview.tech_commands // {} | .. | objects | .test? // empty] | map(select(. != "" and . != "skip" and . != "-")) | first // empty' "$MANIFEST" 2>/dev/null)"
  [ -n "$test_cmd" ] || test_cmd="npm test"
  suite="$(cd "$EXPRESS" && timeout 900 bash -c "$test_cmd" 2>&1)"
  suite_rc=$?
  if [ "$suite_rc" -eq 124 ]; then
    bad="$bad express's suite ('$test_cmd') did not finish in 900s;"
  elif [ "$suite_rc" -ne 0 ]; then
    bad="$bad express's suite ('$test_cmd') exited $suite_rc: $(one_line "$(printf '%s\n' "$suite" | tail -5)" | cut -c1-240);"
  fi
  if ! after="$(restore_check)"; then bad="$bad express differs from its lane-start state after the suite: $(one_line "$after");"; fi
  if [ -n "$bad" ]; then fail "$bad"; else
    pass "dev.sh status exit 0 with its named markers ($(printf '%s' "$st" | grep -oE 'NO_SERVERS=true|---REPORT---' | head -1)); 'test' refused by the Usage line (no such mode); express's own suite '$test_cmd' exit 0 ($(printf '%s\n' "$suite" | grep -cE 'passing|✓|ok ' ) passing-shaped line(s))"
  fi
fi

# ─── A.13 — the OpenCode compile layer for adopters (known gap) ─────────────

beat A.13-opencode-layer-gap P37
spends none
if [ -d "$EXPRESS/.opencode" ] && [ -n "$(find "$EXPRESS/.opencode" -name 'opencode.jsonc' -o -name '*.md' 2>/dev/null | head -1)" ]; then
  fail "express carries a compiled .opencode/ layer — the ledgered gap (no pfm command wires it for adopters) no longer holds: remove the entry or name what produced it"
elif pfm --help 2>&1 | grep -qi 'opencode'; then
  fail "pfm --help names an opencode verb — the ledgered absence is no longer true"
else
  known A.13-opencode-layer-gap
fi

# ─── A.14 — the picker's cached release notice, refreshed by the internal verb

beat A.14-release-notice X41
spends none
expect-log 'latest Professor release returned'
bad=""
RN_PORT=18477
RN_CACHE=/tmp/lane-a-update-check.json
rm -f "$RN_CACHE" "$RN_CACHE.lock"
cat >/tmp/lane-a-redirect.py <<'PY'
import http.server, sys
PORT = int(sys.argv[1])
class H(http.server.BaseHTTPRequestHandler):
    def do_HEAD(self):
        if self.path.endswith('/releases/latest'):
            self.send_response(302)
            self.send_header('Location', 'http://127.0.0.1:%d/lane/professor/releases/tag/v9.9.9' % PORT)
        else:
            self.send_response(200)
        self.end_headers()
    do_GET = do_HEAD
    def log_message(self, *args):
        pass
http.server.HTTPServer(('127.0.0.1', PORT), H).serve_forever()
PY
python3 /tmp/lane-a-redirect.py "$RN_PORT" >/dev/null 2>&1 &
RN_PID=$!
up=""
for _ in 1 2 3 4 5 6 7 8 9 10; do
  [ "$(curl -s -o /dev/null -w '%{http_code}' -m 2 "http://127.0.0.1:$RN_PORT/lane/x")" = 200 ] && { up=yes; break; }
  sleep 1
done
if [ -z "$up" ]; then
  kill "$RN_PID" >/dev/null 2>&1
  fail "the local redirect server never answered on :$RN_PORT (python3 http.server) — the release lookup has nothing hermetic to hit"
else
  url="http://127.0.0.1:$RN_PORT/lane/professor/releases/latest"
  o="$(pfm internal update-check --cache "$RN_CACHE" --current v0.0.1 --url "$url" 2>&1)"; rc=$?
  [ "$rc" -eq 0 ] || bad="$bad update-check exited $rc ($(one_line "$o"));"
  [ -s "$RN_CACHE" ] || bad="$bad no cache written at $RN_CACHE;"
  jq -e '.latest == "v9.9.9" and .current == "v0.0.1" and .release_url == "http://127.0.0.1:'"$RN_PORT"'/lane/professor/releases/tag/v9.9.9" and (.checked_at | length) > 0' "$RN_CACHE" >/dev/null 2>&1 ||
    bad="$bad the cache does not carry latest v9.9.9 / current v0.0.1 / the tag release_url / checked_at: $(one_line "$(cat "$RN_CACHE" 2>&1)");"
  first_bytes="$(cat "$RN_CACHE" 2>/dev/null)"
  sleep 1
  o="$(pfm internal update-check --cache "$RN_CACHE" --current v0.0.1 --url "$url" 2>&1)"; rc=$?
  [ "$rc" -eq 0 ] || bad="$bad the second (fresh-cache) run exited $rc ($(one_line "$o"));"
  [ "$(cat "$RN_CACHE" 2>/dev/null)" = "$first_bytes" ] || bad="$bad a lookup within the 6h freshness window rewrote the cache;"
  # Error, never absence: a URL that answers 200 (no redirect) is a named failure and the cache is kept.
  o="$(pfm internal update-check --cache "$RN_CACHE" --current v0.0.1 --url "http://127.0.0.1:$RN_PORT/lane/nothing" 2>&1)"; rc=$?
  [ "$rc" -eq 1 ] && printf '%s' "$o" | grep -qF 'latest Professor release returned 200' ||
    bad="$bad a 200 (no redirect) answer exited $rc without 'latest Professor release returned 200' ($(one_line "$o"));"
  [ "$(cat "$RN_CACHE" 2>/dev/null)" = "$first_bytes" ] || bad="$bad a failed lookup changed the cache (the last good notice must survive);"
  pfm internal update-check --current v0.0.1 --url "$url" >/dev/null 2>&1
  [ $? -eq 2 ] || bad="$bad update-check without --cache did not exit 2 (usage);"
  o="$(pfm internal update-check --cache "$RN_CACHE" --current not-a-version --url "$url" 2>&1)"; rc=$?
  [ "$rc" -eq 1 ] && printf '%s' "$o" | grep -qF 'is not vMAJOR.MINOR.PATCH' || bad="$bad a malformed --current exited $rc without naming the version shape ($(one_line "$o"));"
  kill "$RN_PID" >/dev/null 2>&1
  rm -f /tmp/lane-a-redirect.py "$RN_CACHE" "$RN_CACHE.lock"
  if [ -n "$bad" ]; then fail "$bad"; else
    pass "update-check followed the release redirect to v9.9.9 and wrote the notice (current v0.0.1, release_url, checked_at); a second run inside the freshness window left the cache byte-identical; a 200 answer, a missing --cache and a malformed --current each refused by name with the last notice kept"
  fi
fi

# ─── A.15 — cross-lane: an adopter update + the hook rewrite under a live fleet

beat A.15-fleet-unchanged-by-update C1 I23
spends "cc:$SEAT"
target_live "$E1_CHAT"
if requires; then
  bad=""
  LEDGER_H="$MANAGED/settings-hook-ownership.json"
  fleet_rows() { pfm ls --tsv 2>&1; }
  hooks_of_live_chats() { # the ownership ledger plus, per live chat's seat, its settings hooks and its ledger rows
    local acct dir
    cat "$LEDGER_H" 2>&1
    for acct in $(pfm ls --tsv 2>/dev/null | awk -F'\t' 'NR > 1 && $1 ~ /^live-/ && $9 ~ /^[0-9]+$/ { print $9 }' | sort -u); do
      dir="$(jq -r --argjson a "$acct" '.accounts[] | select(.id == $a) | .configDir' "$CONFIG")"
      case "$dir" in "~"*) dir="$HOME${dir#\~}" ;; esac
      printf 'seat %s %s\n' "$acct" "$dir"
      jq -S '.hooks' "$dir/settings.json" 2>&1
      jq -c --arg d "$dir" '[.hooks[] | select(.path | startswith($d))]' "$LEDGER_H" 2>&1
    done
  }
  [ -s "$LEDGER_H" ] || bad="$bad no hook ownership ledger at $LEDGER_H (I23);"
  live_before="$(pfm ls --tsv 2>/dev/null | awk -F'\t' 'NR > 1 && $1 ~ /^live-/ { print $5 }' | sort | tr '\n' ' ')"
  rows_before="$(fleet_rows)"
  hooks_before="$(hooks_of_live_chats)"
  upd="$(pfm update check --root "$EXPRESS" 2>&1)"
  upd_rc=$?
  [ "$upd_rc" -eq 0 ] && [ "$(printf '%s\n' "$upd" | tail -1)" = clean ] || bad="$bad the adopter update (pfm update check --root $EXPRESS) exited $upd_rc, not clean: $(one_line "$(printf '%s\n' "$upd" | tail -2)");"
  rw="$( (cd "$SOURCE_REPO" && pfm install --yes 2>&1) )"
  rw_rc=$?
  [ "$rw_rc" -eq 0 ] || bad="$bad the hook rewrite (pfm install --yes from $SOURCE_REPO) exited $rw_rc ($(one_line "$(printf '%s\n' "$rw" | tail -3)"));"
  printf '%s\n' "$rw" | grep -q 'summary changed=' || bad="$bad pfm install --yes printed no 'summary changed=' line — the rewrite cannot be judged;"
  rows_after="$(fleet_rows)"
  hooks_after="$(hooks_of_live_chats)"
  if [ "$rows_after" != "$rows_before" ]; then
    # size (7) and activity (8) are a chat's own clock; a difference confined to
    # them is not the fleet changing under the update, and is reported as such.
    masked_before="$(printf '%s\n' "$rows_before" | awk -F'\t' -v OFS='\t' '{ $7 = ""; $8 = ""; print }')"
    masked_after="$(printf '%s\n' "$rows_after" | awk -F'\t' -v OFS='\t' '{ $7 = ""; $8 = ""; print }')"
    if [ "$masked_before" = "$masked_after" ]; then
      rows_note="rows identical except size/activity columns of the live chats"
    else
      bad="$bad pfm ls --tsv rows changed under the update (beyond size/activity): $(one_line "$(diff <(printf '%s\n' "$masked_before") <(printf '%s\n' "$masked_after") | grep '^[<>]' | head -4)");"
    fi
  else
    rows_note="rows byte-identical"
  fi
  [ "$hooks_after" = "$hooks_before" ] ||
    bad="$bad the hook ownership ledger or a live chat's seat hooks changed: $(one_line "$(diff <(printf '%s\n' "$hooks_before") <(printf '%s\n' "$hooks_after") | grep '^[<>]' | head -4)");"
  live_after="$(pfm ls --tsv 2>/dev/null | awk -F'\t' 'NR > 1 && $1 ~ /^live-/ { print $5 }' | sort | tr '\n' ' ')"
  [ "$live_after" = "$live_before" ] || bad="$bad the set of live chats changed: '$live_before' → '$live_after';"
  st="$(pfm chat status "$E1_CHAT" 2>&1)"
  st_rc=$?
  [ "$st_rc" -eq 0 ] && printf '%s' "$st" | grep -qiE 'idle|working' || bad="$bad $E1_CHAT no longer answers status after the update (exit $st_rc: $(one_line "$st"));"
  if [ -n "$bad" ]; then fail "$bad"; else
    pass "live chats [$live_before] · adopter update clean + hook rewrite ($(printf '%s\n' "$rw" | grep -oE 'summary changed=[0-9]+' | tail -1)) · pfm ls $rows_note · ownership ledger and every live chat's seat hooks byte-identical · $E1_CHAT answers status"
  fi
fi

lane_end
