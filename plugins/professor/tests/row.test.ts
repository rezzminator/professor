import { describe, expect, it } from 'vitest';
import { Door, readDoorAnswer } from '../plugins/professor/src/door.ts';
import { drawRow, failureRow, type RowAgent } from '../plugins/professor/src/row.ts';
import { plain } from '../plugins/professor/src/text.ts';
import { AGENTS, ANSWER, CHILD, FOREMAN, LOOK, NOW, PLANNER, SESSION_CWD } from './scene.ts';

const ESC = String.fromCharCode(0x1b);
const R = `${ESC}[0m`;
const REVERSE = `${ESC}[7m`;
const { tokens: TOKENS, window: WINDOW, model: MODEL, elapsed: ELAPSED, tools: TOOLS, label: LABEL, muted: MUTED, good: GOOD, warn: WARN, bad: BAD, dim: DIM } = LOOK.palette;
const BAR = ` ${DIM}│${R} `;
const SPEND = ANSWER.agents[0]!.spend!;

/** A door that took pfm's answer, the planner's facts changed by `facts`. */
function answered(facts: Record<string, unknown> = {}): Door {
  const door = new Door();
  const answer = JSON.parse(JSON.stringify(ANSWER));
  answer.agents[0] = { ...answer.agents[0], ...facts };
  door.land(readDoorAnswer({ exitCode: 0, stdout: JSON.stringify(answer), stderr: '' }));
  return door;
}
function failed(cause: string, door = new Door()): Door {
  door.fail(cause);
  return door;
}
const scene = (agents: readonly RowAgent[], pfm: Door, columns: number | undefined) => ({ agents, pfm, now: NOW, columns, sessionCwd: SESSION_CWD });
/** Every row of the four-agent list as the terminal shows it. */
const list = (pfm: Door, columns: number | undefined) => AGENTS.map((agent) => plain(drawRow(scene(AGENTS, pfm, columns), agent.id)));
/** The planner's row alone, unbounded, as its parts between the bars: pulse, who, model, state, spend, cache, then flags, notes and the task. */
const alone = (agent: Partial<RowAgent>, facts: Record<string, unknown>, bar = BAR) => {
  const row = drawRow(scene([{ ...PLANNER, ...agent }], answered(facts), undefined), 'a1');
  return bar === BAR ? row.split(BAR) : plain(row).split(bar);
};

