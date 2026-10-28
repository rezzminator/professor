// Conformance of the TypeScript plugin-log against ../spec.json, plus the shell writer's lock, rotation and requeue.
// Run: node --experimental-strip-types --test plugins/plugin-log/ts/plugin-log.test.ts
import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import { execFile, spawn } from 'node:child_process';
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, statSync, truncateSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { promisify } from 'node:util';
import * as log from './plugin-log.ts';

const spec = JSON.parse(readFileSync(join(dirname(fileURLToPath(import.meta.url)), '..', 'spec.json'), 'utf8'));
const exec = promisify(execFile);
const now = 1791991231123;
const fields = { plugin: 'demo', level: 'error', event: 'write-failed', msg: 'm', where: 'src/demo.ts#write', session: 's1' } as const;
const row = (msg: string, level: log.PluginLogLevel = 'error') => log.levelRow({ ...fields, level, msg }, now);
const rows = (dir: string, file: string): any[] => existsSync(join(dir, file)) ? readFileSync(join(dir, file), 'utf8').trim().split('\n').map((line) => JSON.parse(line)) : [];
const ok = { exitCode: 0, stderr: '', stdout: '' };
const written = (argv: string[]) => argv.slice(6).filter((_, n) => n % 2 === 1).map((text) => JSON.parse(text).msg as string);
const shell = (dir: string, pairs: string[]) => exec('/bin/sh', ['-c', log.APPEND_SCRIPT, 'plugin-log', dir, 'demo', ...pairs]).then(() => 0, (error) => error.code as number);
/** The real writer: each argv run as a process. */
const run: log.PluginLogRun = (argv) => new Promise((resolve) => {
  execFile(argv[0]!, argv.slice(1), (error, stdout, stderr) => resolve({ exitCode: error ? (typeof error.code === 'number' ? error.code : 1) : 0, stdout, stderr }));
});
const scratch = (name: string) => mkdtempSync(join(tmpdir(), `plugin-log-${name}-`));
const fill = (path: string) => { writeFileSync(path, ''); truncateSync(path, spec.capBytes + 1); };
const screenLine = (template: string, values: Record<string, string | number>) => template.replace(/\{(\w+)\}/g, (_, key: string) => String(values[key]));

describe('spec', () => {
  it('holds the constants', () => {
    assert.deepEqual([log.PLUGIN_LOG_CAP_BYTES, log.PLUGIN_LOG_KEEP, log.ROW_MAX_BYTES, log.PLUGIN_LOG_HOLD_ROWS], [spec.capBytes, spec.keep, spec.rowMaxBytes, spec.holdRows]);
  });
  for (const v of spec.dirs) {
    it(`resolves the directory for override ${JSON.stringify(v.override)} and home ${JSON.stringify(v.home)}`, () => {
      assert.equal(log.pluginLogDir(v.override ?? undefined, v.home ?? undefined, v.plugin), v.dir ?? undefined);
    });
  }
  for (const v of spec.rows) {
    it(`writes the spec row: ${v.name}`, () => {
      const input: Record<string, unknown> = { ...v.fields };
      for (const [key, [text, n]] of Object.entries(v.repeat ?? {}) as [string, [string, number]][]) input[key] = text.repeat(n);
      if (v.repeatDetail) input.detail = Object.fromEntries(Object.entries(v.repeatDetail as Record<string, [string, number]>).map(([key, [text, n]]) => [key, text.repeat(n)]));
      const text = log.levelRow(input as log.PluginLogFields, v.nowMs);
      assert.equal(text, v.row);
      assert.ok(Buffer.byteLength(text) <= spec.rowMaxBytes);
      const keys = Object.keys(JSON.parse(text));
      assert.deepEqual(keys, spec.keys.filter((key: string) => keys.includes(key)));
    });
  }
  it('shows only errors on screen', () => {
    for (const [level, shown] of Object.entries(spec.onScreen)) assert.equal(log.onScreen(level as log.PluginLogLevel), shown);
  });
  it('refuses a level it has no file for', () => {
    assert.throws(() => log.levelRow({ ...fields, level: 'fatal' as log.PluginLogLevel }, now), /invalid level/);
  });
  for (const [epoch, expected] of [[0, '1970-01-01T00:00:00.000Z'], [-1, '1969-12-31T23:59:59.999Z'], [Date.parse('+010000-01-01T00:00:00.000Z'), '+010000-01-01T00:00:00.000Z']] as const) {
    it(`stamps ${epoch} as ${expected}`, () => assert.equal(JSON.parse(log.levelRow(fields, epoch)).ts, expected));
  }
});

