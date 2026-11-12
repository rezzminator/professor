#!/usr/bin/env bash
# ci-run.sh — pfm's journey and installer end-to-end suites of one OS, or both, on GitHub Actions (ci.yml) over
# the checkout exactly as it is on disk, uncommitted work and unpushed plugin commits included; no local machine.
#   bash infra/fence/journey/run.sh --ci-linux|--ci-mac [--ci-linux|--ci-mac]   (run.sh hands its flags here)
# --ci-linux runs `journey (linux)` and `gate (e2e)`, --ci-mac `journey (macos)`, `macos (unit)` and
# `macos (e2e)`, both flags one run with all five; ci.yml's dispatch input `only` skips every other job.
# The snapshot: one commit whose tree is the disk (tracked edits, deletions, untracked files not ignored), parent
# HEAD, built with a throwaway index; a clean checkout is HEAD itself. Each plugin (a submodule) records the
# commit on its disk the same way, or its recorded gitlink when not populated (its objects then in the main
# repository's modules/ directory). The caller's branch, HEAD, index, working tree and stash stay untouched, and
# no local ref is made. It pushes the snapshot with a plain `git push` to
# origin's ci-tmp/{branch}-{UTC stamp}-{short id}, so .githooks/pre-push runs the leak gate on it, then each
# plugin commit its origin holds no ref to under the same branch name in that origin, each scanned by
# scripts/leak-check.sh before anything is pushed; it starts the run, prints
# its URL, waits for it (bound JOURNEY_CI_WAIT_S, an hour), prints one line per job (name, result, duration)
# and keeps each failed job's failed-step log and the run's journey artifacts in {checkout}/tmp/journey/ci/{run
# id}/. Every pushed branch is deleted on any exit, an interrupt or a timeout first cancelling the run; a branch
# that would not go is named with the command that deletes it.
# A push is publication: a person runs this, or an agent on the owner's ask in that turn.
# BROKEN STATE: `journey-ci: REFUSED {cause}` and exit 2 before anything is dispatched (no gh, gh not
# authenticated, origin not on GitHub, the leak gate not armed, with no terms or red on a plugin, a push refused); exit 1 when
# a selected job did not succeed or the run outlived the bound; 130 on an interrupt.
set -uo pipefail
HERE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
ROOT="$(cd -- "$HERE/../../.." && pwd -P)"
POLL_S="${JOURNEY_CI_POLL_S:-30}"
WAIT_S="${JOURNEY_CI_WAIT_S:-3600}"

say() { echo "journey-ci: $*"; }
refuse() { echo "journey-ci: REFUSED $*" >&2; exit 2; }

linux=0 mac=0
for arg in "$@"; do
  case "$arg" in
    --ci-linux) linux=1 ;;
    --ci-mac) mac=1 ;;
    *) refuse "'$arg': with --ci-linux or --ci-mac only CI flags are taken (CI runs the whole suite; go test flags and --keep are local)" ;;
  esac
done
entries=()
[ "$linux" = 0 ] || entries+=("journey (linux)" "gate (e2e)")
[ "$mac" = 0 ] || entries+=("journey (macos)" "macos (unit)" "macos (e2e)")
[ "${#entries[@]}" -gt 0 ] || refuse "usage: run.sh --ci-linux|--ci-mac [--ci-linux|--ci-mac]"
only="$(IFS=,; printf '%s' "${entries[*]}")"

# 1. What a dispatch needs, checked before anything is pushed.
command -v gh >/dev/null || refuse "gh is not installed (https://cli.github.com)"
command -v jq >/dev/null || refuse "jq is not installed"
gh auth status >/dev/null 2>&1 || refuse "gh is not authenticated (gh auth login)"
url="$(git -C "$ROOT" config --get remote.origin.url)" || refuse "$ROOT has no origin remote"
case "$url" in
  https://github.com/*) repo="${url#https://github.com/}" ;;
  git@github.com:*) repo="${url#git@github.com:}" ;;
  ssh://git@github.com/*) repo="${url#ssh://git@github.com/}" ;;
  *) refuse "origin $url is not a GitHub repository" ;;
