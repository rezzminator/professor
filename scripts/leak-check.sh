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
# A MISSING OR EMPTY TERMS FILE IS A HARD FAILURE, never a pass. Fork CI may explicitly
# select !structural-only in its policy file; that runs the structural patterns and
# visibly reports PRIVATE COVERAGE SKIPPED. Absence never implicitly selects that mode.
#
# Structural patterns stay inline because they name nobody. Their discriminator: a home
# path is a leak only when it names a CONCRETE directory, so the blueprint's own
# documented default (`~/work/<project>`) passes while a
# real directory under the same root does not. A personal mailbox (a gmail address) is
# PII whoever owns it; a fixture carries an invented address instead. Written with a
# bracket class, these four patterns also cannot match their own source text — which is why this file passes the
# scan it now submits itself to.
#
# MATCHED CASE-INSENSITIVELY (grep -i), and that is load-bearing: the pattern once spelled
# a brand in a single capitalisation and matched neither its all-caps nor its mixed-case
# form, so a chat name carried a client's name straight through a "clean" gate. A denylist
# that only knows two capitalisations of a word does not know the word.
set -euo pipefail

# EVERY TOOL RUNS UNDER C EXCEPT THE GREP THAT MATCHES THE PATTERN: sed, tr and bash read bytes, and BSD
# sed under a UTF-8 locale rejects an invalid byte. The grep that matches the pattern (match_grep) runs under
# a UTF-8 locale — the caller's, else C.UTF-8, else UTF-8 — proven at load, so a '.' or a bracket in a term
# matches one multibyte letter and -i folds non-ASCII letters. That grep carries --binary-files=text: under
# a UTF-8 locale GNU grep otherwise skips the matching lines of a binary (NUL-byte) or undecodable file,
# exits 0 and says so only on stderr, and a leak in such a file would pass the gate. Every git diff carries
# --text, or git prints "Binary files … differ" for a NUL-byte file and no line of it is ever judged. With no
# UTF-8 locale the scan refuses.
caller_locale="${LC_ALL:-${LC_CTYPE:-${LANG:-}}}"
export LC_ALL=C

# Named nowhere, identifying nobody — safe to keep in the public file.
STRUCTURAL_PATTERN='/home/[A-Za-z0-9]|/Users/[A-Za-z0-9]|~/work/[A-Za-z0-9]|@gmail[.]com'
STRUCTURAL_COUNT=$(( $(printf '%s' "$STRUCTURAL_PATTERN" | tr -cd '|' | wc -c) + 1 ))

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

# The one door for a grep that matches PATTERN: UTF-8 locale for that command only, binary files read as text.
match_grep() {
  LC_ALL="$match_locale" grep --binary-files=text "$@"
}

