#!/usr/bin/env bash
# ci-run_test.sh — infra/fence/journey/ci-run.sh (run.sh --ci-linux|--ci-mac) against a fake gh and local bare
# repositories standing in for origin and one plugin origin: the snapshot holds the disk, the caller's checkout
# stays untouched, unpushed plugin commits reach the plugin origin, the dispatch names exactly the selected
# jobs and ci.yml's plan job turns them into those entries, every temporary branch goes, a red leak gate
# pushes nothing.
set -uo pipefail
SHTEST_TAG=ci-run-test
source "$(dirname -- "${BASH_SOURCE[0]}")/../../../scripts/shtest.sh"
REPO_ROOT="$(cd "$(dirname -- "${BASH_SOURCE[0]}")/../../.." && pwd)"
SUT="$REPO_ROOT/infra/fence/journey/ci-run.sh"
shtest_isolate_host
unset LEAK_TERMS # the fence sets its own; the script derives the main checkout's
ZERO=0000000000000000000000000000000000000000
EMPTY_TREE=4b825dc642cb6eb9a060e54bf8d69288fbee4904 # git's empty tree: the base of a scan of a new ref

# The fake gh: logs every call, answers from FAKE_GH_* and, at the dispatch, records the remotes' ci-tmp refs.
mkdir -p "$T/bin"
cat >"$T/bin/gh" <<'EOF'
#!/usr/bin/env bash
echo "$*" >>"$GH_LOG"
case "$1 $2" in
  "auth status") exit "${FAKE_GH_AUTH:-0}" ;;
  "workflow run")
    for r in $FAKE_REMOTES; do git -C "$r" for-each-ref --format='%(objectname) %(refname)' 'refs/heads/ci-tmp/' | sed "s|^|${r##*/} |"; done >"$DISPATCH_REFS"
    [ -n "${FAKE_GH_NO_URL:-}" ] || echo "https://github.com/acme/widget/actions/runs/4242" ;;
  "run list") echo "https://github.com/acme/widget/actions/runs/4242" ;;
  "run view")
    if [[ " $* " == *" --log-failed "* ]]; then printf 'prepare\tRun suite\t2026-11-12T10:00:00.0000000Z --- FAIL: Journey/install\n'; exit 0; fi
    if [ -n "${FAKE_GH_HANG:-}" ]; then echo '{"status":"in_progress","conclusion":"","jobs":[]}'; else cat "$FAKE_GH_VIEW"; fi ;;
  "run cancel") ;;
  "run download")
    while [ $# -gt 0 ]; do [ "$1" = --dir ] && d="$2"; shift; done
    mkdir -p "$d/journey-linux" && echo profile >"$d/journey-linux/summary.md" ;;
  *) echo "fake gh: unknown call $*" >&2; exit 64 ;;
esac
EOF
chmod +x "$T/bin/gh"
export PATH="$T/bin:$PATH"

# view <file> <name=conclusion>...: a completed run's jobs, each 812 s long.
view() {
  local f="$1" i=0 pair
  shift
  { echo '{"status":"completed","conclusion":"x","jobs":['
    for pair in "$@"; do
      [ "$i" = 0 ] || echo ,
      printf '{"databaseId":%d,"name":"%s","conclusion":"%s","startedAt":"2026-11-12T10:00:00Z","completedAt":"2026-11-12T10:13:32Z"}' \
        $((100 + i)) "${pair%=*}" "${pair##*=}"
      i=$((i + 1))
    done
    echo ']}'; } >"$f"
}

