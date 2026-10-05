#!/usr/bin/env bash
# Exercise .claude/scripts/check-templates.sh, the templates unit's static-check command, against a
# fake repository: usage, the install FAIL line (alone, no check run), path handling, the file-scoped
# checks (rumdl on .md files only, leak on every file, SKIP wording), the unit-wide checks (every one
# runs after a failure; agent-roster is not one of them), the infra/fence/checks.sh shims and the branch
# where that file is missing, a directory argument, the per-run private scratch directory (the log's name
# plus .d: the catalogue's TMP_BASE, present during the run, gone after a run with no FAIL, kept after a
# run with one so its pointer resolves, no anonymous mktemp), the EXIT trap's PID guard (a forked shell
# running it keeps the suite's scratch), the log, and the exit code. Every tool is a stub on a PATH of stubs and symlinks to the real basics, so
# the suite never touches the host's tools.
#   bash scripts/test-check-templates.sh [path to check-templates.sh]
# BROKEN STATE: a missing script under test fails every case (the copy error is printed), never 0 failed.
# shellcheck disable=SC2016 # the bash -c bodies are single-quoted on purpose: they expand in the child
set -uo pipefail
HERE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
SUT="${1:-$HERE/.claude/scripts/check-templates.sh}"
SHTEST_TAG=check-templates
# shellcheck source=scripts/shtest.sh
source "$HERE/scripts/shtest.sh"

PROJECT="ctroot$$"
R="$T/.$PROJECT"
REC="$T/rec"
trap 'if [ "${BASHPID:-$$}" = "$SHTEST_PID" ]; then rm -rf -- "$T" "/tmp/$PROJECT"; fi' EXIT
# The EXIT trap keeps shtest.sh's PID guard: run by a forked shell it must leave the suite's scratch alone.
trap -p EXIT > "$T/exit-trap"
TRAPF="$T/exit-trap"; mkdir -p "$T/guard-probe"
T="$T/guard-probe" PROJECT="guardprobe$$" SHTEST_PID="$SHTEST_PID" bash -c 'eval "$(cat "$1")"; exit 0' _ "$TRAPF"
if [ -d "$T/guard-probe" ]; then ok "trap: a forked shell running the EXIT trap leaves the scratch alone"
else bad "trap: a forked shell running the EXIT trap removed the scratch"; fi
USAGE='Name the files you changed.'
INSTALL='FAIL install (templates has no installed dependencies — run: pfm install --yes && bash infra/fence/tools.sh)'

# PATH = stubs, then symlinks to the real basics. The stub records name, arguments, cwd and
# LEAK_TERMS, prints STUB_LINES numbered lines, and exits with the next code of $REC/rc.<name>.<first
# arg> or rc.<name>.
mkdir -p "$T/bin" "$T/real" "$T/home" "$T/toolsbin" "$R/.claude/scripts" "$R/infra/fence" "$R/scripts" "$R/docs" "$R/pfm/internal/a"
for c in bash env git dirname basename realpath mkdir date cat tail head mktemp rm tr grep sed awk sort comm xargs wc seq cut uniq tee sh; do
  ln -s "$(command -v "$c")" "$T/real/$c"
done
cat > "$T/stub.sh" <<'STUB'
#!/usr/bin/env bash
name="${0##*/}"
{ printf '%s' "$name"; [ "$#" = 0 ] || printf ' %q' "$@"; printf '\n'; } >> "$STUB_REC/calls"
printf '%s %s LEAK_TERMS=%s\n' "$name" "$PWD" "${LEAK_TERMS-unset}" >> "$STUB_REC/env"
echo "OUT-OF-$name"
[ -z "${STUB_LINES:-}" ] || seq -f 'line %g' 1 "$STUB_LINES"
for key in "$name.${1##*/}" "$name"; do
  f="$STUB_REC/rc.$key"
  if [ -s "$f" ]; then rc="$(head -n1 "$f")"; sed -i 1d "$f"; exit "$rc"; fi
done
exit 0
STUB
chmod +x "$T/stub.sh"
for t in node python3 jq rumdl; do cp "$T/stub.sh" "$T/bin/$t"; done
cp "$T/stub.sh" "$T/toolsbin/jscpd"
for f in scripts/clone-check.sh scripts/leak-check.sh scripts/description-check.sh infra/check-self-hosted-manifest.sh; do
  mkdir -p "$R/$(dirname "$f")"; cp "$T/stub.sh" "$R/$f"