esac
repo="${repo%/}" repo="${repo%.git}"
[[ "$repo" =~ ^[^/]+/[^/]+$ ]] || refuse "origin $url names no {owner}/{repository}"
common="$(git -C "$ROOT" rev-parse --path-format=absolute --git-common-dir)" || refuse "$ROOT is not a git checkout"
# The leak gate: a plain push runs .githooks/pre-push only when core.hooksPath points at it, and a worktree holds
# no private terms file of its own, so LEAK_TERMS points at the main checkout's.
hooks="$(git -C "$ROOT" config --get core.hooksPath || true)"
case "$hooks" in .githooks | "$ROOT/.githooks") ;; *) refuse "the leak gate is not armed: core.hooksPath is '${hooks:-unset}', not .githooks, so a push would skip scripts/leak-check.sh" ;; esac
[ -x "$ROOT/.githooks/pre-push" ] || refuse "the leak gate is not armed: $ROOT/.githooks/pre-push is missing or not executable"
export LEAK_TERMS="${LEAK_TERMS:-$(dirname "$common")/scripts/leak-terms.txt}"
[ -f "$LEAK_TERMS" ] || refuse "the leak gate has no terms: $LEAK_TERMS does not exist (set LEAK_TERMS)"
grep -qx '!structural-only' "$LEAK_TERMS" && refuse "the leak gate has no private terms: $LEAK_TERMS is structural-only"
grep -qvE '^[[:space:]]*(#|!|$)' "$LEAK_TERMS" || refuse "the leak gate has no terms: $LEAK_TERMS holds none"

SCRATCH="$(mktemp -d "${TMPDIR:-/tmp}/journey-ci.XXXXXX")" || refuse "cannot create a scratch directory"
pushed=() # one "{-C|--git-dir}\t{path}\t{ref}" per remote branch made, so cleanup deletes exactly those
run_id='' completed=0
# cleanup cancels a run that has not completed, deletes every branch this run pushed and removes the scratch.
cleanup() {
  local rc=$? line opt dir ref
  set +e
  if [ -n "$run_id" ] && [ "$completed" = 0 ]; then
    if gh run cancel "$run_id" --repo "$repo" >/dev/null 2>&1; then say "cancelled run $run_id"
    else say "could not cancel run $run_id: gh run cancel $run_id --repo $repo" >&2; fi
  fi
  for line in "${pushed[@]}"; do
    IFS=$'\t' read -r opt dir ref <<<"$line"
    if git "$opt" "$dir" push -q origin --delete "$ref" >/dev/null 2>&1; then say "deleted origin $ref ($dir)"
    elif git "$opt" "$dir" ls-remote --exit-code origin "$ref" >/dev/null 2>&1; [ $? = 2 ]; then :
    else say "could not delete origin $ref: git $opt $(printf '%q' "$dir") push origin --delete $ref" >&2; rc=1; fi
  done
  rm -rf "$SCRATCH"
  exit "$rc"
}
trap cleanup EXIT
trap 'say interrupted; exit 130' INT TERM HUP

# snapshot <dir> [path=sha…] prints the commit whose tree is <dir>'s work tree as on disk, each named gitlink set
# to its sha; HEAD itself when that tree is HEAD's. The caller's index is copied, never written.
snapshot() {
  local dir="$1" idx head tree pair real
  shift
  head="$(git -C "$dir" rev-parse --verify -q HEAD)" || { echo "journey-ci: $dir has no HEAD commit" >&2; return 1; }
  idx="$(mktemp "$SCRATCH/index.XXXXXX")" || return 1
  real="$(git -C "$dir" rev-parse --path-format=absolute --git-path index)" || return 1
  if [ -f "$real" ]; then cp "$real" "$idx" || return 1; else rm -f "$idx"; fi
  GIT_INDEX_FILE="$idx" git -C "$dir" add -A -- . || return 1
  for pair in "$@"; do
    GIT_INDEX_FILE="$idx" git -C "$dir" update-index --cacheinfo "160000,${pair#*=},${pair%%=*}" || return 1
  done
  tree="$(GIT_INDEX_FILE="$idx" git -C "$dir" write-tree)" || return 1
  if [ "$tree" = "$(git -C "$dir" rev-parse "$head^{tree}")" ]; then echo "$head"; return 0; fi
  git -C "$dir" commit-tree "$tree" -p "$head" -m "journey-ci: snapshot of the checkout on disk at $STAMP"
}

