// The body of one sub-agent's row in Claude Code's agent list. Every row of the
// list is drawn as one instrument panel: the same columns at the same cells in
// every row, so a glance down a column compares the agents.
//
//   pulse gauge + last growth + share + tokens/window │ who │ model·effort │
//   state + time │ Σ spend │ cache │ flags │ @directory │ task › progress
//
// The engine's props give the live half (status, tokens, readings, last
// request); pfm's door gives the half only transcripts hold (spend with every
// agent below, tool errors, compactions, cache hit, last activity, the agents
// below) and the colours. A fact pfm has not answered yet draws as `…`, one it
// could not give as `?` beside the cause: a missing fact never draws as zero.
// When the row is too narrow every row of the list gives up the same parts, in
// GIVE's order, so the columns stay aligned.

import type { DoorFacts, DoorLook, DoorPulse, DoorState } from './door';
import { pulse, type PulseCell } from './pulse';
import { cells, clean, cut, duration, fit, pad, share, tokens } from './text';

/** The props of an AgentRow this file reads: the engine's AgentInfo, structurally. */
export type RowAgent = {
  id: string; name?: string; type: string; description: string; label: string;
  status: 'pending' | 'running' | 'waiting' | 'idle' | 'completed' | 'failed' | 'killed';
  taskStatus: string; parentId?: string; startTime: number; cwd: string;
  model?: string; effort?: string | number; contextWindowSize?: number;
  tokenCount: number; tokenSamples: readonly number[];
  lastRequest?: { at: number; model: string; effort?: string | number; cacheTtl?: '5m' | '1h' };
  cost?: { usd: number; hasUnknownPrice?: true };
};

/** What pfm's door last said, as the Door class holds it. */
export type RowPfm = {
  state: DoorState; look: DoorLook | undefined; named: ReadonlySet<string>;
  facts: (id: string) => DoorFacts | undefined; read: (id: string) => number | undefined;
};

/** Everything one drawing reads: every agent that has a row (in the list's order), pfm's answer, the time, the row's width. */
export type Scene = { agents: readonly RowAgent[]; pfm: RowPfm; now: number; columns: number | undefined; sessionCwd: string | undefined };

type Rank = {
  gauge: number; tokens: 'rate' | 'plain' | 'none'; who: number; family: boolean; effortWord: boolean; model: boolean;
  spend: 'words' | 'slash' | 'usd' | 'none'; cache: 'full' | 'left' | 'none';
  /** `'short'`: the flags without their words, `2 err` and `↳1/3`. */
  flags: 'words' | 'short';
  /** The cells every row keeps for its task's words after its columns and flags. */
  task: number;
};
const FULL: Rank = { gauge: 15, tokens: 'rate', who: 36, family: true, effortWord: true, model: true, spend: 'words', cache: 'full', flags: 'words', task: 40 };
/** What a list too wide for its rows gives up, one step at a time, least missed first. */
const GIVE: ReadonlyArray<Partial<Rank>> = [
  { spend: 'slash' }, { tokens: 'plain' }, { task: 20 }, { flags: 'short' }, { spend: 'usd' }, { gauge: 8 }, { tokens: 'none' }, { cache: 'left' },
  { effortWord: false }, { cache: 'none' }, { family: false }, { spend: 'none' }, { model: false }, { who: 12 }, { task: 0 },
];
const RANKS: readonly Rank[] = GIVE.reduce<Rank[]>((ranks, step) => [...ranks, { ...ranks[ranks.length - 1]!, ...step }], [FULL]);

/** The fewest cells a task's words are drawn in; with less left they are left out. */
const TASK_MIN = 8;
const CAUSE_MAX = 56;
const CAUSE_MIN = 16;
/** What stands before the cause of pfm's missing or old facts, and alone where the cause is said on another row. */
const MARK = 'pfm ✗';
const IDLE_FROM_MS = 60_000;
const IDLE_LONG_MS = 300_000;
const CACHE_MS = { '5m': 300_000, '1h': 3_600_000 } as const;
const RESET = '\x1b[0m';
const REVERSE = '\x1b[7m';
/**
 * The look before pfm has given one, or without pfm: weight only, red for a failure. With no scale of pfm's the strip
 * is flat, its fill alone (one glyph, no step reaching a level, no drop), and with no thresholds no amount is toned:
 * the mod keeps no copy of the scale or the tones.
 */