# fixture <dir>: origin.git and plug.git, a plugin seeded on develop, and main: a checkout with the script, the
# fake pre-push hook armed, a pushed develop, then on disk an edit, a deletion, an untracked and an ignored file,
# and in the plugin an unpushed commit plus an untracked file.
fixture() {
  local S="$1"
  mkdir -p "$S" && cd "$S" || exit 1
  export GIT_CONFIG_GLOBAL="$S/gitconfig" HOOK_LOG="$S/hook.log" GH_LOG="$S/gh.log" DISPATCH_REFS="$S/dispatch-refs"
  export FAKE_REMOTES="$S/origin.git $S/plug.git" PLUGIN_SCAN_LOG="$S/plugin-scan.log"
  git config --global user.name tester && git config --global user.email tester@example.invalid &&
    git config --global protocol.file.allow always && git config --global init.defaultBranch develop &&
    git config --global "url.$S/origin.git.insteadOf" https://github.com/acme/widget.git &&
    git config --global "url.$S/plug.git.insteadOf" https://github.com/acme/plug.git &&
    git init -q --bare origin.git && git init -q --bare plug.git && git init -q seed &&
    echo p >seed/p.txt && git -C seed add -A && git -C seed commit -qm seed &&
    git -C seed push -q https://github.com/acme/plug.git develop &&
    git init -q main && mkdir -p main/.githooks main/scripts main/infra/fence/journey &&
    cp "$SUT" "$REPO_ROOT/infra/fence/journey/run.sh" main/infra/fence/journey/ &&
    printf 'ignored.txt\n/tmp/\nscripts/leak-terms.txt\n' >main/.gitignore &&
    printf 'secretterm\n' >main/scripts/leak-terms.txt && echo a >main/a.txt && echo b >main/b.txt &&
    cat >main/.githooks/pre-push <<EOF &&
#!/usr/bin/env bash
while read -r _ sha ref _; do
  [ "\$sha" = $ZERO ] && continue
  echo "\$sha \$ref \${LEAK_TERMS:-}" >>"\$HOOK_LOG"
  [ "\${FAKE_LEAK:-clean}" = clean ] || { echo "PUSH BLOCKED: fake leak in \$sha" >&2; exit 1; }
done
EOF
    cat >main/scripts/leak-check.sh <<'EOF2' &&
#!/usr/bin/env bash
# The fake gate of a plugin push: --range OLD NEW --remote NAME, the plugin named by PFM_DEV_REPO_*.
echo "$2 $3 $5 $PFM_DEV_REPO_GIT_DIR $PFM_DEV_REPO_WORK_TREE ${LEAK_TERMS:-}" >>"$PLUGIN_SCAN_LOG"
[ "${FAKE_PLUGIN_LEAK:-clean}" = clean ] || { echo "leak-check: fake leak in plugin commit $3" >&2; exit 1; }
EOF2
    chmod +x main/.githooks/pre-push main/scripts/leak-check.sh && git -C main remote add origin https://github.com/acme/widget.git &&
    git -C main submodule add -q https://github.com/acme/plug.git plugins/plug && git -C main add -A &&
    git -C main commit -qm base && git -C main push -q origin develop &&
    git -C main config core.hooksPath .githooks || { echo "fixture $S failed" >&2; exit 1; }
}
dirty() { # dirty <main>: the disk state the snapshot must hold
  echo edited >"$1/a.txt" && rm "$1/b.txt" && echo new >"$1/new.txt" && echo no >"$1/ignored.txt" &&
    echo q >"$1/plugins/plug/q.txt" && git -C "$1/plugins/plug" add q.txt && git -C "$1/plugins/plug" commit -qm unpushed &&
    echo u >"$1/plugins/plug/untracked.txt"
}
# state <checkout>: everything the caller owns, read without taking git's optional index lock.
state() {
  sha256sum "$(git -C "$1" rev-parse --path-format=absolute --git-path index)" | cut -d' ' -f1
  git -C "$1" rev-parse HEAD; git -C "$1" symbolic-ref -q HEAD || echo detached
  git -C "$1" --no-optional-locks status --porcelain=v1 -uall --ignored | grep -v '^!! tmp/' # tmp/: the run's own output
  git -C "$1" stash list; git -C "$1" for-each-ref
}
# tmp_refs <bare>: its ci-tmp branches, none expected after any run
tmp_refs() { git -C "$1" for-each-ref --format='%(refname)' 'refs/heads/ci-tmp/'; }
gone() { # gone <label> <S>
  if [ -z "$(tmp_refs "$2/origin.git")$(tmp_refs "$2/plug.git")" ]; then ok "$1: every temporary branch is deleted"
  else bad "$1: temporary branches left" "$(tmp_refs "$2/origin.git") $(tmp_refs "$2/plug.git")"; fi
}
dispatched() { # dispatched <label> <S> <only>
  local got
  got="$(grep '^workflow run' "$2/gh.log" | tail -n 1 | sed -E 's|--ref ci-tmp/[^ ]+|--ref BRANCH|')"
  [ "$got" = "workflow run ci.yml --repo acme/widget --ref BRANCH -f only=$3" ] &&
    ok "$1: the dispatch names exactly $3" || bad "$1: dispatch" "expected only=$3" "actual: $got"
}