# 2. The snapshot: each plugin's commit first, then the parent's tree with those gitlinks.
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
links=() plugin_push=()
while read -r key path; do
  [ -n "$path" ] || continue
  name="${key#submodule.}" name="${name%.path}"
  sha="$(git -C "$ROOT" ls-files -s -- "$path" | awk '$1 == "160000" { print $2 }')"
  [ -n "$sha" ] || continue # a .gitmodules entry whose gitlink the checkout no longer holds
  if [ "$(git -C "$ROOT/$path" rev-parse --path-format=absolute --show-toplevel 2>/dev/null)" = "$ROOT/$path" ]; then
    opt=-C dir="$ROOT/$path"
    sha="$(snapshot "$ROOT/$path")" || refuse "cannot snapshot the plugin checkout $path (above)"
  else
    opt=--git-dir dir="$common/modules/$name"
    git "$opt" "$dir" cat-file -e "$sha^{commit}" 2>/dev/null ||
      refuse "plugin $path is not populated and its recorded commit $sha is not in $common/modules/$name"
  fi
  links+=("$path=$sha")
  [ -n "$(git "$opt" "$dir" for-each-ref --contains "$sha" --format=x refs/remotes/origin)" ] ||
    plugin_push+=("$opt"$'\t'"$dir"$'\t'"$sha"$'\t'"$path")
done < <(git -C "$ROOT" config -f "$ROOT/.gitmodules" --get-regexp '^submodule\..*\.path$' 2>/dev/null)
snap="$(snapshot "$ROOT" "${links[@]}")" || refuse "cannot snapshot $ROOT (above)"
current="$(git -C "$ROOT" symbolic-ref --short -q HEAD || echo detached)"
BRANCH="ci-tmp/$current-$STAMP-${snap:0:8}"
git check-ref-format "refs/heads/$BRANCH" || refuse "the temporary branch name $BRANCH is not a valid ref"
say "snapshot $snap of $ROOT ($current) for: $only"

# 3. The plugins' leak gate: a plugin push runs no hook of this repository, so every plugin commit about to
# leave is scanned here by the gate the parent's push runs, before anything is pushed.
empty="$(git -C "$ROOT" hash-object -t tree /dev/null)"
for p in "${plugin_push[@]}"; do
  IFS=$'\t' read -r opt dir sha path <<<"$p"
  PFM_DEV_REPO_GIT_DIR="$(git "$opt" "$dir" rev-parse --absolute-git-dir)" PFM_DEV_REPO_WORK_TREE="$ROOT/$path" \
    "$ROOT/scripts/leak-check.sh" --range "$empty" "$sha" --remote origin ||
    refuse "the leak gate refused plugin $path's $sha (above); nothing was pushed"
done

# 4. The pushes: the parent first, through .githooks/pre-push.
# Each branch is recorded before its push: a signal after origin took the ref still deletes it.
pushed+=("-C"$'\t'"$ROOT"$'\t'"refs/heads/$BRANCH")
git -C "$ROOT" push origin "$snap:refs/heads/$BRANCH" ||
  refuse "the push of $snap to origin $BRANCH was refused (above: the leak gate or origin)"
say "pushed origin refs/heads/$BRANCH ($url)"
for p in "${plugin_push[@]}"; do
  IFS=$'\t' read -r opt dir sha path <<<"$p"
  pushed+=("$opt"$'\t'"$dir"$'\t'"refs/heads/$BRANCH")
  git "$opt" "$dir" push origin "$sha:refs/heads/$BRANCH" ||
    refuse "the push of plugin $path's $sha to its origin $BRANCH was refused (above)"
  say "pushed $path origin refs/heads/$BRANCH ($(git "$opt" "$dir" config --get remote.origin.url))"