done
printf '#!/usr/bin/env bash\ncase "$1" in --print-bin) echo "$TOOLSBIN" ;; esac\n' > "$R/infra/fence/tools.sh"
# The catalogue stub exercises the shims: head_, ok, info, run, repo_git, fail_step, TMP_BASE, REPO_ROOT.
cat > "$R/infra/fence/checks.sh" <<'STUB'
checks_templates_placeholders() {
  head_ "ph"
  printf 'ph REPO_ROOT=%s TMP_BASE=%s PWD=%s DIR=%s\n' "$REPO_ROOT" "$TMP_BASE" "$PWD" "$([ -d "$TMP_BASE" ] && echo 1 || echo 0)" >> "$STUB_REC/shim"
  repo_git rev-parse --show-toplevel >/dev/null || fail_step "repo_git failed"
  info "ph info"
  [ -z "${STUB_PH:-}" ] || { mkdir -p "$TMP_BASE/templates"; echo tok > "$TMP_BASE/templates/unregistered-tokens.txt"; info "full list: $TMP_BASE/templates/unregistered-tokens.txt"; fail_step "ph is red"; }
  ok "ph ok"
}
checks_templates_scratch_paths() {
  head_ "sp"
  [ -z "${STUB_SP_CRASH:-}" ] || exit 9
  run "sp run" -- bash -c "exit ${STUB_SP_RC:-0}"
}
STUB
for f in docs/a.md scripts/a.sh pfm/internal/a/a.go docs/gone.md; do echo x > "$R/$f"; done
for f in check-codex-markers check-opencode-writer; do echo x > "$R/scripts/$f.mjs"; done
git -C "$R" init -q && git -C "$R" add -A && git -C "$R" -c user.name=t -c user.email=t@t commit -qm base
rm "$R/docs/gone.md"
cp "$HERE/.claude/scripts/unit-path.sh" "$R/.claude/scripts/unit-path.sh"
cp "$SUT" "$R/.claude/scripts/check-templates.sh" || echo "test-check-templates: cannot copy $SUT" >&2
R="$(cd "$R" && pwd -P)"

reset() { rm -rf "$REC"; mkdir -p "$REC"; }
rcseq() { local key="$1"; shift; printf '%s\n' "$@" > "$REC/rc.$key"; }
go() { OUT="$(cd "${CWD:-$R}" && env -i PATH="$T/bin:$T/real" HOME="$T/home" TOOLSBIN="$T/toolsbin" STUB_REC="$REC" ${TEST_LEAK_TERMS:+LEAK_TERMS="$TEST_LEAK_TERMS"} \
  ${STUB_PH:+STUB_PH=1} ${STUB_SP_CRASH:+STUB_SP_CRASH=1} ${STUB_SP_RC:+STUB_SP_RC="$STUB_SP_RC"} ${STUB_LINES:+STUB_LINES="$STUB_LINES"} \
  bash "$R/.claude/scripts/check-templates.sh" "$@" 2>&1)"; RC=$?; }
want() { printf '%s' "$1"; shift; [ "$#" = 0 ] || printf ' %q' "$@"; }
chk() { local n="$1"; shift; if "$@"; then ok "$n"; else bad "$n" "rc=$RC" "$OUT"; fi; }
line() { grep -qxF -- "$1" <<<"$OUT"; }
none_ran() { [ ! -e "$REC/calls" ] && [ ! -e "$REC/shim" ]; }
rejected() { [ "$RC" = 2 ] && line "$1" && none_ran; }
called() { grep -qxF -- "$1" "$REC/calls"; }
verdicts() { grep -E '^(PASS|FAIL|SKIP) ' <<<"$OUT"; }
logfile() { sed -n 's/^log: //p' <<<"$OUT"; }

for a in '' -x --fix; do
  reset; go "$a"; chk "usage: argument '$a' prints the usage line, exit 2, no check run" rejected "$USAGE"
done
reset; go; chk "usage: no argument prints the usage line, exit 2, no check run" rejected "$USAGE"
reset; go docs/a.md ''; chk "usage: an empty argument beside a file" rejected "$USAGE"

reset; go docs/a.md
chk "all green: exit 0 and the verdict lines, one each, in order" bash -c '[ "$1" = 0 ] && [ "$(grep -E "^(PASS|FAIL|SKIP) " <<<"$0" | cut -d" " -f2 | tr "\n" " ")" = "rumdl leak clone placeholders scratch-paths descriptions manifest codex-markers opencode-writer-refs " ] && ! grep -qE "^(FAIL|SKIP) " <<<"$0"' "$OUT" "$RC"

