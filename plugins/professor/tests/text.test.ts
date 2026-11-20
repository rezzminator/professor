import { describe, expect, it } from 'vitest';
import { cells, clean, cut, duration, fit, pad, share, tokens } from '../plugins/professor/src/text.ts';

const ESC = String.fromCharCode(0x1b);
const BELL = String.fromCharCode(0x07);
const ACUTE = String.fromCodePoint(0x301);

describe('units', () => {
  it.each([[0, '0'], [870, '870'], [999, '999'], [1_000, '1.0K'], [1_949, '1.9K'], [1_950, '2.0K'], [74_250, '74.3K'], [92_907, '92.9K'], [999_949, '999.9K'], [999_950, '1.0M'], [999_999, '1.0M'], [1_000_000, '1.0M'], [1_250_000, '1.3M'], [1_849_999, '1.8M'], [1_850_000, '1.9M'], [1_874_214, '1.9M']])('writes %i tokens as %s', (count, text) => {
    expect(tokens(count)).toBe(text);
  });

  it.each([[0, '0s'], [59_999, '59s'], [60_000, '1m00s'], [188_000, '3m08s'], [3_599_999, '59m59s'], [3_600_000, '1h00m'], [3_840_000, '1h04m'], [-5_000, '0s']])('writes %i ms as %s', (ms, text) => {
    expect(duration(ms)).toBe(text);
  });

  it.each<[number, number | undefined, number | undefined]>([
    [0, 1_000, 0], [4, 1_000, 0], [5, 1_000, 1], [994, 1_000, 99], [999, 1_000, 99], [1_000, 1_000, 100], [1_200, 1_000, 100], [10, undefined, undefined], [10, 0, undefined],
  ])('reads %i of %s as %s percent', (used, window, percent) => {
    expect(share(used, window)).toBe(percent);
  });
});

describe('cells', () => {
  it.each([['abc', 3], ['💾5m', 4], ['漢字', 4], [`e${ACUTE}`, 1], [`${ESC}[1;97mab${ESC}[0m`, 2], ['▰▱·•●↓', 6], ['', 0]])('counts %j as %i cells', (text, width) => {
    expect(cells(text)).toBe(width);
  });

  it.each([['Reading lexer.go', 16, 'Reading lexer.go'], ['Reading lexer.go', 10, 'Reading l…'], ['Map the parser', 5, 'Map…'], ['漢字漢字', 5, '漢字…'], ['abc', 0, ''], ['abc', 1, '…']])('cuts %j to %i cells as %j', (text, width, out) => {
    expect(cut(text, width)).toBe(out);
  });

  it.each([
    [`${ESC}[1mabcdef${ESC}[0m`, 6, `${ESC}[1mabcdef${ESC}[0m`],
    [`${ESC}[1mabcdef${ESC}[0m`, 4, `${ESC}[1mabc${ESC}[0m…`],
    [`ab${ESC}[31mcd${ESC}[0mef`, 5, `ab${ESC}[31mcd${ESC}[0m${ESC}[0m…`],
    ['漢字漢字', 5, `漢字${ESC}[0m…`],
    ['abc', 0, ''],
  ])('fits styled %j in %i cells as %j', (text, width, out) => {
    expect(fit(text, width)).toBe(out);
  });

  it('pads styled text by the cells it shows', () => {
    expect(pad(`${ESC}[1mab${ESC}[0m`, 4)).toBe(`${ESC}[1mab${ESC}[0m  `);
    expect(pad('abcdef', 4)).toBe('abcdef');
  });
});

describe('clean', () => {
  it('makes one line of model text and takes every escape sequence out', () => {
    expect(clean(`  Reading\n\tlexer.go ${ESC}[31mnow${ESC}[0m ${ESC}]8;;http://x${BELL}link${ESC}]8;;${BELL}${BELL} `)).toBe('Reading lexer.go now link');
  });
});