S="$T/a"
fixture "$S"
dirty "$S/main"
before="$(state "$S/main")" before_plug="$(state "$S/main/plugins/plug")"
plug_head="$(git -C "$S/main/plugins/plug" rev-parse HEAD)"

# --ci-linux, every selected job green.
view "$S/view-ok" "plan=success" "gate (e2e)=success" "journey (linux)=success" "journey (macos)=skipped" "ci=success"
out="$(FAKE_GH_VIEW="$S/view-ok" bash "$S/main/infra/fence/journey/run.sh" --ci-linux 2>&1)" rc=$?
branch="$(sed -n 's|^journey-ci: pushed origin refs/heads/\(ci-tmp/[^ ]*\) .*|\1|p' <<<"$out")"
[ "$rc" = 0 ] && ok 'linux: a green run exits 0' || bad 'linux: exit' "expected 0, actual $rc" "$out"
[[ "$branch" =~ ^ci-tmp/develop-[0-9]{8}T[0-9]{6}Z-[0-9a-f]{8}$ ]] &&
  ok "linux: the branch is ci-tmp/{branch}-{stamp}-{id}" || bad 'linux: branch name' "actual: $branch" "$out"
dispatched linux "$S" 'journey (linux),gate (e2e)'
snap="$(cut -d' ' -f1 "$S/hook.log")"
[ "$(cut -d' ' -f2 "$S/hook.log")" = "refs/heads/$branch" ] && [ "$(wc -l <"$S/hook.log")" = 1 ] &&
  ok 'linux: the leak gate scanned the one snapshot push' || bad 'linux: hook log' "$(cat "$S/hook.log")"
files="$(git -C "$S/origin.git" ls-tree -r --name-only "$snap" | tr '\n' ' ')"
[ "$files" = '.githooks/pre-push .gitignore .gitmodules a.txt infra/fence/journey/ci-run.sh infra/fence/journey/run.sh new.txt plugins/plug scripts/leak-check.sh ' ] &&
  ok 'linux: the snapshot tree holds the edit and the untracked file, not the deleted or ignored ones' ||
  bad 'linux: snapshot files' "actual: $files"
[ "$(git -C "$S/origin.git" show "$snap:a.txt")" = edited ] && [ "$(git -C "$S/origin.git" rev-parse "$snap^")" = "$(git -C "$S/main" rev-parse HEAD)" ] &&
  ok "linux: the snapshot's a.txt is the disk's and its parent is HEAD" || bad 'linux: snapshot content or parent'
link="$(git -C "$S/origin.git" ls-tree "$snap" plugins/plug | awk '{print $3}')"
[ "$(git -C "$S/plug.git" rev-parse "$link^")" = "$plug_head" ] &&
  [ "$(git -C "$S/plug.git" ls-tree -r --name-only "$link" | tr '\n' ' ')" = 'p.txt q.txt untracked.txt ' ] &&
  ok "linux: the plugin gitlink is a snapshot of the plugin's disk over its unpushed commit" ||
  bad 'linux: plugin snapshot' "link $link, plugin HEAD $plug_head"
[ "$(cat "$S/plugin-scan.log")" = "$EMPTY_TREE $link origin $S/main/.git/modules/plugins/plug $S/main/plugins/plug $S/main/scripts/leak-terms.txt" ] &&
  ok "linux: the leak gate scanned the plugin's one leaving commit, from the empty tree, against origin" ||
  bad 'linux: plugin scan log' "link $link" "$(cat "$S/plugin-scan.log")"
want="$(printf 'origin.git %s refs/heads/%s\nplug.git %s refs/heads/%s' "$snap" "$branch" "$link" "$branch")"
[ "$(cat "$S/dispatch-refs")" = "$want" ] && ok 'linux: at the dispatch both origins hold the temporary branch' ||
  bad 'linux: refs at dispatch' "expected: $want" "actual: $(cat "$S/dispatch-refs")"