reset; CWD="$R/docs" go a.md
chk "path: cwd-relative resolves, rumdl gets the repository path" called "$(want rumdl check --output-format concise docs/a.md)"
reset; CWD="$T" go "$R/docs/a.md"
chk "path: absolute resolves" called "$(want rumdl check --output-format concise docs/a.md)"
reset; go ./docs/./a.md
chk "path: ./ segments collapse" called "$(want rumdl check --output-format concise docs/a.md)"
reset; go pfm/internal/a/a.go
chk "outside: a pfm/ file exits 2 naming it, no check run" rejected "check-templates.sh: pfm/internal/a/a.go is outside templates"
reset; go docs/gone.md docs/a.md
chk "deleted: a file deleted at HEAD drops silently" bash -c '! grep -q "FAIL path" <<<"$0" && [ "$1" = 0 ]' "$OUT" "$RC"
reset; go docs/nope.md docs/a.md
chk "no such file: FAIL path line kept, the live file still checked, exit 1" bash -c 'grep -qxF "FAIL path docs/nope.md (no such file in templates)" <<<"$0" && [ "$1" = 1 ] && grep -qx "PASS clone" <<<"$0" && grep -qxF "rumdl check --output-format concise docs/a.md" "$2/calls"' "$OUT" "$RC" "$REC"

reset; go docs/a.md scripts/a.sh
chk "rumdl: only the named .md files, run from the repository root" bash -c 'grep -qxF "$(printf "rumdl check --output-format concise docs/a.md")" "$0/calls" && grep -q "^rumdl $1 " "$0/env"' "$REC" "$R"
reset; go scripts/a.sh
chk "rumdl: no .md named prints exactly the SKIP line, rumdl never run" bash -c 'grep -qxF "SKIP rumdl (no named file it applies to)" <<<"$0" && ! grep -q "^rumdl" "$1/calls"' "$OUT" "$REC"
reset; go docs/a.md scripts/a.sh
chk "leak: every named file, as --files, run from the root" called "$(want leak-check.sh --files docs/a.md scripts/a.sh)"
reset; go docs/nope.md
chk "leak: no resolved file prints SKIP leak" line "SKIP leak (no named file it applies to)"
reset; go docs/a.md
chk "leak: LEAK_TERMS defaults to the main checkout's scripts/leak-terms.txt" grep -qxF "leak-check.sh $R LEAK_TERMS=$R/scripts/leak-terms.txt" "$REC/env"
reset; TEST_LEAK_TERMS=/elsewhere/terms.txt go docs/a.md
chk "leak: a LEAK_TERMS the caller set stands" grep -qxF "leak-check.sh $R LEAK_TERMS=/elsewhere/terms.txt" "$REC/env"

reset; go docs/a.md
chk "unit-wide: the exact commands, whole" bash -c 'for w in "clone-check.sh" "description-check.sh" "check-self-hosted-manifest.sh '"$R"' templates pfm" "node scripts/check-codex-markers.mjs" "node scripts/check-opencode-writer.mjs"; do grep -qxF "$w" "$0/calls" || { echo "missing: $w" >&2; exit 1; }; done' "$REC"
chk "unit-wide: no check is ever given --write, fmt or --fix" bash -c '! grep -qE -- "--write|--fix| fmt" "$0/calls"' "$REC"
LOGF="$(logfile)"
chk "shims: the catalogue's checks see REPO_ROOT, the run's private TMP_BASE (log name + .d, existing) and the root as cwd" grep -qxF "ph REPO_ROOT=$R TMP_BASE=${LOGF%.log}.d PWD=$R DIR=1" "$REC/shim"

reset; STUB_PH=1 go docs/a.md
chk "shims: a fail_step in a catalogue check is FAIL placeholders, the rest unaffected" bash -c 'grep -qx "FAIL placeholders" <<<"$0" && grep -qx "PASS scratch-paths" <<<"$0" && [ "$1" = 1 ]' "$OUT" "$RC"
reset; STUB_SP_RC=3 go docs/a.md
chk "shims: run's non-zero command is FAIL scratch-paths" bash -c 'grep -qx "FAIL scratch-paths" <<<"$0" && grep -qx "PASS placeholders" <<<"$0"' "$OUT"
reset; STUB_SP_CRASH=1 go docs/a.md
chk "shims: a catalogue check that dies is FAIL, never PASS" bash -c 'grep -qx "FAIL scratch-paths" <<<"$0" && ! grep -qx "PASS scratch-paths" <<<"$0"' "$OUT"

