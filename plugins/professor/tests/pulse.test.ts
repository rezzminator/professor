import { describe, expect, it } from 'vitest';
import { litCells, pulse, pulseLevel } from '../plugins/professor/src/pulse.ts';
import { LOOK } from './scene.ts';

const SCALE = LOOK.pulse;

describe('pulse', () => {
  it.each<[number, number | 'drop']>([
    [-90_000, 'drop'], [-1_001, 'drop'], [-1_000, 'drop'], [-999, 0], [-1, 0], [0, 0], [1, 1], [999, 1], [1_000, 2], [4_999, 2], [5_000, 3], [19_999, 3], [20_000, 4], [49_999, 4], [50_000, 5], [1_000_000, 5],
  ])('a reading that adds %i tokens is level %s', (grown, level) => {
    expect(pulseLevel(grown, SCALE)).toBe(level);
  });

  it.each<[number, number | 'drop']>([[-10, 'drop'], [-9, 0], [0, 0], [99, 0], [100, 1], [499, 1], [500, 2]])(
    'on a scale of 100 and 500 tokens with a drop from 10, a reading that adds %i tokens is level %s', (grown, level) => {
      expect(pulseLevel(grown, { ...SCALE, steps: [100, 500], dropFrom: 10 })).toBe(level);
    },
  );

  it.each<[number, number | undefined, number]>([
    [0, 1_000, 0], [1, 1_000, 1], [100, 1_000, 2], [500, 1_000, 8], [999, 1_000, 14], [1_000, 1_000, 15], [1_500, 1_000, 15], [10, undefined, 0], [10, 0, 0],
  ])('%i of %s tokens lights %i of 15 cells', (used, window, lit) => {
    expect(litCells(used, window, 15)).toBe(lit);
  });

  it('right-aligns the steps between readings, leaves the cells before the first reading unborn, and lights from the left', () => {
    expect(pulse([0, 1_300, 1_300, 200], 200, 1_000, 5, SCALE)).toEqual([
      { level: 'unborn', lit: true }, { level: 'unborn', lit: false }, { level: 2, lit: false }, { level: 0, lit: false }, { level: 'drop', lit: false },
    ]);
  });

  it('keeps the newest steps when there are more readings than cells', () => {
    expect(pulse([0, 10, 5_000, 5_000, 65_000], 65_000, undefined, 2, SCALE)).toEqual([{ level: 0, lit: false }, { level: 5, lit: false }]);
  });

  it('draws an agent with no reading yet as all unborn', () => {
    expect(pulse([], 0, 1_000, 3, SCALE)).toEqual([{ level: 'unborn', lit: false }, { level: 'unborn', lit: false }, { level: 'unborn', lit: false }]);
  });
});
