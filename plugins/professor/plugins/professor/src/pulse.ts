// The pulse: how fast an agent's context grew in each engine reading (about
// five seconds), one cell a reading, newest on the right, so the trace moves
// one cell a reading. Beside it the row draws the context gauge, how full the
// window is, filled by litCells. The scale and the gauge are pfm's (the door's
// look.pulse and look.gauge), one for every row and the main status line, so
// two traces compare by eye; this file keeps no copy of them.

import type { DoorPulse } from './door';

/** A pulse cell: its level, 0 up to the scale's steps; `'drop'` where the context shrank by its dropFrom or more; `'unborn'` before the agent's first reading. */
export type PulseCell = number | 'drop' | 'unborn';

/** The level of one reading's growth in tokens on `scale`. */
export function pulseLevel(grown: number, scale: DoorPulse): number | 'drop' {
  if (grown <= -scale.dropFrom) return 'drop';
  let level = 0;
  for (const step of scale.steps) { if (grown >= step) level += 1; }
  return level;
}

/** How many of a gauge's `cells` are on: one at least once anything is used, all only when the window is full. */
export function litCells(used: number, window: number | undefined, cells: number): number {
  if (window === undefined || !(window > 0) || !(used > 0)) return 0;
  if (used >= window) return cells;
  return Math.min(cells - 1, Math.max(1, Math.round((used / window) * cells)));
}

/**
 * The pulse for an agent's readings (`samples`, oldest first), `cells` wide: the newest `cells` steps between
 * readings, right-aligned, the cells before the agent's first reading `'unborn'`.
 */
export function pulse(samples: readonly number[], cells: number, scale: DoorPulse): PulseCell[] {
  const steps: Array<number | 'drop'> = [];
  for (let at = Math.max(1, samples.length - cells); at < samples.length; at += 1) steps.push(pulseLevel(samples[at]! - samples[at - 1]!, scale));
  const unborn = cells - steps.length;
  return Array.from({ length: cells }, (_, at) => (at < unborn ? 'unborn' as const : steps[at - unborn]!));
}