const NEVER = Number.POSITIVE_INFINITY;
const BARE: DoorLook = {
  palette: { tokens: '\x1b[1m', muted: '\x1b[2m', dim: '\x1b[2m', bad: '\x1b[31m' }, effortColors: {},
  pulse: { glyphs: ['_'], drop: '_', steps: [], dropFrom: NEVER, stepMs: 0 },
  tones: { usd: { warn: NEVER, bad: NEVER }, share: { warn: NEVER, bad: NEVER }, cacheHit: { warn: -NEVER, good: NEVER } },
};

type Brush = {
  (role: string, text: string): string;
  /** The gauge's lit cells: the role's colour as the cell's ground, the glyph cut out of it. */
  lit: (role: string, text: string) => string;
  effort: (level: string, text: string) => string;
  /** The look's pulse scale. */
  pulse: DoorPulse;
  /** The role of a share of the context window, of a dollar amount and of a cache-hit share, by the look's tones. */
  shareTone: (percent: number) => string;
  usdTone: (usd: number) => string;
  hitTone: (percent: number) => string;
};

function brush(look: DoorLook): Brush {
  const wrap = (sgr: string | undefined, text: string) => (text === '' || !sgr ? text : `${sgr}${text}${RESET}`);
  const paint = ((role: string, text: string) => wrap(look.palette[role], text)) as Brush;
  paint.lit = (role, text) => (text === '' ? '' : `${look.palette[role] ?? ''}${REVERSE}${text}${RESET}`);
  paint.effort = (level, text) => wrap(look.effortColors[level] ?? look.palette.effort, text);
  paint.pulse = look.pulse;
  const { usd, share, cacheHit } = look.tones;
  paint.shareTone = (percent) => (percent >= share.bad ? 'bad' : percent >= share.warn ? 'warn' : 'good');
  paint.usdTone = (amount) => (amount >= usd.bad ? 'bad' : amount >= usd.warn ? 'warn' : 'label');
  paint.hitTone = (percent) => (percent >= cacheHit.good ? 'good' : percent >= cacheHit.warn ? 'warn' : 'bad');
  return paint;
}

const ended = (agent: RowAgent) => agent.status === 'completed' || agent.status === 'failed' || agent.status === 'killed';

/** How a pfm fact stands for one agent: its facts, or why there are none. */
type Known = { facts: DoorFacts } | { facts?: undefined; why: 'waiting' | 'failed' | 'ended' };

function known(agent: RowAgent, pfm: RowPfm): Known {
  const facts = pfm.facts(agent.id);
  if (facts) return { facts };
  if (pfm.state.state === 'failed') return { why: 'failed' };
  // One pfm never gave facts of (no answer yet, or no transcript of it) and that has ended stays unknown to it.
  return { why: pfm.state.state === 'ok' && ended(agent) ? 'ended' : 'waiting' };
}

function gaugeText(strip: readonly PulseCell[], tone: string, paint: Brush): string {
  let out = '';
  let run = '';
  let style = '';
  const flush = () => { out += style === 'lit' ? paint.lit(tone, run) : paint(style, run); run = ''; };
  for (const cell of strip) {
    const next = cell.lit ? 'lit' : cell.level === 'unborn' ? 'muted' : 'window';
    if (next !== style) { flush(); style = next; }
    const { glyphs, drop } = paint.pulse;
    run += cell.level === 'unborn' ? (cell.lit ? ' ' : glyphs[0]!) : cell.level === 'drop' ? drop : glyphs[Math.min(cell.level, glyphs.length - 1)]!;
  }
  flush();
  return out;
}

