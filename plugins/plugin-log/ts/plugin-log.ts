// The shared plugin-log library: level files under one directory per plugin, rows bounded and keyed in one order,
// rotation past a cap, and a loss-free write queue.
// This file is a vendored copy of the plugin-log canonical source, kept byte-identical in every plugin by a sync
// check: change the canonical source, then re-vendor every copy.
// It runs in the function-hook sandbox: no Node API, every write is a /bin/sh process the caller runs.

export type PluginLogLevel = 'error' | 'warning' | 'info' | 'debug';
export type PluginLogFields = { plugin: string; level: PluginLogLevel; event: string; msg: string; where: string; session?: string; detail?: Record<string, unknown>; /** The row's own time, when it is not the writing time. */ ts?: string };

/** A level file past this size moves aside to `.1`, older generations shifting up. */
export const PLUGIN_LOG_CAP_BYTES = 20971520;
/** Rotated generations each level file keeps (`.1` newest … `.5`). */
export const PLUGIN_LOG_KEEP = 5;
/** One row's bound in UTF-8 bytes, its newline included. */
export const ROW_MAX_BYTES = 4096;
/** At most this many unwritten rows are held for the next flush; past it the oldest go, counted. */
export const PLUGIN_LOG_HOLD_ROWS = 2_000;
/** A live lock holder is waited on this long: under a 15 s hook bound, past the seconds contention reaches under load. */
export const LOCK_WAIT_SECONDS = 10;

const LEVEL_FILES: Record<PluginLogLevel, string> = { error: 'errors.jsonl', warning: 'warnings.jsonl', info: 'info.jsonl', debug: 'debug.jsonl' };

/** `$PLUGIN_LOG_DIR/{plugin}` when the override is set, else `{home}/.local/state/{plugin}/logs`; undefined is logging off. */
export function pluginLogDir(override: string | undefined, home: string | undefined, plugin: string): string | undefined {
  if (override) return `${override.replace(/\/+$/, '')}/${plugin}`;
  return home ? `${home.replace(/\/+$/, '')}/.local/state/${plugin}/logs` : undefined;
}

/** The screen shows error rows only; warning, info and debug rows go to their files. */
export function onScreen(level: PluginLogLevel): boolean {
  return level === 'error';
}

/** UTF-8 byte count without TextEncoder. */
function utf8Bytes(text: string): number {
  let bytes = 0;
  for (const char of text) { const point = char.codePointAt(0)!; bytes += point <= 0x7f ? 1 : point <= 0x7ff ? 2 : point <= 0xffff ? 3 : 4; }
  return bytes;
}

/** Longest code-point prefix of `text` for which `fits` holds (fits('') must hold). */
function fitPrefix(text: string, fits: (prefix: string) => boolean): string {
  const chars = Array.from(text);
  let low = 0; let high = chars.length;
  while (low < high) {
    const at = Math.ceil((low + high) / 2);
    if (fits(chars.slice(0, at).join(''))) low = at; else high = at - 1;
  }
  return chars.slice(0, low).join('');
}

/**
 * One JSONL row, keys in order ts, plugin, level, event, msg, where, session, detail; at most ROW_MAX_BYTES with its
 * newline. An oversized row's detail becomes {truncated, bytes} (bytes: the whole row's), then msg, session, where,
 * event and plugin shorten in that order; it never throws on size.
 */
export function levelRow(fields: PluginLogFields, nowMs: number): string {
  if (!Object.prototype.hasOwnProperty.call(LEVEL_FILES, fields.level)) throw new Error(`plugin log invalid level: ${String(fields.level)}`);
  const own = fields.ts === undefined ? Number.NaN : Date.parse(fields.ts);
  const row: Record<string, unknown> = { ts: new Date(Number.isFinite(own) ? own : nowMs).toISOString(), plugin: fields.plugin, level: fields.level, event: fields.event, msg: fields.msg, where: fields.where, ...(fields.session ? { session: fields.session } : {}), ...(fields.detail !== undefined ? { detail: fields.detail } : {}) };
  const serialize = () => `${JSON.stringify(row)}\n`;
  const bytes = utf8Bytes(serialize());
  if (bytes <= ROW_MAX_BYTES) return serialize();
  row.detail = { truncated: true, bytes };
  for (const key of ['msg', 'session', 'where', 'event', 'plugin']) {
    if (typeof row[key] !== 'string') continue;
    row[key] = fitPrefix(row[key] as string, (prefix) => { row[key] = prefix; return utf8Bytes(serialize()) <= ROW_MAX_BYTES; });
    if (key === 'session' && row.session === '') delete row.session;
    if (utf8Bytes(serialize()) <= ROW_MAX_BYTES) return serialize();
  }
  return serialize();
}

