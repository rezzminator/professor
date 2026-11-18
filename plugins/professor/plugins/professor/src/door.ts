// pfm's door for the agent rows: the one command that answers, for every row
// of the list at once, the facts the engine's AgentRow props do not carry, and
// the look the status line draws them in: its colours, the pulse strip's scale
// and where an amount changes colour. This file
// builds the command, refuses an answer of another shape by the field that is
// wrong, and holds what was last learnt between calls.

import { clean } from './text';

/** The fewest milliseconds between two calls: one per refresh for all rows, never one per row. */
export const DOOR_EVERY_MS = 4_000;
/** How long one call may take before it counts as failed. */
export const DOOR_TIMEOUT_MS = 8_000;

export type DoorSpend = {
  usd: number; usdKnown: boolean; in: number; out: number; tools: number;
  advisorUsd: number; advisorUnpriced: number; floor: boolean; ledger: boolean; apiEquivalent: boolean;
};
export type DoorNested = { total: number; working: number; unknown: number; error?: string };
export type DoorText = { glyph: string; family: string; effortBar: string; context: string; in: string; out: string; usd: string };
/**
 * One agent's facts as pfm read them; with `error`, its counts, cache and activity are unknown, never zero.
 * `absent`: pfm found no transcript of it on disk (it has written none yet, or none any more).
 */
export type DoorFacts = {
  id: string; model: string; effort: string;
  tools: number; toolErrors: number; compactions: number;
  cacheHit: number; cacheLifetime: boolean;
  context: number; contextWindow: number; lastActivity: number;
  nested: DoorNested; spend: DoorSpend | null; text: DoorText; error?: string; absent?: true;
};
/**
 * The pulse strip's scale: the cell per growth level, none to most; the cell of a step that shed `dropFrom` tokens
 * or more; the fewest tokens a step must add to reach each level above the first; a main-line cell's wall time.
 */
export type DoorPulse = { glyphs: readonly string[]; drop: string; steps: readonly number[]; dropFrom: number; stepMs: number };
/** Where an amount turns to the warning colour, and to the bad one, each inclusive. */
export type DoorTone = { warn: number; bad: number };
/** Where a share that is better high turns from bad to the warning colour, and to good, each inclusive. */
export type DoorHitTone = { warn: number; good: number };
/**
 * The status line's look: its SGR sequence per meaning and per effort level for its bar, the pulse strip's scale,
 * and the tones of a dollar amount, of a share of the context window and of a cache-hit share.
 */
export type DoorLook = {
  palette: Record<string, string>; effortColors: Record<string, string>; pulse: DoorPulse;
  tones: { usd: DoorTone; share: DoorTone; cacheHit: DoorHitTone };
};
export type DoorAnswer = { at: number; agents: DoorFacts[]; look: DoorLook };
export type DoorResult = { exitCode: number; stdout: string; stderr: string };

/** What opens pfm's own line naming why the command failed. */
const DOOR_CAUSE = 'pfm statusline --agents: ';

/** The command that asks pfm for the agents `ids` of the session whose transcript is `transcript`, running or not. */
export function doorArgv(transcript: string, ids: readonly string[]): string[] {
  return ['pfm', 'statusline', '--agents', transcript, ...ids];
}

