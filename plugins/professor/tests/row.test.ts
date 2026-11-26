import { describe, expect, it } from 'vitest';
import { Door, readDoorAnswer } from '../plugins/professor/src/door.ts';
import { drawRow, failureRow, type RowAgent } from '../plugins/professor/src/row.ts';
import { cells, plain } from '../plugins/professor/src/text.ts';
import { AGENTS, ANSWER, CHILD, FOREMAN, LOOK, NOW, PLANNER, SESSION_CWD } from './scene.ts';

const ESC = String.fromCharCode(0x1b);
const R = `${ESC}[0m`;
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
  // A row's `columns` are the terminal's less six cells: the footer's padding each side and the engine's lead.
  it.each<[string, () => Door, number, string[]]>([
    ['the terminal is wide', () => answered(), 254, [
      '1/3 ▰▱▱▱▱▱▱▱ ••••••••   +4.5K  17% 171.9K/1.0M   │ planner              │ ◆ Opus 9·▰▰ medium  │ running 12m04s │ Σ $3.41 · 2.0M in · 24.3K out · 57 tools   │ 💾5m✓3m08s 94%     │ 2 errors │ ⟲1 │ Map the parser › Reading lexer.go',
      '    ▰▰▰▰▰▰▰▱ ●•●●●•••    +144  86% 173.0K/200.0K │ door·flights:foreman │ ◇ Sonnet 9·▰▰▰ high │ running 1m01s  │ Σ $12.50+? · 812.0K in · 9.3K out · 1 tool │ 💾5m✗1m40s was 31% │ idle 3m20s │ @door │ Build the door › Running go test ./internal/...',
      '    ▰▱▱▱▱▱▱▱ ····•↓  −22.0K   5%   9.0K/200.0K │ Explore              │ ○ Haiku 9           │ done           │ Σ $?                                       │ 💾– ?%             │ pfm ✗ open agent-a3.jsonl: permission denied │ Find the callers',
      '    ▱▱▱▱▱▱▱▱ ·················· ?%      0/?      │ general-purpose      │                     │ pending        │ Σ …                                        │ 💾–                │ Wait for the build',
    ]],
    ['no row has agents below it', () => answered({ nested: { total: 0, working: 0, unknown: 0 } }), 252, [
      ' ▰▱▱▱▱▱▱▱ ••••••••   +4.5K  17% 171.9K/1.0M   │ planner              │ ◆ Opus 9·▰▰ medium  │ running 12m04s │ Σ $3.41 · 2.0M in · 24.3K out · 57 tools   │ 💾5m✓3m08s 94%     │ 2 errors │ ⟲1 │ Map the parser › Reading lexer.go',
      ' ▰▰▰▰▰▰▰▱ ●•●●●•••    +144  86% 173.0K/200.0K │ door·flights:foreman │ ◇ Sonnet 9·▰▰▰ high │ running 1m01s  │ Σ $12.50+? · 812.0K in · 9.3K out · 1 tool │ 💾5m✗1m40s was 31% │ idle 3m20s │ @door │ Build the door › Running go test ./internal/...',
      ' ▰▱▱▱▱▱▱▱ ····•↓  −22.0K   5%   9.0K/200.0K │ Explore              │ ○ Haiku 9           │ done           │ Σ $?                                       │ 💾– ?%             │ pfm ✗ open agent-a3.jsonl: permission denied │ Find the callers',
      ' ▱▱▱▱▱▱▱▱ ·················· ?%      0/?      │ general-purpose      │                     │ pending        │ Σ …                                        │ 💾–                │ Wait for the build',
    ]],
    ['the list spells the spend in slashes, the flags and the model short', () => answered(), 200, [
      '1/3 ▰▱▱▱▱▱▱▱ ••••••••   +4.5K  17% 171.9K/1.0M   │ planner              │ ◆·▰▰  │ running 12m04s │ Σ $3.41/2.0M/24.3K/57    │ 💾5m✓3m08s 94%     │ 2 err │ ⟲1 │ Map the parser › Reading lexer.go',
      '    ▰▰▰▰▰▰▰▱ ●•●●●•••    +144  86% 173.0K/200.0K │ door·flights:foreman │ ◇·▰▰▰ │ running 1m01s  │ Σ $12.50+?/812.0K/9.3K/1 │ 💾5m✗1m40s was 31% │ idle 3m20s │ @door │ Build the do… › Running g…',
      '    ▰▱▱▱▱▱▱▱ ····•↓  −22.0K   5%   9.0K/200.0K │ Explore              │ ○     │ done           │ Σ $?                     │ 💾– ?%             │ pfm ✗ open agent-a3.jsonl:… │ Find the callers',
      '    ▱▱▱▱▱▱▱▱ ·················· ?%      0/?      │ general-purpose      │       │ pending        │ Σ …                      │ 💾–                │ Wait for the build',
    ]],
    ['it takes the blanks around its bars before the pad of its columns', () => answered(), 174, [
      '1/3 ▰▱▱▱▱▱▱▱ ••••••••   +4.5K  17% 171.9K/1.0M  │planner     │◆·▰▰ │running 12m04s│Σ $3.41/2.0M/24.3K/57   │💾5m✓3m08s 94%    │2 err│⟲1│Map the parser › Reading lexer.…',
      '    ▰▰▰▰▰▰▰▱ ●•●●●•••    +144  86% 173.0K/200.0K│door·flight…│◇·▰▰▰│running 1m01s │Σ $12.50+?/812.0K/9.3K/1│💾5m✗1m40s was 31%│idle 3m20s│@door│Build the d… › Running…',
      '    ▰▱▱▱▱▱▱▱ ····•↓  −22.0K   5%   9.0K/200.0K│Explore     │○    │done          │Σ $?                    │💾– ?%            │pfm ✗ open agent-a3.jso…│Find the callers',
      '    ▱▱▱▱▱▱▱▱ ·················· ?%      0/?     │general-pur…│     │pending       │Σ …                     │💾–               │Wait for the build',
    ]],
    ['it takes the blanks around its bars and the pad of its columns', () => answered(), 170, [
      '1/3 ▰▱▱▱▱▱▱▱ ••••••••   +4.5K  17% 171.9K/1.0M│planner│◆·▰▰│running 12m04s│Σ $3.41/2.0M/24.3K/57│💾5m✓3m08s 94%│2 err│⟲1│Map the parser › Reading lexer.go',
      '    ▰▰▰▰▰▰▰▱ ●•●●●•••    +144  86% 173.0K/200.0K│door·flight…│◇·▰▰▰│running 1m01s│Σ $12.50+?/812.0K/9.3K/1│💾5m✗1m40s was 31%│idle 3m20s│@door│Build the d… › Runni…',
      '    ▰▱▱▱▱▱▱▱ ····•↓  −22.0K   5%   9.0K/200.0K│Explore│○│done│Σ $?│💾– ?%│pfm ✗ open agent-a3.jsonl: permission denied│Find the callers',
      '    ▱▱▱▱▱▱▱▱ ·················· ?%      0/?│general-pur…│pending│Σ …│💾–│Wait for the build',
    ]],
    ["it gives up the spend's counts", () => answered(), 160, [
      '1/3 ▰▱▱▱▱▱▱▱ ••••••••   +4.5K  17% 171.9K/1.0M│planner│◆·▰▰│running 12m04s│Σ $3.41│💾5m✓3m08s 94%│2 err│⟲1│Map the parser › Reading lexer.go',
      '    ▰▰▰▰▰▰▰▱ ●•●●●•••    +144  86% 173.0K/200.0K│door·flight…│◇·▰▰▰│running 1m01s│Σ $12.50+?│💾5m✗1m40s was 31%│idle 3m20s│@door│Build the d… › Running g…',
      '    ▰▱▱▱▱▱▱▱ ····•↓  −22.0K   5%   9.0K/200.0K│Explore│○│done│Σ $?│💾– ?%│pfm ✗ open agent-a3.jsonl: permission denied│Find the callers',
      '    ▱▱▱▱▱▱▱▱ ·················· ?%      0/?│general-pur…│pending│Σ …│💾–│Wait for the build',
    ]],
    ["it gives up the spend's counts and the cache's hit", () => answered(), 140, [
      '1/3 ▰▱▱▱▱▱▱▱ ••••••••   +4.5K  17% 171.9K/1.0M│planner│◆·▰▰│running 12m04s│Σ $3.41│💾✓3m08s│2 err│⟲1│Map the parser › Reading lexer.go',
      '    ▰▰▰▰▰▰▰▱ ●•●●●•••    +144  86% 173.0K/200.0K│door·flight…│◇·▰▰▰│running 1m01s│Σ $12.50+?│💾✗1m40s│idle 3m20s│Build the d… › Runni…',
      '    ▰▱▱▱▱▱▱▱ ····•↓  −22.0K   5%   9.0K/200.0K│Explore│○│done│Σ $?│💾–│pfm ✗ open agent-a3.jsonl: permission denied│Find the callers',
      '    ▱▱▱▱▱▱▱▱ ·················· ?%      0/?│general-pur…│pending│Σ …│💾–│Wait for the build',
    ]],
    ['it gives up the spend and the cache', () => answered(), 120, [
      '1/3 ▰▱▱▱▱▱▱▱ ••••••••   +4.5K  17% 171.9K/1.0M│planner│◆·▰▰│running 12m04s│2 err│⟲1│Map the parser › Reading lexe…',
      '    ▰▰▰▰▰▰▰▱ ●•●●●•••    +144  86% 173.0K/200.0K│door·flight…│◇·▰▰▰│running 1m01s│idle 3m20s│Build the d… › Runni…',
      '    ▰▱▱▱▱▱▱▱ ····•↓  −22.0K   5%   9.0K/200.0K│Explore│○│done│pfm ✗ open agent-a3.jsonl: permi…│Find the callers',
      '    ▱▱▱▱▱▱▱▱ ·················· ?%      0/?│general-pur…│pending│Wait for the build',
    ]],
    ['the block gives up its growth figure and the rows the model', () => answered(), 100, [
      '1/3 ▰▱▱▱▱▱▱▱ ·•••••••• 17% 171.9K/1.0M│planner│running 12m04s│2 err│⟲1│Map the par… › Reading…',
      '    ▰▰▰▰▰▰▰▱ ●●•●●●••• 86% 173.0K/200.0K│door·flight…│running 1m01s│idle 3m20s│@door│Build th…',
      '    ▰▱▱▱▱▱▱▱ ······•↓ 5%   9.0K/200.0K│Explore│done│pfm ✗ open agent-a3.js…│Find the callers',
      '    ▱▱▱▱▱▱▱▱ ·········· ?%      0/?│general-pur…│pending│Wait for the build',
    ]],
    ['the block keeps the gauge and the share alone', () => answered(), 80, [
      '1/3 ▰▱▱▱▱▱▱▱    17%│planner│running 12m04s│2 err│⟲1│Map the par… › Readin…',
      '    ▰▰▰▰▰▰▰▱    86%│door·flight…│running 1m01s│idle 3m20s│@door│Build the…',
      '    ▰▱▱▱▱▱▱▱   5%│Explore│done│pfm ✗ open agent-a3.jso…│Find the callers',
      '    ▱▱▱▱▱▱▱▱     ?%│general-pur…│pending│Wait for the build',
    ]],
    ['it is too narrow for its task', () => answered(), 66, [
      '1/3 ▰▱▱▱▱▱▱▱    17%│planner│running 12m04s│2 err│⟲1│Map the…',
      '    ▰▰▰▰▰▰▰▱    86%│door·flight…│running 1m01s│idle 3m20s',
      '    ▰▱▱▱▱▱▱▱   5%│Explore│done│pfm ✗ open agent-a3.jsonl:…',
      '    ▱▱▱▱▱▱▱▱     ?%│general-pur…│pending│Wait for the build',
    ]],
    ['pfm has not answered yet', () => new Door(), 200, [
      ' ▱▱▱▱▱▱▱▱ ········   +4.5K  17% 171.9K/1.0M   │ planner              │ medium │ running 12m04s │ Σ … │ 💾5m✓3m08s … │ Map the parser › Reading lexer.go',
      ' ▱▱▱▱▱▱▱▱ ········    +144  86% 173.0K/200.0K │ door·flights:foreman │ high   │ running 1m01s  │ Σ … │ 💾5m✗1m40s … │ @door │ Build the door › Running go test ./internal/...',
      ' ▱▱▱▱▱▱▱▱ ······  −22.0K   5%   9.0K/200.0K │ Explore              │        │ done           │ Σ … │ 💾– …        │ Find the callers',
      ' ▱▱▱▱▱▱▱▱ ·················· ?%      0/?      │ general-purpose      │        │ pending        │ Σ … │ 💾–          │ Wait for the build',
    ]],
    ['pfm has not answered yet and the terminal is narrow', () => new Door(), 80, [
      ' ▱▱▱▱▱▱▱▱ ········   +4.5K  17% 171.9K/1.0M│planner│running 12m04s',
      ' ▱▱▱▱▱▱▱▱ ········    +144  86% 173.0K/200.0K│door·flight…│running 1m01s',
      ' ▱▱▱▱▱▱▱▱ ······  −22.0K   5%   9.0K/200.0K│Explore│done│Find the calle…',
      ' ▱▱▱▱▱▱▱▱ ·················· ?%      0/?│general-pur…│pending│Wait for th…',
    ]],
    ['pfm is not there', () => failed('not on PATH'), 200, [
      ' ▱▱▱▱▱▱▱▱ ········   +4.5K  17% 171.9K/1.0M   │ planner              │ medium │ running 12m04s │ $0.46 own  │ 💾5m✓3m08s ?% │ pfm ✗ not on PATH │ Map the parser › Reading lexer.go',
      ' ▱▱▱▱▱▱▱▱ ········    +144  86% 173.0K/200.0K │ door·flights:foreman │ high   │ running 1m01s  │ $12.50 own │ 💾5m✗1m40s ?% │ @door │ pfm ✗ │ Build the door › Running go test ./internal/...',
      ' ▱▱▱▱▱▱▱▱ ······  −22.0K   5%   9.0K/200.0K │ Explore              │        │ done           │ $0.02 own  │ 💾– ?%        │ pfm ✗ │ Find the callers',
      ' ▱▱▱▱▱▱▱▱ ·················· ?%      0/?      │ general-purpose      │        │ pending        │ $? own     │ 💾–           │ pfm ✗ │ Wait for the build',
    ]],
    ['pfm fails after it answered', () => failed('list /c/subagents: permission denied', answered()), 252, [
      '1/3 ▰▱▱▱▱▱▱▱ ••••••••   +4.5K  17% 171.9K/1.0M   │ planner              │ ◆ Opus 9·▰▰ medium  │ running 12m04s │ Σ $3.41/2.0M/24.3K/57    │ 💾5m✓3m08s 94%     │ 2 errors │ ⟲1 │ pfm ✗ list /c/subagents: permiss… │ Map the parser › Reading lexer.go',
      '    ▰▰▰▰▰▰▰▱ ●•●●●•••    +144  86% 173.0K/200.0K │ door·flights:foreman │ ◇ Sonnet 9·▰▰▰ high │ running 1m01s  │ Σ $12.50+?/812.0K/9.3K/1 │ 💾5m✗1m40s was 31% │ idle 3m20s │ @door │ adv $0.30 │ pfm ✗ │ Build the door › Running go test ./internal…',
      '    ▰▱▱▱▱▱▱▱ ····•↓  −22.0K   5%   9.0K/200.0K │ Explore              │ ○ Haiku 9           │ done           │ Σ $?                     │ 💾– ?%             │ pfm ✗ │ Find the callers',
      '    ▱▱▱▱▱▱▱▱ ·················· ?%      0/?      │ general-purpose      │                     │ pending        │ $? own                   │ 💾–                │ pfm ✗ │ Wait for the build',
    ]],
  ])('draws every row to the same cells when %s', (_name, pfm, terminal, rows) => {
    const drawn = list(pfm(), terminal - 6);
    expect(drawn).toEqual(rows);
    // Each row within the cells it is given, a nested row's connector taking two of them.
    expect(drawn.map((row, index) => cells(row) <= terminal - 6 - (AGENTS[index]!.parentId === undefined ? 0 : 2))).toEqual(AGENTS.map(() => true));
  });

  it('fits every row in its cells, its gauge, who and state kept, the block in pfm\'s form, at every terminal width from 80', () => {
    const kept = [['1/3 ▰▱▱▱▱▱▱▱', 'planner', 'running 12m04s'], [' ▰▰▰▰▰▰▰▱', 'door·', 'running 1m01s'], [' ▰▱▱▱▱▱▱▱', 'Explore', 'done'], [' ▱▱▱▱▱▱▱▱', 'general-pur', 'pending']];
    for (let terminal = 80; terminal <= 260; terminal += 1) {
      const drawn = list(answered(), terminal - 6);
      const room = AGENTS.map((agent) => terminal - 6 - (agent.parentId === undefined ? 0 : 2));
      const block = terminal >= 120 ? '1/3 ▰▱▱▱▱▱▱▱ ••••••••   +4.5K  17% 171.9K/1.0M' : terminal >= 100 ? '1/3 ▰▱▱▱▱▱▱▱ ·•••••••• 17% 171.9K/1.0M'
        : terminal >= 90 ? '1/3 ▰▱▱▱▱▱▱▱    17% 171.9K/1.0M' : '1/3 ▰▱▱▱▱▱▱▱    17%';
      expect({ terminal, cells: drawn.map((row) => cells(row)) }).toEqual({ terminal, cells: drawn.map((row, index) => Math.min(cells(row), room[index]!)) });
      expect({ terminal, kept: drawn.map((row, index) => kept[index]!.filter((fact) => !row.includes(fact))) }).toEqual({ terminal, kept: AGENTS.map(() => []) });
      expect({ terminal, block: drawn[0]!.split('│')[0]!.trimEnd() }).toEqual({ terminal, block });
    }
  });

  it('draws every row to the same cells when the surface measures no width', () => {
    expect(list(answered(), undefined)).toEqual([
      '1/3 ▰▱▱▱▱▱▱▱ ••••••••   +4.5K  17% 171.9K/1.0M   │ planner              │ ◆ Opus 9·▰▰ medium  │ running 12m04s │ Σ $3.41 · 2.0M in · 24.3K out · 57 tools   │ 💾5m✓3m08s 94%     │ 2 errors │ ⟲1 │ Map the parser › Reading lexer.go',
      '    ▰▰▰▰▰▰▰▱ ●•●●●•••    +144  86% 173.0K/200.0K │ door·flights:foreman │ ◇ Sonnet 9·▰▰▰ high │ running 1m01s  │ Σ $12.50+? · 812.0K in · 9.3K out · 1 tool │ 💾5m✗1m40s was 31% │ idle 3m20s │ @door │ adv $0.30 │ API-eq │ Build the door › Running go test ./internal/...',
      '    ▰▱▱▱▱▱▱▱ ····•↓  −22.0K   5%   9.0K/200.0K │ Explore              │ ○ Haiku 9           │ done           │ Σ $?                                       │ 💾– ?%             │ pfm ✗ open agent-a3.jsonl: permission denied │ Find the callers',
      '    ▱▱▱▱▱▱▱▱ ·················· ?%      0/?      │ general-purpose      │                     │ pending        │ Σ …                                        │ 💾–                │ Wait for the build',
    ]);
  });

  // A row at rest: its newest reading is the one before it. The strip runs over the growth figure's cells, the lead and
  // the share's pad to one blank before the share, which ends on the same cell in every row, the growing foreman's too.
  const rest = (agent: RowAgent): RowAgent => ({ ...agent, tokenSamples: [...agent.tokenSamples, agent.tokenSamples.at(-1) ?? 0] });
  const still = [rest(PLANNER), FOREMAN, rest(CHILD), AGENTS[3]!];
  it.each<[number, string[], number]>([
    [252, [
      '1/3 ▰▱▱▱▱▱▱▱ ·••●••●·••••••••· 17% 171.9K/1.0M',
      '    ▰▰▰▰▰▰▰▱ ●•●●●•••    +144  86% 173.0K/200.0K',
      '    ▰▱▱▱▱▱▱▱ ·············•↓· 5%   9.0K/200.0K',
      '    ▱▱▱▱▱▱▱▱ ·················· ?%      0/?',
    ], 34],
    [100, [
      '1/3 ▰▱▱▱▱▱▱▱ ••••••••· 17% 171.9K/1.0M',
      '    ▰▰▰▰▰▰▰▱ ●●•●●●••• 86% 173.0K/200.0K',
      '    ▰▱▱▱▱▱▱▱ ·····•↓· 5%   9.0K/200.0K',
      '    ▱▱▱▱▱▱▱▱ ·········· ?%      0/?',
    ], 26],
  ])('draws the strip to one blank before the share at rest, at a terminal of %i', (terminal, blocks, share) => {
    const drawn = still.map((agent) => plain(drawRow(scene(still, answered(), terminal - 6), agent.id)).split('│')[0]!.trimEnd());
    expect(drawn).toEqual(blocks);
    const indent = still.map((agent) => (agent.parentId === undefined ? 0 : 2));
    expect(drawn.map((row, index) => indent[index]! + cells(row.slice(0, row.indexOf('%') + 1)))).toEqual(still.map(() => share));
    // The longest run of blanks between a resting row's strip and its share.
    const gap = (row: string) => Math.max(...[...row.slice(14, row.indexOf('%')).matchAll(/ +/g)].map((run) => run[0].length));
    expect([0, 2, 3].map((index) => gap(drawn[index]!))).toEqual([1, 1, 1]);
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
    ['has agents below that pfm could not list', {}, { nested: { total: 0, working: 0, unknown: 0, error: 'list /c/subagents: permission denied' } }, 0, '?/? ▰▱▱▱▱▱▱▱ ••••••   +4.5K  17% 171.9K/1.0M'],
    ['has agents below that pfm could not read', {}, { nested: { total: 3, working: 0, unknown: 2 } }, 0, '0/3+2? ▰▱▱▱▱▱▱▱ ••••••   +4.5K  17% 171.9K/1.0M'],
    ['has an advisor pfm could not price', {}, { spend: { ...SPEND, advisorUnpriced: 1 } }, 8, 'adv n/a'],
    ['works in another directory', { cwd: '/w/other/' }, {}, 8, '@other'],
    ['has a name', { name: 'lexer' }, {}, 1, 'lexer·planner'],
    ['has an effort and no model', { model: undefined, lastRequest: undefined }, { text: { ...ANSWER.agents[0]!.text, glyph: '', label: '' } }, 2, '▰▰ medium'],
    ['has no transcript yet and wears the label pfm gave the engine\'s model', {}, { absent: true, text: { ...ANSWER.agents[0]!.text, glyph: '◆', label: 'Opus 5.5' } }, 2, '◆ Opus 5.5·medium'],
    ['is named as its type', { name: 'planner' }, {}, 1, 'planner'],
    ['has a window only pfm knows', {}, { contextWindow: 200_000 }, 0, '1/3 ▰▰▰▰▰▰▰▱ ••••••   +4.5K  86% 171.9K/200.0K'],
    ['fills its window', { tokenCount: 1_000_000 }, {}, 0, '1/3 ▰▰▰▰▰▰▰▰ ••••••   +4.5K 100%   1.0M/1.0M'],
    ['has one reading', { tokenSamples: [171_928] }, {}, 0, '1/3 ▰▱▱▱▱▱▱▱ ··············· 17% 171.9K/1.0M'],
    ['fills its window at rest', { tokenCount: 1_000_000, tokenSamples: [1_000_000] }, {}, 0, '1/3 ▰▰▰▰▰▰▰▰ ·············· 100%   1.0M/1.0M'],
    ['has no description', { description: '' }, {}, 8, 'Reading lexer.go'],
    ['is doing what its description says', { label: 'Map the parser' }, {}, 8, 'Map the parser'],
    ['reports progress with an escape sequence and a line break in it', { label: `Reading ${ESC}[31mlexer${ESC}[0m\n.go` }, {}, 8, 'Map the parser › Reading lexer .go'],
  ])('that %s', (_name, agent, facts, part, text) => {
    expect(alone(agent, facts, ' │ ')[part]).toBe(text);
  });

  // The planner's three agents below, one of them working, open its row.
  const COUNT = `${GOOD}1${R}${MUTED}/${R}${LABEL}3${R}`;
  const gauge = (tone: string, on: number, share: string, used: string) =>
    `${COUNT} ${tone}${'▰'.repeat(on)}${R}${DIM}${'▱'.repeat(8 - on)}${R} ${MUTED}${'·'.repeat(15)}${R} ${tone}${share}${R} ${TOKENS}${used}${R}${MUTED}/${R}${WINDOW}100${R}`;
  const sum = (dollars: string) => `${MUTED}Σ${R} ${dollars} ${MUTED}·${R} ${TOKENS}2.0M${R} ${MUTED}in${R} ${MUTED}·${R} ${TOKENS}24.3K${R} ${MUTED}out${R} ${MUTED}·${R} ${TOOLS}57${R} ${MUTED}tools${R}`;
  const unread = { tokenSamples: [], contextWindowSize: 100 };

  it.each<[string, Partial<RowAgent>, Record<string, unknown>, number, string]>([
    ['a window under half full good', { ...unread, tokenCount: 49 }, {}, 0, gauge(GOOD, 4, '49%', '    49')],
    ['a window half full a warning', { ...unread, tokenCount: 50 }, {}, 0, gauge(WARN, 4, '50%', '    50')],
    ['a window under four fifths full a warning', { ...unread, tokenCount: 79 }, {}, 0, gauge(WARN, 6, '79%', '    79')],
    ['a window four fifths full bad', { ...unread, tokenCount: 80 }, {}, 0, gauge(BAD, 6, '80%', '    80')],
    ['a stated count over the transcript\'s', { ...unread, tokenCount: 49 }, { context: 60 }, 0, gauge(GOOD, 4, '49%', '    49')],
    ['the transcript\'s count where Claude Code states zero', { ...unread, tokenCount: 0 }, { context: 60 }, 0, gauge(WARN, 5, '60%', '    60')],
    ['a growing step and a drop in the growth figure\'s colour, a quiet or unborn step muted', { tokenSamples: [100, 100, 6_100, 5_000], tokenCount: 5_000, contextWindowSize: 100_000 }, {}, 0,
      `${COUNT} ${GOOD}▰${R}${DIM}▱▱▱▱▱▱▱${R} ${MUTED}····${R}${WINDOW}●↓${R} ${WINDOW}  −1.1K${R} ${GOOD}  5%${R} ${TOKENS}  5.0K${R}${MUTED}/${R}${WINDOW}100.0K${R}`],
    ['a count of the agents below with none working muted, one it could not read a warning', {}, { nested: { total: 3, working: 0, unknown: 2 } }, 0,
      `${MUTED}0${R}${MUTED}/${R}${LABEL}3${R}${WARN}+2?${R} ${GOOD}▰${R}${DIM}▱▱▱▱▱▱▱${R} ${WINDOW}••••••${R} ${WINDOW}  +4.5K${R} ${GOOD} 17%${R} ${TOKENS}171.9K${R}${MUTED}/${R}${WINDOW}1.0M${R}`],
    ['agents below pfm could not list a warning', {}, { nested: { total: 0, working: 0, unknown: 0, error: 'list /c/subagents: permission denied' } }, 0,
      `${WARN}?/?${R} ${GOOD}▰${R}${DIM}▱▱▱▱▱▱▱${R} ${WINDOW}••••••${R} ${WINDOW}  +4.5K${R} ${GOOD} 17%${R} ${TOKENS}171.9K${R}${MUTED}/${R}${WINDOW}1.0M${R}`],
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

  it('paints in pfm\'s colours, the gauge\'s used cells in the share\'s tone, the rest dimmed', () => {
    expect(drawRow(scene([FOREMAN], answered(), 131), 'a2')).toBe([
      ` ${BAD}▰▰▰▰▰▰▰${R}${DIM}▱${R} ${WINDOW}●●●•••${R} ${WINDOW}   +144${R} ${BAD} 86%${R} ${TOKENS}173.0K${R}${MUTED}/${R}${WINDOW}200.0K${R}`,
      `${TOKENS}door${R}${MUTED}·${R}${LABEL}flight…${R}`,
      `${MODEL}◇${R}${MUTED}·${R}${LOOK.effortColors.high}▰▰▰${R}`,
      `${GOOD}running${R} ${ELAPSED}1m01s${R}`,
      `${MUTED}Σ${R} ${BAD}$12.50${R}${WARN}+?${R}`,
      `${BAD}💾✗1m40s${R}`,
      `${WARN}idle 3m20s${R}`,
      `${LABEL}Build the d…${R} ${MUTED}› Running…${R}`,
    ].join(`${DIM}│${R}`));
  });

  it('paints by weight alone, the engine\'s own dollars named as such, when pfm never answered', () => {
    const bar = ` ${ESC}[2m│${R} `;
    expect(drawRow(scene([{ ...PLANNER, cost: { usd: 0.46, hasUnknownPrice: true } }], failed('not on PATH'), undefined), 'a1')).toBe([
      ` ${ESC}[2m▱▱▱▱▱▱▱▱${R} ${ESC}[2m······${R}   +4.5K  17% ${ESC}[1m171.9K${R}${ESC}[2m/${R}1.0M`,
      `${ESC}[1mplanner${R}`,
      'medium',
      'running 12m04s',
      `$0.46? ${ESC}[2mown${R}`,
      `💾5m✓3m08s ${ESC}[2m?%${R}`,
      `${ESC}[31mpfm ✗ not on PATH${R}`,
      `Map the parser ${ESC}[2m› Reading lexer.go${R}`,
    ].join(bar));
  });

  it.each<[string, number, string]>([
    ['gives the cause its least and the task its least where both just fit', 59, ' ▰▱▱▱▱▱▱▱   5%│Explore│done│pfm ✗ open agent-a3.j…│Find th…'],
    ['gives the cause all that is left where the task\'s least no longer fits', 58, ' ▰▱▱▱▱▱▱▱   5%│Explore│done│pfm ✗ open agent-a3.jsonl: pe…'],
  ])('%s', (_name, columns, row) => {
    expect(plain(drawRow(scene([{ ...CHILD, parentId: undefined }], answered(), columns), 'a3'))).toBe(row);
  });

  it('keeps a nested row inside the two cells its connector takes', () => {
    const pair = [FOREMAN, { ...PLANNER, parentId: 'a2' }];
    expect(pair.map((agent) => plain(drawRow(scene(pair, answered(), 62), agent.id)))).toEqual([
      '    ▰▰▰▰▰▰▰▱    86%│door·flight…│running 1m01s│idle 3m20s',
      '1/3 ▰▱▱▱▱▱▱▱  17%│planner│running 12m04s│2 err│⟲1│Map the p…',
    ]);
  });

  it.each<[string, Record<string, unknown>, string]>([
    ['could not list the agents below', { nested: { total: 0, working: 0, unknown: 0, error: 'list /c/subagents: permission denied' } }, '?/? ▰▱▱▱▱▱▱▱ ••••••   +4.5K  17% 171.9K/1.0M │ planner │ ◆·▰▰ │ running 12m04s │ Σ $3.41/2.0M/24.3K/57 │ 💾5m✓3m08s 94% │ 2 err │ ⟲1 │ Map the pars… › Reading le…'],
    ['could not read two of the agents below', { nested: { total: 3, working: 0, unknown: 2 } }, '0/3+2? ▰▱▱▱▱▱▱▱ ••••••   +4.5K  17% 171.9K/1.0M │ planner │ ◆·▰▰ │ running 12m04s │ Σ $3.41/2.0M/24.3K/57 │ 💾5m✓3m08s 94% │ 2 err │ ⟲1 │ Map the par… › Reading…'],
    ['counted one tool error', { toolErrors: 1 }, '1/3 ▰▱▱▱▱▱▱▱ ••••••   +4.5K  17% 171.9K/1.0M │ planner │ ◆·▰▰ │ running 12m04s │ Σ $3.41/2.0M/24.3K/57 │ 💾5m✓3m08s 94% │ 1 err │ ⟲1 │ Map the pars… › Reading le…'],
  ])('says in a narrow list, without the flags\' words, that pfm %s', (_name, facts, row) => {
    expect(plain(drawRow(scene([PLANNER], answered(facts), 162), 'a1'))).toBe(row);
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

  it('draws the strip on pfm\'s scale, in its glyphs, its widths and with its drop mark', () => {
    const door = new Door();
    const look = { ...LOOK, gauge: { on: '=', off: '-', cells: 4 }, pulse: { glyphs: ['.', ':', '|'], drop: 'v', steps: [500, 5_000], dropFrom: 20_000, stepMs: 5_000, cells: 3 } };
    door.land(readDoorAnswer({ exitCode: 0, stdout: JSON.stringify({ ...ANSWER, look }), stderr: '' }));
    expect(plain(drawRow(scene([{ ...CHILD, parentId: undefined }], door, undefined), 'a3')).split(' │ ')[0]).toBe(' =--- .:v  −22.0K   5%   9.0K/200.0K');
  });

  it('shows the engine\'s own dollars for an ended agent pfm never named', () => {
    expect(plain(drawRow(scene([{ ...CHILD, id: 'a9', parentId: undefined }], answered(), undefined), 'a9'))).toBe(
      ' ▰▱▱▱▱▱▱▱ ····•↓  −22.0K   5%   9.0K/200.0K │ Explore │ done │ $0.02 own │ 💾– ?% │ Find the callers',
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