describe('the list', () => {
  it.each<[string, () => Door, number | undefined, string[]]>([
    ['the terminal is wide', () => answered(), 260, [
      '⎽⎽⎻⎼⎼⎻_⎼⎽⎽⎽⎽⎼⎼⎼     +4.5K  17% 171.9K/1.0M   │ planner              │ ◆ opus·▰▰ medium  │ running 12m04s │ Σ $3.41 · 2.0M in · 24.3K out · 57 tools   │ 💾5m✓3m08s 94%     │ 2 errors │ ⟲1 │ ↳ 1/3 below │ Map the parser › Reading lexer.go',
      '      ⎺⎻⎽⎻⎻¯⎽⎽⎽      +144  86% 172.9K/200.0K │ door·flights:foreman │ ◇ sonnet·▰▰▰ high │ running 1m01s  │ Σ $12.50+? · 812.0K in · 9.3K out · 1 tool │ 💾5m✗1m40s was 31% │ idle 3m20s │ @door │ adv $0.30 │ API-eq │ Build the door › Running go test ./internal/…',
      ' ____________⎼↓  −22.0K   5%   9.0K/200.0K │ Explore              │ ○ haiku           │ done           │ Σ $?                                       │ 💾– ?%             │ pfm ✗ open agent-a3.jsonl: permission denied │ Find the callers',
      '_______________        +0   ?%      0/?      │ general-purpose      │                   │ pending        │ Σ …                                        │ 💾–                │ Wait for the build',
    ]],
    ["the list gives up the growth figure and the spend's words", () => answered(), 200, [
      '⎽⎽⎻⎼⎼⎻_⎼⎽⎽⎽⎽⎼⎼⎼    17% 171.9K/1.0M   │ planner              │ ◆ opus·▰▰ medium  │ running 12m04s │ Σ $3.41/2.0M/24.3K/57    │ 💾5m✓3m08s 94%     │ 2 errors │ ⟲1 │ ↳ 1/3 below │ Map the par… › Reading…',
      '      ⎺⎻⎽⎻⎻¯⎽⎽⎽    86% 172.9K/200.0K │ door·flights:foreman │ ◇ sonnet·▰▰▰ high │ running 1m01s  │ Σ $12.50+?/812.0K/9.3K/1 │ 💾5m✗1m40s was 31% │ idle 3m20s │ @door │ adv $0.30 │ Build the door',
      ' ____________⎼↓   5%   9.0K/200.0K │ Explore              │ ○ haiku           │ done           │ Σ $?                     │ 💾– ?%             │ pfm ✗ open agent-a3.jsonl: permis… │ Find the callers',
      '_______________     ?%      0/?      │ general-purpose      │                   │ pending        │ Σ …                      │ 💾–                │ Wait for the build',
    ]],
    ["it gives up the flags' words, the token figures, the cache and the model's name", () => answered(), 120, [
      '⎼⎽⎽⎽⎽⎼⎼⎼    17% │ planner              │ ◆·▰▰  │ running 12m04s │ Σ $3.41    │ 2 err │ ⟲1 │ ↳1/3 │ Map the par… › Readi…',
      '⎻⎽⎻⎻¯⎽⎽⎽    86% │ door·flights:foreman │ ◇·▰▰▰ │ running 1m01s  │ Σ $12.50+? │ idle 3m20s │ @door │ Build the door',
      ' _____⎼↓   5% │ Explore              │ ○     │ done           │ Σ $?       │ pfm ✗ open agent-a3.j… │ Find the callers',
      '________     ?% │ general-purpose      │       │ pending        │ Σ …        │ Wait for the build',
    ]],
    ['it gives up the spend and the model and cuts each name', () => answered(), 80, [
      '⎼⎽⎽⎽⎽⎼⎼⎼    17% │ planner      │ running 12m04s │ 2 err │ ⟲1 │ ↳1/3 │ Map the p…',
      '⎻⎽⎻⎻¯⎽⎽⎽    86% │ door·flight… │ running 1m01s  │ idle 3m20s │ @door │ Build th…',
      ' _____⎼↓   5% │ Explore      │ done           │ pfm ✗ open agent-a3.jsonl: pe…',
      '________     ?% │ general-pur… │ pending        │ Wait for the build',
    ]],
    ['it is too narrow for its flags', () => answered(), 60, [
      '⎼⎽⎽⎽⎽⎼⎼⎼    17% │ planner      │ running 12m04s │ 2 err │ ⟲…',
      '⎻⎽⎻⎻¯⎽⎽⎽    86% │ door·flight… │ running 1m01s  │ idle 3m20s',
      ' _____⎼↓   5% │ Explore      │ done           │ pfm ✗ ope…',
      '________     ?% │ general-pur… │ pending        │ Wait for…',
    ]],
    ['the surface measures no width', () => answered(), undefined, [
      '⎽⎽⎻⎼⎼⎻_⎼⎽⎽⎽⎽⎼⎼⎼     +4.5K  17% 171.9K/1.0M   │ planner              │ ◆ opus·▰▰ medium  │ running 12m04s │ Σ $3.41 · 2.0M in · 24.3K out · 57 tools   │ 💾5m✓3m08s 94%     │ 2 errors │ ⟲1 │ ↳ 1/3 below │ Map the parser › Reading lexer.go',
      '      ⎺⎻⎽⎻⎻¯⎽⎽⎽      +144  86% 172.9K/200.0K │ door·flights:foreman │ ◇ sonnet·▰▰▰ high │ running 1m01s  │ Σ $12.50+? · 812.0K in · 9.3K out · 1 tool │ 💾5m✗1m40s was 31% │ idle 3m20s │ @door │ adv $0.30 │ API-eq │ Build the door › Running go test ./internal/...',
      ' ____________⎼↓  −22.0K   5%   9.0K/200.0K │ Explore              │ ○ haiku           │ done           │ Σ $?                                       │ 💾– ?%             │ pfm ✗ open agent-a3.jsonl: permission denied │ Find the callers',
      '_______________        +0   ?%      0/?      │ general-purpose      │                   │ pending        │ Σ …                                        │ 💾–                │ Wait for the build',
    ]],
    ['pfm has not answered yet', () => new Door(), 200, [
      '_______________     +4.5K  17% 171.9K/1.0M   │ planner              │ claude-opus-9·medium │ running 12m04s │ Σ … │ 💾5m✓3m08s … │ Map the parser › Reading lexer.go',
      '      _________      +144  86% 172.9K/200.0K │ door·flights:foreman │ claude-sonnet-9·high │ running 1m01s  │ Σ … │ 💾5m✗1m40s … │ @door │ Build the door › Running go test ./internal/...',
      ' ______________  −22.0K   5%   9.0K/200.0K │ Explore              │ claude-haiku-9       │ done           │ Σ … │ 💾– …        │ Find the callers',
      '_______________        +0   ?%      0/?      │ general-purpose      │                      │ pending        │ Σ … │ 💾–          │ Wait for the build',
    ]],
    ['pfm is not there', () => failed('not on PATH'), 200, [
      '_______________     +4.5K  17% 171.9K/1.0M   │ planner              │ claude-opus-9·medium │ running 12m04s │ $0.46 own  │ 💾5m✓3m08s ?% │ pfm ✗ not on PATH │ Map the parser › Reading lexer.go',
      '      _________      +144  86% 172.9K/200.0K │ door·flights:foreman │ claude-sonnet-9·high │ running 1m01s  │ $12.50 own │ 💾5m✗1m40s ?% │ @door │ pfm ✗ │ Build the door › Running go test ./internal/…',
      ' ______________  −22.0K   5%   9.0K/200.0K │ Explore              │ claude-haiku-9       │ done           │ $0.02 own  │ 💾– ?%        │ pfm ✗ │ Find the callers',
      '_______________        +0   ?%      0/?      │ general-purpose      │                      │ pending        │ $? own     │ 💾–           │ pfm ✗ │ Wait for the build',
    ]],
    ['pfm fails after it answered', () => failed('list /c/subagents: permission denied', answered()), 252, [
      '⎽⎽⎻⎼⎼⎻_⎼⎽⎽⎽⎽⎼⎼⎼     +4.5K  17% 171.9K/1.0M   │ planner              │ ◆ opus·▰▰ medium  │ running 12m04s │ Σ $3.41/2.0M/24.3K/57    │ 💾5m✓3m08s 94%     │ 2 errors │ ⟲1 │ ↳ 1/3 below │ pfm ✗ list /c/subagents: permi… │ Map the parser › Reading lexer.go',
      '      ⎺⎻⎽⎻⎻¯⎽⎽⎽      +144  86% 172.9K/200.0K │ door·flights:foreman │ ◇ sonnet·▰▰▰ high │ running 1m01s  │ Σ $12.50+?/812.0K/9.3K/1 │ 💾5m✗1m40s was 31% │ idle 3m20s │ @door │ adv $0.30 │ API-eq │ pfm ✗ │ Build the door › Running go test ./internal/...',
      ' ____________⎼↓  −22.0K   5%   9.0K/200.0K │ Explore              │ ○ haiku           │ done           │ Σ $?                     │ 💾– ?%             │ pfm ✗ │ Find the callers',
      '_______________        +0   ?%      0/?      │ general-purpose      │                   │ pending        │ $? own                   │ 💾–                │ pfm ✗ │ Wait for the build',
    ]],
  ])('draws every row to the same cells when %s', (_name, pfm, columns, rows) => {
    expect(list(pfm(), columns)).toEqual(rows);
  });
});

