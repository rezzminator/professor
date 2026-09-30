#!/usr/bin/env node
// flight-index: rebuilds a flight directory's index.md from its task files'
// frontmatter and checks the graph the dispatcher runs on.
//
//   node flight-index.mjs [--check] {flight directory}
//
// index.md becomes the title line and the table, nothing else; --check prints
// the same report and writes nothing. Stdout carries
// the table, the delta against the index it replaced, the file collisions, the
// largest read and one ERROR line per defect.
//
// Exit 0: no defect. Exit 1: an ERROR or a collision printed (the index is
// still written).
// Exit 2: nothing written: the directory is missing, unreadable or holds no
// task file, so a broken run never reads as an empty flight.

import { readFileSync, writeFileSync, readdirSync, statSync, existsSync } from 'node:fs';
import { join, basename, resolve } from 'node:path';

const TASK_FILE = /^([1-9]\d*)-([a-z]+)\.md$/;
const COLUMNS = ['id', 'needs', 'rating', 'shares', 'reads', 'files', 'title'];
const LIST_KEYS = new Set(['needs', 'shares', 'reads', 'files']);
const RATINGS = new Set(['mechanical', 'precise', 'smart', 'main-chat']);
const READ_BUDGET = 25000;

function unquote(value) {
  const v = value.trim();
  if (v.length >= 2 && ((v[0] === '"' && v.at(-1) === '"') || (v[0] === "'" && v.at(-1) === "'"))) {
    return v.slice(1, -1);
  }
  return v;
}

function splitList(inner) {
  return inner.split(',').map(unquote).filter((item) => item !== '');
}

// Frontmatter subset the speccer writes: `key: scalar`, `key: [a, b]` (one or
// several lines) and `key:` followed by `- item` lines.
function parseFrontmatter(text) {
  const lines = text.split('\n');
  if ((lines[0] ?? '').trim() !== '---') return { error: 'no frontmatter: the first line is not ---' };
  const end = lines.findIndex((line, i) => i > 0 && line.trim() === '---');
  if (end === -1) return { error: 'frontmatter never closes: no second --- line' };
  const fields = {};
  for (let i = 1; i < end; i++) {
    const line = lines[i];
    if (line.trim() === '') continue;
    const match = /^([A-Za-z_][\w-]*):(.*)$/.exec(line);
    if (!match) return { error: `frontmatter line ${i + 1} is not key: value: ${line.trim()}` };
    const [, key, rest] = match;
    let value = rest.trim();
    if (value.startsWith('[')) {
      while (!value.includes(']') && i + 1 < end) value += ' ' + lines[++i].trim();
      if (!value.endsWith(']')) return { error: `frontmatter key ${key}: list never closes with ]` };
      fields[key] = splitList(value.slice(1, -1));
    } else if (value === '' && LIST_KEYS.has(key)) {
      const items = [];
      while (i + 1 < end && /^\s*-\s/.test(lines[i + 1])) items.push(unquote(lines[++i].replace(/^\s*-\s/, '')));
      fields[key] = items;
    } else {
      fields[key] = unquote(value);
    }
  }
  return { fields };
}

function cell(value) {
  const text = Array.isArray(value) ? value.join(', ') : value;
  return text.replaceAll('|', '\\|');
}

function row(task) {
  return `| ${COLUMNS.map((column) => cell(task[column])).join(' | ')} |`;
}

// A row's content, cell by cell, so a formatting-only difference is no change.
function normalize(line) {
  const cells = line.trim().replace(/^\|/, '').replace(/\|$/, '').split(/(?<!\\)\|/).map((c) => c.trim());
  const empty = (c) => (['—', '–', '-', 'none', '[]'].includes(c) ? '' : c);
  return cells.map((c, i) => (LIST_KEYS.has(COLUMNS[i]) ? splitList(empty(c)).join(', ') : c)).join(' | ');
}

function oldRows(indexPath) {
  if (!existsSync(indexPath)) return new Map();
  const rows = new Map();
  for (const line of readFileSync(indexPath, 'utf8').split('\n')) {
    const match = /^\|\s*([^|]+?)\s*\|/.exec(line);
    if (!match || match[1] === 'id' || /^-+$/.test(match[1])) continue;
    rows.set(match[1], normalize(line));
  }
  return rows;
}

function levelAndLetter(id) {
  const match = /^(\d+)-([a-z]+)$/.exec(id);
  return match ? [Number(match[1]), match[2]] : [Infinity, id];
}

function byId(a, b) {
  const [la, xa] = levelAndLetter(a);
  const [lb, xb] = levelAndLetter(b);
  return la - lb || (xa < xb ? -1 : xa > xb ? 1 : 0);
}