/** `lead`: blanks after the gauge that put a row less nested than the list's deepest back on the same cells. */
function pulseColumn(agent: RowAgent, facts: DoorFacts | undefined, rank: Rank, lead: number, paint: Brush): string {
  const window = facts && facts.contextWindow > 0 ? facts.contextWindow : agent.contextWindowSize;
  const used = agent.tokenCount;
  const full = share(used, window);
  const tone = full === undefined ? 'muted' : paint.shareTone(full);
  let out = gaugeText(pulse(agent.tokenSamples, used, window, rank.gauge, paint.pulse), tone, paint) + ' '.repeat(lead);
  if (rank.tokens === 'rate') {
    // The newest cell's reading in figures, beside it: it names what the trace counts.
    const samples = agent.tokenSamples;
    const grown = samples.length < 2 ? 0 : samples[samples.length - 1]! - samples[samples.length - 2]!;
    out += ` ${paint(grown === 0 ? 'muted' : 'window', (grown > 0 ? `+${tokens(grown)}` : grown < 0 ? `−${tokens(-grown)}` : '+0').padStart(7))}`;
  }
  out += ` ${paint(tone, (full === undefined ? '?%' : `${full}%`).padStart(4))}`;
  if (rank.tokens === 'none') return out;
  return `${out} ${paint('tokens', tokens(used).padStart(6))}${paint('muted', '/')}${paint('window', window === undefined ? '?' : tokens(window))}`;
}

function whoColumn(agent: RowAgent, rank: Rank, paint: Brush): string {
  const role = clean(agent.type);
  const name = clean(agent.name ?? '');
  if (name === '' || name === role) return paint('tokens', cut(role, rank.who));
  const named = cut(name, Math.ceil(rank.who / 2));
  return `${paint('tokens', named)}${paint('muted', '·')}${paint('label', cut(role, Math.max(1, rank.who - cells(named) - 1)))}`;
}

function modelColumn(agent: RowAgent, facts: DoorFacts | undefined, rank: Rank, paint: Brush): string {
  if (!rank.model) return '';
  const model = clean(agent.lastRequest?.model ?? agent.model ?? '');
  const glyph = facts?.text.glyph ?? '';
  const family = facts?.text.family || model;
  let out = glyph !== '' && (!rank.family || family === '') ? paint('model', glyph) : paint('model', [glyph, family].filter((part) => part !== '').join(' '));
  const level = facts && facts.effort !== '' ? facts.effort : clean(String(agent.lastRequest?.effort ?? agent.effort ?? ''));
  const bar = facts && facts.effort !== '' ? facts.text.effortBar : '';
  const effort = [paint.effort(level, bar), rank.effortWord || bar === '' ? paint('effort', level) : ''].filter((part) => part !== '').join(' ');
  // The main status line's joint: `◆ opus·▰▰ medium`.
  if (effort !== '') out += `${out === '' ? '' : paint('muted', '·')}${effort}`;
  return out;
}

function stateColumn(agent: RowAgent, facts: DoorFacts | undefined, now: number, paint: Brush): string {
  const since = paint('elapsed', duration(now - agent.startTime));
  if (agent.taskStatus === 'paused') return `${paint('warn', 'paused')} ${since}`;
  switch (agent.status) {
    case 'running': return `${paint('good', 'running')} ${since}`;
    case 'waiting': return `${paint('warn', 'waiting')} ${since}`;
    case 'idle': return `${paint('muted', 'idle')} ${since}`;
    case 'pending': return paint('muted', 'pending');
    default: {
      // An ended agent's time is how long it ran, known only from its transcript's last entry.
      const ran = facts && !facts.error && facts.lastActivity > agent.startTime ? ` ${paint('muted', duration(facts.lastActivity - agent.startTime))}` : '';
      return `${agent.status === 'completed' ? paint('muted', 'done') : paint('bad', agent.status)}${ran}`;
    }
  }
}

function spendColumn(agent: RowAgent, state: Known, rank: Rank, paint: Brush): string {
  if (rank.spend === 'none') return '';
  if (!state.facts) {
    if (state.why === 'waiting') return paint('muted', 'Σ …');
    // Without pfm's sum the engine's own figure stands in, named for what it is: the agent's own requests only.
    if (!agent.cost) return paint('muted', '$? own');
    return `${paint(paint.usdTone(agent.cost.usd), `$${agent.cost.usd.toFixed(2)}`)}${agent.cost.hasUnknownPrice ? paint('warn', '?') : ''} ${paint('muted', 'own')}`;
  }
  const { spend, text } = state.facts;
  const sum = paint('muted', 'Σ');
  if (spend === null) return `${sum} ${paint('warn', '$?')}`;
  // `+?` on the dollars: the sum is a lower bound.
  let out = `${sum} ${spend.usdKnown && text.usd !== '' ? paint(paint.usdTone(spend.usd), text.usd) : paint('warn', '$?')}${spend.floor ? paint('warn', '+?') : ''}`;
  if (rank.spend === 'words') {
    const dot = ` ${paint('muted', '·')} `;
    out += `${dot}${paint('tokens', text.in)} ${paint('muted', 'in')}${dot}${paint('tokens', text.out)} ${paint('muted', 'out')}${dot}${paint('tools', String(spend.tools))} ${paint('muted', spend.tools === 1 ? 'tool' : 'tools')}`;
  } else if (rank.spend === 'slash') {
    const slash = paint('muted', '/');
    out += `${slash}${paint('tokens', text.in)}${slash}${paint('tokens', text.out)}${slash}${paint('tools', String(spend.tools))}`;
  }
  return out;
}

