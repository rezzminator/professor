// Text for one terminal row: how many cells it takes, cutting and padding it,
// cleaning text the engine or a model wrote, and the status line's units.

const ESCAPE = /\x1b\[[0-9;:?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)?|\x1b[@-Z\\-_]?/g;
const CONTROL = /[\x00-\x1f\x7f-\x9f\u{2028}\u{2029}]+/gu;

/** Text with every escape sequence taken out: what the terminal shows of it. */
export function plain(text: string): string {
  return text.replace(ESCAPE, '');
}

/** Engine or model text as one clean line: no escape sequence, no control character, runs of blanks folded. */
export function clean(text: string): string {
  return plain(text).replace(CONTROL, ' ').replace(/\s+/g, ' ').trim();
}

// Hand-written: the function-hook sandbox loads no package, so no width table can be imported.
function pointCells(point: number): number {
  if (point < 0x20 || (point >= 0x7f && point < 0xa0)) return 0;
  if (point < 0x300) return 1;
  if (point <= 0x36f || (point >= 0x200b && point <= 0x200f) || (point >= 0x20d0 && point <= 0x20ff) || (point >= 0xfe00 && point <= 0xfe0f) || point === 0xfeff) return 0;
  if ((point >= 0x1100 && point <= 0x115f) || (point >= 0x2e80 && point <= 0xa4cf) || (point >= 0xac00 && point <= 0xd7a3) || (point >= 0xf900 && point <= 0xfaff)
    || (point >= 0xfe30 && point <= 0xfe4f) || (point >= 0xff00 && point <= 0xff60) || (point >= 0xffe0 && point <= 0xffe6)
    || (point >= 0x1f300 && point <= 0x1faff) || (point >= 0x20000 && point <= 0x3fffd)) return 2;
  return 1;
}

/** The terminal cells `text` takes, its escape sequences taking none. */
export function cells(text: string): number {
  let width = 0;
  for (const char of plain(text)) width += pointCells(char.codePointAt(0)!);
  return width;
}

/** Plain `text` cut to at most `width` cells, the cut marked by a closing `…`. */
export function cut(text: string, width: number): string {
  if (cells(text) <= width) return text;
  if (width <= 0) return '';
  let out = '';
  let used = 0;
  for (const char of text) {
    const next = pointCells(char.codePointAt(0)!);
    if (used + next > width - 1) break;
    out += char;
    used += next;
  }
  return `${out.trimEnd()}…`;
}

/** Styled `text` cut to at most `width` cells: its escape sequences kept whole, the cut closed by a reset and marked by `…`. */
export function fit(text: string, width: number): string {
  if (cells(text) <= width) return text;
  if (width <= 0) return '';
  let out = '';
  let used = 0;
  // An escape sequence takes no cell, so each is copied whole; only what shows is counted.
  for (const piece of text.split(new RegExp(`(${ESCAPE.source})`, 'g'))) {
    if (piece === '') continue;
    if (piece.startsWith('\x1b')) { out += piece; continue; }
    for (const char of piece) {
      const next = pointCells(char.codePointAt(0)!);
      if (used + next > width - 1) return `${out}\x1b[0m…`;
      out += char;
      used += next;
    }
  }
  return out;
}

/** `text`, styled or not, followed by the blanks that bring it to `width` cells. */
export function pad(text: string, width: number): string {
  return text + ' '.repeat(Math.max(0, width - cells(text)));
}

/** A token count as the status line writes it: 870, 74.2K, 1.0M. */
export function tokens(count: number): string {
  const whole = Math.max(0, Math.floor(count));
  if (whole >= 1_000_000) return `${Math.floor(whole / 1_000_000)}.${Math.floor((whole % 1_000_000) / 100_000)}M`;
  if (whole >= 1_000) return `${Math.floor(whole / 1_000)}.${Math.floor((whole % 1_000) / 100)}K`;
  return String(whole);
}

/** A length of time in a width that holds still while it counts: 8s, 3m08s, 1h04m. */
export function duration(ms: number): string {
  const seconds = Math.max(0, Math.floor(ms / 1000));
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m${String(seconds % 60).padStart(2, '0')}s`;
  return `${Math.floor(minutes / 60)}h${String(minutes % 60).padStart(2, '0')}m`;
}

/** The share of `window` that `used` fills, in whole percent: 99 until it is truly full, undefined without a window. */
export function share(used: number, window: number | undefined): number | undefined {
  if (window === undefined || !(window > 0)) return undefined;
  if (used >= window) return 100;
  return Math.min(99, Math.max(0, Math.round((used / window) * 100)));
}
