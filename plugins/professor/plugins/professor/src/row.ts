// The body of one sub-agent's row in Claude Code's agent list. Every row of the
// list is drawn as one instrument panel: the same columns at the same cells in
// every row, so a glance down a column compares the agents.
//
//   agents below + gauge + pulse + last growth + share + tokens/window │ who │
//   model·effort │ state + time │ Σ spend │ cache │ flags │ @directory │ task › progress
//
// The engine's props give the live half (status, tokens, readings, last
// request); pfm's door gives the half only transcripts hold (spend with every
// agent below, tool errors, compactions, cache hit, last activity, the agents
// below) and the colours. A fact pfm has not answered yet draws as `…`, one it
// could not give as `?` beside the cause: a missing fact never draws as zero.
// The context block (gauge, pulse, growth, share, tokens/window) takes its form
// from the terminal's width alone, by pfm's table (look.context), so it stands on
// the main status line's cells; when the row is still too narrow every row of the
// list gives up the same parts, in GIVE's order, so the columns stay aligned.
// The count of the agents below opens the row, in a column as wide as the
// list's widest count, none where no row has one: a row never gives it up.

import type { DoorContextForm, DoorFacts, DoorGauge, DoorLook, DoorPulse, DoorState } from './door';
import { litCells, pulse, type PulseCell } from './pulse';
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
  who: number; family: boolean; effortWord: boolean; model: boolean;
  spend: 'words' | 'slash' | 'usd' | 'none'; cache: 'full' | 'left' | 'none';
  /** `'short'`: the flags without their words, `2 err`. */
  flags: 'words' | 'short';
  /** The cells every row keeps for its task's words after its columns and flags. */
  task: number;
  /** `false`: each column at its own width, not padded to the list's widest. */
  pad: boolean;
  /** `true`: the bar between the parts without the blanks around it. */
  tight: boolean;
};
const FULL: Rank = { who: 36, family: true, effortWord: true, model: true, spend: 'words', cache: 'full', flags: 'words', task: 40, pad: true, tight: false };
/**
 * What a list too wide for its rows gives up, one step at a time, in the order every line pfm draws shrinks in: first
 * a shorter spelling of the same facts (slashes for words, a name cut with `…`, the flags' and the effort's words, the
 * family's name beside its glyph), then the padding (the blanks around each bar, then the alignment pad), only then a fact,
 * least missed first. The gauge, who and the state are never given up.
 */