describe('the append script', () => {
  it('writes each level to its file, directory 0700 and files 0600, with shell text kept literal', async () => {
    const parent = scratch('levels');
    const dir = join(parent, 'logs', 'demo');
    try {
      const text = "literal $(touch bad); ' \n text";
      const pairs = (Object.keys(spec.files) as log.PluginLogLevel[]).flatMap((level) => [spec.files[level], row(text, level)]);
      assert.equal(await shell(dir, pairs), 0);
      assert.equal(statSync(dir).mode & 0o777, spec.dirMode);
      for (const [level, file] of Object.entries(spec.files) as [string, string][]) {
        assert.deepEqual(rows(dir, file), [JSON.parse(row(text, level as log.PluginLogLevel))]);
        assert.equal(statSync(join(dir, file)).mode & 0o777, spec.fileMode);
      }
      assert.equal(existsSync(join(parent, 'bad')), false);
    } finally { rmSync(parent, { recursive: true, force: true }); }
  });

  it('keeps the newest five rotated files of a level file and writes one log-capped row', async () => {
    const dir = scratch('rotate');
    const big = join(dir, 'errors.jsonl');
    try {
      for (let n = 1; n <= 5; n++) writeFileSync(`${big}.${n}`, `generation ${n}\n`);
      fill(big);
      assert.equal(await shell(dir, ['errors.jsonl', row('new')]), 0);
      assert.equal(statSync(`${big}.1`).size, spec.capBytes + 1);
      assert.deepEqual([2, 3, 4, 5].map((n) => readFileSync(`${big}.${n}`, 'utf8')), ['generation 1\n', 'generation 2\n', 'generation 3\n', 'generation 4\n']);
      assert.equal(existsSync(`${big}.6`), false);
      assert.equal(readFileSync(big, 'utf8'), row('new'));
      assert.deepEqual(rows(dir, 'warnings.jsonl'), [{ ts: JSON.parse(row('new')).ts, plugin: 'demo', ...spec.capped, where: 'src/plugin-log.ts#APPEND_SCRIPT', session: 's1', detail: { file: 'errors.jsonl' } }]);
    } finally { rmSync(dir, { recursive: true, force: true }); }
  });

  it('moves a full warnings file aside first when another level file is capped', async () => {
    const dir = scratch('both');
    try {
      fill(join(dir, 'errors.jsonl')); fill(join(dir, 'warnings.jsonl'));
      assert.equal(await shell(dir, ['errors.jsonl', row('new')]), 0);
      const notice = (file: string) => ({ ts: JSON.parse(row('new')).ts, plugin: 'demo', ...spec.capped, where: 'src/plugin-log.ts#APPEND_SCRIPT', session: 's1', detail: { file } });
      assert.deepEqual(rows(dir, 'warnings.jsonl'), [notice('warnings.jsonl'), notice('errors.jsonl')]);
      assert.deepEqual(rows(dir, 'errors.jsonl'), [JSON.parse(row('new'))]);
    } finally { rmSync(dir, { recursive: true, force: true }); }
  });

  it('clears a stage and a broken lock whose writer is dead, keeping a live writer\'s stage', async () => {
    const dir = scratch('stale');
    const dead = 999_999;
    assert.throws(() => process.kill(dead, 0));
    try {
      for (const name of [`.lock.${dead}`, `.lock.dead.${dead}`, `.lock.${process.pid}`]) mkdirSync(join(dir, name));
      assert.equal(await shell(dir, ['errors.jsonl', row('x')]), 0);
      assert.equal(existsSync(join(dir, `.lock.${dead}`)), false);
      assert.equal(existsSync(join(dir, `.lock.dead.${dead}`)), false);
      assert.equal(existsSync(join(dir, `.lock.${process.pid}`)), true);
      assert.equal(readFileSync(join(dir, 'errors.jsonl'), 'utf8'), row('x'));
    } finally { rmSync(dir, { recursive: true, force: true }); }
  });

  it('breaks a lock whose holder is gone', async () => {
    const dir = scratch('dead-holder');
    try {
      mkdirSync(join(dir, '.lock')); writeFileSync(join(dir, '.lock', 'pid'), `999999 ${Math.floor(Date.now() / 1000)}\n`);
      assert.equal(await shell(dir, ['errors.jsonl', row('x')]), 0);
      assert.equal(readFileSync(join(dir, 'errors.jsonl'), 'utf8'), row('x'));
      assert.equal(existsSync(join(dir, '.lock')), false);
    } finally { rmSync(dir, { recursive: true, force: true }); }
  });

  it('holds the directory lock across its cap and append', async () => {
    const dir = scratch('held');
    try {
      mkdirSync(join(dir, '.lock')); writeFileSync(join(dir, '.lock', 'pid'), `${process.pid} ${Math.floor(Date.now() / 1000)}\n`);
      const child = spawn('/bin/sh', ['-c', log.APPEND_SCRIPT, 'plugin-log', dir, 'demo', 'errors.jsonl', row('late')]);
      const done = new Promise<number | null>((resolve) => child.on('close', resolve));
      await new Promise((resolve) => setTimeout(resolve, 400));
      assert.equal(existsSync(join(dir, 'errors.jsonl')), false);
      rmSync(join(dir, '.lock'), { recursive: true, force: true });
      assert.equal(await done, 0);
      assert.equal(readFileSync(join(dir, 'errors.jsonl'), 'utf8'), row('late'));
    } finally { rmSync(dir, { recursive: true, force: true }); }
  });

  it('waits on a live holder by the clock, never by a count of tries', async () => {
    const dir = scratch('wait');
    const bin = join(dir, 'bin');
    const [lock, tries, clock] = [join(dir, '.lock'), join(dir, 'tries'), join(dir, 'clock')];
    // `sleep` returns at once, counts its calls, moves the clock STEP seconds on, and frees the lock at try RELEASE_AT; `date +%s` reads that clock.
    mkdirSync(bin);
    writeFileSync(join(bin, 'sleep'), '#!/bin/sh\nn=$(( $(cat "$TRIES" 2>/dev/null || echo 0) + 1 )); echo "$n" > "$TRIES"\necho "$(( $(cat "$CLOCK") + STEP ))" > "$CLOCK"\n[ "$n" != "${RELEASE_AT:-}" ] || rm -rf "$LOCK"\n', { mode: 0o755 });
    writeFileSync(join(bin, 'date'), '#!/bin/sh\nif [ "$*" = "+%s" ]; then cat "$CLOCK"; else PATH=${PATH#*:} exec date "$@"; fi\n', { mode: 0o755 });
    const append = (env: Record<string, string>) => {
      rmSync(tries, { force: true }); rmSync(join(dir, 'errors.jsonl'), { force: true });
      writeFileSync(clock, `${Math.floor(Date.now() / 1000)}\n`);
      mkdirSync(lock); writeFileSync(join(lock, 'pid'), `${process.pid} ${Math.floor(Date.now() / 1000)}\n`);
      return new Promise<{ code: number | null; stderr: string; tries: string }>((resolve) => {
        const child = spawn('/bin/sh', ['-c', log.APPEND_SCRIPT, 'plugin-log', dir, 'demo', 'errors.jsonl', row('late')], { env: { ...process.env, PATH: `${bin}:${process.env.PATH}`, LOCK: lock, TRIES: tries, CLOCK: clock, ...env } });
        let stderr = '';
        child.stderr.on('data', (chunk) => { stderr += chunk; });
        child.on('close', (code) => resolve({ code, stderr, tries: readFileSync(tries, 'utf8').trim() }));
      });
    };
    try {
      const released = await append({ STEP: '0', RELEASE_AT: '101' });
      assert.deepEqual([released.code, released.tries], [0, '101']);
      assert.equal(readFileSync(join(dir, 'errors.jsonl'), 'utf8'), row('late'));
      const given = await append({ STEP: '1' });
      assert.equal(given.tries, String(log.LOCK_WAIT_SECONDS + 1));
      assert.notEqual(given.code, 0);
      assert.match(given.stderr, /lock held by another writer/);
    } finally { rmSync(dir, { recursive: true, force: true }); }
  });

  it('refuses a file that is no level file', async () => {
    const dir = scratch('bad-file');
    try { assert.equal(await shell(dir, ['../escape.jsonl', row('x')]), 2); } finally { rmSync(dir, { recursive: true, force: true }); }
  });

  it('bounds batches at 64 pairs and keeps rows in order', () => {
    const input = Array.from({ length: 65 }, (_, n) => row(String(n)));
    const batches = log.appendArgv('/x/logs/demo', 'demo', input);
    assert.deepEqual(batches.map((a) => [a[0], a[3], a[4], a[5], (a.length - 6) / 2]), [['/bin/sh', 'plugin-log', '/x/logs/demo', 'demo', 64], ['/bin/sh', 'plugin-log', '/x/logs/demo', 'demo', 1]]);
    assert.deepEqual(batches.flatMap((a) => a.slice(6).filter((_, n) => n % 2 === 1)), input);
  });
});

