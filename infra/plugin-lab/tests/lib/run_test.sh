#!/usr/bin/env bash
# lib/run.sh: run folder and socket bound (L7), budget preflight (L1), ledger, prune (L18).
set -uo pipefail
# shellcheck source=../testlib.sh
source "$(dirname "$0")/../testlib.sh"
lab_lib run

# L7: {run}/t.sock 100 bytes accepted, 101 refused
root=$T/r
pad() { local n=$(( $1 - ${#root} - 1 - 14 - 7 )); printf 'x%.0s' $(seq 1 "$n"); } # /{id}-yymmdd-HHMMSS/t.sock
LAB_ROOT=$root lab_run_dir "$(pad 100)" >/dev/null 2>&1; rc=$?
expect_eq 'socket 100 bytes accepted' "$rc $(( ${#LAB_RUN} + 7 ))" '0 100'
root=$T/q
out=$(LAB_ROOT=$root lab_run_dir "$(pad 101)" 2>&1); rc=$?
expect_eq 'socket 101 bytes refused rc' "$rc" 2
expect_has 'socket 101 bytes refused line' "$out" 'ERROR socket-path-long 101>100'
LAB_ROOT=$root lab_run_dir same >/dev/null 2>&1; first=$LAB_RUN
LAB_ROOT=$root lab_run_dir same >/dev/null 2>&1
expect_eq 'a second run in the same second gets its own folder' "$([[ "$LAB_RUN" != "$first" && -d "$LAB_RUN" ]] && echo distinct)" distinct
expect_eq 'run folder is private' "$(stat -c %a "$LAB_RUN" 2>/dev/null || stat -f %Lp "$LAB_RUN")" 700

# project name: the main checkout's directory minus its leading dot, also from a worktree
repo=$T/.proj; git init -q "$repo"; git -C "$repo" -c user.name=t -c user.email=t@example.invalid commit -q --allow-empty -m i
git -C "$repo" worktree add -q "$T/wt-flight" 2>/dev/null
expect_eq 'project from a worktree' "$(LAB_REPO=$T/wt-flight lab_project)" proj

# L1: ledger sum + cap = budget passes; + 0.01 refuses, before anything starts
ledger=$T/ledger.tsv
printf 'a\ts\tp\tr\td\tPASS\t8.20\t1.80\tt\t1\tx\n' > "$ledger"
out=$(lab_budget_preflight "$ledger" 1.80 10.00 2>&1); rc=$?
expect_eq 'budget at the bound passes' "$rc" 0
expect_eq 'budget line' "$out" 'budget: 1.80 left of 10.00 · cap 1.80'
printf 'b\ts\tp\tr\td\tFAIL\t0.70\t1.80\tt\t1\tx\n' >> "$ledger"
out=$(lab_budget_preflight "$ledger" 1.80 10.00 2>&1); rc=$?
expect_eq 'budget over refuses rc' "$rc" 2
expect_eq 'budget over refuses line' "$out" 'ERROR budget-exhausted spent=8.90 cap=1.80 budget=10.00'
printf 'a\ts\tp\tr\td\tPASS\t8.20\t1.80\tt\t1\tx\n' > "$ledger"
out=$(lab_budget_preflight "$ledger" 1.81 10.00 2>&1); rc=$?
expect_eq 'budget + 0.01 refuses' "$rc" 2
printf 'c\ts\tp\tr\td\tERROR\t?\t0.25\tt\t1\tx\n' > "$ledger"
expect_eq 'unknown spend counts at its cap' "$(lab_ledger_sum "$ledger")" 0.25
expect_eq 'no ledger sums to zero' "$(lab_ledger_sum "$T/none.tsv")" 0.00
printf 'a\ts\tp\tr\td\tRUNNING\t?\t0.25\tt\t?\tx\na\ts\tp\tr\td\tPASS\t0.10\t0.25\tt\t1\tx\nb\ts\tp\tr\td\tRUNNING\t?\t0.25\tt\t?\tx\n' > "$ledger"
expect_eq 'a final row stands in for its RUNNING row; an open RUNNING row counts at its cap' "$(lab_ledger_sum "$ledger")" 0.35
mkdir "$ledger.lock"; (sleep 0.3; rmdir "$ledger.lock") &
lab_ledger_lock "$ledger"; rc=$?; wait
expect_eq 'a held ledger lock is waited for' "$rc $([[ -d $ledger.lock ]] && echo held)" '0 held'
lab_ledger_unlock "$ledger"
# the holder's rmdir lands between mkdir's EEXIST and the recheck: retried, never an error
fakebin=$T/fakebin; mkdir -p "$fakebin"; realmkdir=$(command -v mkdir)
cat > "$fakebin/mkdir" <<EOF
#!/usr/bin/env bash
# the first lock attempt fails as if another run held the lock and released it at once
if [[ \${1-} == "$ledger.lock" && ! -e "$T/raced" ]]; then : > "$T/raced"; echo "mkdir: cannot create directory '\$1': File exists" >&2; exit 1; fi
exec "$realmkdir" "\$@"
EOF
chmod +x "$fakebin/mkdir"
PATH=$fakebin:$PATH lab_ledger_lock "$ledger" 2>/dev/null; rc=$?
expect_eq 'a lock released before the recheck is retried' "$rc $([[ -d $ledger.lock ]] && echo held)" '0 held'
lab_ledger_unlock "$ledger"
lab_ledger_append "$T/l2/ledger.tsv" r1 s p rev dirty PASS 0.02 0.25 2026-01-01T00:00:00Z 9 lab-reports/r1.md
expect_eq 'ledger append is one tab row' "$(cat "$T/l2/ledger.tsv")" "$(printf 'r1\ts\tp\trev\tdirty\tPASS\t0.02\t0.25\t2026-01-01T00:00:00Z\t9\tlab-reports/r1.md')"

# L18: prune deletes old run folders under the root only, never through a symlink
pr=$T/prune; mkdir -p "$pr/old-200101-000000" "$pr/new-260101-000000" "$T/outside/keep"
touch -t 202001010000 "$pr/old-200101-000000"
ln -s "$T/outside" "$pr/link"
touch -t 202001010000 "$T/outside"
out=$(LAB_ROOT=$pr lab_prune 7 2>&1)
expect_eq 'prune prints the deleted path' "$out" "$pr/old-200101-000000"
expect_eq 'prune kept the new run and the symlink target' "$([[ -d $pr/new-260101-000000 && -d $T/outside/keep && ! -e $pr/old-200101-000000 ]] && echo kept)" kept
finish