/**
 * A directory lock born with its holder's token (`pid epoch`) in `$lock/pid`: staged under a private name and
 * renamed into place, so the lock never exists without its token. A lock whose holder is gone, or whose token is
 * older than a minute (no append takes that long), is broken; a live one is waited on for `LOCK_WAIT_SECONDS`. A stage
 * or a broken lock (`$lock.{pid}`, `$lock.dead.{pid}`) whose writer is dead is cleared: a writer killed between its
 * mkdir and its rename leaves one behind.
 * Breaking and releasing first claim the token by renaming `$lock/pid` to a private name: only one mover wins that
 * rename, and the claimer removes the lock only when the token it took is the one it saw stale (or its own), so no
 * waiter removes a lock another has since broken and taken, and a holder outlived by an age break never removes its
 * successor's lock. A claimer's rename can hide the token from its new holder's check for a moment: the holder that
 * then reads its own token back owns the lock. A claimer that dies holding the token, or holds it past a minute,
 * has its claim put back by a waiter, so the lock is broken by the same rules; the claim's name carries its own
 * start time (`pid.claim.{epoch}.{pid}`), since the renamed token keeps the token's age, not the claim's.
 * The script that embeds it defines `fail FILE CAUSE`.
 */
export const LOCK_FN = `lk_claim() {
  lk_c="$lk_lock/pid.claim.$(date +%s).$$"
  mv "$lk_lock/pid" "$lk_c" 2>/dev/null || return 1
  if [ "$(cat "$lk_c" 2>/dev/null)" = "$1" ]; then
    lk_gone="$lk_lock.dead.$$"
    rm -rf "$lk_gone"
    mv "$lk_lock" "$lk_gone" 2>/dev/null || return 1
    rm -rf "$lk_gone" 2>/dev/null || :
    return 0
  fi
  mv "$lk_c" "$lk_lock/pid" 2>/dev/null || :
  return 1
}
lock() {
  lk_lock=$1
  lk_token="$$ $(date +%s)"
  lk_until=$((\${lk_token#* } + ${LOCK_WAIT_SECONDS}))
  lk_stage="$lk_lock.$$"
  for lk_s in "$lk_lock".[0-9]* "$lk_lock".dead.[0-9]*; do
    lk_p=\${lk_s##*.}
    case $lk_p in ''|*[!0-9]*) continue ;; esac
    if [ -d "$lk_s" ] && [ "$lk_p" != "$$" ] && ! kill -0 "$lk_p" 2>/dev/null; then rm -rf "$lk_s" 2>/dev/null || :; fi
  done
  while :; do
    rm -rf "$lk_stage"
    mkdir "$lk_stage" || fail "$lk_stage" 'cannot create the lock directory'
    printf '%s\\n' "$lk_token" > "$lk_stage/pid"
    if [ ! -e "$lk_lock" ] && mv "$lk_stage" "$lk_lock" 2>/dev/null && [ "$(cat "$lk_lock/pid" 2>/dev/null)" = "$lk_token" ]; then break; fi
    # mv onto a lock that appeared meanwhile moves the stage into it.
    rm -rf "$lk_stage" "$lk_lock/\${lk_stage##*/}"
    [ "$(date +%s)" -le "$lk_until" ] || fail "$lk_lock" 'lock held by another writer'
    holder=$(cat "$lk_lock/pid" 2>/dev/null) || holder=
    # Our own token: our stage became the lock, and the check above read it while a claimer had it renamed aside.
    if [ "$holder" = "$lk_token" ]; then break; fi
    if [ -n "$holder" ] && { ! kill -0 "\${holder%% *}" 2>/dev/null || [ -n "$(find "$lk_lock/pid" -mmin +1 2>/dev/null)" ]; }; then
      lk_claim "$holder" || :
      continue
    fi
    # A claimer that died holding the token leaves the lock without one: put its claim back, so the rule above breaks it.
    if [ -z "$holder" ]; then
      for lk_o in "$lk_lock"/pid.claim.*; do
        lk_t=\${lk_o##*/pid.claim.}; lk_t=\${lk_t%%.*}
        if [ -e "$lk_o" ] && { ! kill -0 "\${lk_o##*.}" 2>/dev/null || case $lk_t in ''|*[!0-9]*) true ;; *) [ $(($(date +%s) - lk_t)) -gt 60 ] ;; esac; }; then
          mv "$lk_o" "$lk_lock/pid" 2>/dev/null || :
          break
        fi
      done
    fi
    sleep 0.05
  done
  trap 'lk_claim "$lk_token" || :' EXIT
  trap 'exit 1' HUP INT TERM
}`;