# infra/fence/checks.sh missing: both catalogue checks FAIL, named in the log, every other check still runs.
reset; mv "$R/infra/fence/checks.sh" "$T/checks.sh.away"; go docs/a.md; LOGF="$(logfile)"
mv "$T/checks.sh.away" "$R/infra/fence/checks.sh"
chk "catalogue missing: FAIL placeholders and FAIL scratch-paths, the other seven checks PASS, exit 1" bash -c '[ "$1" = 1 ] && grep -qx "FAIL placeholders" <<<"$0" && grep -qx "FAIL scratch-paths" <<<"$0" && [ "$(grep -c "^PASS " <<<"$0")" = 7 ] && ! grep -q "^SKIP " <<<"$0"' "$OUT" "$RC"
chk "catalogue missing: the log names infra/fence/checks.sh and the check that did not run" bash -c 'grep -qF "infra/fence/checks.sh could not be sourced — placeholders did not run" "$0" && grep -qF "infra/fence/checks.sh could not be sourced — scratch-paths did not run" "$0"' "$LOGF"

# A directory is no file: FAIL path, the live file beside it still checked, exit 1.
reset; go docs docs/a.md
chk "directory: FAIL path (not a file), the live file still checked, exit 1" bash -c '[ "$1" = 1 ] && grep -qxF "FAIL path docs (not a file)" <<<"$0" && grep -qx "PASS clone" <<<"$0" && grep -qxF "rumdl check --output-format concise docs/a.md" "$2/calls"' "$OUT" "$RC" "$REC"

# The scratch is private to the run: ${log%.log}.d, made after the log name, gone on exit after a run with
# no FAIL and kept after a run with one (a FAIL line's pointer into it must resolve), no mktemp.
rm -rf "/tmp/$PROJECT"
reset; go docs/a.md; LOGF="$(logfile)"
chk "scratch: gone after a green run, only the log remains in /tmp/{project}/check-templates" bash -c '[ ! -e "${0%.log}.d" ] && [ -f "$0" ] && [ "$(ls "$(dirname "$0")" | grep -c "\.d$")" = 0 ]' "$LOGF"
reset; STUB_PH=1 go docs/a.md; LOGF="$(logfile)"
chk "scratch: kept after a run with a FAIL, the file a FAIL line points into still there" bash -c '[ "$1" = 1 ] && [ -f "${0%.log}.d/templates/unregistered-tokens.txt" ]' "$LOGF" "$RC"
chk "scratch: the kept directory is the one the FAIL line names" bash -c 'grep -qF "full list: ${0%.log}.d/templates/unregistered-tokens.txt" "$0"' "$LOGF"
reset; rcseq clone-check.sh 2; go docs/a.md; LOGF="$(logfile)"
chk "scratch: kept after a FAIL from any check, not only a catalogue one" bash -c '[ "$1" = 1 ] && [ -d "${0%.log}.d" ]' "$LOGF" "$RC"
reset; go docs/nope.md docs/a.md; LOGF="$(logfile)"
chk "scratch: kept after a FAIL path line" bash -c '[ "$1" = 1 ] && [ -d "${0%.log}.d" ]' "$LOGF" "$RC"
reset; go docs/a.md; LOGF1="$(logfile)"; TB1="$(sed -n 's/.* TMP_BASE=\([^ ]*\) .*/\1/p' "$REC/shim")"
reset; go docs/a.md; LOGF2="$(logfile)"; TB2="$(sed -n 's/.* TMP_BASE=\([^ ]*\) .*/\1/p' "$REC/shim")"
chk "scratch: two runs get two different private TMP_BASE directories, each its own log's name" bash -c '[ "$0" != "$1" ] && [ "$0" = "${2%.log}.d" ] && [ "$1" = "${3%.log}.d" ]' "$TB1" "$TB2" "$LOGF1" "$LOGF2"
printf '#!/bin/sh\necho mktemp >> "$STUB_REC/anon"\nexit 99\n' > "$T/bin/mktemp"; chmod +x "$T/bin/mktemp"
reset; go docs/a.md
chk "scratch: no anonymous mktemp — a mktemp that always fails changes nothing" bash -c '[ "$1" = 0 ] && [ ! -e "$0/anon" ]' "$REC" "$RC"
rm "$T/bin/mktemp"
rm -rf "/tmp/$PROJECT"
reset; go --fix
chk "scratch: a usage exit leaves no scratch directory behind" bash -c 'ls "/tmp/'"$PROJECT"'/check-templates" 2>/dev/null | grep -c "\.d$" | grep -qx 0'

