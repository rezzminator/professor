#!/usr/bin/env bash
set -euo pipefail

# baseline-sync.sh — release Step-0 gate, mechanical half. Compares .professor/VERSION
# against the blueprint origin's published tags. A version gap consisting solely of
# SELF ROUND-TRIPS (releases whose content this repo already contains: every gap tag
# reachable from the local blueprint clone's main, zero commits on origin/main the
# clone lacks) is synced mechanically — VERSION + manifest version/updated_at + one
# drift.md update-history row. Anything else is genuine peer content: exit 10, resync
# this install from the Professor framework repo. Keeps the tag-collision guarantee: after a sync, VERSION equals the
# highest published tag, so the next computed release version exceeds every
# published tag.

DRY_RUN=0
for arg in "$@"; do
  case "$arg" in
    --dry-run|-n) DRY_RUN=1 ;;
    *)
      echo "baseline-sync: unknown argument: $arg" >&2
      exit 1
      ;;
  esac
done

if [[ -z "${PROJECT_ROOT:-}" ]]; then
  PROJECT_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || true)"
fi
if [[ -z "${PROJECT_ROOT:-}" ]]; then
  echo "baseline-sync: could not determine PROJECT_ROOT (not inside a git repo; set PROJECT_ROOT env)" >&2
  exit 1
fi

VERSION_FILE="$PROJECT_ROOT/.professor/VERSION"
MANIFEST_FILE="$PROJECT_ROOT/.professor/manifest.json"
DRIFT_FILE="$PROJECT_ROOT/.professor/drift.md"
DRIFT_HISTORY_FILE="$PROJECT_ROOT/.professor/drift-history.md"

if [[ ! -f "$VERSION_FILE" || ! -f "$MANIFEST_FILE" ]]; then
  echo "baseline-sync: $PROJECT_ROOT/.professor/{VERSION,manifest.json} not found" >&2
  exit 1
fi

LOCAL_VER="$(tr -d '[:space:]' < "$VERSION_FILE")"

BLUEPRINT_REPO="$(jq -r '.interview.blueprint_repo // empty' "$MANIFEST_FILE")"
CLONE_PATH_RAW="$(jq -r '.interview.blueprint_clone_path // empty' "$MANIFEST_FILE")"
if [[ -z "$CLONE_PATH_RAW" ]]; then
  echo "baseline-sync: manifest.json .interview.blueprint_clone_path is missing/null" >&2
  exit 1
fi

# leading ~ expansion
case "$CLONE_PATH_RAW" in
  "~"|"~/"*) CLONE_PATH_RAW="${HOME}${CLONE_PATH_RAW#\~}" ;;
esac

CLONE="${BLUEPRINT_CLONE:-$CLONE_PATH_RAW}"

if [[ ! -d "$CLONE/.git" ]]; then
  echo "baseline-sync: blueprint clone not found at $CLONE — clone ${BLUEPRINT_REPO:-the blueprint repo} there first" >&2
  exit 1
fi

if ! git -C "$CLONE" fetch --tags --quiet origin; then
  echo "baseline-sync: git fetch --tags failed for $CLONE (network needed)" >&2
  exit 1
fi
if ! git -C "$CLONE" fetch --quiet origin main; then
  echo "baseline-sync: git fetch origin main failed for $CLONE (network needed)" >&2
  exit 1
fi

LATEST_VER="$(git -C "$CLONE" tag --list 'v*' --sort=-v:refname | head -1)"
LATEST_VER="${LATEST_VER#v}"

if [[ -z "$LATEST_VER" ]]; then
  echo "baseline-sync: no v* tags found in $CLONE" >&2
  exit 1
fi

vercmp() {
  # echoes eq|gt|lt for "$1" vs "$2"
  if [[ "$1" == "$2" ]]; then
    echo eq
    return
  fi
  local top
  top="$(printf '%s\n%s\n' "$1" "$2" | sort -V | tail -1)"
  if [[ "$top" == "$1" ]]; then
    echo gt
  else
    echo lt
  fi
}

CMP="$(vercmp "$LOCAL_VER" "$LATEST_VER")"

if [[ "$CMP" == "eq" ]]; then
  echo "baseline-sync: IN-SYNC at v${LOCAL_VER}"
  exit 0
fi

if [[ "$CMP" == "gt" ]]; then
  echo "baseline-sync: AHEAD — .professor/VERSION v${LOCAL_VER} exceeds latest published tag v${LATEST_VER}; nothing to sync"
  exit 0
fi

# LOCAL < LATEST: gap analysis
behind="$(git -C "$CLONE" rev-list --count main..origin/main)"
if (( behind > 0 )); then
  echo "baseline-sync: PEER-CONTENT — ${behind} commit(s) on origin/main not in the local clone; resync this install from the Professor framework repo" >&2
  exit 10
fi

mapfile -t ALL_TAGS < <(git -C "$CLONE" tag --list 'v*' | sed 's/^v//' | sort -V)
GAP_TAGS=()
for t in "${ALL_TAGS[@]}"; do
  if [[ "$(vercmp "$t" "$LOCAL_VER")" == "gt" ]]; then
    GAP_TAGS+=("$t")
  fi
done