describe('one row', () => {
  it.each<[string, Partial<RowAgent>, Record<string, unknown>, number, string]>([
    ['waits on its parent', { status: 'waiting' }, {}, 3, 'waiting 12m04s'],
    ['sits idle', { status: 'idle' }, {}, 3, 'idle 12m04s'],
    ['is paused', { taskStatus: 'paused' }, {}, 3, 'paused 12m04s'],
    ['failed after twelve minutes of work', { status: 'failed' }, {}, 3, 'failed 12m00s'],
    ['was killed and pfm knows no last activity', { status: 'killed' }, { lastActivity: 0 }, 3, 'killed'],
    ['has a part pfm could not price', {}, { spend: { ...SPEND, usdKnown: false } }, 4, 'Σ $? · 2.0M in · 24.3K out · 57 tools'],
    ['used one tool', {}, { spend: { ...SPEND, tools: 1 } }, 4, 'Σ $3.41 · 2.0M in · 24.3K out · 1 tool'],
    ['runs a model that keeps no cache', {}, { cacheLifetime: false }, 5, '💾∅ 94%'],
    ['caches for an hour', { lastRequest: { ...PLANNER.lastRequest!, cacheTtl: '1h' } }, {}, 5, '💾1h✓58m08s 94%'],
    ['sent its last request uncached', { lastRequest: { at: NOW - 112_000, model: 'claude-opus-9' } }, {}, 5, '💾– 94%'],
    ['has no cache reading yet', {}, { cacheHit: -1 }, 5, '💾5m✓3m08s'],
    ['has been quiet for a minute', {}, { lastActivity: NOW - 60_000 }, 6, 'idle 1m00s'],
    ['has been quiet for a moment less', {}, { lastActivity: NOW - 59_999 }, 6, '2 errors'],
    ['had one tool error', {}, { toolErrors: 1 }, 6, '1 error'],
    ['has agents below that pfm could not list', {}, { nested: { total: 0, working: 0, unknown: 0, error: 'list /c/subagents: permission denied' } }, 8, '↳ ?/? below'],
    ['has agents below that pfm could not read', {}, { nested: { total: 3, working: 0, unknown: 2 } }, 8, '↳ 0/3 below +2?'],
    ['has an advisor pfm could not price', {}, { spend: { ...SPEND, advisorUnpriced: 1 } }, 9, 'adv n/a'],
    ['works in another directory', { cwd: '/w/other/' }, {}, 9, '@other'],
    ['has a name', { name: 'lexer' }, {}, 1, 'lexer·planner'],
    ['has an effort and no model', { model: undefined, lastRequest: undefined }, { text: { ...ANSWER.agents[0]!.text, glyph: '', family: '' } }, 2, '▰▰ medium'],
    ['is named as its type', { name: 'planner' }, {}, 1, 'planner'],
    ['has a window only pfm knows', {}, { contextWindow: 200_000 }, 0, '⎽⎽⎻⎼⎼⎻_⎼⎽⎽⎽⎽⎼⎼⎼   +4.5K  86% 171.9K/200.0K'],
    ['fills its window', { tokenCount: 1_000_000 }, {}, 0, '⎽⎽⎻⎼⎼⎻_⎼⎽⎽⎽⎽⎼⎼⎼   +4.5K 100%   1.0M/1.0M'],
    ['has one reading', { tokenSamples: [171_928] }, {}, 0, '   ____________      +0  17% 171.9K/1.0M'],
    ['has no description', { description: '' }, {}, 9, 'Reading lexer.go'],
    ['is doing what its description says', { label: 'Map the parser' }, {}, 9, 'Map the parser'],
    ['reports progress with an escape sequence and a line break in it', { label: `Reading ${ESC}[31mlexer${ESC}[0m\n.go` }, {}, 9, 'Map the parser › Reading lexer .go'],
  ])('that %s', (_name, agent, facts, part, text) => {
    expect(alone(agent, facts, ' │ ')[part]).toBe(text);
  });

  const gauge = (tone: string, lit: number, share: string, used: string) =>
    `${tone}${REVERSE}${' '.repeat(lit)}${R}${MUTED}${'_'.repeat(15 - lit)}${R} ${MUTED}     +0${R} ${tone}${share}${R} ${TOKENS}${used}${R}${MUTED}/${R}${WINDOW}100${R}`;
  const sum = (dollars: string) => `${MUTED}Σ${R} ${dollars} ${MUTED}·${R} ${TOKENS}2.0M${R} ${MUTED}in${R} ${MUTED}·${R} ${TOKENS}24.3K${R} ${MUTED}out${R} ${MUTED}·${R} ${TOOLS}57${R} ${MUTED}tools${R}`;
  const unread = { tokenSamples: [], contextWindowSize: 100 };

  it.each<[string, Partial<RowAgent>, Record<string, unknown>, number, string]>([
    ['a window under half full good', { ...unread, tokenCount: 49 }, {}, 0, gauge(GOOD, 7, ' 49%', '    49')],
    ['a window half full a warning', { ...unread, tokenCount: 50 }, {}, 0, gauge(WARN, 8, ' 50%', '    50')],
    ['a window under four fifths full a warning', { ...unread, tokenCount: 79 }, {}, 0, gauge(WARN, 12, ' 79%', '    79')],
    ['a window four fifths full bad', { ...unread, tokenCount: 80 }, {}, 0, gauge(BAD, 12, ' 80%', '    80')],
    ['a spend under two dollars plain', {}, { spend: { ...SPEND, usd: 1.99 } }, 4, sum(`${LABEL}$3.41${R}`)],
    ['a spend of two dollars a warning', {}, { spend: { ...SPEND, usd: 2 } }, 4, sum(`${WARN}$3.41${R}`)],
    ['a spend under ten dollars a warning', {}, { spend: { ...SPEND, usd: 9.99 } }, 4, sum(`${WARN}$3.41${R}`)],
    ['a spend of ten dollars bad', {}, { spend: { ...SPEND, usd: 10 } }, 4, sum(`${BAD}$3.41${R}`)],
    ['a cache hit under half bad', {}, { cacheHit: 49 }, 5, `${GOOD}💾5m✓3m08s${R} ${BAD}49%${R}`],
    ['a cache hit of half a warning', {}, { cacheHit: 50 }, 5, `${GOOD}💾5m✓3m08s${R} ${WARN}50%${R}`],
    ['a cache hit under four fifths a warning', {}, { cacheHit: 79 }, 5, `${GOOD}💾5m✓3m08s${R} ${WARN}79%${R}`],
    ['a cache hit of four fifths good', {}, { cacheHit: 80 }, 5, `${GOOD}💾5m✓3m08s${R} ${GOOD}80%${R}`],
    ['a quiet of under five minutes a warning', {}, { lastActivity: NOW - 299_999 }, 6, `${WARN}idle 4m59s${R}`],
    ['a quiet of five minutes bad', {}, { lastActivity: NOW - 300_000 }, 6, `${BAD}idle 5m00s${R}`],
  ])('paints %s', (_name, agent, facts, part, styled) => {
    expect(alone(agent, facts)[part]).toBe(styled);
  });

  it('paints in pfm\'s colours, the gauge\'s lit cells in reverse video', () => {
    expect(drawRow(scene([FOREMAN], answered(), 130), 'a2')).toBe([
      `${BAD}${REVERSE}⎻⎽⎻⎻¯⎽⎽${R}${WINDOW}⎽${R} ${BAD} 86%${R}`,
      `${TOKENS}door${R}${MUTED}·${R}${LABEL}flights:foreman${R}`,
      `${MODEL}◇ sonnet${R}${MUTED}·${R}${LOOK.effortColors.high}▰▰▰${R}`,
      `${GOOD}running${R} ${ELAPSED}1m01s${R}`,
      `${MUTED}Σ${R} ${BAD}$12.50${R}${WARN}+?${R}`,
      `${BAD}💾✗1m40s${R}`,
      `${WARN}idle 3m20s${R}`,
      `${LABEL}Build the d…${R} ${MUTED}› Running…${R}`,
    ].join(BAR));
  });

  it('paints by weight alone, the engine\'s own dollars named as such, when pfm never answered', () => {
    const bar = ` ${ESC}[2m│${R} `;
    expect(drawRow(scene([{ ...PLANNER, cost: { usd: 0.46, hasUnknownPrice: true } }], failed('not on PATH'), undefined), 'a1')).toBe([
      `${REVERSE}___${R}____________   +4.5K  17% ${ESC}[1m171.9K${R}${ESC}[2m/${R}1.0M`,
      `${ESC}[1mplanner${R}`,
      `claude-opus-9${ESC}[2m·${R}medium`,
      'running 12m04s',
      `$0.46? ${ESC}[2mown${R}`,
      `💾5m✓3m08s ${ESC}[2m?%${R}`,
      `${ESC}[31mpfm ✗ not on PATH${R}`,
      `Map the parser ${ESC}[2m› Reading lexer.go${R}`,
    ].join(bar));
  });

  it.each<[string, number, string]>([
    ['gives the cause its least and the task its least where both just fit', 66, ' _____⎼↓   5% │ Explore │ done │ pfm ✗ open agent-a3.j… │ Find th…'],
    ['gives the cause all that is left where the task\'s least no longer fits', 65, ' _____⎼↓   5% │ Explore │ done │ pfm ✗ open agent-a3.jsonl: perm…'],
  ])('%s', (_name, columns, row) => {
    expect(plain(drawRow(scene([{ ...CHILD, parentId: undefined }], answered(), columns), 'a3'))).toBe(row);
  });

  it('keeps a nested row inside the two cells its connector takes', () => {
    const pair = [FOREMAN, { ...PLANNER, parentId: 'a2' }];
    expect(pair.map((agent) => plain(drawRow(scene(pair, answered(), 60), agent.id)))).toEqual([
      '⎻⎽⎻⎻¯⎽⎽⎽    86% │ door·flight… │ running 1m01s  │ idle 3m20s',
      '⎼⎽⎽⎽⎽⎼⎼⎼  17% │ planner      │ running 12m04s │ 2 err │ ⟲…',
    ]);
  });

  it.each<[string, Record<string, unknown>, string]>([
    ['could not list the agents below', { nested: { total: 0, working: 0, unknown: 0, error: 'list /c/subagents: permission denied' } }, '⎼⎽⎽⎽⎽⎼⎼⎼  17% │ planner │ ◆·▰▰ │ running 12m04s │ Σ $3.41 │ 2 err │ ⟲1 │ ↳?/? │ Map the parser'],
    ['could not read two of the agents below', { nested: { total: 3, working: 0, unknown: 2 } }, '⎼⎽⎽⎽⎽⎼⎼⎼  17% │ planner │ ◆·▰▰ │ running 12m04s │ 2 err │ ⟲1 │ ↳0/3+2? │ Map the pars… › Reading le…'],
    ['counted one tool error', { toolErrors: 1 }, '⎼⎽⎽⎽⎽⎼⎼⎼  17% │ planner │ ◆·▰▰ │ running 12m04s │ Σ $3.41 │ 1 err │ ⟲1 │ ↳1/3 │ Map the parser'],
  ])('says in a narrow list, without the flags\' words, that pfm %s', (_name, facts, row) => {
    expect(plain(drawRow(scene([PLANNER], answered(facts), 100), 'a1'))).toBe(row);
  });

  it('says how old pfm\'s facts are once its answer leaves an agent out', () => {
    const door = answered();
    door.land(readDoorAnswer({ exitCode: 0, stdout: JSON.stringify({ ...ANSWER, at: NOW + 44_000, agents: [ANSWER.agents[1]] }), stderr: '' }));
    const flags = (agent: RowAgent) => plain(drawRow({ ...scene([agent], door, undefined), now: NOW + 105_000 }, agent.id)).split(' │ ').slice(6, 8);
    expect(flags({ ...PLANNER, status: 'waiting' })).toEqual(['pfm 1m45s ago', '2 errors']);
    expect(flags({ ...PLANNER, status: 'completed' })).toEqual(['pfm 1m45s ago', '2 errors']);
    // Its last activity is as old as the facts: no quiet is counted from it, though it is 1m49s back.
    expect(flags(PLANNER)).toEqual(['pfm 1m45s ago', '2 errors']);
    // The answer names the foreman: its facts are fresh, and its quiet is counted.
    expect(flags(FOREMAN)).toEqual(['idle 5m05s', '@door']);
  });

  it('draws the strip on pfm\'s scale, in its glyphs and with its drop mark', () => {
    const door = new Door();
    const look = { ...LOOK, pulse: { glyphs: ['.', ':', '|'], drop: 'v', steps: [500, 5_000], dropFrom: 20_000, stepMs: 5_000 } };
    door.land(readDoorAnswer({ exitCode: 0, stdout: JSON.stringify({ ...ANSWER, look }), stderr: '' }));
    expect(plain(drawRow(scene([{ ...CHILD, parentId: undefined }], door, undefined), 'a3')).split(' │ ')[0]).toBe(' ............:v  −22.0K   5%   9.0K/200.0K');
  });

  it('shows the engine\'s own dollars for an ended agent pfm never named', () => {
    expect(plain(drawRow(scene([{ ...CHILD, id: 'a9', parentId: undefined }], answered(), undefined), 'a9'))).toBe(
      ' ____________⎼↓  −22.0K   5%   9.0K/200.0K │ Explore │ claude-haiku-9 │ done │ $0.02 own │ 💾– ?% │ Find the callers',
    );
  });
});

describe('a row whose drawing threw', () => {
  it.each([
    ['names the cause before the engine\'s own words', `throw: Cannot read ${ESC}[31mproperties${ESC}[0m\nof undefined`, 'Reading lexer.go', `${ESC}[31mprofessor ✗ throw: Cannot read properties of undefined${R} ${ESC}[2m│${R} Reading lexer.go`],
    ['cuts a long cause and stands alone without words', `timeout: ${'x'.repeat(80)}`, '', `${ESC}[31mprofessor ✗ timeout: ${'x'.repeat(46)}…${R}`],
  ])('%s', (_name, cause, label, row) => {
    expect(failureRow(cause, label)).toBe(row);
  });
});