function main(argv) {
  const check = argv[0] === '--check';
  const args = check ? argv.slice(1) : argv;
  if (args.length !== 1) {
    console.error('usage: node flight-index.mjs [--check] {flight directory}');
    return 2;
  }
  const dir = resolve(args[0]);
  let names;
  try {
    names = readdirSync(dir);
  } catch (err) {
    console.error(`NOT BUILT: cannot read ${dir}: ${err.message}`);
    return 2;
  }
  const taskNames = names.filter((name) => TASK_FILE.test(name)).sort((a, b) => byId(a.slice(0, -3), b.slice(0, -3)));
  if (taskNames.length === 0) {
    console.error(`NOT BUILT: no task file ({level}-{letter}.md) in ${dir}`);
    return 2;
  }

  const errors = [];
  const tasks = new Map();
  const unparsed = new Set();
  for (const name of taskNames) {
    const path = join(dir, name);
    const { fields, error } = parseFrontmatter(readFileSync(path, 'utf8'));
    if (error) {
      errors.push(`${name}: ${error}`);
      unparsed.add(name.slice(0, -3));
      continue;
    }
    const missing = COLUMNS.filter((column) => !(column in fields));
    if (missing.length) errors.push(`${name}: frontmatter lacks ${missing.join(', ')}`);
    const task = Object.fromEntries(COLUMNS.map((c) => [c, fields[c] ?? (LIST_KEYS.has(c) ? [] : '')]));
    for (const key of LIST_KEYS) if (!Array.isArray(task[key])) task[key] = splitList(task[key]);
    if (task.id !== name.slice(0, -3)) errors.push(`${name}: id ${task.id || '(empty)'} differs from the file name`);
    if (!RATINGS.has(task.rating)) errors.push(`${name}: rating ${task.rating || '(empty)'} is none of ${[...RATINGS].join(', ')}`);
    task.bytes = statSync(path).size;
    tasks.set(name.slice(0, -3), task);
  }

  for (const [id, task] of tasks) {
    for (const need of task.needs) if (!tasks.has(need) && !unparsed.has(need)) errors.push(`${id}.md: needs ${need}, which is no task file here`);
    for (const read of task.reads) if (!existsSync(join(dir, read))) errors.push(`${id}.md: reads ${read}, which is not in the directory`);
  }

  // Transitive needs, and every cycle reported once per task on it.
  const reach = new Map();
  const onCycle = new Set();
  const visit = (id, stack) => {
    if (reach.has(id)) return reach.get(id);
    if (stack.includes(id)) {
      stack.slice(stack.indexOf(id)).forEach((member) => onCycle.add(member));
      return new Set();
    }
    const all = new Set();
    for (const need of tasks.get(id)?.needs ?? []) {
      if (!tasks.has(need)) continue;
      all.add(need);
      for (const deeper of visit(need, [...stack, id])) all.add(deeper);
    }
    reach.set(id, all);
    return all;
  };
  for (const id of tasks.keys()) visit(id, []);
  for (const id of [...onCycle].sort(byId)) errors.push(`${id}.md: needs form a cycle through ${id}`);

  const owners = new Map();
  for (const [id, task] of tasks) for (const file of task.files) owners.set(file, [...(owners.get(file) ?? []), id]);
  const collisions = [];
  for (const [file, ids] of owners) {
    for (let i = 0; i < ids.length; i++) {
      for (let j = i + 1; j < ids.length; j++) {
        const [a, b] = [ids[i], ids[j]];
        if (!reach.get(a)?.has(b) && !reach.get(b)?.has(a)) collisions.push(`${file}: ${a}, ${b}`);
      }
    }
  }

  let largest = { bytes: 0, id: '' };
  for (const [id, task] of tasks) {
    const bytes = task.bytes + task.reads.reduce((sum, read) => {
      const path = join(dir, read);
      return sum + (existsSync(path) ? statSync(path).size : 0);
    }, 0);
    if (bytes > largest.bytes) largest = { bytes, id };
    if (bytes > READ_BUDGET) errors.push(`${id}.md: task file plus reads is ${bytes} chars, over the ${READ_BUDGET} budget`);
  }

  const indexPath = join(dir, 'index.md');
  const before = oldRows(indexPath);
  const ids = [...tasks.keys()].sort(byId);
  const rows = ids.map((id) => row(tasks.get(id)));
  const table = [`| ${COLUMNS.join(' | ')} |`, `| ${COLUMNS.map(() => '---').join(' | ')} |`, ...rows];
  try {
    if (!check) writeFileSync(indexPath, `# ${basename(dir)} — index\n\n${table.join('\n')}\n`);
  } catch (err) {
    console.error(`NOT BUILT: cannot write ${indexPath}: ${err.message}`);
    return 2;
  }

  const added = ids.filter((id) => !before.has(id));
  const changed = ids.filter((id, i) => before.has(id) && before.get(id) !== normalize(rows[i]));
  const removed = [...before.keys()].filter((id) => !tasks.has(id)).sort(byId);
  const list = (items) => (items.length ? items.join(', ') : 'none');

  console.log(`INDEX ${indexPath} · ${ids.length} tasks${check ? ' · check only, not written' : ''}`);
  console.log(table.join('\n'));
  console.log(`DELTA added ${list(added)} · changed ${list(changed)} · removed ${list(removed)}`);
  console.log(`COLLISIONS ${collisions.length}${collisions.length ? ': ' + collisions.join('; ') : ''}`);
  console.log(`LARGEST READ ${largest.bytes} chars (${largest.id})`);
  console.log(`ERRORS ${errors.length}`);
  for (const error of errors) console.log(`ERROR ${error}`);
  return errors.length || collisions.length ? 1 : 0;
}

process.exitCode = main(process.argv.slice(2));