/** Appends `file row` pairs under the directory's lock; cap, rotation and append happen under it, so two writers never both move a full file aside. */
export const APPEND_SCRIPT = `set -eu
umask 077
dir=$1; plugin=$2; shift 2
fail() { printf '%s: %s\\n' "$1" "$2" >&2; exit 1; }
# No operand starts with '-': a relative directory is passed as ./dir, so no command reads it as an option.
case $dir in /*) ;; *) dir=./$dir;; esac
mkdir -p "$dir" || fail "$dir" 'cannot create the log directory'
${LOCK_FN}
lock "$dir/.lock"
cap() {
  if [ -f "$1" ]; then
    bytes=$(wc -c < "$1") || exit 1
    bytes=$((bytes))
    if [ "$bytes" -gt ${PLUGIN_LOG_CAP_BYTES} ]; then
      n=${PLUGIN_LOG_KEEP}; while [ "$n" -gt 1 ]; do n=$((n - 1)); if [ -e "$1.$n" ]; then mv -f "$1.$n" "$1.$((n + 1))" || exit 1; fi; done
      mv -f "$1" "$1.1" || exit 1; return 0
    fi
  fi
  return 1
}
capped() {
  msg='level file exceeded ${PLUGIN_LOG_CAP_BYTES} bytes; moved aside'
  detail=$(printf '{"file":"%s"}' "$1")
  truncated=false
  while :; do
    notice=$(printf '{"ts":"%s","plugin":"%s","level":"warning","event":"log-capped","msg":"%s","where":"src/plugin-log.ts#APPEND_SCRIPT"%s,"detail":%s}' "$ts" "$plugin" "$msg" "$session" "$detail")
    # BSD wc pads its count with spaces, which would land in the row: arithmetic expansion drops them.
    bytes=$(printf '%s\\n' "$notice" | wc -c) || exit 1
    bytes=$((bytes))
    [ "$bytes" -gt ${ROW_MAX_BYTES} ] || break
    if [ "$truncated" = false ]; then
      detail=$(printf '{"truncated":true,"bytes":%s}' "$bytes"); truncated=true
    else
      [ -n "$msg" ] || fail "$dir/warnings.jsonl" 'cap warning metadata exceeds ${ROW_MAX_BYTES} bytes'
      msg=\${msg%?}
    fi
  done
  printf '%s\\n' "$notice" >> "$dir/warnings.jsonl"
}
while [ "$#" -gt 0 ]; do
  file=$1; row=$2; shift 2
  case "$file" in errors.jsonl|warnings.jsonl|info.jsonl|debug.jsonl) ;; *) printf '%s: invalid level file: %s\\n' "$dir" "$file" >&2; exit 2;; esac
  ts=\${row#*\\"ts\\":\\"}; ts=\${ts%%\\"*}
  header=\${row%?}; header=\${header%?}; header=\${header%%,\\"detail\\":*}
  case "$header" in *',"session":'*) session=',"session":'\${header##*,\\"session\\":};; *) session=;; esac
  path=$dir/$file
  if cap "$path"; then
    if [ "$file" != warnings.jsonl ] && cap "$dir/warnings.jsonl"; then capped warnings.jsonl; fi
    capped "$file"
  fi
  printf '%s' "$row" >> "$path"
done`;

/** The append processes for `rows`: at most 64 file/row pairs each. */
export function appendArgv(dir: string, plugin: string, rows: readonly string[]): string[][] {
  const batches: string[][] = [];
  for (let at = 0; at < rows.length; at += 64) {
    const pairs = rows.slice(at, at + 64).flatMap((row) => {
      const level: unknown = JSON.parse(row).level;
      const file = typeof level === 'string' ? LEVEL_FILES[level as PluginLogLevel] : undefined;
      if (file === undefined) throw new Error(`plugin log invalid level: ${String(level)}`);
      return [file, row];
    });
    batches.push(['/bin/sh', '-c', APPEND_SCRIPT, 'plugin-log', dir, plugin, ...pairs]);
  }
  return batches;
}