function cacheColumn(agent: RowAgent, state: Known, rank: Rank, now: number, paint: Brush): string {
  if (rank.cache === 'none') return '';
  const last = agent.lastRequest;
  const facts = state.facts;
  const hitKnown = facts !== undefined && !facts.error;
  const hit = (lapsed: boolean): string => {
    if (rank.cache !== 'full') return '';
    if (!hitKnown) return ` ${paint('muted', state.facts === undefined && state.why === 'waiting' ? '…' : '?%')}`;
    if (facts.cacheHit < 0) return '';
    if (lapsed) return ` ${paint('muted', `was ${facts.cacheHit}%`)}`;
    return ` ${paint(paint.hitTone(facts.cacheHit), `${facts.cacheHit}%`)}`;
  };
  if (!last) return paint('muted', '💾–');
  if (facts && !facts.cacheLifetime) return `${paint('muted', '💾∅')}${hit(false)}`;
  if (!last.cacheTtl) return `${paint('muted', '💾–')}${hit(false)}`;
  const left = last.at + CACHE_MS[last.cacheTtl] - now;
  const life = rank.cache === 'full' ? last.cacheTtl : '';
  return left > 0 ? `${paint('good', `💾${life}✓${duration(left)}`)}${hit(false)}` : `${paint('bad', `💾${life}✗${duration(-left)}`)}${hit(true)}`;
}

type Tail = {
  flags: string[];
  /** Flags a row drops, last first, before the whole list gives up a column for them. */
  notes: string[];
  /** Why pfm's facts are missing or old, when they are; `''` is the mark alone. */
  cause: string | undefined;
};

function tail(agent: RowAgent, state: Known, scene: Scene, rank: Rank, paint: Brush): Tail {
  const out: string[] = [];
  const short = rank.flags === 'short';
  const facts = state.facts;
  // Facts pfm's last answer left out (it found no transcript of the agent, or an answer before named it) are as old
  // as the answer that gave them, and the row says how old.
  const read = facts && scene.pfm.state.state === 'ok' && !scene.pfm.named.has(agent.id) ? scene.pfm.read(agent.id) : undefined;
  if (read !== undefined) out.push(paint('muted', `pfm ${duration(scene.now - read)} ago`));
  if (facts && !facts.error) {
    const quiet = scene.now - facts.lastActivity;
    // A quiet read from old facts would only count the facts' age.
    if (read === undefined && agent.status === 'running' && facts.lastActivity > 0 && quiet >= IDLE_FROM_MS) out.push(paint(quiet >= IDLE_LONG_MS ? 'bad' : 'warn', `idle ${duration(quiet)}`));
    if (facts.toolErrors > 0) out.push(paint('bad', `${facts.toolErrors} ${short ? 'err' : facts.toolErrors === 1 ? 'error' : 'errors'}`));
    if (facts.compactions > 0) out.push(paint('window', `⟲${facts.compactions}`));
  }
  if (facts) {
    const nested = facts.nested;
    // Short, the arrow alone says the agents are below.
    const gap = short ? '' : ' ';
    if (nested.error !== undefined) out.push(paint('warn', short ? '↳?/?' : '↳ ?/? below'));
    else if (nested.total > 0) {
      const working = `${paint(nested.working > 0 ? 'good' : 'muted', String(nested.working))}${paint('muted', '/')}${paint('label', String(nested.total))}`;
      out.push(`${paint('muted', '↳')}${gap}${working}${short ? '' : ` ${paint('muted', 'below')}`}${nested.unknown > 0 ? paint('warn', `${gap}+${nested.unknown}?`) : ''}`);
    }
  }
  const notes: string[] = [];
  if (scene.sessionCwd !== undefined && agent.cwd !== '' && agent.cwd !== scene.sessionCwd) {
    notes.push(paint('tools', `@${cut(clean(agent.cwd.replace(/\/+$/, '').split('/').pop() ?? ''), 24)}`));
  }
  // The sum's rare notes ride here, so they widen no column: the advisor's part of it, and a part priced at the gateway's API-equivalent rate.
  const spend = facts?.spend;
  if (spend) {
    if (spend.advisorUsd > 0) notes.push(paint('muted', `adv $${spend.advisorUsd.toFixed(2)}`));
    else if (spend.advisorUnpriced > 0) notes.push(paint('muted', 'adv n/a'));
    if (spend.apiEquivalent) notes.push(paint('muted', 'API-eq'));
  }
  // A failed door says why once, on the list's first row; every other row carries the mark alone.
  const cause = scene.pfm.state.state === 'failed' ? (scene.agents[0]?.id === agent.id ? scene.pfm.state.cause : '') : facts?.error;
  return { flags: out, notes, cause };
}