if [[ ${#GAP_TAGS[@]} -eq 0 ]]; then
  echo "baseline-sync: PEER-CONTENT — no gap tags found above v${LOCAL_VER} though LOCAL < LATEST; resync this install from the Professor framework repo" >&2
  exit 10
fi

for t in "${GAP_TAGS[@]}"; do
  if ! git -C "$CLONE" merge-base --is-ancestor "v${t}" main; then
    echo "baseline-sync: PEER-CONTENT — tag v${t} not contained in the local clone; resync this install from the Professor framework repo" >&2
    exit 10
  fi
done

GAP_LIST="$(printf 'v%s, ' "${GAP_TAGS[@]}")"
GAP_LIST="${GAP_LIST%, }"

echo "baseline-sync: SELF-ROUND-TRIP v${LOCAL_VER} → v${LATEST_VER} (${GAP_LIST})"

TODAY="$(date +%Y-%m-%d)"
NEW_ROW="| v${LOCAL_VER} | v${LATEST_VER} | Self round-trip sync (baseline-sync.sh): ${GAP_LIST} already contained in the local blueprint clone; no peer content consumed. |"

if (( DRY_RUN )); then
  echo "would: write v${LATEST_VER} to .professor/VERSION"
  echo "would: set manifest.json .version=\"${LATEST_VER}\" .updated_at=\"${TODAY}\""
  echo "would: append update-history row to drift.md: ${NEW_ROW}"
  echo "would: enforce the 5-row update-history cap (archive surplus rows to drift-history.md if any)"
  echo "baseline-sync: DRY-RUN — no files written"
  exit 0
fi

# 1. VERSION
printf '%s\n' "$LATEST_VER" > "$VERSION_FILE"

# 2. manifest.json (atomic)
MANIFEST_TMP="$(mktemp "${MANIFEST_FILE}.XXXXXX")"
jq --arg v "$LATEST_VER" --arg d "$TODAY" '.version = $v | .updated_at = $d' "$MANIFEST_FILE" > "$MANIFEST_TMP"
mv "$MANIFEST_TMP" "$MANIFEST_FILE"

# 3 & 4. drift.md table surgery + history-cap enforcement
if [[ ! -f "$DRIFT_FILE" ]]; then
  echo "baseline-sync: $DRIFT_FILE not found — cannot append update-history row" >&2
  exit 1
fi

heading_line="$(grep -n -m1 '^## Update history$' "$DRIFT_FILE" | cut -d: -f1)"
if [[ -z "$heading_line" ]]; then
  echo "baseline-sync: '## Update history' heading not found in $DRIFT_FILE" >&2
  exit 1
fi

table_start="$(awk -v s="$heading_line" 'NR>s && /^\|/{print NR; exit}' "$DRIFT_FILE")"
if [[ -z "$table_start" ]]; then
  echo "baseline-sync: no table found under '## Update history' in $DRIFT_FILE" >&2
  exit 1
fi

table_end="$(awk -v s="$table_start" 'NR<s{next} /^\|/{last=NR; next} {exit} END{print last}' "$DRIFT_FILE")"

data_start=$((table_start + 2))
data_end=$table_end
original_data_count=$((data_end - data_start + 1))
total_data_count=$((original_data_count + 1))

archived_count=0
if (( total_data_count > 5 )); then
  archived_count=$((total_data_count - 5))
fi

archive_start=$data_start
archive_end=$((data_start + archived_count - 1))
keep_start=$((archive_end + 1))
keep_end=$data_end

ARCHIVED_ROWS=""
if (( archived_count > 0 )); then
  ARCHIVED_ROWS="$(sed -n "${archive_start},${archive_end}p" "$DRIFT_FILE")"
fi

DRIFT_TMP="$(mktemp "${DRIFT_FILE}.XXXXXX")"
{
  # everything up to and including the separator row, unchanged
  head -n "$((table_start + 1))" "$DRIFT_FILE"
  # kept original data rows
  if (( archived_count > 0 )); then
    sed -n "${keep_start},${keep_end}p" "$DRIFT_FILE"
  else
    sed -n "${data_start},${data_end}p" "$DRIFT_FILE"
  fi
  # the new self-round-trip row
  printf '%s\n' "$NEW_ROW"
  # everything after the original table, unchanged (blank line, ---, Divergences…)
  tail -n +"$((table_end + 1))" "$DRIFT_FILE"
} > "$DRIFT_TMP"
mv "$DRIFT_TMP" "$DRIFT_FILE"

if (( archived_count > 0 )); then
  if [[ ! -f "$DRIFT_HISTORY_FILE" ]]; then
    cat > "$DRIFT_HISTORY_FILE" <<'EOF'
# Drift history — archived update-history rows

Archive of `.professor/drift.md` "## Update history" rows past the 5 most recent (the cap rule lives in drift.md). Rows verbatim, oldest first.

| From    | To      | Notes |
| ------- | ------- | ----- |
EOF
  fi
  printf '%s\n' "$ARCHIVED_ROWS" >> "$DRIFT_HISTORY_FILE"
fi

if (( archived_count > 0 )); then
  echo "baseline-sync: SYNCED — VERSION v${LOCAL_VER} → v${LATEST_VER}; manifest updated; drift.md row appended, ${archived_count} row(s) archived to drift-history.md"
else
  echo "baseline-sync: SYNCED — VERSION v${LOCAL_VER} → v${LATEST_VER}; manifest updated; drift.md row appended"
fi