/** A failed batch goes back to the front of `pending`, order kept; past the hold the oldest go. Returns how many went. */
export function requeueRows(pending: string[], rows: readonly string[], hold: number = PLUGIN_LOG_HOLD_ROWS): number {
  pending.unshift(...rows);
  const dropped = Math.max(0, pending.length - hold);
  if (dropped > 0) pending.splice(0, dropped);
  return dropped;
}

export type PluginLogRun = (argv: string[]) => Promise<{ exitCode: number; stdout: string; stderr: string }>;
/**
 * `held`: rows queued after this flush; `lost`: rows the hold let go, reported by the flush that wrote their
 * `plugin-log-rows-dropped` row. `error` and `screen` come once per failing streak: the cause, and the one line the
 * caller shows on screen.
 */
/** A `$.process.run` its abandoned hook dispatch aborted: the caller retries the work on the next dispatch, nothing failed.
 * shortcut: Claude Code says so only in the message, "$.process.run(/bin/sh) aborted"; match a typed abort once it has one. */
export const runAborted = (cause: string): boolean => /\$\.process\.run\([^)]*\) aborted$/.test(cause);

export type FlushResult = { held: number; lost: number; error?: string; screen?: string };

/**
 * Serialized append batches. A batch that fails (or is killed after writing part of it) goes back to the front of the
 * queue whole, so a row may be written twice, never lost; past the hold the oldest rows go, and the next write that
 * succeeds carries a `plugin-log-rows-dropped` error row counting them. A success re-arms the streak line. With no
 * directory, logging is off: rows go nowhere, said once.
 */
export class PluginLogQueue {
  private rows: string[] = [];
  private writing: Promise<unknown> = Promise.resolve();
  private failing = false;
  private lost = 0;
  private readonly dir: string | undefined;
  private readonly plugin: string;
  constructor(dir: string | undefined, plugin: string) { this.dir = dir; this.plugin = plugin; }
  add(row: string): void { this.rows.push(row); }
  get held(): number { return this.rows.length; }
  flush(run: PluginLogRun): Promise<FlushResult> {
    const pending = this.writing.then(async (): Promise<FlushResult> => {
      const dir = this.dir;
      if (dir === undefined) {
        const any = this.rows.splice(0).length > 0;
        const report = any && !this.failing;
        if (any) this.failing = true;
        return { held: 0, lost: 0, ...(report ? { error: 'no PLUGIN_LOG_DIR and no HOME', screen: `${this.plugin}: plugin log off: no PLUGIN_LOG_DIR and no HOME` } : {}) };
      }
      const rows = this.rows.splice(0);
      if (rows.length === 0) return { held: 0, lost: 0 };
      const lost = this.lost;
      if (lost > 0) {
        this.lost = 0;
        rows.unshift(levelRow({ plugin: this.plugin, level: 'error', event: 'plugin-log-rows-dropped', msg: `${lost} unwritten ${lost === 1 ? 'row was' : 'rows were'} dropped past the ${PLUGIN_LOG_HOLD_ROWS}-row hold`, where: 'src/plugin-log.ts#PluginLogQueue', detail: { rows: lost } }, Date.parse(JSON.parse(rows[0]!).ts)));
      }
      let written = 0;
      try {
        for (const argv of appendArgv(dir, this.plugin, rows)) {
          const out = await run(argv);
          if (out.exitCode !== 0) throw new Error(out.stderr.split('\n')[0]?.trim() || `exit ${out.exitCode}`);
          written += (argv.length - 6) / 2;
          this.failing = false;
        }
        return { held: this.rows.length, lost };
      } catch (error) {
        // Unwritten, the count goes back to the counter, never into the queue as a row the hold could drop.
        if (written === 0 && lost > 0) { rows.shift(); this.lost += lost; }
        this.lost += requeueRows(this.rows, rows.slice(written), PLUGIN_LOG_HOLD_ROWS);
        const cause = error instanceof Error ? error.message : String(error);
        const held = this.rows.length;
        // An abandoned hook dispatch aborts its run: the rows wait for the next flush, which is no failure to show.
        if (runAborted(cause)) return { held, lost: written > 0 ? lost : 0 };
        const report = !this.failing;
        this.failing = true;
        return { held, lost: written > 0 ? lost : 0, ...(report ? { error: cause, screen: `${this.plugin}: plugin log ${dir} failed: ${cause}; ${held} ${held === 1 ? 'row is' : 'rows are'} kept for the next write` } : {}) };
      }
    });
    this.writing = pending;
    return pending;
  }
}
