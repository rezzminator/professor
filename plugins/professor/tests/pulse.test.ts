import { describe, expect, it } from 'vitest';
import { litCells, pulse, pulseLevel } from '../plugins/professor/src/pulse.ts';
import { LOOK } from './scene.ts';

const SCALE = LOOK.pulse;

describe('pulse', () => {
  it.each<[number, number | 'drop']>([
    [-90_000, 'drop'], [-1_001, 'drop'], [-1_000, 'drop'], [-999, 0], [-1, 0], [0, 0], [1, 1], [4_999, 1], [5_000, 2], [1_000_000, 2],
  ])('a reading that adds %i tokens is level %s', (grown, level) => {
    expect(pulseLevel(grown, SCALE)).toBe(level);
  });

  it.each<[number, number | 'drop']>([[-10, 'drop'], [-9, 0], [0, 0], [99, 0], [100, 1], [499, 1], [500, 2]])(
    'on a scale of 100 and 500 tokens with a drop from 10, a reading that adds %i tokens is level %s', (grown, level) => {
      expect(pulseLevel(grown, { ...SCALE, steps: [100, 500], dropFrom: 10 })).toBe(level);
    },
  );

  it.each<[number, number | undefined, number]>([
    [0, 1_000, 0], [1, 1_000, 1], [187, 1_000, 1], [188, 1_000, 2], [500, 1_000, 4], [937, 1_000, 7], [999, 1_000, 7], [1_000, 1_000, 8], [1_500, 1_000, 8], [10, undefined, 0], [10, 0, 0],
  ])('%i of %s tokens turns on %i of 8 cells', (used, window, on) => {
    expect(litCells(used, window, 8)).toBe(on);
  });

  it('right-aligns the steps between readings and leaves the cells before the first reading unborn', () => {
    expect(pulse([0, 1_300, 1_300, 200], 5, SCALE)).toEqual(['unborn', 'unborn', 1, 0, 'drop']);
  });

  it('keeps the newest steps when there are more readings than cells', () => {
    expect(pulse([0, 10, 5_000, 5_000, 65_000], 2, SCALE)).toEqual([0, 2]);
  });

  it('draws an agent with no reading yet as all unborn', () => {
    expect(pulse([], 3, SCALE)).toEqual(['unborn', 'unborn', 'unborn']);
  });
});
