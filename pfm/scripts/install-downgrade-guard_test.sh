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

GOCACHE_DIR="$T/gocache"
mkdir -p "$GOCACHE_DIR"
GOENV=(GOCACHE="$GOCACHE_DIR" GOTOOLCHAIN=local CGO_ENABLED=0)

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

shtest_end
