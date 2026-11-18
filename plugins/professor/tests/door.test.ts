import { describe, expect, it } from 'vitest';
import { Door, DOOR_EVERY_MS, doorArgv, readDoorAnswer } from '../plugins/professor/src/door.ts';
import { ANSWER, LOOK, NOW, TRANSCRIPT } from './scene.ts';

const ESC = String.fromCharCode(0x1b);
const BELL = String.fromCharCode(0x07);
const ok = (answer: unknown) => ({ exitCode: 0, stdout: `${JSON.stringify(answer)}\n`, stderr: '' });
/** ANSWER with one value replaced, by the path of keys to it. */
function withValue(path: Array<string | number>, value: unknown): unknown {
  const copy = JSON.parse(JSON.stringify(ANSWER));
  let at = copy;
  for (const key of path.slice(0, -1)) at = at[key];
  at[path[path.length - 1]!] = value;
  return copy;
}

describe('door command', () => {
  it('asks pfm once for every agent the list names, in its order', () => {
    expect(doorArgv(TRANSCRIPT, ['a2', 'a1'])).toEqual(['pfm', 'statusline', '--agents', TRANSCRIPT, 'a2', 'a1']);
  });
});

describe('door answer', () => {
  it('reads every agent, a missing spend, an agent error, an absent agent and the look', () => {
    const answer = readDoorAnswer(ok(ANSWER));
    expect(answer.at).toBe(NOW);
    expect(answer.look).toEqual(LOOK);
    expect(answer.agents.map((agent) => agent.id)).toEqual(['a1', 'a2', 'a3', 'a4']);
    expect([answer.agents[2]!.absent, answer.agents[3]!.absent, answer.agents[3]!.error]).toEqual([undefined, true, 'open agent-a4.jsonl: no such file or directory']);
    expect(answer.agents[0]).toEqual({
      id: 'a1', model: 'claude-opus-9', effort: 'medium', tools: 40, toolErrors: 2, compactions: 1, cacheHit: 94, cacheLifetime: true, context: 170_000, contextWindow: 0,
      lastActivity: NOW - 4_000, nested: { total: 3, working: 1, unknown: 0 },
      spend: { usd: 3.41, usdKnown: true, in: 2_000_000, out: 24_300, tools: 57, advisorUsd: 0, advisorUnpriced: 0, floor: false, ledger: true, apiEquivalent: false },
      text: { glyph: '◆', family: 'opus', effortBar: '▰▰', context: '170.0K', in: '2.0M', out: '24.3K', usd: '$3.41' },
    });
    expect(answer.agents[2]!.spend).toBe(null);
    expect(answer.agents[2]!.error).toBe('open agent-a3.jsonl: permission denied');
  });

  it('takes escape sequences out of the words pfm sends', () => {
    const answer = readDoorAnswer(ok(withValue(['agents', 0, 'text', 'family'], `op${ESC}]0;title${BELL}us${ESC}[31m`)));
    expect(answer.agents[0]!.text.family).toBe('opus');
  });

  it.each<[string, { exitCode: number; stdout: string; stderr: string }, string]>([
    ['pfm says why', { exitCode: 1, stdout: '', stderr: '\npfm statusline --agents: list /c/subagents: permission denied\nmore\n' }, 'list /c/subagents: permission denied'],
    ['pfm warns before it says why', { exitCode: 1, stdout: '', stderr: 'statusline: agents: price table (dollars show $?): offline\npfm statusline --agents: list /c/subagents: permission denied\n' }, 'list /c/subagents: permission denied'],
    ['pfm says no cause line', { exitCode: 2, stdout: '', stderr: 'usage: pfm statusline\n' }, 'usage: pfm statusline'],
    ['pfm fails silent', { exitCode: 2, stdout: '', stderr: '' }, 'pfm exited 2 without a word'],
    ['pfm prints nothing', { exitCode: 0, stdout: '\n', stderr: '' }, 'pfm answered nothing'],
    ['pfm prints no JSON', { exitCode: 0, stdout: 'usage: pfm statusline', stderr: '' }, 'pfm answered no JSON'],
    ['the answer is a list', ok([]), 'answer is not an object'],
    ['agents is no list', ok(withValue(['agents'], {})), 'answer.agents is not a list'],
    ['a count is text', ok(withValue(['agents', 0, 'cacheHit'], '94')), 'answer.agents[0].cacheHit is not a number'],
    ['an id is empty', ok(withValue(['agents', 1, 'id'], '')), 'answer.agents[1].id is empty'],
    ['a flag is missing', ok(withValue(['agents', 1, 'spend', 'usdKnown'], undefined)), 'answer.agents[1].spend.usdKnown is not true or false'],
    ['a word is a number', ok(withValue(['agents', 0, 'text', 'glyph'], 4)), 'answer.agents[0].text.glyph is not text'],
    ['nested is missing', ok(withValue(['agents', 2, 'nested'], undefined)), 'answer.agents[2].nested is not an object'],
    ['a colour is no colour sequence', ok(withValue(['look', 'palette', 'good'], `${ESC}]0;title${BELL}`)), 'answer.look.palette.good is not a colour sequence'],
    ['the look is missing', ok(withValue(['look'], undefined)), 'answer.look is not an object'],
    ['absent is a word', ok(withValue(['agents', 3, 'absent'], 'yes')), 'answer.agents[3].absent is not true or false'],
    ['the pulse is missing', ok(withValue(['look', 'pulse'], undefined)), 'answer.look.pulse is not an object'],
    ['a step is text', ok(withValue(['look', 'pulse', 'steps', 1], '250')), 'answer.look.pulse.steps[1] is not a number'],
    ['the glyphs are none', ok(withValue(['look', 'pulse', 'glyphs'], [])), 'answer.look.pulse.glyphs is not a list of words'],
    ['a glyph is a number', ok(withValue(['look', 'pulse', 'glyphs', 2], 3)), 'answer.look.pulse.glyphs[2] is not text'],
    ['the drop threshold is missing', ok(withValue(['look', 'pulse', 'dropFrom'], undefined)), 'answer.look.pulse.dropFrom is not a number'],
    ['the tones are missing', ok(withValue(['look', 'tones'], undefined)), 'answer.look.tones is not an object'],
    ['a tone is text', ok(withValue(['look', 'tones', 'share', 'bad'], '80')), 'answer.look.tones.share.bad is not a number'],
    ['the cache-hit tone is missing', ok(withValue(['look', 'tones', 'cacheHit'], undefined)), 'answer.look.tones.cacheHit is not an object'],
  ])('refuses an answer when %s', (_name, result, cause) => {
    expect(() => readDoorAnswer(result)).toThrow(new Error(cause));
  });
});