/** A task's words in `width` cells: what it is, then what it is doing now. */
function task(agent: RowAgent, width: number, paint: Brush): string {
  const what = clean(agent.description);
  const doing = clean(agent.label);
  if (what === '' || doing === what || doing === '') return paint('label', cut(what || doing, width));
  if (cells(what) + 3 + cells(doing) <= width) return `${paint('label', what)} ${paint('muted', `› ${doing}`)}`;
  const head = Math.min(cells(what), Math.max(12, Math.floor(width / 2)));
  if (width - head - 3 < 6) return paint('label', cut(what, width));
  return `${paint('label', cut(what, head))} ${paint('muted', `› ${cut(doing, width - head - 3)}`)}`;
}

/** How many agents with a row stand above `agent`: the engine's connector takes two cells of its row for each. */
function depth(agent: RowAgent, byId: ReadonlyMap<string, RowAgent>): number {
  let levels = 0;
  for (let up = agent.parentId === undefined ? undefined : byId.get(agent.parentId); up !== undefined && levels < 16; up = up.parentId === undefined ? undefined : byId.get(up.parentId)) levels += 1;
  return levels;
}

/** `words`: the cells of the task's words written whole, 0 with none. */
type Drawn = { columns: string[]; tail: Tail; indent: number; words: number };

/** The cells a row keeps for its cause, bar and mark included: the mark alone, or with the cause's first CAUSE_MIN cells. */
function causeLeast(cause: string | undefined): number {
  if (cause === undefined) return 0;
  return 3 + MARK.length + (cause === '' ? 0 : 1 + Math.min(CAUSE_MIN, cells(cause)));
}

function draw(agent: RowAgent, scene: Scene, rank: Rank, paint: Brush, depths: ReadonlyMap<string, number>): Drawn {
  const state = known(agent, scene.pfm);
  const indent = 2 * (depths.get(agent.id) ?? 0);
  const what = clean(agent.description);
  const doing = clean(agent.label);
  return {
    columns: [
      pulseColumn(agent, state.facts, rank, 2 * Math.max(0, ...depths.values()) - indent, paint), whoColumn(agent, rank, paint), modelColumn(agent, state.facts, rank, paint),
      stateColumn(agent, state.facts, scene.now, paint), spendColumn(agent, state, rank, paint), cacheColumn(agent, state, rank, scene.now, paint),
    ],
    tail: tail(agent, state, scene, rank, paint),
    indent,
    words: what === '' || doing === '' || doing === what ? cells(what || doing) : cells(what) + 3 + cells(doing),
  };
}

function depths(agents: readonly RowAgent[]): Map<string, number> {
  const byId = new Map(agents.map((agent) => [agent.id, agent]));
  return new Map(agents.map((agent) => [agent.id, depth(agent, byId)]));
}

/** The parts every row of the list keeps, and each column's width in cells (a nested row's indent inside the first). */
export type Layout = { rank: number; widths: number[] };

