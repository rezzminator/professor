// @ts-nocheck — this file drives midrun.js (a CommonJS host script at the repo root, OUTSIDE the
// engine's sandboxed TS surface) as a real child process, mirroring persist.test.ts's harness. The
// engine package has no @types/node (its own src/ never touches the filesystem), so Node builtins
// have no ambient types here; ts-nocheck keeps that a test-file-local concern.
//
// midrun.js resolves its repo root via `git rev-parse --show-toplevel` from process.cwd() first
// (falling back to __dirname, then cwd) and writes its report under {repoRoot}/tmp/rr-midrun/. Each
// test copies midrun.js verbatim into a fresh throwaway git repo under os.tmpdir() and runs THAT
// copy with cwd set to the throwaway repo, so every write lands there instead of the real repo.
import { describe, it, expect, afterEach } from 'vitest';
import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

const REAL_MIDRUN_JS = path.join(__dirname, '..', '..', 'midrun.js');
const midrunExists = fs.existsSync(REAL_MIDRUN_JS);

const tmpDirs: string[] = [];

function mkThrowawayRepo(): string {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'rr-midrun-test-'));
  tmpDirs.push(dir);
  spawnSync('git', ['init', '--quiet'], { cwd: dir, encoding: 'utf8' });
  fs.copyFileSync(REAL_MIDRUN_JS, path.join(dir, 'midrun.js'));
  return dir;
}

function writeJournal(repoDir: string, runName: string, lines: unknown[]): string {
  const runDir = path.join(repoDir, runName);
  fs.mkdirSync(runDir, { recursive: true });
  fs.writeFileSync(path.join(runDir, 'journal.jsonl'), lines.map((l) => JSON.stringify(l)).join('\n') + '\n');
  return runDir;
}

function runMidrun(repoDir: string, args: string[]) {
  const res = spawnSync('node', [path.join(repoDir, 'midrun.js'), ...args], {
    cwd: repoDir,
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

// item 8 is conditional on midrun.js already existing (another agent was authoring it at task time).
const maybeDescribe = midrunExists ? describe : describe.skip;

maybeDescribe('midrun.js — DEGRADED shape (finalize agents present, zero brainer coords) (v3.2.0)', () => {
  const journalLines = [
    { type: 'dispatch', key: 'k-init', agentId: 'agent-init-1' },
    {
      type: 'completion',
      key: 'k-init',
      agentId: 'agent-init-1',
      result: {
        refinement: { facts: [{ fact: 'pgvector handles most workloads', why: 'headline' }] },
        synthesiser: { focus: 'lead with cost' },
      },
    },
    { type: 'dispatch', key: 'k-ref', agentId: 'agent-ref-1' },
    {
      type: 'completion',
      key: 'k-ref',
      agentId: 'agent-ref-1',
      result: { report: 'refined: 0.96 (verified)', queriesTried: ['q1'], counterFound: false },
    },
    { type: 'dispatch', key: 'k-judge', agentId: 'agent-judge-1' },
    {
      type: 'completion',
      key: 'k-judge',
      agentId: 'agent-judge-1',
      result: {
        goalMet: true,
        verificationSound: true,
        needsCompute: false,
        computeSound: true,
        reasoning: 'answer verified against the ledger',
        directive: '',
      },
    },
  ];

  it('status mode sets degraded:true in the stdout JSON', () => {
    const dir = mkThrowawayRepo();
    const runDir = writeJournal(dir, 'wf_degraded', journalLines);

    const res = runMidrun(dir, ['status', runDir]);
    expect(res.status).toBe(0);
    const summary = JSON.parse(res.stdout);
    expect(summary.degraded).toBe(true);
    expect(summary.waves.coordinated).toBe(0);
    expect(summary.headline).toMatch(/DEGRADED/);

    const reportContent = fs.readFileSync(summary.report, 'utf8');
    expect(reportContent).toContain('DEGRADED SHAPE');
  });

  it('findings mode falls back to the finalize reconstruction (initiator/refiner/judge) instead of a coord', () => {
    const dir = mkThrowawayRepo();
    const runDir = writeJournal(dir, 'wf_degraded', journalLines);

    const res = runMidrun(dir, ['findings', runDir]);
    expect(res.status).toBe(0);
    const summary = JSON.parse(res.stdout);
    expect(summary.degraded).toBe(true);

    const reportContent = fs.readFileSync(summary.report, 'utf8');
    expect(reportContent).toContain('DEGRADED fallback');
    expect(reportContent).toContain('pgvector handles most workloads'); // initiator fact group
    expect(reportContent).toContain('counterFound: false'); // refiner pass
    expect(reportContent).toContain('goalMet: true'); // judge
    expect(reportContent).not.toContain('Primary — latest brainer coord memory');
  });
});

maybeDescribe('midrun.js — healthy run with one brainer coord (v3.2.0)', () => {
  it('findings mode reports the coord\'s resultSoFar answer, not degraded', () => {
    const dir = mkThrowawayRepo();
    const journalLines = [
      { type: 'dispatch', key: 'k-coord', agentId: 'agent-coord-1' },
      {
        type: 'completion',
        key: 'k-coord',
        agentId: 'agent-coord-1',
        result: {
          resultSoFar: {
            answer: 'pgvector is the best fit for this workload',
            confidence: 'high',
            keyClaimIds: [1, 2],
            resolved: ['index type'],
            openGaps: [],
            tensions: [],
          },
          rescore: [],
          add: [],
          lookupNext: [],
          rename: [],
          drop: [],
          stop: { done: true, reason: 'goal answered' },
        },
      },
    ];
    const runDir = writeJournal(dir, 'wf_healthy', journalLines);

    const res = runMidrun(dir, ['findings', runDir]);
    expect(res.status).toBe(0);
    const summary = JSON.parse(res.stdout);
    expect(summary.degraded).toBe(false);
    expect(summary.waves.coordinated).toBe(1);

    const reportContent = fs.readFileSync(summary.report, 'utf8');
    expect(reportContent).toContain('Primary — latest brainer coord memory');
    expect(reportContent).toContain('pgvector is the best fit for this workload');
  });
});