describe('door state', () => {
  it('asks once at a time and no sooner than DOOR_EVERY_MS after the last call began', () => {
    const door = new Door();
    expect(door.state).toEqual({ state: 'waiting' });
    expect(door.begin(NOW)).toBe(true);
    expect(door.begin(NOW + DOOR_EVERY_MS)).toBe(false);
    door.land(readDoorAnswer(ok(ANSWER)));
    expect(door.state).toEqual({ state: 'ok' });
    expect(door.begin(NOW + DOOR_EVERY_MS - 1)).toBe(false);
    expect(door.begin(NOW + DOOR_EVERY_MS)).toBe(true);
  });

  it('keeps the look and each agent\'s last facts through a failed call and an answer that no longer names it', () => {
    const door = new Door();
    door.land(readDoorAnswer(ok(ANSWER)));
    // An absent agent gives no facts: none before, and the ones before stand.
    expect([[...door.named], door.facts('a4'), door.read('a4')]).toEqual([['a1', 'a2', 'a3'], undefined, undefined]);
    door.land(readDoorAnswer(ok({ ...ANSWER, at: NOW + 4_000, agents: [ANSWER.agents[1], { ...ANSWER.agents[3], id: 'a1' }] })));
    expect([...door.named]).toEqual(['a2']);
    expect(door.facts('a1')!.toolErrors).toBe(2);
    expect([door.read('a1'), door.read('a2'), door.read('a4')]).toEqual([NOW, NOW + 4_000, undefined]);
    door.fail('not on PATH');
    expect(door.state).toEqual({ state: 'failed', cause: 'not on PATH' });
    expect(door.look).toEqual(LOOK);
    expect(door.facts('a2')!.cacheHit).toBe(31);
    door.keep(new Set(['a2']));
    expect([door.facts('a1'), door.read('a1')]).toEqual([undefined, undefined]);
    expect(door.facts('a2')!.id).toBe('a2');
  });

  it('asks again at once after a call that ended with no answer and no failure', () => {
    const door = new Door();
    expect(door.begin(NOW)).toBe(true);
    door.drop();
    expect(door.state).toEqual({ state: 'waiting' });
    expect(door.begin(NOW + 1)).toBe(true);
  });
});
