#!/usr/bin/env bash
# leak-check.sh — mechanical leak gate: private brand names, maintainer PII, and
# machine-absolute paths must never reach the public repo. Wired as pre-push via
# .githooks/ (git config core.hooksPath .githooks). Stock service ports
# (5432/5433/4566/4567) are industry defaults, not identifying — deliberately ungated.
#
# THE DENYLIST IS NOT IN THIS FILE, and that is the whole design. A gate that spells
# out the names it hunts publishes them itself: this script is tracked and public, so
# every private term it once named was readable by anyone who cloned the repo — and
# because it excluded itself from its own scan, it never once reported that. Private
# terms now live in an untracked, gitignored terms file (scripts/leak-terms.txt;
# override: LEAK_TERMS env), and this script is scanned like any other file.
#
# A MISSING OR EMPTY TERMS FILE IS A HARD FAILURE, never a pass. A fresh clone has no
# terms file; without this rule the gate would scan for the structural patterns alone,
# find nothing, and print "clean" — the exact coincidence-detector shape it exists to
# refuse. "We have no denylist" and "we found no leak" must never print the same word.
#
# Structural patterns stay inline because they name nobody. Their discriminator: a home
# path is a leak only when it names a CONCRETE directory, so the blueprint's own
# documented defaults (`~/work/<project>`, `$HOME/work/{MEMORY_VAULT_DIR}`) pass while a
# real directory under the same root does not. Written as a bracket class, these three
# patterns also cannot match their own source text — which is why this file passes the
# scan it now submits itself to.
#
# MATCHED CASE-INSENSITIVELY (grep -i), and that is load-bearing: the pattern once spelled
# a brand in a single capitalisation and matched neither its all-caps nor its mixed-case
# form, so a chat name carried a client's name straight through a "clean" gate. A denylist
# that only knows two capitalisations of a word does not know the word.
set -euo pipefail

# Named nowhere, identifying nobody — safe to keep in the public file.
STRUCTURAL_PATTERN='/home/[A-Za-z0-9]|/Users/[A-Za-z0-9]|~/work/[A-Za-z0-9]'

# Tokens that MATCH a structural pattern but name nobody. Each is removed from a line
# before the line is judged, so a line carrying a real path ALONGSIDE one still fails.
# Removal requires a non-word character after the token, so a longer username that merely
# starts with an allowed one is NOT suppressed and still fails. Every suppression is
# COUNTED and reported: an allowlist that hides its own work is the next
# coincidence detector, and a growing one that says nothing is how a real path arrives.
# Ordered longest-first so a longer token always wins over a shorter one it contains.
# ONLY invented names may be added here — a name that exists on somebody's machine does
# not belong in an allowlist, it belongs in the terms file.
#   /home/account-42, /home/tester, /home/test, /home/me, /home/x
#                    — the Go suites' synthetic HOMEs; no machine on earth is any of these
#   ~/work/alpha, ~/work/Foo — invented project names in a fixture statusline and a doc example
#   ~/work/professor — this repo's own public name, quoted in a historical release note
#   mreza0100       — this repo's former GitHub owner, still quoted by historical release
#                     notes; a private term that is a substring of it must not fail them
BENIGN_TOKENS='(/home/account-42|~/work/professor|mreza0100|/home/tester|~/work/alpha|/home/test|~/work/Foo|/home/me|/home/x)'

