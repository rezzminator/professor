#!/usr/bin/env bash
# Self-test for scripts/install-downgrade-guard.sh: it must let an installed
# binary whose commit HEAD still contains pass (ok), refuse one built from a
# commit HEAD does not contain — whether a plain descendant-mismatch or a
# sibling branch — refuse one from a revision this repo has never heard of,
# let FORCE=1 override any refusal, and treat a missing binary or one with no
# vcs.revision as "could not check", never as ok or as a failure.
#
# It builds real Go binaries with `go build -buildvcs=true` at three commits
# in a throwaway git+Go fixture (never this repo), so every case here exists
# in the real world: the guard's own inputs, not a mock of them.
#
# Harness style follows scripts/arch-c24_test.sh, the sibling shell test.
set -uo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
SUT="$ROOT/scripts/install-downgrade-guard.sh"
SHTEST_TAG=pfm-install-downgrade-guard-test
# shellcheck source=/dev/null
source "$ROOT/../scripts/shtest.sh"

GOENV=(GOCACHE="$(go env GOCACHE)" GOTOOLCHAIN=local CGO_ENABLED=0)

gitc() { git -C "$1" -c user.email=t@example.invalid -c user.name=t "${@:2}"; }

# mkrepo <dir>: a minimal git+Go module, one file, ready to commit.
mkrepo() {
  local dir=$1
  mkdir -p "$dir"
  cat > "$dir/go.mod" <<'EOF'
module fixture

go 1.21
EOF
  printf 'package main\n\nfunc main() {}\n' > "$dir/main.go"
  git -C "$dir" init -q || return 1
}

buildbin() {
  # buildbin <dir> <outfile>: builds the checked-out commit's tree, vcs-stamped.
  local dir=$1 out=$2
  ( cd "$dir" && env "${GOENV[@]}" go build -buildvcs=true -o "$out" . )
}

REPO="$T/repo"
BIN="$T/bin"
mkdir -p "$BIN"

if ! mkrepo "$REPO"; then
  bad "install-guard: could not build the git+Go fixture (are git and go available?)"
  shtest_end
  exit $?
fi

gitc "$REPO" add -A
gitc "$REPO" commit -q -m A
SHA_A="$(git -C "$REPO" rev-parse HEAD)"
BR_MAIN="$(git -C "$REPO" symbolic-ref --short HEAD)"

printf 'package main\n\nfunc main() { _ = 1 }\n' > "$REPO/main.go"
gitc "$REPO" commit -q -am B
SHA_B="$(git -C "$REPO" rev-parse HEAD)"

gitc "$REPO" checkout -q -b side "$SHA_A"
printf 'package main\n\nfunc main() { _ = 2 }\n' > "$REPO/main.go"
gitc "$REPO" commit -q -am C
SHA_C="$(git -C "$REPO" rev-parse HEAD)"

# Build a real binary at each commit, returning to BR_MAIN (HEAD=B) after
# each excursion so the repo's steady state matches case (a) and (c).
gitc "$REPO" checkout -q "$SHA_A"
buildbin "$REPO" "$BIN/A"
gitc "$REPO" checkout -q "$BR_MAIN"
buildbin "$REPO" "$BIN/B"
gitc "$REPO" checkout -q side
buildbin "$REPO" "$BIN/C"
gitc "$REPO" checkout -q "$BR_MAIN"

# (f) a binary with no vcs.revision at all.
( cd "$REPO" && env "${GOENV[@]}" go build -buildvcs=false -o "$BIN/F" . )

# (g) a binary built in a second, unrelated temp repo — its commit exists,
# just not in $REPO.
OTHER="$T/other"
if mkrepo "$OTHER"; then
  gitc "$OTHER" add -A
  gitc "$OTHER" commit -q -m other
  buildbin "$OTHER" "$BIN/G"
fi