/**
 * Chooses, for the whole list, the fullest rank at which every row fits `scene.columns` with its flags, the least of
 * a cause and its task's words up to the rank's `task` cells, and that rank's column widths.
 */
export function layout(scene: Scene): Layout {
  const paint = brush(scene.pfm.look ?? BARE);
  const deep = depths(scene.agents);
  let chosen: Layout = { rank: 0, widths: [] };
  for (let rank = 0; rank < RANKS.length; rank += 1) {
    const rows = scene.agents.map((agent) => draw(agent, scene, RANKS[rank]!, paint, deep));
    const widths = [0, 1, 2, 3, 4, 5].map((column) => Math.max(0, ...rows.map((row) => (row.columns[column] === '' ? 0 : cells(row.columns[column]!) + (column === 0 ? row.indent : 0)))));
    chosen = { rank, widths };
    if (scene.columns === undefined) break;
    const block = widths.reduce((sum, width) => sum + (width > 0 ? width + 3 : 0), 0) - 3;
    const need = (row: Drawn) => block + row.tail.flags.reduce((sum, flag) => sum + 3 + cells(flag), 0)
      + causeLeast(row.tail.cause) + (row.words > 0 && RANKS[rank]!.task > 0 ? 3 + Math.min(row.words, RANKS[rank]!.task) : 0);
    if (Math.max(0, ...rows.map(need)) <= scene.columns) break;
  }
  return chosen;
}

/** The body of agent `id`'s row, never wider than the row: its columns padded to the list's widths, its flags, then the cause and its task in what is left. */
export function drawRow(scene: Scene, id: string, chosen: Layout = layout(scene)): string {
  const agent = scene.agents.find((each) => each.id === id);
  if (!agent) return '';
  const paint = brush(scene.pfm.look ?? BARE);
  const row = draw(agent, scene, RANKS[chosen.rank]!, paint, depths(scene.agents));
  const bar = ` ${paint('dim', '│')} `;
  const parts: string[] = [];
  chosen.widths.forEach((width, column) => { if (width > 0) parts.push(pad(row.columns[column] ?? '', width - (column === 0 ? row.indent : 0))); });
  parts.push(...row.tail.flags);
  let body = parts.join(bar);
  const left = () => (scene.columns === undefined ? Number.MAX_SAFE_INTEGER : scene.columns - row.indent - cells(body) - 3);
  const rest = causeLeast(row.tail.cause) + (row.words > 0 ? 3 + Math.min(row.words, Math.max(TASK_MIN, RANKS[chosen.rank]!.task)) : 0);
  for (const note of row.tail.notes) {
    if (left() - cells(note) < rest) break;
    body += bar + note;
  }
  if (row.tail.cause !== undefined) {
    // The cause and the task share what is left: the task keeps its words up to half of it, the cause takes the rest;
    // where both cannot have their least, the cause takes it all and the task is left out.
    const alone = left() - MARK.length - 1;
    const shared = alone - (row.words > 0 ? 3 + Math.min(row.words, Math.max(TASK_MIN, Math.floor(left() / 2))) : 0);
    const room = shared >= CAUSE_MIN ? shared : alone - (row.words > 0 ? 3 + TASK_MIN : 0) >= CAUSE_MIN ? CAUSE_MIN : alone;
    const why = row.tail.cause === '' ? '' : cut(row.tail.cause, Math.min(CAUSE_MAX, room));
    body += bar + paint('bad', why === '' ? MARK : `${MARK} ${why}`);
  }
  if (row.words > 0 && left() >= Math.min(row.words, TASK_MIN)) body += bar + task(agent, left(), paint);
  // A list too narrow for even its last rank is cut here, marked, before the engine cuts it unmarked at the row's edge.
  return scene.columns === undefined ? body.trimEnd() : fit(body.trimEnd(), scene.columns - row.indent);
}

/** The row when drawing it threw: the cause, then the engine's own words, so a failure never draws an empty row. */
export function failureRow(cause: string, label: string): string {
  const words = clean(label);
  return `${BARE.palette.bad}professor ✗ ${cut(clean(cause), CAUSE_MAX)}${RESET}${words === '' ? '' : ` ${BARE.palette.dim}│${RESET} ${words}`}`;
}
