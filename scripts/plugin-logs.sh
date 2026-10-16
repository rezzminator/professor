#!/usr/bin/env bash
# Report every contract row and rotate by rename. Failure to look is never health.
set -euo pipefail
umask 077
command -v jq > /dev/null 2>&1 || { echo 'plugin-logs: jq absent' >&2; exit 5; }
usage() { echo "plugin-logs: $*; usage: report|rotate [--dir D] [--archive YYYY-MM-DD] [--today YYYY-MM-DD] [--root R]" >&2; exit 2; }
unreadable() { printf 'folder unreadable: %s: %s\n' "$1" "$2"; exit 4; }
mode=${1:-}; [[ "$mode" == report || "$mode" == rotate ]] || usage 'expected report or rotate'
shift
dir=${PLUGIN_LOG_DIR:-}; root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
archive=; today=
while [ "$#" -gt 0 ]; do
  [ "$#" -ge 2 ] || usage "missing value for $1"
  case "$1" in
    --dir) dir=$2 ;;
    --root) root=$2 ;;
    --archive) archive=$2 ;;
    --today) today=$2 ;;
    *) usage "unknown argument $1" ;;
  esac
  shift 2
done
[ -n "$dir" ] || usage 'PLUGIN_LOG_DIR unset and no --dir'
[[ "$mode" == report || -z "$archive" ]] || usage '--archive is report only'
[[ "$mode" == rotate || -z "$today" ]] || usage '--today is rotate only'
valid_date() {
  jq -en --arg d "$1" '$d | test("^[0-9]{4}-[0-9]{2}-[0-9]{2}$") and (try ((strptime("%Y-%m-%d") | mktime | strftime("%Y-%m-%d")) == .) catch false)' > /dev/null
}
if [ -n "$archive" ]; then valid_date "$archive" || usage "invalid archive date $archive"; fi
if [ -n "$today" ]; then valid_date "$today" || usage "invalid today date $today"; fi
[ -e "$dir" ] || { printf 'no log folder: %s\n' "$dir"; exit 3; }
[ -d "$dir" ] && [ -r "$dir" ] && [ -x "$dir" ] || unreadable "$dir" 'directory is not readable/searchable'
scratch=$(mktemp -d "${TMPDIR:-/tmp}/plugin-logs.XXXXXX") || unreadable "$dir" 'mktemp failed'
cleanup() {
  local status=$?
  rm -rf -- "$scratch" || status=4
  exit "$status"
}
trap cleanup EXIT
read_mtime() {
  if mtime=$(stat -c %Y -- "$1" 2>/dev/null); then :
  elif mtime=$(stat -f %m -- "$1" 2> "$scratch/err"); then :
  else unreadable "$1" "$(cat "$scratch/err")"; fi
}
if ! git config --file "$root/.gitmodules" --get-regexp '^submodule\..*\.path$' > "$scratch/modules" 2> "$scratch/err"; then
  echo "DERIVE-FAILED: no plugins in .gitmodules ($root/.gitmodules): $(cat "$scratch/err")" >&2; exit 2
fi
: > "$scratch/roster"
while read -r key path; do
  case "$path" in plugins/*)
    plugin=${path#plugins/}
    [[ "$plugin" =~ ^[a-z0-9]+(-[a-z0-9]+)*$ ]] || usage "DERIVE-FAILED: invalid plugin path $path"
    printf '%s\n' "$plugin" >> "$scratch/roster" ;;
  esac
done < "$scratch/modules"
[ -s "$scratch/roster" ] || usage 'DERIVE-FAILED: no plugins in .gitmodules'
# Also account for a writer whose checkout has left the roster, without hiding its rows.
find "$dir/." -mindepth 1 -maxdepth 1 -print0 > "$scratch/children" 2> "$scratch/err" || unreadable "$dir" "$(cat "$scratch/err")"
while IFS= read -r -d '' path; do
  [ -d "$path" ] || unreadable "$path" 'unexpected entry in log root'
  plugin=${path##*/}
  [[ "$plugin" =~ ^[a-z0-9]+(-[a-z0-9]+)*$ ]] || unreadable "$path" 'invalid plugin name'
  printf '%s\n' "$plugin" >> "$scratch/roster"