# The fixtures below are their own git repos, unrelated to this repo, so the
# fence's mounted-repo overrides (repo_git's PFM_DEV_REPO_GIT_DIR /
# PFM_DEV_REPO_WORK_TREE, which would otherwise point every git call at the
# outer professor tree) are dropped — arch-c24_test.sh's c24() explains why.
run() { env -u PFM_DEV_REPO_GIT_DIR -u PFM_DEV_REPO_WORK_TREE bash "$SUT" "$@" 2>&1; }
run_forced() { env -u PFM_DEV_REPO_GIT_DIR -u PFM_DEV_REPO_WORK_TREE FORCE=1 bash "$SUT" "$@" 2>&1; }

# ---- (a) installed=A, HEAD=B -> ok, exit 0 -----------------------------------
out="$(run "$BIN/A" "$REPO")"; rc=$?
if [[ "$out" == *"install-guard: ok"* ]] && [ $rc -eq 0 ]; then
  ok "install-guard: (a) installed A is an ancestor of HEAD B — ok, exit 0"
else
  bad "install-guard: (a) expected ok, exit 0" "$out (rc $rc)"
fi

# ---- (b) installed=B, HEAD=A -> REFUSED, exit 1 ------------------------------
gitc "$REPO" checkout -q "$SHA_A"
out="$(run "$BIN/B" "$REPO")"; rc=$?
if [[ "$out" == *"REFUSED"* ]] && [ $rc -eq 1 ]; then
  ok "install-guard: (b) installed B is not in HEAD A's history — REFUSED, exit 1"
else
  bad "install-guard: (b) expected REFUSED, exit 1" "$out (rc $rc)"
fi

# ---- (d) case (b) with FORCE=1 -> FORCED line, exit 0 ------------------------
out="$(run_forced "$BIN/B" "$REPO")"; rc=$?
if [[ "$out" == *"FORCED past refusal"* ]] && [ $rc -eq 0 ]; then
  ok "install-guard: (d) FORCE=1 overrides the (b) refusal — FORCED, exit 0"
else
  bad "install-guard: (d) expected FORCED, exit 0" "$out (rc $rc)"
fi
gitc "$REPO" checkout -q "$BR_MAIN"

# ---- (c) installed=C, HEAD=B -> REFUSED, exit 1 (sibling branch) ------------
out="$(run "$BIN/C" "$REPO")"; rc=$?
if [[ "$out" == *"REFUSED"* ]] && [ $rc -eq 1 ]; then
  ok "install-guard: (c) installed C is a sibling of HEAD B, not an ancestor — REFUSED, exit 1"
else
  bad "install-guard: (c) expected REFUSED, exit 1" "$out (rc $rc)"
fi

# ---- (e) no installed binary -> exit 0, "nothing to protect" ----------------
out="$(run "$BIN/does-not-exist" "$REPO")"; rc=$?
if [[ "$out" == *"nothing to protect"* ]] && [ $rc -eq 0 ]; then
  ok "install-guard: (e) no installed binary — exit 0, nothing to protect"
else
  bad "install-guard: (e) expected exit 0, nothing to protect" "$out (rc $rc)"
fi

# ---- (f) a -buildvcs=false binary -> exit 0, cannot verify -------------------
out="$(run "$BIN/F" "$REPO")"; rc=$?
if [[ "$out" == *"cannot verify"* ]] && [ $rc -eq 0 ]; then
  ok "install-guard: (f) a binary with no vcs.revision — exit 0, cannot verify"
else
  bad "install-guard: (f) expected exit 0, cannot verify" "$out (rc $rc)"
fi

# ---- (g) a binary from an unrelated repo -> REFUSED as unknown, exit 1 ------
if [ -x "$BIN/G" ]; then
  out="$(run "$BIN/G" "$REPO")"; rc=$?
  if [[ "$out" == *"REFUSED"* ]] && [ $rc -eq 1 ]; then
    ok "install-guard: (g) installed binary from an unrelated repo — REFUSED as unknown, exit 1"
  else
    bad "install-guard: (g) expected REFUSED, exit 1" "$out (rc $rc)"
  fi
else
  bad "install-guard: (g) could not build the unrelated-repo fixture binary"
fi