done

# 5. The run.
out="$(gh workflow run ci.yml --repo "$repo" --ref "$BRANCH" -f only="$only" 2>&1)" ||
  refuse "gh workflow run refused the dispatch: $out"
run_url="$(grep -o 'https://[^ ]*/actions/runs/[0-9]*' <<<"$out" | tail -n 1)"
deadline=$((SECONDS + 120))
while [ -z "$run_url" ]; do # an older gh prints no run URL: the branch is this run's alone
  run_url="$(gh run list --repo "$repo" --workflow ci.yml --branch "$BRANCH" --event workflow_dispatch \
    --json url --jq '.[0].url // empty' 2>/dev/null)"
  [ -n "$run_url" ] && break
  [ "$SECONDS" -lt "$deadline" ] || refuse "the dispatch on $BRANCH named no run within 120s: $out"
  sleep "$POLL_S" & wait $! # POLL-STEP: GitHub's run list, bounded by the deadline above
done
run_id="${run_url##*/}"
say "run $run_url"

deadline=$((SECONDS + WAIT_S))
while :; do
  if view="$(gh run view "$run_id" --repo "$repo" --json status,conclusion,jobs 2>&1)" &&
    [ "$(jq -r .status <<<"$view")" = completed ]; then
    completed=1
    break
  fi
  [ -n "$(jq -r '.status // empty' <<<"$view" 2>/dev/null)" ] || say "warning: gh run view $run_id failed: $view" >&2
  [ "$SECONDS" -lt "$deadline" ] || { say "FAIL run $run_id not completed after ${WAIT_S}s"; exit 1; }
  sleep "$POLL_S" & wait $! # POLL-STEP: the run's own status, bounded by WAIT_S
done

# 6. The verdict: a line per job that ran, the failed steps' logs, the journey artifacts.
jq -r '.jobs[] | select(.conclusion != "skipped")
  | [.name, .conclusion, ((try ((.completedAt | fromdate) - (.startedAt | fromdate)) catch null) // "?" | tostring) + "s"] | @tsv' <<<"$view" |
  while IFS=$'\t' read -r name result took; do printf 'journey-ci: job %-18s %-10s %s\n' "$name" "$result" "$took"; done
OUT="$ROOT/tmp/journey/ci/$run_id"
mkdir -p "$OUT" || { say "FAIL cannot create $OUT"; exit 1; }
rc=0
for e in "${entries[@]}"; do
  got="$(jq -r --arg n "$e" '[.jobs[] | select(.name == $n) | .conclusion] | first // "absent"' <<<"$view")"
  [ "$got" = success ] || { say "FAIL $e: $got"; rc=1; }
done
while IFS=$'\t' read -r job name; do
  [ -n "$job" ] || continue
  log="$OUT/$(tr -c 'a-z0-9\n' '-' <<<"$name" | tr -s '-' | sed 's/-$//').log"
  if gh run view "$run_id" --repo "$repo" --job "$job" --log-failed >"$log" 2>&1; then
    say "$name failed; its failed-step log $log ($(wc -l <"$log") lines) ends:"
    cut -f3- "$log" | sed 's/^[0-9T:.-]*Z //' | grep -vE '^\s*$|^go: downloading |^##\[(group|endgroup)\]' | tail -n 70 | cut -c1-250
  else
    say "$name failed; its failed-step log could not be read (gh: $(tail -n 1 "$log"))"
  fi
done < <(jq -r '.jobs[] | select(.conclusion == "failure") | [.databaseId, .name] | @tsv' <<<"$view")
if gh run download "$run_id" --repo "$repo" --dir "$OUT" --pattern 'journey-*' >/dev/null 2>"$SCRATCH/download"; then
  say "artifacts $OUT"
else
  say "warning: the run's journey artifacts could not be downloaded into $OUT: $(tail -n 1 "$SCRATCH/download")" >&2
fi
[ "$rc" = 0 ] && say "PASS every selected job succeeded" || say "FAIL not every selected job succeeded"
exit "$rc"