[ "$(state "$S/main")" = "$before" ] && [ "$(state "$S/main/plugins/plug")" = "$before_plug" ] &&
  ok "linux: the caller's HEAD, index, status, stash and refs are unchanged" ||
  bad 'linux: caller state changed' "$(diff <(echo "$before") <(state "$S/main"))"
jobs="$(grep '^journey-ci: job ' <<<"$out")"
[ "$jobs" = "journey-ci: job plan               success    812s
journey-ci: job gate (e2e)         success    812s
journey-ci: job journey (linux)    success    812s
journey-ci: job ci                 success    812s" ] && ok 'linux: one line per job that ran: name, result, duration' || bad 'linux: job lines' "$jobs"
[ "$(cat "$S/main/tmp/journey/ci/4242/journey-linux/summary.md")" = profile ] &&
  ok 'linux: the journey artifacts land in tmp/journey/ci/{run id}' || bad 'linux: artifacts' "$out"
grep -qx "journey-ci: run https://github.com/acme/widget/actions/runs/4242" <<<"$out" && ok 'linux: the run URL is printed' || bad 'linux: run url' "$out"
gone linux "$S"

# --ci-mac, the journey red: exit 1, its failed-step log kept and its tail printed.
: >"$S/hook.log"
view "$S/view-red" "plan=success" "macos (unit)=success" "macos (e2e)=success" "journey (macos)=failure" "ci=failure"
out="$(FAKE_GH_VIEW="$S/view-red" bash "$S/main/infra/fence/journey/run.sh" --ci-mac 2>&1)" rc=$?
[ "$rc" = 1 ] && ok 'mac: a red job exits 1' || bad 'mac: exit' "expected 1, actual $rc" "$out"
dispatched mac "$S" 'journey (macos),macos (unit),macos (e2e)'
log="$S/main/tmp/journey/ci/4242/journey-macos.log"
grep -qx "journey-ci: journey (macos) failed; its failed-step log $log (1 lines) ends:" <<<"$out" &&
  grep -qx -- '--- FAIL: Journey/install' <<<"$out" && grep -qx 'journey-ci: FAIL journey (macos): failure' <<<"$out" &&
  ok 'mac: the failed job is named, its log path absolute and its tail printed' || bad 'mac: failure report' "$out"
gone 'mac after a failed run' "$S"

# Both flags: one run with all five entries.
view "$S/view-both" "journey (linux)=success" "gate (e2e)=success" "journey (macos)=success" "macos (unit)=success" "macos (e2e)=success"
out="$(FAKE_GH_VIEW="$S/view-both" bash "$S/main/infra/fence/journey/run.sh" --ci-mac --ci-linux 2>&1)" rc=$?
[ "$rc" = 0 ] && ok 'both: exit 0' || bad 'both: exit' "$rc" "$out"
dispatched both "$S" 'journey (linux),gate (e2e),journey (macos),macos (unit),macos (e2e)'
gone both "$S"

# An interrupt cancels the run and deletes the branches; so does the wait bound.
: >"$S/gh.log"
set -m
FAKE_GH_HANG=1 JOURNEY_CI_POLL_S=1 bash "$S/main/infra/fence/journey/run.sh" --ci-linux >"$S/int.out" 2>&1 &
pid=$!
set +m
for _ in $(seq 100); do grep -q '^run view' "$S/gh.log" && break; sleep 0.2; done # POLL-STEP: the first status poll
kill -INT "$pid"
wait "$pid"
rc=$?
[ "$rc" = 130 ] && grep -qx 'run cancel 4242 --repo acme/widget' "$S/gh.log" &&
  ok 'interrupt: exit 130 and the run is cancelled' || bad 'interrupt' "rc $rc" "$(cat "$S/int.out" "$S/gh.log")"
gone interrupt "$S"
: >"$S/gh.log"
out="$(FAKE_GH_HANG=1 JOURNEY_CI_WAIT_S=0 bash "$S/main/infra/fence/journey/run.sh" --ci-linux 2>&1)" rc=$?
[ "$rc" = 1 ] && grep -qx 'journey-ci: FAIL run 4242 not completed after 0s' <<<"$out" &&
  grep -qx 'run cancel 4242 --repo acme/widget' "$S/gh.log" && ok 'timeout: exit 1 and the run is cancelled' ||
  bad timeout "rc $rc" "$out"
gone timeout "$S"

# A run URL the dispatch does not print is found by the branch.
out="$(FAKE_GH_NO_URL=1 FAKE_GH_VIEW="$S/view-ok" bash "$S/main/infra/fence/journey/run.sh" --ci-linux 2>&1)" rc=$?
[ "$rc" = 0 ] && grep -q '^run list --repo acme/widget --workflow ci.yml --branch ci-tmp/' "$S/gh.log" &&
  ok 'no url: the run is found by its branch' || bad 'no url' "rc $rc" "$out"

# Refusals before anything leaves: a red leak gate, an unarmed gate, no private terms, gh not authenticated, a stray flag.
refused() { # refused <label> <expected line> <env…> -- <args…>
  local label="$1" want="$2" o r
  shift 2
  : >"$S/hook.log"
  o="$(env "$@" 2>&1)" r=$?
  if [ "$r" = 2 ] && [ "$(grep 'REFUSED' <<<"$o" | sed -E 's|ci-tmp/[^ ]+|ci-tmp/BRANCH|; s|[0-9a-f]{40}|SNAP|g')" = "$want" ] && [ -z "$(tmp_refs "$S/origin.git")$(tmp_refs "$S/plug.git")" ]; then
    ok "refusal: $label exits 2 and pushes nothing"
  else bad "refusal: $label" "expected rc 2 and: $want" "actual rc $r: $o"; fi
}
run_sh=(bash "$S/main/infra/fence/journey/run.sh")
refused 'a red leak gate' "journey-ci: REFUSED the push of SNAP to origin ci-tmp/BRANCH was refused (above: the leak gate or origin)" \
  FAKE_LEAK=red "${run_sh[@]}" --ci-linux
refused 'a red leak gate on a plugin' "journey-ci: REFUSED the leak gate refused plugin plugins/plug's SNAP (above); nothing was pushed" \
  FAKE_PLUGIN_LEAK=red "${run_sh[@]}" --ci-linux
[ ! -s "$S/hook.log" ] && ok "refusal: a red plugin gate stops before the parent's push" ||
  bad 'refusal: the parent was pushed before the plugin gate' "$(cat "$S/hook.log")"
refused 'gh not authenticated' 'journey-ci: REFUSED gh is not authenticated (gh auth login)' FAKE_GH_AUTH=1 "${run_sh[@]}" --ci-mac
refused 'a go test flag' "journey-ci: REFUSED '-run': with --ci-linux or --ci-mac only CI flags are taken (CI runs the whole suite; go test flags and --keep are local)" \
  "${run_sh[@]}" --ci-linux -run x
printf '!structural-only\n' >"$S/terms-structural"
refused 'structural-only terms' "journey-ci: REFUSED the leak gate has no private terms: $S/terms-structural is structural-only" \
  LEAK_TERMS="$S/terms-structural" "${run_sh[@]}" --ci-linux
git -C "$S/main" config --unset core.hooksPath
refused 'an unarmed leak gate' "journey-ci: REFUSED the leak gate is not armed: core.hooksPath is 'unset', not .githooks, so a push would skip scripts/leak-check.sh" \
  "${run_sh[@]}" --ci-linux
git -C "$S/main" config core.hooksPath .githooks

# A worktree: its plugin is not populated and pinned to a commit only the main repository's modules/ holds; the
# terms come from the main checkout; a clean worktree pushes its HEAD itself.
W="$T/w"
fixture "$W"
echo pin >"$W/main/plugins/plug/pin.txt" && git -C "$W/main/plugins/plug" add pin.txt && git -C "$W/main/plugins/plug" commit -qm pin &&
  git -C "$W/main" add plugins/plug && git -C "$W/main" commit -qm pin && git -C "$W/main" worktree add -q "$W/wt" -b feature ||
  { bad 'worktree fixture'; shtest_end; exit 1; }
pin="$(git -C "$W/main/plugins/plug" rev-parse HEAD)"
view "$W/view" "journey (linux)=success" "gate (e2e)=success"
out="$(FAKE_GH_VIEW="$W/view" bash "$W/wt/infra/fence/journey/run.sh" --ci-linux 2>&1)" rc=$?
[ "$rc" = 0 ] && ok 'worktree: exit 0' || bad 'worktree: exit' "$rc" "$out"
[ "$(cat "$W/hook.log")" = "$(git -C "$W/wt" rev-parse HEAD) refs/heads/$(sed -n 's|^journey-ci: pushed origin refs/heads/\(ci-tmp/[^ ]*\) .*|\1|p' <<<"$out") $W/main/scripts/leak-terms.txt" ] &&
  ok "worktree: a clean checkout pushes HEAD itself, the gate reading the main checkout's terms" || bad 'worktree: hook log' "$(cat "$W/hook.log")"
[ "$(cat "$W/plugin-scan.log")" = "$EMPTY_TREE $pin origin $W/main/.git/modules/plugins/plug $W/wt/plugins/plug $W/main/scripts/leak-terms.txt" ] &&
  ok "worktree: the unpopulated plugin's pin is scanned from the main repository's modules/" ||
  bad 'worktree: plugin scan log' "pin $pin" "$(cat "$W/plugin-scan.log")"
[[ "$(grep '^plug.git' "$W/dispatch-refs")" == "plug.git $pin refs/heads/ci-tmp/feature-"* ]] &&
  ok "worktree: the unpopulated plugin's local-only pin is pushed from the main repository's modules/" ||
  bad 'worktree: plugin push' "pin $pin" "$(cat "$W/dispatch-refs")"
gone worktree "$W"

# ci.yml's plan job turns each dispatched `only` into exactly those jobs and entries.
plan() { # plan <only>: the plan step's outputs, one per line
  python3 - "$REPO_ROOT/.github/workflows/ci.yml" "$1" "$T/plan.out" <<'EOF'
import os, subprocess, sys, yaml
step = yaml.safe_load(open(sys.argv[1]))["jobs"]["plan"]["steps"][0]
env = dict(os.environ, **{k: v for k, v in step["env"].items() if not v.startswith("${{")})
env.update(EXCLUDE="", ONLY=sys.argv[2], GITHUB_OUTPUT=sys.argv[3])
open(sys.argv[3], "w").close()
r = subprocess.run(["bash", "-c", step["run"]], env=env, capture_output=True, text=True)
if r.returncode:
    sys.exit("plan step failed: " + r.stdout + r.stderr)
EOF
  cat "$T/plan.out"
}
gate_all='[{"slice":"unit","only":"pfm\\.unit"},{"slice":"e2e","only":"pfm\\.e2e"},{"slice":"race","only":"pfm\\.race"},{"slice":"mirrors","only":"templates\\.mirrors"},{"slice":"rest","skip":"pfm\\.(unit|e2e|race)|templates\\.mirrors"}]'
journey_all='[{"os":"linux","runner":"ubuntu-latest"},{"os":"macos","runner":"macos-15"}]'
while IFS='|' read -r label only want; do
  got="$(plan "$only" 2>&1 | paste -sd ' ' | sed 's/ *$//')"
  want="${want//GATE_ALL/"$gate_all"}" want="${want//JOURNEY_ALL/"$journey_all"}"
  [ "$got" = "$want" ] && ok "ci.yml plan: $label" || bad "ci.yml plan: $label" "expected: $want" "actual: $got"
done <<'ROWS'
a pull request runs every entry||json=[] gate=GATE_ALL macos=[{"part":"unit"},{"part":"e2e"}] journey=JOURNEY_ALL
--ci-linux|journey (linux),gate (e2e)|json=["build","fresh-install","macos","release","scan"] gate=[{"slice":"e2e","only":"pfm\\.e2e"}] macos=[{"part":"unit"},{"part":"e2e"}] journey=[{"os":"linux","runner":"ubuntu-latest"}]
--ci-mac|journey (macos),macos (unit),macos (e2e)|json=["build","fresh-install","gate","release","scan"] gate=GATE_ALL macos=[{"part":"unit"},{"part":"e2e"}] journey=[{"os":"macos","runner":"macos-15"}]
both flags|journey (linux),gate (e2e),journey (macos),macos (unit),macos (e2e)|json=["build","fresh-install","release","scan"] gate=[{"slice":"e2e","only":"pfm\\.e2e"}] macos=[{"part":"unit"},{"part":"e2e"}] journey=JOURNEY_ALL
an unknown entry fails|journey (windows)|plan step failed: ::error::only names no job or matrix entry: journey (windows)
ROWS

shtest_end
