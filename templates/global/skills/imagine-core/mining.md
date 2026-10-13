# History recipes

The imagine-historian's tested recipes. Each script is copied into `{run}/scripts/` as written and run there; its outputs, caches and the store snapshot go to `{run}/work/`. You read a script's output, never the raw source it counted.

- [Layout and trust](#layout-and-trust)
- [Census — every mode's first script](#census--every-modes-first-script)
- [FACTORS — the user's own words](#factors--the-users-own-words)
- [LINEAGE — every version in four git processes](#lineage--every-version-in-four-git-processes)
- [MINE — runs, metrics, cost](#mine--runs-metrics-cost)
- [A Haiku reader's brief](#a-haiku-readers-brief)

## Layout and trust

- Every script caps its reads (`CAP`, `MAX_LINES`, `LONG`), prints `CAPPED` when it hits a cap, and prints `FAILED: {what}` for a source it could not read — a script that stops early says so too — so a short output never passes for an empty source.
- Before trusting a script, recount three of its rows by hand with a one-line `jq`, `grep` or `git show`. A mismatch is a bug in the script, fixed before any number leaves.
- A fix to a script reruns it on its cache: every git and store result is cached under `{run}/work/`, so a rerun takes seconds.

## Census — every mode's first script

`census.sh {run} {repo} '{a|b|c}'` prints one row per source — `source exists files bytes hits state` — about 2 KB; you open only the rows with hits. Claude config dirs come from `$CLAUDE_CONFIG_DIR`, `$HOME/.claude` and the store snapshot's `agents.config_dir`/`seat_dir` (taken first, § MINE); seats sharing one `projects/` through a symlink count once. Codex and OpenCode are counted here and read through `pfm transcript show {locator} --only prompt --grep '{regex}' -i --text 300`, the locator a Codex session file or `{opencode.db}#{session id}`; never a copy of either store.

```sh
#!/usr/bin/env bash
# census.sh {run} {repo} {target-regex} — one row per source: source, exists, files, bytes, hits, state (read|empty|failed:cause).
# Out: {run}/work/census.tsv, printed too. Hits are files (or OpenCode sessions) whose text matches; a grep error is failed, never 0.
set -euo pipefail
trap 'rc=$?; [ $rc -eq 0 ] || echo "FAILED: census stopped at line $LINENO (exit $rc)" >&2' EXIT
# {target-regex} is a plain word alternation (a|b|c): grep takes it whole, the OpenCode query one LIKE per word
run=$1; repo=$2; re=$3; out=$run/work/census.tsv; mkdir -p "$run/work"
dirs=$( { echo "${CLAUDE_CONFIG_DIR:-}"; echo "$HOME/.claude"; [ -f "$run/work/callmeter-snap.db" ] &&
          sqlite3 -readonly "$run/work/callmeter-snap.db" "SELECT config_dir FROM agents UNION SELECT seat_dir FROM agents"; } |
        grep -v '^$' | while read -r d; do [ -d "$d/projects" ] && (cd "$d/projects" && pwd -P) || true; done | sort -u)
# seats sharing one projects/ through a symlink resolve to one row
row() { # name, then paths (files or dirs); the files are every regular file under them matching $glob
  local name=$1; shift; local files n bytes hits
  [ $# -gt 0 ] || { printf '%s\tno\t0\t0\t0\tempty\n' "$name"; return; }
  files=$(find "$@" -type f -name "${glob:-*}" 2>/dev/null || true); n=$(printf '%s' "$files" | grep -c . || true)
  if [ "$n" -eq 0 ]; then printf '%s\t%s\t0\t0\t0\tempty\n' "$name" "$( [ -e "$1" ] && echo yes || echo no)"; return; fi
  bytes=$(printf '%s\n' "$files" | tr '\n' '\0' | xargs -0 cat | wc -c | tr -d ' ')
  hits=$(printf '%s\n' "$files" | tr '\n' '\0' | { xargs -0 grep -l -i -E -e "$re" 2>"$run/work/census.err" || true; } | wc -l | tr -d ' ')
  [ -s "$run/work/census.err" ] && { printf '%s\tyes\t%s\t%s\t-\tfailed:%s\n' "$name" "$n" "$bytes" "$(head -c 80 "$run/work/census.err" | tr '\t\n' '  ')"; return; }
  printf '%s\tyes\t%s\t%s\t%s\t%s\n' "$name" "$n" "$bytes" "$hits" "$( [ "$hits" -gt 0 ] && echo read || echo empty)"
}
{
  printf 'source\texists\tfiles\tbytes\thits\tstate\n'
  glob=retro.md row retro "$repo/.professor/retro.md"
  glob='*.md' row research "$repo/.professor/RR"
  glob='*.md' row design-docs "$repo/docs/design"
  glob='requirements.md' row flight-requirements "$HOME/.local/state/pfm/flights"
  while read -r d; do
    glob='*.md' row "memory:$d" $(find "$d" -maxdepth 2 -type d -name memory 2>/dev/null | head -500)
    glob='*.jsonl' row "claude-transcripts:$d" "$d"
  done <<< "$dirs"
  glob='*.jsonl' row codex-transcripts "${CODEX_HOME:-$HOME/.codex}/sessions"
  oc=${XDG_DATA_HOME:-$HOME/.local/share}/opencode/opencode.db
  if [ -f "$oc" ]; then
    if s=$(sqlite3 "file:$oc?immutable=1" "SELECT count(*) FROM session" 2>&1); then
      like=$(printf '%s' "$re" | tr 'A-Z' 'a-z' | sed "s/|/%' OR lower(data) LIKE '%/g")
      h=$(sqlite3 "file:$oc?immutable=1" "SELECT count(DISTINCT session_id) FROM part WHERE lower(data) LIKE '%$like%'" 2>&1) || h="failed:$h"
      printf 'opencode\tyes\t%s\t%s\t%s\t%s\n' "$s" "$(wc -c < "$oc" | tr -d ' ')" "$h" "$(case $h in failed*) echo "failed:query";; 0) echo empty;; *) echo read;; esac)"
    else printf 'opencode\tyes\t-\t-\t-\tfailed:%s\n' "$(printf '%s' "$s" | head -c 80)"; fi
  else printf 'opencode\tno\t0\t0\t0\tempty\n'; fi
} | tee "$out"
```

## FACTORS — the user's own words

Retro inbox: print only the entries that hit, as heading plus hit lines, and read that:

```sh
awk '/^## /{h=$0; n=NR} tolower($0) ~ /{regex}/ {print n": "h; print NR": "substr($0,1,300)}' .professor/retro.md | uniq
```

Research reports and design docs: `grep -l -i -E` names the files, `collector` returns the hit passages verbatim.

Typed turns. A user record carrying `origin.kind` is typed only when it reads `human`; `task-notification`, `peer` and `plugin` records are injections. Records older than `origin` fall back to shape (no `isMeta`, no `isCompactSummary`) and are marked `fallback`. Queued turns are `queue-operation` records with `operation` `enqueue`. Either way a turn opening with `<` (a command, a paste, a notification, an agent message) or `[` (an image, a handoff, a workflow) is not typed text. A session's first typed turn is often a prompt another chat sent: marked `seed` and ranked below the turns after it. A turn past `LONG` characters is a seed or a paste: counted apart in `turns-long.tsv`, never ranked.

`turns.sh {run} '{target-regex}' '{evaluative-regex}' {config-dir}/projects` writes every hit to `turns-all.tsv` and the top 150 to `turns.tsv` — `session line date score mode length text(200) seed|turn`, about 40 KB — scored by target hits (three at most, weight 3) plus evaluative hits (five at most):

```sh
#!/usr/bin/env bash
# turns.sh {run} {target-regex} {eval-regex} {transcript-root}... — the user's typed turns that hit, scored and capped.
# Out: {run}/work/turns-all.tsv (every typed turn), {run}/work/turns.tsv (top CAP by score); prints counts, FAILED lines.
set -euo pipefail
run=$1; tre=$2; ere=$3; shift 3; cap=${CAP:-150}
mkdir -p "$run/work"; : > "$run/work/turns-all.tsv"
find "$@" -maxdepth 2 -name '*.jsonl' -not -path '*/subagents/*' | while read -r f; do
  # a transcript whose records carry origin is filtered by it; an older one falls back to shape, its rows marked
  if grep -qF '"origin":{"kind"' "$f"; then mode=origin; else mode=fallback; fi
  jq -r --arg tre "$tre" --arg ere "$ere" --arg mode "$mode" '
    def typed: if type=="string" then . elif type=="array" then (map(select(.type=="text").text)|join(" ")) else "" end;
    (if .type=="user" and (if $mode=="origin" then .origin.kind=="human" else (.isMeta!=true and .isCompactSummary!=true) end)
       then (.message.content|typed)
     elif .type=="queue-operation" and .operation=="enqueue" then (.content // "")
     else empty end) as $c
    | select($c != "" and ($c|test("^\\s*(<|\\[|This session is being continued)")|not))
    | (([([$c|scan($tre;"i")]|length), 3]|min)*3 + ([([$c|scan($ere;"i")]|length), 5]|min)) as $s
    | [.sessionId, input_line_number, (.timestamp // "")[0:10], $s, $mode, ($c|length), ($c|gsub("[\t\n\r]";" ")|.[0:200])] | @tsv' "$f" 2> "$run/work/jq.err" \
    || echo "FAILED: $f $(head -c 200 "$run/work/jq.err")" >&2
done | awk -F'\t' -v OFS='\t' '!($1 in seen){seen[$1]=1; seed=1} ($1 in seen) && seed==0 {} {s=seed; seed=0} $4>0 {print $0, (s ? "seed" : "turn")}' >> "$run/work/turns-all.tsv"
# column 8: "seed" is a session's first typed turn — often a prompt another chat sent — ranked below the turns after it
# a turn past LONG chars is a seed or a paste, not a typed turn: counted apart, never ranked
awk -F'\t' -v L="${LONG:-3000}" '$6>L' "$run/work/turns-all.tsv" > "$run/work/turns-long.tsv"
awk -F'\t' -v L="${LONG:-3000}" '$6<=L' "$run/work/turns-all.tsv" | sort -t$'\t' -k8,8r -k4,4nr -k6,6n | awk -v c="$cap" 'NR<=c' > "$run/work/turns.tsv"  # awk reads to the end: head would SIGPIPE sort under pipefail
echo "hits $(wc -l < "$run/work/turns-all.tsv") long $(wc -l < "$run/work/turns-long.tsv") kept $(wc -l < "$run/work/turns.tsv") bytes $(wc -c < "$run/work/turns.tsv") sessions $(cut -f1 "$run/work/turns.tsv" | sort -u | wc -l)"
```

You read only its count line and the score histogram (`cut -f4 turns.tsv | sort -n | uniq -c`). One Haiku reader (§ A Haiku reader's brief) takes `turns.tsv` with one order: for each row naming a criterion for judging agent work, return `{session}:{line} {date} | {metric name} | {one-line definition}`, the turn's own words never copied past four, then `ROWS {n} KEPT {n}`. Verify at most 10 returned rows with `pfm transcript show {session} --lines {line} --only prompt --text 300`. Only for sessions the ranking picks, more of the session's typed turns: `pfm transcript show {session} --only prompt --grep '{regex}' -i`. The returns stay in `{run}/work/`; `factors.md` carries the metric, its definition and the coordinates only.

## LINEAGE — every version in four git processes

History once, searched in place; every `--follow` walk in one loop:

```sh
git -C {repo} log {ref} --name-status --format='==%h %cI %s' > {run}/work/hist.txt
for p in {path}…; do git -C {repo} log {ref} --follow -M --format=%H --name-only -- "$p" | awk 'NF' | grep -v '^[0-9a-f]\{40\}$'; done | sort -u > {run}/work/paths.txt
```

`paths.txt` is every path the files ran under. `versions.py {repo} {ref} {run} $(cat {run}/work/paths.txt)` prints one row per version — commit, committer time, path, blob, bytes (0 on a delete), first `v*` tag (`-` when none carries it), parent count — from four git processes: `rev-list --parents` once into a dict (ancestry is a walk over it, never `git merge-base` per pair), one `log --raw --no-abbrev` for every blob id, one `cat-file --batch-check` for every size (`git cat-file --batch` fed the same blob ids returns every text in one process), one `name-rev --annotate-stdin` for every tag. A skeleton: eras, live windows and lineage files are yours to add on top, each run over its rows.

```python
#!/usr/bin/env python3
"""versions.py {repo} {ref} {run} {path}... — every version of the paths with four git processes, cached in {run}/work/cache/.
Prints one TSV row per version: commit, committer time, path, blob, bytes, first v* tag, parent count."""
import hashlib, json, os, subprocess, sys

repo, ref, run, paths = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4:]
cache = os.path.join(run, "work", "cache"); os.makedirs(cache, exist_ok=True)

def git(*args, stdin=None):
    r = subprocess.run(["git", "-C", repo, *args], input=stdin, capture_output=True)
    if r.returncode:
        sys.exit(f"FAILED: git {' '.join(args[:3])}: {r.stderr.decode()[:200]}")
    return r.stdout

def cached(name, fn):
    p = os.path.join(cache, name + ".json")
    if os.path.exists(p):
        return json.load(open(p))
    v = fn(); json.dump(v, open(p, "w")); return v

# 1 ancestry once: commit -> parents; ancestry tests walk this dict, never `git merge-base` per pair
parents = cached("parents", lambda: {l.split()[0]: l.split()[1:] for l in git("rev-list", "--parents", ref).decode().splitlines()})
# 2 every blob id of every path, one log
def raw():
    rows, c = [], None
    for l in git("log", ref, "--raw", "--no-abbrev", "--no-renames", "--format===%H %cI", "--", *paths).decode().splitlines():
        if l.startswith("=="):
            c = l[2:].split()
        elif l.startswith(":"):
            meta, p = l.split("\t", 1)
            rows.append([c[0], c[1], p, meta.split()[3], meta.split()[4]])
    return rows
key = hashlib.sha1((ref + "\0" + "\0".join(paths)).encode()).hexdigest()[:10]
rows = cached("raw-" + key, raw)
# 3 every text's size, one cat-file process (the same process serves --batch for the texts themselves)
blobs = sorted({r[3] for r in rows if r[4] != "D"})
sizes = cached("sizes-" + key, lambda: dict(l.split()[0::2] for l in git("cat-file", "--batch-check", stdin="\n".join(blobs).encode()).decode().splitlines()))
# 4 first release tag per commit, one name-rev process
commits = sorted({r[0] for r in rows})
tags = cached("tags-" + key, lambda: dict(zip(commits, git("name-rev", "--refs=v*", "--name-only", "--no-undefined", "--always", "--annotate-stdin",
                                                   stdin="\n".join(commits).encode()).decode().split())))
for c, t, p, blob, st in rows:
    tag = tags.get(c, "")
    print("\t".join([c[:12], t, p, blob[:12], sizes.get(blob, "0") if st != "D" else "0", tag.split("~")[0].split("^")[0] if tag.startswith("v") else "-", str(len(parents.get(c, [])))]))
```

A version's text is the coordinate `{sha}:{path}`; check three resolve with `git show {sha}:{path} | wc -c` against the row's bytes. Why an era began: one numbered `tracer` question per era boundary — "between `{sha_a}:{path}` and `{sha_b}:{path}`: what changed in behaviour, and which commit message, release note line or design-doc section says why".

## MINE — runs, metrics, cost

The store: load `/callmeter:callmeter` and its `schema.md` for tables and joins, and read only a snapshot. On a store with no `-wal` file beside it the skill's read-only `.backup` fails with `unable to open database file`; take the immutable backup, then switch the copy to a rollback journal, or every `-readonly` open of it fails the same way:

```sh
s="${XDG_STATE_HOME:-}"; case "$s" in /*) ;; *) s="$HOME/.local/state" ;; esac
CM_DB="${CALLMETER_HOME:-$s/callmeter}/callmeter.db"; SNAP={run}/work/callmeter-snap.db
for try in 1 2 3 4 5; do  # a hook writing during an immutable backup leaves a malformed copy: discard it, take it again
  rm -f "$SNAP" "$SNAP-wal" "$SNAP-shm"
  { sqlite3 -readonly -cmd '.timeout 15000' "$CM_DB" ".backup '$SNAP'" 2>/dev/null || sqlite3 "file:$CM_DB?immutable=1" ".backup '$SNAP'"; } &&
    sqlite3 "$SNAP" 'PRAGMA journal_mode=DELETE' >/dev/null &&
    [ "$(sqlite3 -readonly "$SNAP" 'PRAGMA quick_check')" = ok ] &&
    [ "$(sqlite3 -readonly "$SNAP" 'SELECT count(*) > 0 FROM agents')" = 1 ] && { echo SNAP-OK; break; }  # an empty copy passes quick_check
  rm -f "$SNAP"; [ "$try" = 5 ] && echo "FAILED: snapshot after 5 tries"; sleep 3
done
```

Transcripts: `{config-dir}/projects/{project}/{session}.jsonl` is a main chat; its sub-agents sit at `{config-dir}/projects/{project}/{session}/subagents/agent-{agentId}.jsonl`, each beside `agent-{agentId}.meta.json` holding `agentType` and `toolUseId`, the dispatching Agent call. In the dispatching transcript that call is an assistant `tool_use` (`name` `Agent` or `Task`) with `input.subagent_type`; the user line carrying its `tool_result` has `toolUseResult.agentId`.

`find-runs.sh {run} {name,name,…}` takes every name in `names.json` and finds each run three ways; on agreement the three id sets match, the store holding only what it recorded since its own first row:

```sh
#!/usr/bin/env bash
# find-runs.sh {run} {name,name,...} — sub-agent runs of the names by meta file, by dispatch, by callmeter.
# Out: {run}/work/runs-{meta,dispatch,callmeter}.tsv; prints the three counts, CAPPED or FAILED lines.
set -euo pipefail
run=$1; names=$2; cap=${CAP:-100000}
data=$run/work; snap=$data/callmeter-snap.db
namesj=$(jq -cn --arg n "$names" '$n|split(",")')
namesre=$(printf '"subagent_type":"(%s)"' "${names//,/|}")
dirs=$( { sqlite3 -readonly "$snap" "SELECT config_dir FROM agents UNION SELECT seat_dir FROM agents" || echo "FAILED: callmeter config dirs" >&2
          echo "${CLAUDE_CONFIG_DIR:-}"; echo "$HOME/.claude"; } | grep -v '^$' | sort -u)
: > "$data/meta.tmp"; : > "$data/dispatch.tmp"
while read -r d; do [ -d "$d/projects" ] || { echo "MISSING $d/projects" >&2; continue; }
  # Route 1: each sub-agent's meta file
  find "$d/projects" -path '*/subagents/agent-*.meta.json' > "$data/meta-files.txt"
  if [ "$(wc -l < "$data/meta-files.txt")" -gt "$cap" ]; then echo "CAPPED meta scan at $cap in $d" >&2; fi
  head -n "$cap" "$data/meta-files.txt" | tr '\n' '\0' | xargs -0 jq -r --argjson n "$namesj" 'select(.agentType as $t | $n | index($t))
    | [(input_filename|capture("agent-(?<id>[^/]+)\\.meta\\.json$").id), .agentType, .toolUseId,
       (input_filename|split("/")|.[-3]), (input_filename|sub("\\.meta\\.json$"; ".jsonl"))] | @tsv' >> "$data/meta.tmp"
  # Route 2: the dispatching Agent call, joined to the agentId its result returned
  grep -rlE --include='*.jsonl' -e "$namesre" "$d/projects" > "$data/dispatch-files.txt" || true
  if [ "$(wc -l < "$data/dispatch-files.txt")" -gt "$cap" ]; then echo "CAPPED dispatch scan at $cap in $d" >&2; fi
  head -n "$cap" "$data/dispatch-files.txt" | while read -r f; do
    jq -r --argjson n "$namesj" '
      (select(.type=="assistant") | .message.content[]? | select(.type=="tool_use" and (.name=="Agent" or .name=="Task"))
        | select(.input.subagent_type as $t | $n | index($t))
        | ["U", .id, .input.subagent_type, (.input.model // ""), (input_filename|split("/")|.[-1]|sub("\\.jsonl$";""))]),
      (select(.type=="user" and (.toolUseResult|type)=="object" and .toolUseResult.agentId != null)
        | .toolUseResult.agentId as $a | .message.content[]? | select(.type=="tool_result") | ["R", .tool_use_id, $a])
      | @tsv' "$f" || echo "FAILED: jq $f" >&2
  done | awk -F'\t' '$1=="U"{u[$2]=$3"\t"$4"\t"$5} $1=="R"{r[$2]=$3} END{for(k in u) print k"\t"u[k]"\t"r[k]}' >> "$data/dispatch.tmp"
done <<< "$dirs"
sort -u -t$'\t' -k1,1 "$data/meta.tmp" > "$data/runs-meta.tsv"
sort -u -t$'\t' -k1,1 "$data/dispatch.tmp" > "$data/runs-dispatch.tsv"
# Route 3: callmeter's agents table
sqlite3 -readonly -separator $'\t' "$snap" "SELECT agent_id, agent_type, parent_tool_use_id, session_id, started FROM agents
  WHERE agent_type IN (SELECT value FROM json_each('$namesj')) ORDER BY started" > "$data/runs-callmeter.tsv" || echo "FAILED: callmeter agents" >&2
echo "meta $(wc -l < "$data/runs-meta.tsv") dispatch $(wc -l < "$data/runs-dispatch.tsv") callmeter $(wc -l < "$data/runs-callmeter.tsv")"
```

Columns: `runs-meta.tsv` agent id, type, dispatching `tool_use` id, session, transcript path; `runs-dispatch.tsv` `tool_use` id, type, model override, dispatching transcript, agent id. Compare the id sets (`cut -f1 runs-meta.tsv`, `cut -f5 runs-dispatch.tsv`, `cut -f1 runs-callmeter.tsv`, each `sort`, then `comm`); every id one route holds and another lacks is named in `reach-{source}.json`, and a dispatch row with an empty agent id is a run whose result never returned.

`run-metrics.py {transcript}…` prints one JSON row per transcript, counts only:

- reads: Read, Grep, Glob; writes: Edit, Write, NotebookEdit, MultiEdit.
- Bash by its command: a redirect outside quotes into a file, `sed -i`, `mv`/`cp`/`rm`/`mkdir`/`tee`, or a writing `git` verb is a write; a command whose every simple command is a known reader is a read; everything else (`python3`, `node`, `make`, a project script) is `ambiguous`, counted apart and never folded into either side.
- `hops`: the reads since the previous write, recorded at each write; `hops_before_first_write`, and `trailing_reads` after the last write.
- `usage`: one API message spans several transcript lines and its usage grows across them, so the last line per `message.id` counts; summing every line overcounts, taking the first undercounts.

```python
#!/usr/bin/env python3
"""run-metrics.py {transcript.jsonl}... — one JSON row per transcript: tool mix, read:write, hops, usage. Counts only.
An unreadable file prints a row with "failed"; MAX_LINES caps lines per file and the row says "capped"."""
import json, os, shlex, sys
from collections import Counter

READ_TOOLS, WRITE_TOOLS = {"Read", "Grep", "Glob"}, {"Edit", "Write", "NotebookEdit", "MultiEdit"}
BASH_READ = {"cat", "head", "tail", "less", "grep", "rg", "ls", "find", "wc", "jq", "stat", "file", "tree", "diff", "awk", "cut", "sort", "uniq"}
BASH_WRITE = {"mv", "cp", "rm", "mkdir", "touch", "tee", "patch", "chmod", "ln"}
GIT_READ = {"log", "show", "diff", "status", "blame", "grep", "ls-files", "rev-parse", "cat-file"}
GIT_WRITE = {"add", "commit", "mv", "rm", "apply", "checkout", "reset"}
MAX_LINES = int(os.environ.get("MAX_LINES", "200000"))

def bash_class(cmd):
    """read | write | ambiguous from the command text; anything not provably one side is ambiguous."""
    try:
        lex = shlex.shlex(cmd, posix=True, punctuation_chars=True)
        lex.whitespace_split = True
        toks = list(lex)
    except ValueError:
        return "ambiguous"
    for i, t in enumerate(toks):
        if t in (">", ">>", ">|") and i + 1 < len(toks) and toks[i + 1] != "/dev/null":
            return "write"
    kinds, seg = [], []
    for t in toks + [";"]:
        if t not in (";", "&&", "||", "|", "&", "(", ")", ";;"):
            seg.append(t)
            continue
        while seg and (seg[0] in ("if", "then", "do", "else", "elif", "!", "{", "}") or ("=" in seg[0] and not seg[0].startswith("-"))):
            seg = seg[1:]
        if seg and seg[0] not in ("for", "while", "until", "case", "fi", "done", "esac", "[", "[[", "test", "echo", "printf", "exit", "cd", "true", "set"):
            kinds.append(simple_class(seg))
        seg = []
    if "write" in kinds:
        return "write"
    return "read" if kinds and all(k == "read" for k in kinds) else "ambiguous"

def simple_class(words):
    prog = os.path.basename(words[0])
    if prog == "git" and len(words) > 1:
        return "read" if words[1] in GIT_READ else "write" if words[1] in GIT_WRITE else "ambiguous"
    if prog == "sed":
        return "write" if any(w.startswith("-i") for w in words) else "read" if "-n" in words else "ambiguous"
    if prog == "sqlite3":
        return "read" if "-readonly" in words else "ambiguous"
    return "read" if prog in BASH_READ else "write" if prog in BASH_WRITE else "ambiguous"

def one(path):
    row = {"transcript": os.path.basename(path), "tools": Counter(), "bash": Counter()}
    seq, usage, lines = [], {}, 0
    try:
        with open(path, encoding="utf-8") as fh:
            for line in fh:
                lines += 1
                if lines > MAX_LINES:
                    row["capped"] = MAX_LINES
                    break
                ev = json.loads(line)
                msg = ev.get("message") or {}
                if ev.get("type") != "assistant" or not isinstance(msg.get("content"), list):
                    continue
                if msg.get("id") and msg.get("usage"):
                    usage[msg["id"]] = (msg.get("model", "?"), msg["usage"])
                for c in msg["content"]:
                    if c.get("type") != "tool_use":
                        continue
                    name = c.get("name", "?")
                    row["tools"][name] += 1
                    if name in READ_TOOLS:
                        seq.append("R")
                    elif name in WRITE_TOOLS:
                        seq.append("W")
                    elif name == "Bash":
                        k = bash_class((c.get("input") or {}).get("command", ""))
                        row["bash"][k] += 1
                        seq.append({"read": "R", "write": "W"}.get(k, "?"))
    except (OSError, ValueError) as e:
        return {"transcript": os.path.basename(path), "failed": f"{type(e).__name__}: {e}"[:200]}
    hops, run = [], 0
    for s in seq:
        if s == "R":
            run += 1
        elif s == "W":
            hops.append(run)
            run = 0
    sums, models = Counter(), Counter()
    for model, u in usage.values():
        models[model] += 1
        for k in ("input_tokens", "cache_creation_input_tokens", "cache_read_input_tokens", "output_tokens"):
            sums[k] += u.get(k) or 0
    reads, writes = seq.count("R"), seq.count("W")
    row.update(reads=reads, writes=writes, ambiguous=seq.count("?"), read_write=round(reads / writes, 2) if writes else None,
               hops=hops, hops_before_first_write=hops[0] if hops else None, trailing_reads=run,
               requests=len(usage), lines=lines, usage=dict(sums), models=dict(models),
               tools=dict(row["tools"]), bash=dict(row["bash"]))
    return row

for p in sys.argv[1:]:
    print(json.dumps(one(p)))
```

Run it over every transcript `find-runs.sh` found, and take a distribution with `jq`:

```sh
cut -f5 {run}/work/runs-meta.tsv | tr '\n' '\0' | xargs -0 python3 {run}/scripts/run-metrics.py > {run}/work/run-metrics.jsonl
jq -r '.hops[]?' {run}/work/run-metrics.jsonl | sort -n | uniq -c        # reads before each write
jq -r '.hops_before_first_write // empty' {run}/work/run-metrics.jsonl | sort -n | uniq -c
```

A row with `failed` is that run `failed {cause}`; `ambiguous` above a few percent of a run's calls is named beside its ratio, since the ratio then rests on the unambiguous calls only.

Cost:

- A run's tokens from its transcript equal the store's `requests` for its `agent_id` with `pending = 0 AND source IS NOT 'ledger'`; the store's `ledger` rows are its side calls, which no transcript holds.
- A run's dollars: `sum(requests.cost_usd_micros) / 1e6` over its `agent_id`, side calls included, per the `/callmeter:callmeter` cost rules (count the NULL prices; the sum is then a floor).
- A run the store never recorded is priced from `pfm model-cost {model}` (`/chatlog` § Current prices), USD per million tokens: `IN` × `input_tokens`, `W5M` or `W1H` × `cache_creation_input_tokens`, `HIT` × `cache_read_input_tokens`, `OUT` × `output_tokens`, the `LONG` threshold switching tiers; read its stderr for a stale-table warning before trusting it.

## A Haiku reader's brief

Transcript passages and other text outside a repository reach you through one `Agent(model: "haiku")` per batch. Size a batch by the bytes of its named ranges (`sed -n '{a},{b}p' {file} | wc -c`), up to about 200 KB per reader, never splitting one transcript across readers; past that, more readers in one message. Shape, with invented values:

````text
You are a read-only extraction reader. Execute each numbered order exactly; judge nothing, summarize nothing, write no file. Read with `sed -n '{a},{b}p' {file} | jq`, only the line ranges named.

Sources:
- A: {config-dir}/projects/{project}/{session}/subagents/agent-{agentId}.jsonl (JSONL, 412 lines)

Orders:
1. A, line 1: the first 300 characters of `.message.content` (the string, or the first text block's `.text`), verbatim.
2. A, lines 380–412: the last `assistant` line holding a `text` block; the first whitespace-delimited token of that text, verbatim.
3. A, lines 40–60: every `tool_use` `.name` in order, verbatim.

Return one block per order, nothing else: `{n}. {source}:{line}` (the exact line, never a range), then the verbatim value in a fenced block; or `{n}. MISS — {why}` (line absent, field absent, or the read failed with its error). Never paraphrase or guess a value.
````

Before using a return, recompute one of its values yourself with the same `sed -n … | jq`; a return that disagrees is discarded and its orders go to a fresh reader.
