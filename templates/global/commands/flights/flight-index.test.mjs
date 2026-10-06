import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, writeFileSync, readFileSync, rmSync, mkdirSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, dirname } from 'node:path';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';

const SCRIPT = join(dirname(fileURLToPath(import.meta.url)), 'flight-index.mjs');

function flight(files) {
  const dir = mkdtempSync(join(tmpdir(), 'flight-index-'));
  for (const [name, text] of Object.entries(files)) writeFileSync(join(dir, name), text);
  return dir;
}

function task(id, { needs = '[]', rating = 'mechanical', shares = '[]', reads = '[]', files = '[src/a.ts]', title = `task ${id}`, body = '## Goal\n' } = {}) {
  return `---\nid: ${id}\ntitle: ${title}\nrating: ${rating}\nneeds: ${needs}\nshares: ${shares}\nreads: ${reads}\nfiles: ${files}\n---\n${body}`;
}

function run(dir, ...flags) {
  const result = spawnSync(process.execPath, [SCRIPT, ...flags, dir], { encoding: 'utf8' });
  return { code: result.status, out: result.stdout, err: result.stderr };
}

test('the index becomes the title and the table, and prose left in it is gone', () => {
  const dir = flight({
    '1-a.md': task('1-a', { files: '[src/a.ts]' }),
    '2-a.md': task('2-a', { needs: '[1-a]', files: '[src/b.ts]', title: 'uses a | b' }),
    'index.md': '# old — index\n\n| id | needs |\n| --- | --- |\n| 1-a | |\n\n## NOTES\n- Revision (1-a round 1): a long history nobody should re-read.\n',
  });
  try {
    const { code, out } = run(dir);
    assert.equal(code, 0, out);
    const index = readFileSync(join(dir, 'index.md'), 'utf8');
    assert.doesNotMatch(index, /NOTES|Revision/);
    assert.match(index, /^# flight-index-\w+ — index\n\n\| id \| needs \| rating \| shares \| reads \| files \| title \|\n/);
    assert.match(index, /\| 2-a \| 1-a \| mechanical \|  \|  \| src\/b\.ts \| uses a \\\| b \|/);
    assert.match(out, /DELTA added 2-a · changed 1-a · removed none/);
    assert.match(out, /ERRORS 0/);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('a rerun over an unchanged directory reports no delta', () => {
  const dir = flight({ '1-a.md': task('1-a'), '1-b.md': task('1-b', { files: '[src/b.ts]' }) });
  try {
    run(dir);
    const { code, out } = run(dir);
    assert.equal(code, 0, out);
    assert.match(out, /DELTA added none · changed none · removed none/);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('a removed task file shows as removed', () => {
  const dir = flight({ '1-a.md': task('1-a'), '1-b.md': task('1-b', { files: '[src/b.ts]' }) });
  try {
    run(dir);
    rmSync(join(dir, '1-b.md'));
    const { out } = run(dir);
    assert.match(out, /DELTA added none · changed none · removed 1-b/);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('lists spread over several lines and block lists parse like inline ones', () => {
  const multi = '---\nid: 1-a\ntitle: multi\nrating: precise\nneeds: []\nshares:\n  - pkg-x\nreads: []\nfiles: [src/a.ts,\n  src/b.ts]\n---\n';
  const dir = flight({ '1-a.md': multi });
  try {
    const { code, out } = run(dir);
    assert.equal(code, 0, out);
    assert.match(out, /\| 1-a \|  \| precise \| pkg-x \|  \| src\/a\.ts, src\/b\.ts \| multi \|/);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('frontmatter defects are ERROR lines and exit 1, the old index stays untouched', () => {
  const old = '# old — index\n';
  const dir = flight({
    'index.md': old,
    '1-a.md': task('1-a', { rating: 'main' }),
    '1-b.md': task('1-c', { files: '[src/b.ts]' }),
    '2-a.md': task('2-a', { needs: '[1-a, diagnosis-e1-reload]', reads: '[0-missing.md]', files: '[src/c.ts]' }),
    '2-b.md': 'no frontmatter at all\n',
    '3-a.md': task('3-a', { needs: '[2-b]', files: '[src/d.ts]' }),
  });
  try {
    const { code, out } = run(dir);
    assert.equal(code, 1, out);
    assert.match(out, /ERROR 1-a\.md: rating main is none of mechanical, precise, smart/);
    assert.match(out, /ERROR 1-b\.md: id 1-c differs from the file name/);
    assert.match(out, /ERROR 2-a\.md: needs diagnosis-e1-reload, which is no task file here/);
    assert.match(out, /ERROR 2-a\.md: reads 0-missing\.md, which is not in the directory/);
    assert.match(out, /ERROR 2-b\.md: no frontmatter: the first line is not ---/);
    assert.doesNotMatch(out, /3-a\.md: needs 2-b/, 'an unparsable task is reported once, not again by every task needing it');
    assert.equal(readFileSync(join(dir, 'index.md'), 'utf8'), old);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('a needs cycle is an error on every task in it', () => {
  const dir = flight({
    '1-a.md': task('1-a', { needs: '[2-a]' }),
    '2-a.md': task('2-a', { needs: '[1-a]', files: '[src/b.ts]' }),
  });
  try {
    const { code, out } = run(dir);
    assert.equal(code, 1, out);
    assert.match(out, /ERROR 1-a\.md: needs form a cycle through 1-a/);
    assert.match(out, /ERROR 2-a\.md: needs form a cycle through 2-a/);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('a file in two tasks collides unless a needs chain orders them', () => {
  const dir = flight({
    '1-a.md': task('1-a', { files: '[docs/lanes.md]' }),
    '1-b.md': task('1-b', { files: '[docs/lanes.md]' }),
    '2-a.md': task('2-a', { needs: '[1-a]', files: '[src/x.ts]' }),
    '3-a.md': task('3-a', { needs: '[2-a]', files: '[src/x.ts]' }),
  });
  try {
    const { code, out } = run(dir);
    assert.equal(code, 1, out);
    assert.match(out, /COLLISIONS 1: docs\/lanes\.md: 1-a, 1-b\n/);
    const index = readFileSync(join(dir, 'index.md'), 'utf8');
    assert.match(index, /\| 1-a \|/);
    assert.match(index, /\| 1-b \|/);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('the largest read counts the task file plus its reads, and over budget is an error', () => {
  const big = 'x'.repeat(25000);
  const dir = flight({
    '0-contract.md': big,
    '1-a.md': task('1-a', { reads: '[0-contract.md]' }),
    '1-b.md': task('1-b', { files: '[src/b.ts]' }),
  });
  try {
    const { code, out } = run(dir);
    const size = readFileSync(join(dir, '1-a.md')).length + 25000;
    assert.match(out, new RegExp(`LARGEST READ ${size} chars \\(1-a\\)`));
    assert.equal(code, 1, out);
    assert.match(out, new RegExp(`ERROR 1-a\\.md: task file plus reads is ${size} chars, over the 25000 budget`));
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('a directory with no task file writes nothing and exits 2', () => {
  const dir = flight({ 'run.md': 'flight x\n' });
  try {
    const { code, err } = run(dir);
    assert.equal(code, 2);
    assert.match(err, /NOT BUILT: no task file/);
    assert.throws(() => readFileSync(join(dir, 'index.md')));
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('a missing directory exits 2 and says so', () => {
  const { code, err } = run(join(tmpdir(), 'flight-index-does-not-exist'));
  assert.equal(code, 2);
  assert.match(err, /NOT BUILT: cannot read/);
});

test('--check reports the same and leaves the index untouched', () => {
  const old = '# old — index\n\n## NOTES\n- kept by --check\n';
  const dir = flight({ '1-a.md': task('1-a', { rating: 'main' }), 'index.md': old });
  try {
    const { code, out } = run(dir, '--check');
    assert.equal(code, 1, out);
    assert.match(out, /check only, not written/);
    assert.match(out, /^INDEX .* · 1 tasks · check only, not written · not written: 1 errors$/m);
    assert.match(out, /ERROR 1-a\.md: rating main/);
    assert.equal(readFileSync(join(dir, 'index.md'), 'utf8'), old);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('a 0- shared file is never read as a task', () => {
  const dir = flight({ '0-contract.md': '# the contract, no frontmatter\n', '1-a.md': task('1-a', { reads: '[0-contract.md]' }) });
  try {
    const { code, out } = run(dir);
    assert.equal(code, 0, out);
    assert.match(out, /· 1 tasks/);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('a hand-written row differing only in spacing is not a change', () => {
  const index = '# x — index\n\n| id | needs | rating | shares | reads | files | title |\n| --- | --- | --- | --- | --- | --- | --- |\n|1-a| — | mechanical | none |[]|src/a.ts ,src/b.ts|task 1-a|\n';
  const dir = flight({ '1-a.md': task('1-a', { files: '[src/a.ts, src/b.ts]' }), 'index.md': index });
  try {
    const { out } = run(dir);
    assert.match(out, /DELTA added none · changed none · removed none/);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('a main-chat task is a row like any other', () => {
  const dir = flight({ '1-a.md': task('1-a', { rating: 'main-chat', files: '[.claude/scripts/dev.sh]' }) });
  try {
    const { code, out } = run(dir);
    assert.equal(code, 0, out);
    assert.match(out, /ERRORS 0/);
    assert.match(readFileSync(join(dir, 'index.md'), 'utf8'), /\| 1-a \|  \| main-chat \|  \|  \| \.claude\/scripts\/dev\.sh \| task 1-a \|/);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('input handling preserves the index on errors and accepts valid task lists', async (t) => {
  const cases = [
    {
      name: 'an ERROR keeps the old index',
      files: { '1-a.md': task('1-a', { rating: 'main' }), 'index.md': '# old — index\n' },
      code: 1,
      lines: [/^INDEX .* · 1 tasks · not written: 1 errors$/m, /^ERROR 1-a\.md: rating main is none of /m],
    },
    {
      name: 'a collision alone still writes',
      files: {
        '1-a.md': task('1-a', { files: '[docs/lanes.md]' }),
        '1-b.md': task('1-b', { files: '[docs/lanes.md]' }),
      },
      code: 1,
      writes: true,
      lines: [/^COLLISIONS 1: docs\/lanes\.md: 1-a, 1-b$/m, /^ERRORS 0$/m],
    },
    {
      name: 'an unparsed task is never a removal',
      files: { '1-a.md': task('1-a'), '2-b.md': task('2-b', { files: '[src/b.ts]' }) },
      cleanFirst: true,
      rewrite: ['2-b.md', 'no frontmatter at all\n'],
      code: 1,
      lines: [/^DELTA added none · changed none · removed none$/m, /^ERROR 2-b\.md: no frontmatter: /m],
    },
    {
      name: 'a directory named like a task file',
      files: { '1-a.md': task('1-a', { needs: '[1-b]' }) },
      directory: '1-b.md',
      code: 1,
      lines: [/^ERROR 1-b\.md: cannot read: EISDIR: illegal operation on a directory, read$/m, /^ERRORS 1$/m],
    },
    {
      name: 'an unreadable task is never a removal',
      files: {
        '1-a.md': task('1-a', { needs: '[1-b]' }),
        '1-b.md': task('1-b', { files: '[src/b.ts]' }),
      },
      cleanFirst: true,
      directory: '1-b.md',
      code: 1,
      lines: [/^DELTA added none · changed none · removed none$/m, /^ERROR 1-b\.md: cannot read: EISDIR: /m, /^ERRORS 1$/m],
    },
    {
      name: 'a file listed twice in one task',
      files: { '1-a.md': task('1-a', { files: '[src/a.ts, src/a.ts]' }) },
      code: 0,
      lines: [/^COLLISIONS 0$/m],
    },
    ...['—', '–', '-', 'none', '[]'].map((marker) => ({
      name: `needs: ${marker}`,
      files: { '1-a.md': task('1-a', { needs: marker, shares: marker, reads: marker }) },
      code: 0,
      lines: [/^ERRORS 0$/m, /^\| 1-a \|  \| mechanical \|  \|  \| src\/a\.ts \| task 1-a \|$/m],
    })),
    {
      name: 'files: none',
      files: { '1-a.md': task('1-a', { files: 'none' }) },
      code: 0,
      lines: [/^ERRORS 0$/m, /^\| 1-a \|  \| mechanical \|  \|  \|  \| task 1-a \|$/m],
    },
    {
      name: 'a misnamed task file',
      files: {
        '1-a.md': task('1-a'),
        '1-b.txt': 'any content\n',
        '2-A.md': 'any content\n',
        '0-notes.md': 'notes\n',
        'run.md': 'run\n',
      },
      directory: '3-scratch.txt',
      code: 1,
      lines: [
        /^ERROR 1-b\.txt: named like a task file but not \{level\}-\{letter\}\.md$/m,
        /^ERROR 2-A\.md: named like a task file but not \{level\}-\{letter\}\.md$/m,
        /^ERRORS 2$/m,
      ],
    },
  ];
  for (const scenario of cases) {
    await t.test(scenario.name, () => {
      const dir = flight(scenario.files);
      try {
        let old = scenario.files['index.md'];
        if (scenario.cleanFirst) {
          const first = run(dir);
          assert.equal(first.code, 0, first.out);
          old = readFileSync(join(dir, 'index.md'), 'utf8');
        }
        if (scenario.rewrite) writeFileSync(join(dir, scenario.rewrite[0]), scenario.rewrite[1]);
        if (scenario.directory) {
          rmSync(join(dir, scenario.directory), { force: true });
          mkdirSync(join(dir, scenario.directory));
        }
        const { code, out, err } = run(dir);
        assert.equal(code, scenario.code, out + err);
        for (const line of scenario.lines) assert.match(out, line);
        assert.equal(err, '', 'task-file defects belong on stdout without a stack trace');
        if (old !== undefined) assert.equal(readFileSync(join(dir, 'index.md'), 'utf8'), old);
        else if (scenario.writes) {
          const index = readFileSync(join(dir, 'index.md'), 'utf8');
          assert.match(index, /\| 1-a \|/);
          assert.match(index, /\| 1-b \|/);
        } else if (code === 1) assert.throws(() => readFileSync(join(dir, 'index.md')), { code: 'ENOENT' });
      } finally {
        rmSync(dir, { recursive: true, force: true });
      }
    });
  }
});