# Rebase equivalents still carry every installed change, but new commit IDs.
gitc "$REPO" checkout -q -b installed-series "$SHA_B"
printf 'package main\n\nfunc main() { _ = 1; _ = 3 }\n' > "$REPO/main.go"
gitc "$REPO" commit -q -am B2
SHA_B2="$(git -C "$REPO" rev-parse HEAD)"
buildbin "$REPO" "$BIN/B2"
gitc "$REPO" checkout -q -b rebased-series "$SHA_A"
printf 'upstream\n' > "$REPO/upstream.txt"
gitc "$REPO" add upstream.txt
gitc "$REPO" commit -q -m upstream
gitc "$REPO" cherry-pick "$SHA_B" "$SHA_B2" >/dev/null
out="$(run "$BIN/B2" "$REPO")"; rc=$?
if [[ "$out" == *"install-guard: ok"* && "$out" == *"rebase-equivalent"* ]] && [ $rc -eq 0 ]; then
  ok "install-guard: complete two-commit rebase is safe, exit 0"
else
  bad "install-guard: expected complete rebase to pass" "$out (rc $rc)"
fi

# Matching only the installed tip cannot excuse a missing earlier change.
gitc "$REPO" checkout -q -b partial-series "$SHA_A"
printf 'package main\n\nfunc main() { _ = 1 }\n' > "$REPO/main.go"
printf 'different combined patch\n' > "$REPO/combined.txt"
gitc "$REPO" add -A
gitc "$REPO" commit -q -m combined
gitc "$REPO" cherry-pick "$SHA_B2" >/dev/null
out="$(run "$BIN/B2" "$REPO")"; rc=$?
if [[ "$out" == *"REFUSED"* ]] && [ $rc -eq 1 ]; then
  ok "install-guard: only equivalent tip with unmatched earlier history refuses"
else
  bad "install-guard: incomplete equivalence must refuse" "$out (rc $rc)"
fi

# Ordinary patch-id strips semantic whitespace inside source strings.
gitc "$REPO" checkout -q -b spaced "$SHA_A"
printf 'package main\n\nfunc main() { _ = "a b" }\n' > "$REPO/main.go"
gitc "$REPO" commit -q -am spaced
buildbin "$REPO" "$BIN/spaced"
gitc "$REPO" checkout -q -b unspaced "$SHA_A"
printf 'package main\n\nfunc main() { _ = "ab" }\n' > "$REPO/main.go"
gitc "$REPO" commit -q -am unspaced
out="$(run "$BIN/spaced" "$REPO")"; rc=$?
if [[ "$out" == *"REFUSED"* ]] && [ $rc -eq 1 ]; then
  ok "install-guard: semantic whitespace difference is divergent, exit 1"
else
  bad "install-guard: whitespace-only semantic divergence must refuse" "$out (rc $rc)"
fi

# An empty installed-only commit has no patch proof and must remain refused.
gitc "$REPO" checkout -q -b installed-empty "$SHA_B2"
gitc "$REPO" commit -q --allow-empty -m empty
buildbin "$REPO" "$BIN/empty"
gitc "$REPO" checkout -q rebased-series
out="$(run "$BIN/empty" "$REPO")"; rc=$?
if [[ "$out" == *"REFUSED"* ]] && [ $rc -eq 1 ]; then
  ok "install-guard: empty installed-only commit has no equivalence proof"
else
  bad "install-guard: empty installed-only history must refuse" "$out (rc $rc)"
fi

# Independent changes cannot be accepted in the opposite history order.
gitc "$REPO" checkout -q -b ordered-installed "$SHA_A"
printf 'first\n' > "$REPO/first.txt"
gitc "$REPO" add first.txt
gitc "$REPO" commit -q -m first
SHA_FIRST="$(git -C "$REPO" rev-parse HEAD)"
printf 'second\n' > "$REPO/second.txt"
gitc "$REPO" add second.txt
gitc "$REPO" commit -q -m second
SHA_SECOND="$(git -C "$REPO" rev-parse HEAD)"
buildbin "$REPO" "$BIN/ordered"
gitc "$REPO" checkout -q -b reverse-candidate "$SHA_A"
gitc "$REPO" cherry-pick "$SHA_SECOND" "$SHA_FIRST" >/dev/null
out="$(run "$BIN/ordered" "$REPO")"; rc=$?
if [[ "$out" == *"REFUSED"* ]] && [ $rc -eq 1 ]; then
  ok "install-guard: reversed independent patch order refuses despite identical trees"