# True when the line still matches PATTERN after benign tokens and the configured
# ignore tokens are removed. Ignore tokens are lowercase regexes (the terms-file
# convention), so they are applied to the lowercased line; PATTERN matches with -i.
line_is_real_hit() {
  local line tok
  line="$(printf '%s ' "$1" | sed -E "s#${BENIGN_TOKENS}([^A-Za-z0-9_-])#<BENIGN>\2#g")"
  if (( ${#ignore_tokens[@]} > 0 )); then
    line="$(printf '%s' "$line" | tr '[:upper:]' '[:lower:]')"
    for tok in "${ignore_tokens[@]}"; do
      line="$(printf '%s' "$line" | sed -E "s#${tok}#<IGNORED>#g")"
    done
  fi
  printf '%s' "$line" | grep -qiE "$PATTERN"
}

usage() {
  echo "Usage: leak-check.sh [--range OLD NEW | --files f1 [f2 ...]]" >&2
  exit 2
}

mode="staged"
range_old=""
range_new=""
files=()

if [[ $# -eq 0 ]]; then
  mode="staged"
elif [[ "$1" == "--range" ]]; then
  [[ $# -eq 3 ]] || usage
  mode="range"
  range_old="$2"
  range_new="$3"
elif [[ "$1" == "--files" ]]; then
  [[ $# -ge 2 ]] || usage
  mode="files"
  shift
  files=("$@")
else
  usage
fi

# ---- denylist load: the gate refuses to run without one -----------------------
script_dir="$(dirname "$(readlink -f "$0")")"
terms_file="${LEAK_TERMS:-$script_dir/leak-terms.txt}"

if [[ ! -f "$terms_file" ]]; then
  echo "leak-check: FAILED — terms file not found: $terms_file" >&2
  echo "leak-check: the private denylist is untracked by design (see .gitignore)." >&2
  echo "leak-check: create it as one lowercase regex alternative per line ('#' comments)," >&2
  echo "leak-check: or point LEAK_TERMS at one. Refusing to report clean with no denylist." >&2
  exit 1
fi

# The terms file also carries the machine's own ignore list, private like the terms:
#   !ignore-token <lowercase regex>  stripped from a line BEFORE it is judged, so a
#                                    known-public string (a public handle, a code
#                                    identifier a term matches inside) stops failing
#                                    while a real leak on the same line still does
#   !ignore-path <git glob>          a path never scanned at all — reported on every
#                                    run, because an unannounced blind spot is a leak path
terms=()
ignore_tokens=()
ignore_paths=()
while IFS= read -r term || [[ -n "$term" ]]; do
  term="${term%$'\r'}"
  [[ "$term" =~ ^[[:space:]]*# ]] && continue
  [[ -z "${term//[[:space:]]/}" ]] && continue
  case "$term" in
    '!ignore-token '*) ignore_tokens+=("${term#!ignore-token }"); continue ;;
    '!ignore-path '*) ignore_paths+=("${term#!ignore-path }"); continue ;;
    '!'*) echo "leak-check: FAILED — unknown directive in $terms_file: $term" >&2; exit 1 ;;
  esac
  terms+=("$term")
done < "$terms_file"
if (( ${#ignore_paths[@]} > 0 )); then
  printf 'leak-check: %d configured path ignore(s) — NOT scanned: %s\n' "${#ignore_paths[@]}" "${ignore_paths[*]}" >&2
fi

if (( ${#terms[@]} == 0 )); then
  echo "leak-check: FAILED — terms file has zero usable terms: $terms_file" >&2
  echo "leak-check: an empty denylist finds nothing, which is not the same as finding nothing." >&2
  exit 1
fi

terms_alt="$(IFS='|'; printf '%s' "${terms[*]}")"
PATTERN="(${terms_alt}|${STRUCTURAL_PATTERN})"

if [[ -n "${PFM_DEV_REPO_GIT_DIR:-}" || -n "${PFM_DEV_REPO_WORK_TREE:-}" ]]; then
  if [[ -z "${PFM_DEV_REPO_GIT_DIR:-}" || -z "${PFM_DEV_REPO_WORK_TREE:-}" ]]; then
    echo "leak-check: PFM_DEV_REPO_GIT_DIR and PFM_DEV_REPO_WORK_TREE must be set together" >&2
    exit 1
  fi
  repo_root="$PFM_DEV_REPO_WORK_TREE"
  repo_git() {
    git --git-dir="$PFM_DEV_REPO_GIT_DIR" --work-tree="$PFM_DEV_REPO_WORK_TREE" \
      -c safe.directory="$PFM_DEV_REPO_WORK_TREE" "$@"
  }
else
  repo_root="$(git rev-parse --show-toplevel)"
  repo_git() { git "$@"; }
fi
cd "$repo_root"

# The two data files that hold real private values by design are the only self-exclusions
# left. leak-check.sh is NOT among them any more: it scans itself.
terms_rel=""
case "$terms_file" in
  "$repo_root"/*) terms_rel="${terms_file#"$repo_root"/}" ;;
esac

diff_excludes=(
  ':(exclude)scripts/placeholder-map.tsv'
  ':(exclude)scripts/leak-terms.txt'
  ':(exclude).githooks'
  ':(exclude)LICENSE'
  ':(exclude).opencode/LICENSE'
)
for glob in ${ignore_paths[@]+"${ignore_paths[@]}"}; do
  diff_excludes+=(":(exclude,glob)$glob")
done

is_excluded_path() {
  local p="$1" glob
  case "$p" in
    scripts/placeholder-map.tsv|scripts/leak-terms.txt|LICENSE|.opencode/LICENSE) return 0 ;;
    .githooks/*|.githooks) return 0 ;;
  esac
  [[ -n "$terms_rel" && "$p" == "$terms_rel" ]] && return 0
  for glob in ${ignore_paths[@]+"${ignore_paths[@]}"}; do
    # shellcheck disable=SC2053 — the configured glob is matched as a pattern on purpose
    [[ "$p" == $glob ]] && return 0
  done
  return 1
}

# Reads a unified diff (-U0) on stdin, prints "LEAK {file}: {content}" for
# every match found in an ADDED line (starts with "+", not "+++"), tracking
# the current file from "+++ b/..." headers.
#
# Single-pass: the loop below only accumulates added lines + their file into
# arrays (no subprocess). Matching runs as ONE `grep -inE` over the whole
# accumulated stream, instead of forking grep once per added line — a diff
# with tens of thousands of added lines used to fork tens of thousands of
# grep processes and never return.
scan_diff_stream() {
  local file=""
  local line
  local -a files=()
  local -a contents=()

  while IFS= read -r line; do
    if [[ "$line" == "+++ /dev/null" ]]; then
      file=""
    elif [[ "$line" == "+++ b/"* ]]; then
      file="${line#+++ b/}"
    elif [[ "$line" == "+++"* ]]; then
      file="${line#+++ }"
    elif [[ "$line" == "+"* ]]; then
      contents+=("${line#+}")
      files+=("$file")
    fi
  done

  # This function is the last stage of a pipeline, so it runs in a subshell:
  # nothing it assigns survives to the parent. Coverage is therefore written
  # to $coverage_file (a file the parent already created) rather than handed
  # back through a variable — and it is written UNCONDITIONALLY, including
  # the zero-added-lines case, because that case is exactly the one a
  # coincidence detector would otherwise skip reporting on.
  local added_lines=${#contents[@]}
  # bash 3.2 — the macOS system bash a git hook's restricted PATH resolves to —
  # has no associative arrays, so the seen-set is a newline-delimited string.
  # git quotes any path containing a newline, so the delimiter stays unambiguous.
  local seen_files=$'\n'
  local distinct_files=0
  local unattributed=0
  local suppressed=0

  if (( added_lines > 0 )); then
    local f
    for f in "${files[@]}"; do
      # An added line with no `+++` header before it belongs to no file: the
      # diff stream was malformed (a format change, a truncated pipe). Counting
      # it as a file would report coverage of a file that does not exist, so it
      # is counted separately and named in the verdict.
      if [[ -z "$f" ]]; then
        unattributed=$((unattributed + 1))
        continue
      fi
      if [[ "$seen_files" != *$'\n'"$f"$'\n'* ]]; then
        seen_files+="$f"$'\n'
        distinct_files=$((distinct_files + 1))
      fi
    done

    local matches
    matches="$(printf '%s\n' "${contents[@]}" | grep -niE "$PATTERN" || true)"
    if [[ -n "$matches" ]]; then
      local idx content
      while IFS=: read -r idx content; do
        if line_is_real_hit "$content"; then
          printf 'LEAK %s: %s\n' "${files[idx-1]}" "$content"
        else
          suppressed=$((suppressed + 1))
        fi
      done <<< "$matches"
    fi
  fi

  {
    printf 'added_lines=%d\n' "$added_lines"
    printf 'distinct_files=%d\n' "$distinct_files"
    printf 'term_count=%d\n' "${#terms[@]}"
    printf 'structural_patterns=3\n'
    printf 'suppressed=%d\n' "$suppressed"
    printf 'unattributed=%d\n' "$unattributed"
  } > "$coverage_file"

  (( suppressed > 0 )) && printf 'leak-check: %d benign- or ignore-token line(s) suppressed by the documented allowlist and the configured ignore tokens\n' "$suppressed" >&2
  return 0
}

hits_file="$(mktemp)"
coverage_file="$(mktemp)"
trap 'rm -f "$hits_file" "$coverage_file"' EXIT

case "$mode" in
  staged)
    repo_git diff --cached -U0 --no-color -- . "${diff_excludes[@]}" \
      | scan_diff_stream > "$hits_file"
    ;;
  range)
    repo_git diff "$range_old" "$range_new" -U0 --no-color -- . "${diff_excludes[@]}" \
      | scan_diff_stream > "$hits_file"
    ;;
  files)
    : > "$hits_file"
    # Three outcomes per named path, and they are NOT interchangeable:
    #   scanned  — examined, verdict earned
    #   excluded — deliberately not examined (the data files that hold real private
    #              values by design); a list of only these is legitimately clean
    #   skipped  — named but not a regular file: examined by nothing. Normal for a
    #              deleted path in a changed-file list, but never counted as clean.
    # A run whose only non-excluded paths were all skipped examined NOTHING and always
    # fails: "we scanned 40 files and found no leak" and "we scanned zero files" must
    # not print the same word. A caller whose shell folded its file list into one
    # argument (zsh does not word-split an unquoted variable) lands exactly there.
    scanned=0
    skipped=0
    excluded=0
    suppressed=0
    for f in "${files[@]}"; do
      if is_excluded_path "$f"; then
        excluded=$((excluded + 1))
        continue
      fi
      if [[ -f "$f" ]]; then
        scanned=$((scanned + 1))
        matches="$(grep -niE "$PATTERN" "$f")" && rc=0 || rc=$?
        if (( rc >= 2 )); then
          printf 'SCAN-ERROR %s: leak-check could NOT read this file (grep rc=%d) — treated as FAILURE, never as clean\n' "$f" "$rc" >> "$hits_file"
        elif [[ -n "$matches" ]]; then
          while IFS=: read -r lnum content; do
            if line_is_real_hit "$content"; then
              printf 'LEAK %s: %s\n' "$f" "$content" >> "$hits_file"
            else
              suppressed=$((suppressed + 1))
            fi
          done <<< "$matches"
        fi
      else
        skipped=$((skipped + 1))
        printf 'NOT-SCANNED %s: not a regular file (deleted or misnamed) — examined by nothing, counted as clean by nothing\n' "$f" >&2
      fi
    done
    if (( scanned == 0 && skipped > 0 )); then
      printf 'SCAN-ERROR: --files named %d path(s), %d excluded by design, and every one of the remaining %d was NOT a regular file — nothing was examined, refusing to report clean\n' \
        "${#files[@]}" "$excluded" "$skipped" >> "$hits_file"
    fi
    printf 'leak-check: scanned %d file(s) (%d excluded by design, %d not a regular file) against %d private term(s) + %d structural pattern(s); %d benign-token line(s) suppressed\n' \
      "$scanned" "$excluded" "$skipped" "${#terms[@]}" 3 "$suppressed" >&2
    ;;
esac

n="$(wc -l < "$hits_file" | tr -d ' ')"

if [[ "$n" -gt 0 ]]; then
  cat "$hits_file"
  echo "leak-check: FAILED — ${n} leak line(s)" >&2
  exit 1
fi

if [[ "$mode" == "staged" || "$mode" == "range" ]]; then
  # scan_diff_stream ran in a subshell (last stage of the diff pipeline) and
  # left its coverage in $coverage_file — a "clean" verdict here must name
  # what it scanned, and if that file is missing or unparseable this is a
  # FAILURE, never a silent default of 0 reported as clean.
  if [[ ! -s "$coverage_file" ]]; then
    echo "leak-check: FAILED — the scan's own coverage could not be read (${mode} coverage file is missing or empty); refusing to report clean" >&2
    exit 1
  fi

  added_lines=""
  distinct_files=""
  term_count=""
  structural_patterns=""
  suppressed=""
  unattributed=""
  while IFS='=' read -r key value; do
    case "$key" in
      added_lines) added_lines="$value" ;;
      distinct_files) distinct_files="$value" ;;
      term_count) term_count="$value" ;;
      structural_patterns) structural_patterns="$value" ;;
      suppressed) suppressed="$value" ;;
      unattributed) unattributed="$value" ;;
    esac
  done < "$coverage_file"

  if [[ -z "$added_lines" || -z "$distinct_files" || -z "$term_count" || -z "$structural_patterns" || -z "$suppressed" || -z "$unattributed" ]]; then
    echo "leak-check: FAILED — the scan's own coverage could not be read (${mode} coverage file is unparseable); refusing to report clean" >&2
    exit 1
  fi

  if [[ "$added_lines" -eq 0 ]]; then
    echo "leak-check: NOTHING TO SCAN — the ${mode} diff contained no added lines; nothing could leak and no verdict was earned"
    exit 0
  fi

  verdict="leak-check: clean — ${added_lines} added line(s) across ${distinct_files} file(s) scanned against ${term_count} private term(s) + ${structural_patterns} structural pattern(s); ${suppressed} benign-token line(s) suppressed"
  (( unattributed > 0 )) && verdict+=" — WARNING: ${unattributed} added line(s) carried no file header and are attributed to no file; the diff stream was malformed"
  echo "$verdict"
  exit 0
fi

echo "leak-check: clean"
exit 0