describe('PluginLogQueue', () => {
  it('requeues a failed batch, order kept, and writes it with the next flush', async () => {
    const queue = new log.PluginLogQueue('/x', 'demo');
    queue.add(row('a')); queue.add(row('b'));
    const failed = await queue.flush(async () => ({ exitCode: 1, stderr: 'lock held by another writer\n', stdout: '' }));
    assert.deepEqual(failed, { held: 2, lost: 0, error: 'lock held by another writer', screen: screenLine(spec.screen.failing, { plugin: 'demo', dir: '/x', cause: 'lock held by another writer', held: 2 }) });
    queue.add(row('c'));
    const seen: string[] = [];
    assert.deepEqual(await queue.flush(async (argv) => { seen.push(...written(argv)); return ok; }), { held: 0, lost: 0 });
    assert.deepEqual(seen, ['a', 'b', 'c']);
  });

  it('retries a killed batch whole, so a row may repeat but none is lost', async () => {
    const queue = new log.PluginLogQueue('/x', 'demo');
    for (let n = 0; n < 70; n++) queue.add(row(String(n)));
    let runs = 0;
    const out = await queue.flush(async () => (++runs === 2 ? Promise.reject(new Error('aborted: still running after 15000ms')) : ok));
    assert.deepEqual([out.held, out.lost, out.error], [6, 0, 'aborted: still running after 15000ms']);
    const seen: string[] = [];
    await queue.flush(async (argv) => { seen.push(...written(argv)); return ok; });
    assert.deepEqual(seen, ['64', '65', '66', '67', '68', '69']);
  });

  it('drops only past the hold, oldest first, and writes the count as an error row once a write succeeds', async () => {
    const queue = new log.PluginLogQueue('/x', 'demo');
    for (let n = 0; n < spec.holdRows + 3; n++) queue.add(row(String(n)));
    const failed = await queue.flush(async () => ({ exitCode: 1, stderr: 'disk full', stdout: '' }));
    assert.deepEqual([failed.held, failed.lost, failed.error], [spec.holdRows, 0, 'disk full']);
    const again = await queue.flush(async () => ({ exitCode: 1, stderr: 'disk full', stdout: '' }));
    assert.deepEqual(again, { held: spec.holdRows, lost: 0 });
    const seen: any[] = [];
    assert.deepEqual(await queue.flush(async (argv) => { seen.push(...argv.slice(6).filter((_, n) => n % 2 === 1).map((text) => JSON.parse(text))); return ok; }), { held: 0, lost: 3 });
    assert.deepEqual(seen[0], { ts: JSON.parse(row('3')).ts, plugin: 'demo', level: spec.dropped.level, event: spec.dropped.event, msg: screenLine(spec.dropped.msg, { rows: 3 }), where: 'src/plugin-log.ts#PluginLogQueue', detail: { rows: 3 } });
    assert.equal(seen[1].msg, '3');
    assert.equal(seen.length, spec.holdRows + 1);
    assert.deepEqual(await queue.flush(async () => ok), { held: 0, lost: 0 });
  });

  it('serializes concurrent flushes and says one screen line per failing streak, re-armed by a success', async () => {
    const queue = new log.PluginLogQueue('/x/logs/demo', 'demo');
    const results: log.FlushResult[] = [];
    for (let n = 0; n < 2; n++) { queue.add(row('a')); results.push(await queue.flush(async () => { throw new Error('permission denied'); })); }
    assert.deepEqual(results, [{ held: 1, lost: 0, error: 'permission denied', screen: 'demo: plugin log /x/logs/demo failed: permission denied; 1 row is kept for the next write' }, { held: 2, lost: 0 }]);
    queue.add(row('b'));
    assert.deepEqual(await queue.flush(async () => ok), { held: 0, lost: 0 });
    queue.add(row('c'));
    assert.deepEqual((await queue.flush(async () => ({ exitCode: 2, stderr: 'disk full\ncontext', stdout: '' }))).screen, 'demo: plugin log /x/logs/demo failed: disk full; 1 row is kept for the next write');
    assert.deepEqual(await queue.flush(async () => ok), { held: 0, lost: 0 });
    const starts: string[] = []; let release!: () => void;
    const gate = new Promise<void>((resolve) => { release = resolve; });
    queue.add(row('first'));
    const first = queue.flush(async (argv) => { starts.push(...written(argv)); await gate; return ok; });
    await Promise.resolve(); await Promise.resolve();
    queue.add(row('second'));
    const second = queue.flush(async (argv) => { starts.push(...written(argv)); return ok; });
    await Promise.resolve(); await Promise.resolve();
    assert.deepEqual(starts, ['first']); release(); await first; await second;
    assert.deepEqual(starts, ['first', 'second']);
    assert.deepEqual(await queue.flush(async () => { throw new Error('empty queue executed'); }), { held: 0, lost: 0 });
  });

  it('with no directory, writes nothing and says the log is off once', async () => {
    const queue = new log.PluginLogQueue(undefined, 'demo');
    assert.deepEqual(await queue.flush(async () => { throw new Error('ran'); }), { held: 0, lost: 0 });
    queue.add(row('a'));
    assert.deepEqual(await queue.flush(async () => { throw new Error('ran'); }), { held: 0, lost: 0, error: 'no PLUGIN_LOG_DIR and no HOME', screen: screenLine(spec.screen.off, { plugin: 'demo' }) });
    queue.add(row('b'));
    assert.deepEqual(await queue.flush(async () => { throw new Error('ran'); }), { held: 0, lost: 0 });
  });

  it('fails on a real unwritable directory, holds the rows, and writes them once it recovers', async () => {
    const parent = scratch('recover');
    const dir = join(parent, 'logs');
    try {
      writeFileSync(dir, 'a file where the directory goes');
      const queue = new log.PluginLogQueue(dir, 'demo');
      queue.add(row('a')); queue.add(row('b', 'info'));
      const failed = await queue.flush(run);
      assert.equal(failed.held, 2);
      assert.match(failed.screen ?? '', new RegExp(`^demo: plugin log ${dir} failed: .+; 2 rows are kept for the next write$`));
      rmSync(dir);
      queue.add(row('c'));
      assert.deepEqual(await queue.flush(run), { held: 0, lost: 0 });
      assert.deepEqual(rows(dir, 'errors.jsonl').map((r) => r.msg), ['a', 'c']);
      assert.deepEqual(rows(dir, 'info.jsonl').map((r) => r.msg), ['b']);
      chmodSync(dir, 0o700);
    } finally { rmSync(parent, { recursive: true, force: true }); }
  });
});