else
  bad "install-guard: reversed patch history must refuse" "$out (rc $rc)"
fi

# A merge-only installed commit cannot be proved by its individual side patches.
gitc "$REPO" checkout -q -b merge-installed "$SHA_A"
printf 'main\n' > "$REPO/merge-main.txt"
gitc "$REPO" add merge-main.txt
gitc "$REPO" commit -q -m merge-main
SHA_MERGE_MAIN="$(git -C "$REPO" rev-parse HEAD)"
gitc "$REPO" checkout -q -b merge-side "$SHA_A"
printf 'side\n' > "$REPO/merge-side.txt"
gitc "$REPO" add merge-side.txt
gitc "$REPO" commit -q -m merge-side
SHA_MERGE_SIDE="$(git -C "$REPO" rev-parse HEAD)"
gitc "$REPO" checkout -q merge-installed
gitc "$REPO" merge -q --no-ff -m merge merge-side
buildbin "$REPO" "$BIN/merge"
gitc "$REPO" checkout -q -b linear-candidate "$SHA_A"
gitc "$REPO" cherry-pick "$SHA_MERGE_MAIN" "$SHA_MERGE_SIDE" >/dev/null
out="$(run "$BIN/merge" "$REPO")"; rc=$?
if [[ "$out" == *"REFUSED"* ]] && [ $rc -eq 1 ]; then
  ok "install-guard: installed merge refuses despite matching linear side patches"
else
  bad "install-guard: installed merge history must refuse" "$out (rc $rc)"
fi

gitc "$REPO" checkout -q rebased-series

# A shared patch reverted before divergence cannot prove installed replay.
gitc "$REPO" checkout -q -b shared-reverted "$SHA_A"
printf 'replayed\n' > "$REPO/replay.txt"
gitc "$REPO" add replay.txt
gitc "$REPO" commit -q -m shared-add
SHA_SHARED_ADD="$(git -C "$REPO" rev-parse HEAD)"
gitc "$REPO" revert --no-edit "$SHA_SHARED_ADD" >/dev/null
SHA_SHARED_REVERT="$(git -C "$REPO" rev-parse HEAD)"
gitc "$REPO" checkout -q -b installed-replay
gitc "$REPO" cherry-pick "$SHA_SHARED_ADD" >/dev/null
buildbin "$REPO" "$BIN/replay"
gitc "$REPO" checkout -q -b missing-replay "$SHA_SHARED_REVERT"
printf 'unrelated\n' > "$REPO/unrelated.txt"
gitc "$REPO" add unrelated.txt
gitc "$REPO" commit -q -m unrelated
out="$(run "$BIN/replay" "$REPO")"; rc=$?
if [[ "$out" == *"REFUSED"* ]] && [ $rc -eq 1 ]; then
  ok "install-guard: shared reverted patch cannot excuse missing installed replay"
else
  bad "install-guard: historical reverted patch must not prove current installed replay" "$out (rc $rc)"
fi

gitc "$REPO" checkout -q rebased-series

# A failed fingerprint producer is ERROR, never unmatched/accepted history.
REAL_GIT="$(command -v git)"
mkdir -p "$T/error-bin"
{
  printf '#!/usr/bin/env bash\nfor arg; do [ "$arg" != log ] || exit 17; done\nexec %q "$@"\n' "$REAL_GIT"
} > "$T/error-bin/git"
chmod +x "$T/error-bin/git"
out="$(PATH="$T/error-bin:$PATH" run "$BIN/B2" "$REPO")"; rc=$?
if [[ "$out" == *"install-guard: ERROR"* && "$out" == *"fingerprint installed-only history"* ]] && [ $rc -eq 2 ]; then
  ok "install-guard: failed Git fingerprint producer is ERROR, exit 2"
else
  bad "install-guard: fingerprint failure must be visible ERROR" "$out (rc $rc)"
fi

shtest_end