type Json = Record<string, unknown>;
const SGR = /^(?:\x1b\[[0-9;]*m)*$/;

function object(value: unknown, at: string): Json {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) throw new Error(`${at} is not an object`);
  return value as Json;
}
function number(from: Json, key: string, at: string): number {
  const value = from[key];
  if (typeof value !== 'number' || !Number.isFinite(value)) throw new Error(`${at}.${key} is not a number`);
  return value;
}
function flag(from: Json, key: string, at: string): boolean {
  const value = from[key];
  if (typeof value !== 'boolean') throw new Error(`${at}.${key} is not true or false`);
  return value;
}
function text(from: Json, key: string, at: string): string {
  const value = from[key];
  if (typeof value !== 'string') throw new Error(`${at}.${key} is not text`);
  return clean(value);
}
function optionalText(from: Json, key: string, at: string): string | undefined {
  return from[key] === undefined ? undefined : text(from, key, at) || undefined;
}
function numbers(from: Json, key: string, at: string): number[] {
  const value = from[key];
  if (!Array.isArray(value)) throw new Error(`${at}.${key} is not a list`);
  return value.map((each: unknown, index) => {
    if (typeof each !== 'number' || !Number.isFinite(each)) throw new Error(`${at}.${key}[${index}] is not a number`);
    return each;
  });
}
function words(from: Json, key: string, at: string): string[] {
  const value = from[key];
  if (!Array.isArray(value) || value.length === 0) throw new Error(`${at}.${key} is not a list of words`);
  return value.map((each: unknown, index) => {
    if (typeof each !== 'string') throw new Error(`${at}.${key}[${index}] is not text`);
    return clean(each);
  });
}
function tone(value: unknown, at: string): DoorTone {
  const from = object(value, at);
  return { warn: number(from, 'warn', at), bad: number(from, 'bad', at) };
}
function hitTone(value: unknown, at: string): DoorHitTone {
  const from = object(value, at);
  return { warn: number(from, 'warn', at), good: number(from, 'good', at) };
}
function colours(value: unknown, at: string): Record<string, string> {
  const out: Record<string, string> = {};
  for (const [key, sgr] of Object.entries(object(value, at))) {
    if (typeof sgr !== 'string' || !SGR.test(sgr)) throw new Error(`${at}.${key} is not a colour sequence`);
    out[key] = sgr;
  }
  return out;
}

function facts(value: unknown, at: string): DoorFacts {
  const from = object(value, at);
  const id = text(from, 'id', at);
  if (id === '') throw new Error(`${at}.id is empty`);
  const nested = object(from.nested, `${at}.nested`);
  const words = object(from.text, `${at}.text`);
  let spend: DoorSpend | null = null;
  if (from.spend !== null) {
    const bill = object(from.spend, `${at}.spend`);
    const where = `${at}.spend`;
    spend = {
      usd: number(bill, 'usd', where), usdKnown: flag(bill, 'usdKnown', where), in: number(bill, 'in', where), out: number(bill, 'out', where),
      tools: number(bill, 'tools', where), advisorUsd: number(bill, 'advisorUsd', where), advisorUnpriced: number(bill, 'advisorUnpriced', where),
      floor: flag(bill, 'floor', where), ledger: flag(bill, 'ledger', where), apiEquivalent: flag(bill, 'apiEquivalent', where),
    };
  }
  const nestedError = optionalText(nested, 'error', `${at}.nested`);
  const error = optionalText(from, 'error', at);
  const absent = from.absent !== undefined && flag(from, 'absent', at);
  return {
    id, model: text(from, 'model', at), effort: text(from, 'effort', at),
    tools: number(from, 'tools', at), toolErrors: number(from, 'toolErrors', at), compactions: number(from, 'compactions', at),
    cacheHit: number(from, 'cacheHit', at), cacheLifetime: flag(from, 'cacheLifetime', at),
    context: number(from, 'context', at), contextWindow: number(from, 'contextWindow', at), lastActivity: number(from, 'lastActivity', at),
    nested: {
      total: number(nested, 'total', `${at}.nested`), working: number(nested, 'working', `${at}.nested`), unknown: number(nested, 'unknown', `${at}.nested`),
      ...(nestedError === undefined ? {} : { error: nestedError }),
    },
    spend,
    text: {
      glyph: text(words, 'glyph', `${at}.text`), family: text(words, 'family', `${at}.text`), effortBar: text(words, 'effortBar', `${at}.text`),
      context: text(words, 'context', `${at}.text`), in: text(words, 'in', `${at}.text`), out: text(words, 'out', `${at}.text`), usd: text(words, 'usd', `${at}.text`),
    },
    ...(error === undefined ? {} : { error }),
    ...(absent ? { absent: true as const } : {}),
  };
}

/**
 * The answer of one finished door command. Throws the cause, in words a row can show, when the command failed
 * (pfm's own stderr line) or answered a shape this file does not know (the field that is wrong).
 */