reset; rcseq clone-check.sh 2; go docs/a.md
chk "each check: one failing check is FAIL and every other still PASSes, exit 1" bash -c '[ "$1" = 1 ] && grep -qx "FAIL clone" <<<"$0" && [ "$(grep -c "^PASS " <<<"$0")" = 8 ]' "$OUT" "$RC"
reset; for k in rumdl leak-check.sh clone-check.sh description-check.sh check-self-hosted-manifest.sh node; do rcseq "$k" 1 1 1; done; STUB_PH=1 STUB_SP_RC=1 go docs/a.md
chk "each check: every check fails and every one still ran, nine FAIL lines, exit 1" bash -c '[ "$1" = 1 ] && [ "$(grep -c "^FAIL " <<<"$0")" = 9 ] && ! grep -q "^PASS " <<<"$0"' "$OUT" "$RC"
reset; rcseq node 1; go docs/a.md
chk "each check: the node checks fail separately (first is codex-markers)" bash -c 'grep -qx "FAIL codex-markers" <<<"$0" && grep -qx "PASS opencode-writer-refs" <<<"$0"' "$OUT"

LOGF=; reset; STUB_LINES=3 go docs/a.md; LOGF="$(logfile)"
chk "log: the path is printed as 'log: /tmp/{project}/check-templates/{UTC}-{pid}.log'" bash -c '[[ "$0" =~ ^/tmp/'"$PROJECT"'/check-templates/[0-9]{8}T[0-9]{6}Z-[0-9]+\.log$ ]]' "$LOGF"
chk "log: the checks' full output is in the file and not on the screen" bash -c 'grep -qx "OUT-OF-clone-check.sh" "$0" && grep -qx "line 3" "$0" && ! grep -q "OUT-OF" <<<"$1"' "$LOGF" "$OUT"

reset; rm "$T/bin/jq"; go docs/a.md
chk "install: a missing jq prints only the FAIL install line, exit 1, no check run" bash -c '[ "$1" = 1 ] && [ "$0" = "$2" ]' "$OUT" "$RC" "$INSTALL"
cp "$T/stub.sh" "$T/bin/jq"
reset; rm "$T/bin/node"; go docs/a.md
chk "install: a missing node" bash -c '[ "$1" = 1 ] && [ "$0" = "$2" ]' "$OUT" "$RC" "$INSTALL"
cp "$T/stub.sh" "$T/bin/node"
reset; rm "$T/bin/python3"; go docs/a.md
chk "install: a missing python3" bash -c '[ "$1" = 1 ] && [ "$0" = "$2" ]' "$OUT" "$RC" "$INSTALL"
cp "$T/stub.sh" "$T/bin/python3"
reset; rcseq python3.-c 1; go docs/a.md
chk "install: PyYAML not importable" bash -c '[ "$1" = 1 ] && [ "$0" = "$2" ]' "$OUT" "$RC" "$INSTALL"
reset; rm "$T/bin/rumdl"; go docs/a.md
chk "install: rumdl on neither PATH nor the uv tool dir" bash -c '[ "$1" = 1 ] && [ "$0" = "$2" ]' "$OUT" "$RC" "$INSTALL"
mkdir -p "$T/home/.local/share/uv/tools/rumdl/bin"; cp "$T/stub.sh" "$T/home/.local/share/uv/tools/rumdl/bin/rumdl"
reset; go docs/a.md
chk "install: rumdl found in the uv tool dir passes and is the one run" bash -c '[ "$1" = 0 ] && grep -qxF "rumdl check --output-format concise docs/a.md" "$2/calls"' "$OUT" "$RC" "$REC"
rm "$T/toolsbin/jscpd"
reset; go docs/a.md
chk "install: jscpd absent from the tools dir and PATH" bash -c '[ "$1" = 1 ] && [ "$0" = "$2" ]' "$OUT" "$RC" "$INSTALL"
cp "$T/stub.sh" "$T/bin/jscpd"
reset; go docs/a.md
chk "install: jscpd found on PATH passes" bash -c '[ "$1" = 0 ]' "$OUT" "$RC"
rm "$T/bin/jscpd"
reset; go nope.md
chk "install: no FAIL path line beside the install line" bash -c '[ "$0" = "$1" ]' "$OUT" "$INSTALL"

shtest_end
