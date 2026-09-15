// @ts-nocheck — this file drives persist.js (a CommonJS host script at the repo root, OUTSIDE the
// engine's sandboxed TS surface) as a real child process. The engine package has no @types/node
// (its own src/ never touches the filesystem — workflow.js runs sandboxed), so Node builtins
// (fs/path/os/child_process) have no ambient types here; ts-nocheck keeps that a test-file-local
// concern instead of adding a dependency the rest of the package has no other use for.
//
// persist.js resolves its repo root via `execSync('git rev-parse --show-toplevel', { cwd: __dirname })`
// — i.e. relative to WHERE THE SCRIPT FILE ITSELF LIVES, not the caller's cwd. So to keep every write
// out of the real repo, each test copies persist.js verbatim into a fresh throwaway git repo under
// os.tmpdir() and runs THAT copy — __dirname then resolves to the throwaway repo, and every RR/... path
// persist.js writes lands there instead of the real skills tree.
import { describe, it, expect, afterEach } from 'vitest';
import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

const REAL_PERSIST_JS = path.join(__dirname, '..', '..', 'persist.js');

const tmpDirs: string[] = [];

// a fresh throwaway git repo (persist.js's own `git rev-parse --show-toplevel` needs a real .git
// upward from the script) with a verbatim copy of the real persist.js inside it.
function mkThrowawayRepo(): string {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'rr-persist-test-'));
  tmpDirs.push(dir);
  spawnSync('git', ['init', '--quiet'], { cwd: dir, encoding: 'utf8' });
  fs.copyFileSync(REAL_PERSIST_JS, path.join(dir, 'persist.js'));
  return dir;
}

function runPersist(dir: string, outFile: string) {
  const res = spawnSync('node', [path.join(dir, 'persist.js'), outFile], {
    cwd: dir,
    encoding: 'utf8',
  });
  return res;
}

afterEach(() => {
  while (tmpDirs.length) {
    const d = tmpDirs.pop()!;
    fs.rmSync(d, { recursive: true, force: true });
  }
});

// ── (7a) tombstone on unparseable output file (garbage text) ──
// Incident: v1–v3 of a retried query left zero artifacts — an aborted/killed run must leave an
// auditable trace instead of vanishing.
describe('persist.js — tombstone salvage on unparseable output (v3.2.0)', () => {
  it('exits 0 and writes RR/_aborted/*-tombstone.md with the parse-error reason + the last-30-lines block', () => {
    const dir = mkThrowawayRepo();
    const outFile = path.join(dir, 'garbage-output.json');
    const garbage = 'this is not json at all {{{ totally broken output stream\nline two\nline three';
    fs.writeFileSync(outFile, garbage);

    const res = runPersist(dir, outFile);
    expect(res.status).toBe(0);

    const summary = JSON.parse(res.stdout);
    expect(summary.tombstone).toBeTruthy();
    expect(summary.tombstone).toMatch(/RR[\\/]_aborted[\\/].*-tombstone\.md$/);
    expect(summary.reason).toBeTruthy(); // the JSON.parse error message

    const tombstoneContent = fs.readFileSync(summary.tombstone, 'utf8');
    expect(tombstoneContent).toContain('# RR aborted-run tombstone');
    expect(tombstoneContent).toContain(summary.reason);
    expect(tombstoneContent).toContain('## Last 30 lines of raw output');
    expect(tombstoneContent).toContain('this is not json at all');
  });
});

// ── (7b) tombstone on parseable output whose result.files is empty ──
describe('persist.js — tombstone salvage on an empty result.files (v3.2.0)', () => {
  it('exits 0 and writes a tombstone reporting "result.files is empty" even though the JSON parsed fine', () => {
    const dir = mkThrowawayRepo();
    const outFile = path.join(dir, 'empty-files-output.json');
    const raw = { result: { dir: 'RR/empty-test', verdict: null, confidence: null, files: {} } };
    fs.writeFileSync(outFile, JSON.stringify(raw));

    const res = runPersist(dir, outFile);
    expect(res.status).toBe(0);

    const summary = JSON.parse(res.stdout);
    expect(summary.tombstone).toBeTruthy();
    expect(summary.reason).toBe('workflow completed but result.files is empty');

    const tombstoneContent = fs.readFileSync(summary.tombstone, 'utf8');
    expect(tombstoneContent).toContain('# RR aborted-run tombstone');
    expect(tombstoneContent).toContain('workflow completed but result.files is empty');
    expect(tombstoneContent).toContain('## Last 30 lines of raw output');
  });
});

// ── (7c) happy path — files map incl. _sources.json referencing one existing + one missing cache file ──
// Incident: an ad-hoc, unfiltered sweep archived 136/191 foreign files; persist.js's own resource
// archiving must follow ONLY _sources.json's claim-referenced paths, counting a missing one rather
// than failing.
describe('persist.js — happy path writes files + provenance-filtered resource archiving (v3.2.0)', () => {
  it('writes every file, copies only the existing cache path into resources/, and reports {copied:1, missing:1}', () => {
    const dir = mkThrowawayRepo();

    const cacheFile = path.join(dir, 'cache-fixture.txt');
    fs.writeFileSync(cacheFile, 'cached page content');
    const missingCachePath = path.join(dir, 'does-not-exist-on-disk.txt');

    const sourcesDoc = {
      note: 'claim-referenced cache files',
      sources: [
        { cachePath: cacheFile, source: 'https://real-source.example/a' },
        { cachePath: missingCachePath, source: 'https://real-source.example/b' },
      ],
    };
    const raw = {
      result: {
        dir: 'RR/happy-test',
        verdict: 'pgvector wins',
        confidence: 'high',
        files: {
          'result.md': '# Report\n\nbody',
          '_sources.json': JSON.stringify(sourcesDoc),
        },
      },
    };
    const outFile = path.join(dir, 'happy-output.json');
    fs.writeFileSync(outFile, JSON.stringify(raw));

    const res = runPersist(dir, outFile);
    expect(res.status).toBe(0);

    const summary = JSON.parse(res.stdout);
    expect(summary.dir).toBe(path.join(dir, 'RR', 'happy-test'));
    expect(summary.written).toBe(2);
    expect(summary.files).toEqual(['_sources.json', 'result.md']);
    expect(summary.verdict).toBe('pgvector wins');
    expect(summary.confidence).toBe('high');
    expect(summary.resources).toEqual({ copied: 1, missing: 1 });

    expect(fs.readFileSync(path.join(summary.dir, 'result.md'), 'utf8')).toBe('# Report\n\nbody');
    expect(fs.existsSync(path.join(summary.dir, '_sources.json'))).toBe(true);
    const copiedPath = path.join(summary.dir, 'resources', path.basename(cacheFile));
    expect(fs.existsSync(copiedPath)).toBe(true);
    expect(fs.readFileSync(copiedPath, 'utf8')).toBe('cached page content');
  });
});