export function readDoorAnswer(result: DoorResult): DoorAnswer {
  if (result.exitCode !== 0) {
    // pfm's own cause line, else its first word: a warning printed before the failure is not its cause.
    const lines = result.stderr.split('\n').map(clean).filter((line) => line !== '');
    const said = lines.find((line) => line.startsWith(DOOR_CAUSE)) ?? lines[0] ?? '';
    throw new Error(said === '' ? `pfm exited ${result.exitCode} without a word` : said.replace(DOOR_CAUSE, ''));
  }
  let parsed: unknown;
  try { parsed = JSON.parse(result.stdout); } catch { throw new Error(result.stdout.trim() === '' ? 'pfm answered nothing' : 'pfm answered no JSON'); }
  const answer = object(parsed, 'answer');
  if (!Array.isArray(answer.agents)) throw new Error('answer.agents is not a list');
  const look = object(answer.look, 'answer.look');
  const scale = object(look.pulse, 'answer.look.pulse');
  const tones = object(look.tones, 'answer.look.tones');
  const at = 'answer.look.pulse';
  return {
    at: number(answer, 'at', 'answer'),
    agents: answer.agents.map((agent, index) => facts(agent, `answer.agents[${index}]`)),
    look: {
      palette: colours(look.palette, 'answer.look.palette'), effortColors: colours(look.effortColors, 'answer.look.effortColors'),
      pulse: { glyphs: words(scale, 'glyphs', at), drop: text(scale, 'drop', at), steps: numbers(scale, 'steps', at), dropFrom: number(scale, 'dropFrom', at), stepMs: number(scale, 'stepMs', at) },
      tones: {
        usd: tone(tones.usd, 'answer.look.tones.usd'), share: tone(tones.share, 'answer.look.tones.share'),
        cacheHit: hitTone(tones.cacheHit, 'answer.look.tones.cacheHit'),
      },
    },
  };
}

/** `waiting`: no call has finished yet; `failed`: the last call did, by `cause`. */
export type DoorState = { state: 'waiting' } | { state: 'ok' } | { state: 'failed'; cause: string };

/**
 * What the door last said. An agent keeps the facts of the last answer that named it: pfm answers every agent asked
 * for, but a failed call or an agent it found no transcript of (`absent`) leaves the facts before standing, or none.
 * The look, once learnt, outlives a failed call.
 */
export class Door {
  state: DoorState = { state: 'waiting' };
  look: DoorLook | undefined;
  /** Agents the last good answer gave facts of. */
  named = new Set<string>();
  private readonly known = new Map<string, DoorFacts>();
  /** When pfm read each agent's facts: the time of the answer that gave them. */
  private readonly readAt = new Map<string, number>();
  private asked: number | undefined;
  private flying = false;

  /** Whether a call is due at `now`: none in flight, and DOOR_EVERY_MS since the last began. Marks it begun. */
  begin(now: number): boolean {
    if (this.flying || (this.asked !== undefined && now - this.asked < DOOR_EVERY_MS)) return false;
    this.flying = true;
    this.asked = now;
    return true;
  }
  land(answer: DoorAnswer): void {
    this.flying = false;
    this.state = { state: 'ok' };
    this.look = answer.look;
    const read = answer.agents.filter((agent) => !agent.absent);
    this.named = new Set(read.map((agent) => agent.id));
    for (const agent of read) { this.known.set(agent.id, agent); this.readAt.set(agent.id, answer.at); }
  }
  fail(cause: string): void {
    this.flying = false;
    this.state = { state: 'failed', cause };
  }
  /** The call in flight ended with no answer and no failure: the next `begin` asks again at once. */
  drop(): void {
    this.flying = false;
    this.asked = undefined;
  }
  facts(id: string): DoorFacts | undefined { return this.known.get(id); }
  /** When pfm read `id`'s facts, in milliseconds since the epoch. */
  read(id: string): number | undefined { return this.readAt.get(id); }
  /** Forgets every agent that has no row any more. */
  keep(ids: ReadonlySet<string>): void {
    for (const id of this.known.keys()) { if (!ids.has(id)) { this.known.delete(id); this.readAt.delete(id); } }
  }
}