done < "$scratch/children"
LC_ALL=C sort -u "$scratch/roster" > "$scratch/plugins"
check_dir() {
  [ -d "$1" ] && [ -r "$1" ] && [ -x "$1" ] || unreadable "$1" 'directory is not readable/searchable'
}
vanished_level() {
  if [ ! -e "$1" ] && [ ! -L "$1" ]; then
    printf 'skipped vanished level file: %s\n' "$1"
    return 0
  fi
  return 1
}
list_levels() { # one inventory, including same-day archive collision suffixes
  local folder=$1 archived=$2 name
  : > "$scratch/levels"
  [ -e "$folder" ] || return 0
  check_dir "$folder"
  find "$folder" -mindepth 1 -maxdepth 1 -print0 > "$scratch/list" 2> "$scratch/err" || unreadable "$folder" "$(cat "$scratch/err")"
  jq -jRs 'split("\u0000") | map(select(length > 0)) | sort[] | . + "\u0000"' "$scratch/list" > "$scratch/sorted-list"
  while IFS= read -r -d '' path; do
    name=${path##*/}
    if [[ "$name" =~ ^(errors|warnings|info)\.jsonl(\.1)?$ ]] ||
       { [ "$archived" = 1 ] && [[ "$name" =~ ^(errors|warnings|info)-[0-9]+\.jsonl(\.1)?$ ]]; }; then
      if [ ! -f "$path" ] || [ ! -r "$path" ]; then
        if [ "$mode" = rotate ] && vanished_level "$path"; then continue; fi
        unreadable "$path" 'level file is unreadable'
      fi
      printf '%s\0' "$path" >> "$scratch/levels"
    fi
  done < "$scratch/sorted-list"
}
archive_dates() { # emits only valid date directories; invalid names are kept, not pruned
  local base=$1 candidate
  : > "$scratch/dates"
  [ -e "$base" ] || return 0
  check_dir "$base"
  find "$base" -mindepth 1 -maxdepth 1 -print0 > "$scratch/archive-list" 2> "$scratch/err" || unreadable "$base" "$(cat "$scratch/err")"
  while IFS= read -r -d '' candidate; do
    if [ -d "$candidate" ] && valid_date "${candidate##*/}"; then
      printf '%s\n' "${candidate##*/}" >> "$scratch/dates"
    fi
  done < "$scratch/archive-list"
}
if [ "$mode" = rotate ]; then
  [ -n "$today" ] || today=$(jq -nr 'now | strftime("%Y-%m-%d")')
  cutoff=$(jq -nr --arg d "$today" '$d + "T00:00:00Z" | fromdateiso8601 - 28*86400')
  while IFS= read -r plugin; do
    pdir="$dir/$plugin"
    [ -e "$pdir" ] || continue
    check_dir "$pdir"
    archive_dates "$pdir/archive"
    list_levels "$pdir" 0
    if [ -s "$scratch/levels" ]; then
      dest="$pdir/archive/$today"
      mkdir -p -- "$dest" 2> "$scratch/err" || unreadable "$dest" "$(cat "$scratch/err")"
      check_dir "$dest"
      while IFS= read -r -d '' path; do
        name=${path##*/}; target="$dest/$name"; n=2
        while [ -e "$target" ] || [ -L "$target" ]; do
          target="$dest/${name%%.jsonl*}-$n.jsonl${name#*.jsonl}"
          n=$((n + 1))
        done
        if ! mv -- "$path" "$target" 2> "$scratch/err"; then
          if vanished_level "$path"; then continue; fi
          unreadable "$path" "$(cat "$scratch/err")"
        fi
      done < "$scratch/levels"
    fi
    while IFS= read -r date; do
      epoch=$(jq -nr --arg d "$date" '$d+"T00:00:00Z"|fromdateiso8601')
      if [ "$epoch" -lt "$cutoff" ]; then
        rm -rf -- "$pdir/archive/$date" 2> "$scratch/err" || unreadable "$pdir/archive/$date" "$(cat "$scratch/err")"
      fi
    done < "$scratch/dates"
    decisions="$pdir/decisions"
    if [ -e "$decisions" ]; then
      check_dir "$decisions"
      find "$decisions" -mindepth 1 -maxdepth 1 -type f -print0 > "$scratch/decisions" 2> "$scratch/err" || unreadable "$decisions" "$(cat "$scratch/err")"
      while IFS= read -r -d '' path; do
        read_mtime "$path"
        if [ "$mtime" -lt "$cutoff" ]; then
          rm -- "$path" 2> "$scratch/err" || unreadable "$path" "$(cat "$scratch/err")"
        fi
      done < "$scratch/decisions"
    fi
  done < "$scratch/plugins"
  printf 'rotated: %s · %s\n' "$dir" "$today"
  exit 0
fi
: > "$scratch/rows"; : > "$scratch/rotates"
skipped=0; archive_found=0
# Normalize valid rows per line; malformed rows are skipped.
read_rows() { # path plugin level -> normalized valid rows
  local file=$1 plugin=$2 level=$3 line part normalized status
  { exec 9< "$file"; } 2> "$scratch/err" || unreadable "$file" "$(cat "$scratch/err")"
  while IFS= LC_ALL=C read -r -n 4096 line <&9 || [ -n "$line" ]; do
    if [ "${#line}" -ge 4096 ]; then
      while IFS= LC_ALL=C read -r -n 4096 part <&9 && [ "${#part}" -ge 4096 ]; do :; done
      skipped=$((skipped + 1))
      continue
    fi
    status=0
    normalized=$(jq -cen --arg row "$line" --arg p "$plugin" --arg l "$level" '
      try (
      ($row | fromjson) as $r |
      select(($row|utf8bytelength)+1 <= 4096) |
      select(($r|type)=="object") |
      select(($r|keys)-["ts","plugin","level","event","msg","where","session","detail"] == []) |
      select($r.plugin==$p and $r.level==$l) |
      select(($r.event|type)=="string" and ($r.event|test("^[a-z0-9]+(-[a-z0-9]+)*$"))) |
      select(($r.msg|type)=="string" and ($r.where|type)=="string" and ($r.where|length)>0) |
      select(($r|has("session")|not) or ($r.session|type)=="string") |
      select(($r|has("detail")|not) or ($r.detail|type)=="object") |
      select(($r.ts|type)=="string" and ($r.ts|test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\\.[0-9]+)?Z$"))) |
      ($r.ts|sub("\\.[0-9]+Z$";"Z")) as $base |
      ($base|fromdateiso8601) as $seconds |
      select(($seconds|strftime("%Y-%m-%dT%H:%M:%SZ"))==$base) |
      $r + {_time: ($seconds + ([$r.ts|capture("(?<f>\\.[0-9]+)Z$").f|"0"+.|tonumber] | .[0] // 0))}
      ) catch empty
    ' 2> "$scratch/err") || status=$?
    if [ "$status" = 0 ]; then
      printf '%s\n' "$normalized" >> "$scratch/rows"
    elif [ "$status" = 4 ]; then
      skipped=$((skipped + 1)) # malformed or noncontract row
    else
      unreadable "$file" "jq normalization failed ($status): $(cat "$scratch/err")"
    fi
  done
  exec 9<&-
}
while IFS= read -r plugin; do
  pdir="$dir/$plugin"
  [ -e "$pdir" ] || continue
  check_dir "$pdir"
  archive_dates "$pdir/archive"
  cat "$scratch/dates" >> "$scratch/rotates"
  folder=$pdir; archived=0
  if [ -n "$archive" ]; then
    folder="$pdir/archive/$archive"; archived=1
    if [ -e "$folder" ]; then check_dir "$folder"; archive_found=1; fi
  fi
  list_levels "$folder" "$archived"
  while IFS= read -r -d '' path; do
    name=${path##*/}
    case "$name" in errors*) level=error ;; warnings*) level=warning ;; info*) level=info ;; esac
    read_rows "$path" "$plugin" "$level"
  done < "$scratch/levels"
done < "$scratch/plugins"
span=$(jq -rs 'if length==0 then "none" else sort_by(._time)|.[0].ts+" .. "+.[-1].ts end' "$scratch/rows")
last=$(LC_ALL=C sort "$scratch/rotates" | tail -1)
printf 'plugin logs: %s · span: %s · last rotate: %s · contract-skipped: %s\n' "$dir" "$span" "${last:-none}" "$skipped"
if [ -n "$archive" ]; then
  if [ "$archive_found" = 1 ]; then printf 'archive: %s\n' "$archive";
  else printf 'no archive %s\n' "$archive"; fi
fi
for level in error warning; do
  if [ "$level" = error ]; then echo ERRORS; else echo WARNINGS; fi
  jq -rs --arg l "$level" '
    def safe: @json | .[1:-1];
    map(select(.level==$l)) | group_by(.plugin)[] |
    .[0].plugin, (group_by([.event,.where])[] | sort_by(._time) |
      "\(.[0].event) · \(.[0].where|safe) · \(length) · \(.[0].ts) · \(.[-1].ts) · \(.[-1].msg|safe)")
  ' "$scratch/rows"
done
echo INFO
jq -rs 'map(select(.level=="info"))|group_by(.plugin)[]|"\(.[0].plugin) \(length)"' "$scratch/rows"
jq -rs --rawfile plugins "$scratch/plugins" '
  ($plugins | split("\n") | map(select(length > 0))) - (map(.plugin) | unique) |
  "no valid rows: " + (if length == 0 then "none" else join(", ") end)
' "$scratch/rows"