# True when the line still matches PATTERN after benign tokens and the configured
# ignore tokens are removed. Ignore tokens are lowercase regexes (the terms-file
# convention), so they are applied to the lowercased line; PATTERN matches with -i.
line_is_real_hit() {
  local line tok
  line="$(printf '%s ' "$1" | sed -E "s#${BENIGN_TOKENS}([^A-Za-z0-9_-])#<BENIGN>\2#g")" || return 2
  if (( ${#ignore_tokens[@]} > 0 )); then
    line="$(printf '%s' "$line" | tr '[:upper:]' '[:lower:]')" || return 2
    for tok in "${ignore_tokens[@]}"; do
      line="$(printf '%s' "$line" | sed -E "s#${tok}#<IGNORED>#g")" || return 2
    done
  fi
  match_grep -qiE "$PATTERN" <<<"$line"
}

usage() {
  echo "Usage: leak-check.sh [--range OLD NEW [--remote NAME] | --files f1 [f2 ...]]" >&2
  exit 2
}

mode="staged"
range_old=""
range_new=""
range_remote=""
files=()

if [[ $# -eq 0 ]]; then
  mode="staged"
elif [[ "$1" == "--range" ]]; then
  # --remote NAME: the remote the push goes to (pre-push's $1). A new ref's commits that configured
  # remote already holds were judged by the push that published them; another remote's were not, and
  # a target that is no configured remote (a URL, or no --remote) excludes nothing.
  if [[ $# -eq 5 && "$4" == "--remote" && -n "$5" ]]; then range_remote="$5"; else [[ $# -eq 3 ]] || usage; fi
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
structural_only=0
while IFS= read -r term || [[ -n "$term" ]]; do
  term="${term%$'\r'}"
  [[ "$term" =~ ^[[:space:]]*# ]] && continue
  [[ -z "${term//[[:space:]]/}" ]] && continue
  case "$term" in
    '!structural-only') structural_only=1; continue ;;
    '!ignore-token '*) ignore_tokens+=("${term#!ignore-token }"); continue ;;
    '!ignore-path '*) ignore_paths+=("${term#!ignore-path }"); continue ;;
    '!'*) echo "leak-check: FAILED — unknown directive in $terms_file: $term" >&2; exit 1 ;;
  esac
  terms+=("$term")
done < "$terms_file"
if (( ${#ignore_paths[@]} > 0 )); then
  printf 'leak-check: %d configured path ignore(s) — NOT scanned: %s\n' "${#ignore_paths[@]}" "${ignore_paths[*]}" >&2
fi

if (( structural_only )); then
  if (( ${#terms[@]} + ${#ignore_tokens[@]} + ${#ignore_paths[@]} > 0 )); then
    echo "leak-check: FAILED — !structural-only cannot be mixed with private terms or ignores: $terms_file" >&2
    exit 1
  fi
  echo "leak-check: PRIVATE COVERAGE SKIPPED — explicit structural-only policy; scanning $STRUCTURAL_COUNT structural pattern(s)" >&2
elif (( ${#terms[@]} == 0 )); then
  echo "leak-check: FAILED — terms file has zero usable terms: $terms_file" >&2
  echo "leak-check: an empty denylist finds nothing, which is not the same as finding nothing." >&2
  exit 1
fi

# The locale match_grep runs under: the first of the caller's own, C.UTF-8 and UTF-8 (the macOS name) under
# which a two-byte letter reads as one character and -i pairs both ASCII and non-ASCII letters. None does:
# refuse, never scan under a byte locale.
match_locale=""
tried_locales=""
for candidate in "$caller_locale" C.UTF-8 UTF-8; do
  [[ -n "$candidate" ]] || continue
  [[ " $tried_locales " == *" $candidate "* ]] && continue
  tried_locales="${tried_locales:+$tried_locales }$candidate"
  match_locale="$candidate"
  # A two-byte letter is one character, and -i pairs É/é and I/i: zh_CN.GB18030 passes the first alone,
  # tr_TR.UTF-8 pairs no I/i, and under either a term's other case would scan clean.
  if printf '\303\251\n' | match_grep -qE '^.$' \
    && printf '\303\211\n' | match_grep -qiE $'^\303\251$' \
    && printf 'I\n' | match_grep -qiE '^i$'; then
    break
  fi
  match_locale=""
done
if [[ -z "$match_locale" ]]; then
  echo "leak-check: FAILED — no UTF-8 locale for grep (tried: $tried_locales); under a byte locale a term's '.' or bracket misses a multibyte letter; refusing to scan" >&2
  exit 1
fi

PATTERN="$STRUCTURAL_PATTERN"
if (( ! structural_only )); then
  terms_alt="$(IFS='|'; printf '%s' "${terms[*]}")"
  PATTERN="(${terms_alt}|${STRUCTURAL_PATTERN})"
fi

# Validate at data entry: a term or ignore token the tools cannot compile would otherwise
# fail open — every --range hit judged "suppressed", a --range grep that "finds nothing".
# Both checks run before any mode touches git or a file, and an unusable terms file stops
# the gate the same way in every mode.
if (( ${#ignore_tokens[@]} > 0 )); then
  for tok in "${ignore_tokens[@]}"; do
    if ! printf '' | sed -E "s#${tok}#<IGNORED>#g" > /dev/null; then
      echo "leak-check: FAILED — unusable ignore token in $terms_file: $tok (sed -E rejects it; a token is a lowercase ERE without '#')" >&2
      exit 1
    fi
  done
fi
match_grep -qiE "$PATTERN" < /dev/null && rc=0 || rc=$?
if (( rc >= 2 )); then
  echo "leak-check: FAILED — the terms in $terms_file do not compile as one extended regex (grep rc=$rc); refusing to scan" >&2
  exit 1
fi

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

# Private policy inputs are self-excluded; required public licenses are justified below.
# leak-check.sh is NOT excluded: it scans itself.
terms_rel=""
case "$terms_file" in
  "$repo_root"/*) terms_rel="${terms_file#"$repo_root"/}" ;;
esac

# placeholder-map holds source-specific replacement values; leak-terms holds the
# private denylist. A configured in-repo denylist is the same private policy input.
# Licenses require public copyright attribution; exclude them, but always scan hooks.
diff_excludes=(
  ':(exclude)LICENSE'
  ':(exclude).opencode/LICENSE'
  ':(exclude)scripts/placeholder-map.tsv'
  ':(exclude)scripts/leak-terms.txt'
)
[[ -z "$terms_rel" ]] || diff_excludes+=(":(exclude,literal)$terms_rel")
for glob in ${ignore_paths[@]+"${ignore_paths[@]}"}; do
  diff_excludes+=(":(exclude,glob)$glob")
done

is_excluded_path() {
  local p="$1" glob
  case "$p" in
    LICENSE|.opencode/LICENSE|scripts/placeholder-map.tsv|scripts/leak-terms.txt) return 0 ;;
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

  # A `+++` line is a file header only between `diff --git` and the file's first `@@`: inside a hunk an
  # added line that itself starts with "++" reads "+++ …" too, and it is content to judge. A hunk's lines
  # all carry a one-character prefix, so `diff --git` and `@@` at column 0 are always headers.
  local in_hunk=0
  while IFS= read -r line; do
    if [[ "$line" == "diff --git "* ]]; then
      in_hunk=0
    elif [[ "$line" == "@@ "* ]]; then
      in_hunk=1
    elif (( ! in_hunk )) && [[ "$line" == "+++ /dev/null" ]]; then
      file=""
    elif (( ! in_hunk )) && [[ "$line" == "+++ b/"* ]]; then
      file="${line#+++ b/}"
    elif (( ! in_hunk )) && [[ "$line" == "+++"* ]]; then
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

    local matches rc
    matches="$(printf '%s\n' "${contents[@]}" | match_grep -niE "$PATTERN")" && rc=0 || rc=$?
    if (( rc >= 2 )); then
      local error_seen=$'\n'
      for f in "${files[@]}"; do
        if [[ "$error_seen" != *$'\n'"$f"$'\n'* ]]; then
          error_seen+="$f"$'\n'
          printf 'SCAN-ERROR %s: %s diff matcher failed (rc=%d) — refusing clean\n' "${f:-<unattributed>}" "$mode" "$rc" >&2
        fi
      done
      return 1
    fi
    if [[ -n "$matches" ]]; then
      local idx content
      while IFS=: read -r idx content; do
        if line_is_real_hit "$content"; then
          printf 'LEAK %s: %s\n' "${files[idx-1]}" "$content"
        else
          rc=$?
          if (( rc >= 2 )); then
            printf 'SCAN-ERROR %s: matched line could not be judged (rc=%d) — refusing clean\n' "${files[idx-1]}" "$rc"
          else
            suppressed=$((suppressed + 1))
          fi
        fi
      done <<< "$matches"
    fi
  fi

  {
    printf 'added_lines=%d\n' "$added_lines"
    printf 'distinct_files=%d\n' "$distinct_files"
    printf 'term_count=%d\n' "${#terms[@]}"
    printf 'structural_patterns=%d\n' "$STRUCTURAL_COUNT"
    printf 'suppressed=%d\n' "$suppressed"
    printf 'unattributed=%d\n' "$unattributed"
  } > "$coverage_file"

  (( suppressed > 0 )) && printf 'leak-check: %d benign- or ignore-token line(s) suppressed by the documented allowlist and the configured ignore tokens\n' "$suppressed" >&2
  return 0
}

hits_file="$(mktemp)"
coverage_file="$(mktemp)"
commits_file="$(mktemp)"
trap 'rm -f "$hits_file" "$coverage_file" "$commits_file"' EXIT

range_diff_stream() {
  local old_type commit count=0 empty_tree remote_list known remote_name
  local ref_list ref object nested_remote revisions
  if ! old_type="$(repo_git cat-file -t "$range_old")"; then
    echo "leak-check: FAILED — cannot classify range base $range_old; commit enumeration did not run" >&2
    return 1
  fi
  # Commit enumeration walks commits: a tip that is a tree or a blob has none, and would read as an
  # empty push. Refuse it by name.
  if ! repo_git cat-file -e "${range_new}^{commit}" 2> /dev/null; then
    echo "leak-check: FAILED — range tip $range_new does not resolve to a commit (a tree or blob ref has no commits to enumerate); refusing to report clean" >&2
    return 1
  fi
  case "$old_type" in
    tree)
      empty_tree="$(repo_git hash-object -t tree /dev/null)" || return 1
      if [[ "$range_old" != "$empty_tree" ]]; then
        echo 'leak-check: FAILED — a tree range base must be the empty-tree new-ref sentinel' >&2
        return 1
      fi
      # A new ref pushed to a configured remote: every commit that remote does not hold; what it holds
      # was judged by the push that published it, and another remote's refs prove nothing about this
      # target. A target git does not know by name (a URL pushed to directly, or no --remote at all)
      # has no provably pre-scanned refs: every reachable commit is scanned, no remote excluded.
      known=""
      if [[ -n "$range_remote" ]]; then
        if ! remote_list="$(repo_git remote)"; then
          echo "leak-check: FAILED — cannot list the configured remotes to exclude those $range_remote holds" >&2
          return 1
        fi
        while IFS= read -r remote_name; do
          [[ "$remote_name" == "$range_remote" ]] && known=1
        done <<< "$remote_list"
      fi
      if [[ -n "$known" ]]; then
        if ! ref_list="$(repo_git for-each-ref --format='%(objectname) %(refname)' refs/remotes/)"; then
          echo "leak-check: FAILED — cannot list remote-tracking refs for $range_remote; commit enumeration did not run" >&2
          return 1
        fi
        revisions="$range_new"
        while read -r object ref; do
          [[ "$ref" == "refs/remotes/$range_remote/"* ]] || continue
          # Git's --remotes glob crosses slashes. A configured remote beneath this target owns
          # its own namespace; ambiguous branch names there are scanned conservatively too.
          nested_remote=""
          while IFS= read -r remote_name; do
            if [[ "$remote_name" == "$range_remote/"* && "$ref" == "refs/remotes/$remote_name/"* ]]; then
              nested_remote=1
              break
            fi
          done <<< "$remote_list"
          [[ -n "$nested_remote" ]] || revisions+=$'\n^'"$object"
        done <<< "$ref_list"
        repo_git rev-list --stdin <<< "$revisions" > "$commits_file" || known=failed
      else
        repo_git rev-list "$range_new" > "$commits_file" || known=failed
      fi
      if [[ "$known" == failed ]]; then
        echo "leak-check: FAILED — cannot enumerate commits reachable from $range_new that the push target does not hold" >&2
        return 1
      fi
      ;;
    commit|tag)
      if ! repo_git rev-list "$range_new" "^$range_old" > "$commits_file"; then
        echo "leak-check: FAILED — cannot enumerate newly reachable commits in $range_old..$range_new" >&2
        return 1
      fi
      ;;
    *)
      echo "leak-check: FAILED — unsupported range base type $old_type; commit enumeration did not run" >&2
      return 1
      ;;
  esac
  while IFS= read -r commit; do
    [[ -n "$commit" ]] || continue
    count=$((count + 1))
    # -m examines additions against EVERY merge parent; --root includes root commits.
    if ! repo_git diff-tree --root -m --no-commit-id -r --text -U0 --no-color "$commit" -- . "${diff_excludes[@]}"; then
      echo "leak-check: FAILED — cannot scan diff for reachable commit $commit" >&2
      return 1
    fi
  done < "$commits_file"
  printf 'leak-check: commit enumeration completed — %d newly reachable commit(s), merges included\n' "$count" >&2
}

case "$mode" in
  staged)
    repo_git diff --cached --text -U0 --no-color -- . "${diff_excludes[@]}" \
      | scan_diff_stream > "$hits_file"
    ;;
  range)
    if ! range_diff_stream | scan_diff_stream > "$hits_file"; then
      echo 'leak-check: FAILED — reachable-commit scan failed; refusing to report clean' >&2
      exit 1
    fi
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
    regular=()
    for f in "${files[@]}"; do
      if is_excluded_path "$f"; then
        excluded=$((excluded + 1))
        continue
      fi
      if [[ -f "$f" ]]; then
        scanned=$((scanned + 1))
        regular+=("$f")
      else
        skipped=$((skipped + 1))
        printf 'NOT-SCANNED %s: not a regular file (deleted or misnamed) — examined by nothing, counted as clean by nothing\n' "$f" >&2
      fi
    done
    if (( ${#regular[@]} > 0 )); then
      match_grep -niEH --null -e "$PATTERN" -- "${regular[@]}" > "$coverage_file" 2>/dev/null && rc=0 || rc=$?
      if (( rc >= 2 )); then
        for f in "${regular[@]}"; do
          matches="$(match_grep -niE -e "$PATTERN" -- "$f")" && rc=0 || rc=$?
          if (( rc >= 2 )); then
            printf 'SCAN-ERROR %s: leak-check could NOT read this file (grep rc=%d) — treated as FAILURE, never as clean\n' "$f" "$rc" >> "$hits_file"
          elif [[ -n "$matches" ]]; then
            while IFS=: read -r lnum content; do
              if line_is_real_hit "$content"; then
                printf 'LEAK %s: %s\n' "$f" "$content" >> "$hits_file"
              else
                rc=$?
                if (( rc >= 2 )); then
                  printf 'SCAN-ERROR %s: matched line could not be judged (rc=%d) — refusing clean\n' "$f" "$rc" >> "$hits_file"
                else
                  suppressed=$((suppressed + 1))
                fi
              fi
            done <<< "$matches"
          fi
        done
      else
        matched_files=()
        matched_contents=()
        while IFS= read -r -d '' f && IFS=: read -r lnum content; do
          matched_files+=("$f")
          matched_contents+=("$content")
        done < "$coverage_file"
        if (( ${#matched_files[@]} > 0 )); then
          # Apply line_is_real_hit's substitutions to the candidate lines together;
          # forking its sed and grep pipeline per line dominates a whole-tree scan.
          normalized_file="$coverage_file.normalized"
          lower_file="$coverage_file.lower"
          real_file="$coverage_file.real"
          trap 'rm -f "$hits_file" "$coverage_file" "$commits_file" "$normalized_file" "$lower_file" "$real_file"' EXIT
          printf '%s \n' "${matched_contents[@]}" \
            | sed -E "s#${BENIGN_TOKENS}([^A-Za-z0-9_-])#<BENIGN>\2#g" > "$normalized_file"
          if (( ${#ignore_tokens[@]} > 0 )); then
            tr '[:upper:]' '[:lower:]' < "$normalized_file" > "$lower_file"
            sed_args=()
            for tok in "${ignore_tokens[@]}"; do
              sed_args+=(-e "s#${tok}#<IGNORED>#g")
            done
            sed -E "${sed_args[@]}" "$lower_file" > "$normalized_file"
          fi
          match_grep -niE "$PATTERN" "$normalized_file" > "$real_file" && rc=0 || rc=$?
          if (( rc >= 2 )); then
            # The matched lines could not be judged: name every source file they came from,
            # once each, never the temp file the judge read. First-seen order, same
            # newline-delimited seen-string as scan_diff_stream (bash 3.2 has no associative arrays).
            judge_seen=$'\n'
            for f in "${matched_files[@]}"; do
              if [[ "$judge_seen" != *$'\n'"$f"$'\n'* ]]; then
                judge_seen+="$f"$'\n'
                printf "SCAN-ERROR %s: leak-check could NOT judge this file's matched lines (grep rc=%d) — treated as FAILURE, never as clean\n" "$f" "$rc" >> "$hits_file"
              fi
            done
          else
            real_indices=()
            while IFS=: read -r lnum content; do
              real_indices[lnum]=1
            done < "$real_file"
            for (( i=0; i<${#matched_files[@]}; i++ )); do
              if [[ -n "${real_indices[i+1]:-}" ]]; then
                printf 'LEAK %s: %s\n' "${matched_files[i]}" "${matched_contents[i]}" >> "$hits_file"
              else
                suppressed=$((suppressed + 1))
              fi
            done
          fi
        fi
      fi
    fi
    if (( scanned == 0 && skipped > 0 )); then
      printf 'SCAN-ERROR: --files named %d path(s), %d excluded by design, and every one of the remaining %d was NOT a regular file — nothing was examined, refusing to report clean\n' \
        "${#files[@]}" "$excluded" "$skipped" >> "$hits_file"
    fi
    printf 'leak-check: scanned %d file(s) (%d excluded by design, %d not a regular file) against %d private term(s) + %d structural pattern(s); %d benign-token line(s) suppressed\n' \
      "$scanned" "$excluded" "$skipped" "${#terms[@]}" "$STRUCTURAL_COUNT" "$suppressed" >&2
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
