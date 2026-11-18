// The pulse gauge: one strip of cells that is two readings at once. How many
// cells are lit, from the left, is how full the agent's context window is; the
// height of each cell is how fast its context grew in one engine reading (about
// five seconds), newest on the right, so the trace moves one cell a reading.
// The scale is pfm's (the door's look.pulse), one for every row and the main
// status line, so two traces compare by eye; this file keeps no copy of it.

import type { DoorPulse } from './door';

/** `level`: 0 up to the scale's steps, `'drop'` where the context shrank by its dropFrom or more, `'unborn'` before the agent's first reading. */
export type PulseCell = { level: number | 'drop' | 'unborn'; lit: boolean };

/** The level of one reading's growth in tokens on `scale`. */
export function pulseLevel(grown: number, scale: DoorPulse): number | 'drop' {
  if (grown <= -scale.dropFrom) return 'drop';
  let level = 0;
  for (const step of scale.steps) { if (grown >= step) level += 1; }
  return level;
}

/** How many of `cells` are lit: one at least once anything is used, all only when the window is full. */
export function litCells(used: number, window: number | undefined, cells: number): number {
  if (window === undefined || !(window > 0) || !(used > 0)) return 0;
  if (used >= window) return cells;
  return Math.min(cells - 1, Math.max(1, Math.round((used / window) * cells)));
}

/**
 * The strip for an agent's readings (`samples`, oldest first), `cells` wide: the newest `cells` steps between
 * readings, right-aligned, the cells before the agent's first reading `'unborn'`.
 */
export function pulse(samples: readonly number[], used: number, window: number | undefined, cells: number, scale: DoorPulse): PulseCell[] {
  const lit = litCells(used, window, cells);
  const steps: Array<number | 'drop'> = [];
  for (let at = Math.max(1, samples.length - cells); at < samples.length; at += 1) steps.push(pulseLevel(samples[at]! - samples[at - 1]!, scale));
  const unborn = cells - steps.length;
  return Array.from({ length: cells }, (_, at) => ({ level: at < unborn ? 'unborn' as const : steps[at - unborn]!, lit: at < lit }));
}