const GIVE: ReadonlyArray<Partial<Rank>> = [
  { spend: 'slash' }, { who: 24 }, { flags: 'short' }, { effortWord: false }, { family: false }, { task: 20 }, { who: 12 },
  { tight: true }, { pad: false },
  { spend: 'usd' }, { cache: 'left' }, { spend: 'none' }, { cache: 'none' }, { model: false }, { task: 0 },
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
/** The growth figure's cells and the share's, each right-aligned in them, as pfm's main line draws them. */
const GROWTH_CELLS = 7;
const SHARE_CELLS = 4;
/**
 * The blank before the gauge. The engine's lead before a row takes the two cells the main status line's urgency emoji
 * takes, and pfm puts one blank between that emoji and its gauge: with this one the gauges stand on the same cells.
 */
const GAUGE_LEAD = ' ';
/**
 * The cells a terminal holds beyond a top-level row's `columns`: Claude Code's footer padding, two cells each side,
 * and the engine's two-cell lead before the row. pfm's width table counts the terminal's cells (the hook's COLUMNS).
 */
const ROW_INSET = 6;
/**
 * The look before pfm has given one, or without pfm: weight only, red for a failure. With no scale of pfm's the strip
 * is flat: the gauge without its fill (brush's `fill`) and the pulse quiet (one glyph, no step reaching a level, no
 * drop), and with no thresholds no amount is toned: the mod keeps no copy of the scale's steps or the tones. The gauge's
 * and the pulse's cells here only hold a row's place until pfm's answer brings its own.
 */
const NEVER = Number.POSITIVE_INFINITY;
const BARE: DoorLook = {
  palette: { tokens: '\x1b[1m', muted: '\x1b[2m', dim: '\x1b[2m', bad: '\x1b[31m' }, effortColors: {},
  gauge: { on: '▱', off: '▱', cells: 8 },
  pulse: { glyphs: ['·'], drop: '·', steps: [], dropFrom: NEVER, stepMs: 0, cells: 6 },
  context: [{ from: 0, pulse: true, growth: true, window: true }],
  tones: { usd: { warn: NEVER, bad: NEVER }, share: { warn: NEVER, bad: NEVER }, cacheHit: { warn: -NEVER, good: NEVER } },
};

type Brush = {
  (role: string, text: string): string;
  effort: (level: string, text: string) => string;
  /** The look's context gauge and pulse scale. */
  gauge: DoorGauge;
  pulse: DoorPulse;
  /** `false` in the bare look: the gauge draws no fill, as pfm has not said where the share's tones turn. */
  fill: boolean;
  /** The role of a share of the context window, of a dollar amount and of a cache-hit share, by the look's tones. */
  shareTone: (percent: number) => string;
  usdTone: (usd: number) => string;
  hitTone: (percent: number) => string;
};

function brush(look: DoorLook): Brush {
  const wrap = (sgr: string | undefined, text: string) => (text === '' || !sgr ? text : `${sgr}${text}${RESET}`);
  const paint = ((role: string, text: string) => wrap(look.palette[role], text)) as Brush;
  paint.effort = (level, text) => wrap(look.effortColors[level] ?? look.palette.effort, text);
  paint.gauge = look.gauge;
  paint.pulse = look.pulse;
  paint.fill = look !== BARE;
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

/** The gauge: its first `on` cells, the window's used part, in the share's tone, the rest dimmed, as pfm draws its quota bars. */
function gaugeText(on: number, tone: string, paint: Brush): string {
  const { on: used, off, cells: width } = paint.gauge;
  return paint(tone, used.repeat(on)) + paint('dim', off.repeat(width - on));
}

/** The pulse: a step that grew in the growth figure's colour, a drop in the window's, a quiet or unborn step muted. */
function pulseText(trace: readonly PulseCell[], paint: Brush): string {
  const { glyphs, drop } = paint.pulse;
  let out = '';
  let run = '';
  let style = '';
  const flush = () => { out += paint(style, run); run = ''; };
  for (const cell of trace) {
    const next = cell === 'drop' || (typeof cell === 'number' && cell > 0) ? 'window' : 'muted';
    if (next !== style) { flush(); style = next; }
    run += cell === 'drop' ? drop : cell === 'unborn' ? glyphs[0]! : glyphs[Math.min(cell, glyphs.length - 1)]!;
  }
  flush();
  return out;
}

/** The context block's form at the scene's width: pfm's first form that many terminal cells hold, the fullest where the width is unknown. */
function contextForm(scene: Scene, look: DoorLook): DoorContextForm {
  const forms = look.context;
  if (scene.columns === undefined) return forms[0]!;
  return forms.find((form) => scene.columns! + ROW_INSET >= form.from) ?? forms[forms.length - 1]!;
}

/**
 * The tokens the gauge counts: Claude Code's tokenCount when it is above zero and registers against the window (without a
 * window any count above zero stands), since a missing, zero or too-small count often trails the agent's real context;
 * else pfm's prompt size of the agent's newest completed call, read from its transcript; else the count as stated.
 */
function contextUsed(agent: RowAgent, facts: DoorFacts | undefined, window: number | undefined): number {
  const stated = agent.tokenCount;
  if (stated > 0 && (share(stated, window) ?? 1) > 0) return stated;
  return facts && facts.context > 0 ? facts.context : stated;
}

/**
 * `lead`: the cells after the strip that put a row less nested than the list's deepest back on the same cells. Without
 * a growth figure the strip widens over the figure's blank cells, the lead and the share's pad, to one blank before the
 * share, so the share stays on its cells and no run of blanks sits between them; a figure drawn takes its cells back.
 */
function pulseColumn(agent: RowAgent, facts: DoorFacts | undefined, form: DoorContextForm, lead: number, paint: Brush): string {
  const window = facts && facts.contextWindow > 0 ? facts.contextWindow : agent.contextWindowSize;
  const used = contextUsed(agent, facts, window);
  const full = share(used, window);
  const tone = full === undefined ? 'muted' : paint.shareTone(full);
  const samples = agent.tokenSamples;
  const grown = samples.length < 2 ? 0 : samples[samples.length - 1]! - samples[samples.length - 2]!;
  const figure = form.growth && grown !== 0;
  const widen = form.pulse && !figure;
  const part = full === undefined ? '?%' : `${full}%`;
  let out = GAUGE_LEAD + gaugeText(paint.fill ? litCells(used, window, paint.gauge.cells) : 0, tone, paint);
  if (form.pulse) {
    const strip = paint.pulse.cells + lead + (widen ? (form.growth ? 1 + GROWTH_CELLS : 0) + Math.max(0, SHARE_CELLS - cells(part)) : 0);
    out += ` ${pulseText(pulse(samples, strip, paint.pulse), paint)}`;
  } else out += ' '.repeat(lead);
  if (form.growth && !widen) {
    // The newest cell's reading in figures, beside it: it names what the trace counts. A step that neither grew nor
    // shrank draws no figure, so a context at rest reads as no figure, never as a size of 0.
    out += ` ${grown === 0 ? ' '.repeat(GROWTH_CELLS) : paint('window', (grown > 0 ? `+${tokens(grown)}` : `−${tokens(-grown)}`).padStart(GROWTH_CELLS))}`;
  }
  out += ` ${paint(tone, widen ? part : part.padStart(SHARE_CELLS))}`;
  if (!form.window) return out;
  return `${out} ${paint('tokens', tokens(used).padStart(6))}${paint('muted', '/')}${paint('window', window === undefined ? '?' : tokens(window))}`;
}

/** The agents below `facts`' agent, working of all, as the row's first part; `''` with none, or no facts. */
function nestedText(facts: DoorFacts | undefined, paint: Brush): string {
  const nested = facts?.nested;
  if (nested === undefined) return '';
  if (nested.error !== undefined) return paint('warn', '?/?');
  if (nested.total === 0) return '';
  const working = `${paint(nested.working > 0 ? 'good' : 'muted', String(nested.working))}${paint('muted', '/')}${paint('label', String(nested.total))}`;
  return `${working}${nested.unknown > 0 ? paint('warn', `+${nested.unknown}?`) : ''}`;
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
function causeLeast(cause: string | undefined, bar: number): number {
  if (cause === undefined) return 0;
  return bar + MARK.length + (cause === '' ? 0 : 1 + Math.min(CAUSE_MIN, cells(cause)));
}

/** The list's rows as each draws: the depth of each, and the cells of the widest count of the agents below. */
type Tree = { depths: ReadonlyMap<string, number>; below: number };

function draw(agent: RowAgent, scene: Scene, rank: Rank, form: DoorContextForm, paint: Brush, tree: Tree): Drawn {
  const state = known(agent, scene.pfm);
  const { depths } = tree;
  const indent = 2 * (depths.get(agent.id) ?? 0);
  const what = clean(agent.description);
  const doing = clean(agent.label);
  return {
    columns: [
      pad(nestedText(state.facts, paint), tree.below) + pulseColumn(agent, state.facts, form, 2 * Math.max(0, ...depths.values()) - indent, paint), whoColumn(agent, rank, paint), modelColumn(agent, state.facts, rank, paint),
      stateColumn(agent, state.facts, scene.now, paint), spendColumn(agent, state, rank, paint), cacheColumn(agent, state, rank, scene.now, paint),
    ],
    tail: tail(agent, state, scene, rank, paint),
    indent,
    words: what === '' || doing === '' || doing === what ? cells(what || doing) : cells(what) + 3 + cells(doing),
  };
}

function tree(scene: Scene, paint: Brush): Tree {
  const byId = new Map(scene.agents.map((agent) => [agent.id, agent]));
  const below = Math.max(0, ...scene.agents.map((agent) => cells(nestedText(known(agent, scene.pfm).facts, paint))));
  return { depths: new Map(scene.agents.map((agent) => [agent.id, depth(agent, byId)])), below };
}

/** The parts every row of the list keeps, and each column's width in cells (a nested row's indent inside the first). */
export type Layout = { rank: number; widths: number[] };

/** The cells of the bar between two parts at a rank. */
const barCells = (rank: Rank): number => (rank.tight ? 1 : 3);

/**
 * Chooses, for the whole list, the fullest rank at which every row fits `scene.columns` with its flags, the least of
 * a cause and its task's words up to the rank's `task` cells, and that rank's column widths.
 */
export function layout(scene: Scene): Layout {
  const look = scene.pfm.look ?? BARE;
  const paint = brush(look);
  const form = contextForm(scene, look);
  const deep = tree(scene, paint);
  let chosen: Layout = { rank: 0, widths: [] };
  for (let rank = 0; rank < RANKS.length; rank += 1) {
    const at = RANKS[rank]!;
    const bar = barCells(at);
    const rows = scene.agents.map((agent) => draw(agent, scene, at, form, paint, deep));
    const own = (row: Drawn, column: number) => (row.columns[column] === '' ? 0 : cells(row.columns[column]!) + (column === 0 ? row.indent : 0));
    const widths = [0, 1, 2, 3, 4, 5].map((column) => Math.max(0, ...rows.map((row) => own(row, column))));
    chosen = { rank, widths };
    if (scene.columns === undefined) break;
    const block = (row: Drawn) => (at.pad ? widths : widths.map((_, column) => own(row, column))).reduce((sum, width) => sum + (width > 0 ? width + bar : 0), 0) - bar;
    const need = (row: Drawn) => block(row) + row.tail.flags.reduce((sum, flag) => sum + bar + cells(flag), 0)
      + causeLeast(row.tail.cause, bar) + (row.words > 0 && at.task > 0 ? bar + Math.min(row.words, at.task) : 0);
    if (Math.max(0, ...rows.map(need)) <= scene.columns) break;
  }
  return chosen;
}

/** The body of agent `id`'s row, never wider than the row: its columns padded to the list's widths, its flags, then the cause and its task in what is left. */
export function drawRow(scene: Scene, id: string, chosen: Layout = layout(scene)): string {
  const agent = scene.agents.find((each) => each.id === id);
  if (!agent) return '';
  const look = scene.pfm.look ?? BARE;
  const paint = brush(look);
  const rank = RANKS[chosen.rank]!;
  const row = draw(agent, scene, rank, contextForm(scene, look), paint, tree(scene, paint));
  const bar = rank.tight ? paint('dim', '│') : ` ${paint('dim', '│')} `;
  const gap = barCells(rank);
  const parts: string[] = [];
  chosen.widths.forEach((width, column) => {
    const text = row.columns[column] ?? '';
    if (rank.pad ? width > 0 : text !== '') parts.push(rank.pad ? pad(text, width - (column === 0 ? row.indent : 0)) : text);
  });
  parts.push(...row.tail.flags);
  let body = parts.join(bar);
  const left = () => (scene.columns === undefined ? Number.MAX_SAFE_INTEGER : scene.columns - row.indent - cells(body) - gap);
  const rest = causeLeast(row.tail.cause, gap) + (row.words > 0 ? gap + Math.min(row.words, Math.max(TASK_MIN, rank.task)) : 0);
  for (const note of row.tail.notes) {
    if (left() - cells(note) < rest) break;
    body += bar + note;
  }
  if (row.tail.cause !== undefined) {
    // The cause and the task share what is left: the task keeps its words up to half of it, the cause takes the rest;
    // where both cannot have their least, the cause takes it all and the task is left out.
    const alone = left() - MARK.length - 1;
    const shared = alone - (row.words > 0 ? gap + Math.min(row.words, Math.max(TASK_MIN, Math.floor(left() / 2))) : 0);
    const room = shared >= CAUSE_MIN ? shared : alone - (row.words > 0 ? gap + TASK_MIN : 0) >= CAUSE_MIN ? CAUSE_MIN : alone;
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
