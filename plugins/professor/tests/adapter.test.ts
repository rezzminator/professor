import { describe, expect, it } from 'vitest';
import { register } from '../plugins/professor/hooks/professor.ts';
import type { RowAgent } from '../plugins/professor/src/row.ts';
import { plain } from '../plugins/professor/src/text.ts';
import { AGENTS, ANSWER, CHILD, NOW, PLANNER, SESSION_CWD, TRANSCRIPT, UNSTARTED } from './scene.ts';

// A stand-in engine: its clock, timers and waits held by the test, pfm answered by the test, and the hooks the
// module registers driven the way Claude Code drives them: every row of a list asked at once.
type Hook = (...args: any[]) => any;
type Run = { exitCode: number; stdout: string; stderr: string };

const ESC = String.fromCharCode(0x1b);
/** The door command for a list of `ids`. */
const door = (...ids: string[]) => ['pfm', 'statusline', '--agents', TRANSCRIPT, ...ids];
const LOGS = '/u/.local/state/professor/logs';
const IDS = AGENTS.map((agent) => agent.id);
const DOOR = door(...IDS);
const ok = (answer: unknown): Run => ({ exitCode: 0, stdout: `${JSON.stringify(answer)}\n`, stderr: '' });

// The planner's row alone, unbounded: before pfm answers, with its facts, and with the cause of their absence.
const UNANSWERED = ' ▱▱▱▱▱▱▱▱ ······   +4.5K  17% 171.9K/1.0M │ planner │ claude-opus-9·medium │ running 12m04s │ Σ … │ 💾5m✓3m08s … │ Map the parser › Reading lexer.go';
const ANSWERED = '1/3 ▰▱▱▱▱▱▱▱ ••••••   +4.5K  17% 171.9K/1.0M │ planner │ ◆ opus·▰▰ medium │ running 12m04s │ Σ $3.41 · 2.0M in · 24.3K out · 57 tools │ 💾5m✓3m08s 94% │ 2 errors │ ⟲1 │ Map the parser › Reading lexer.go';
const refused = (cause: string) => ` ▱▱▱▱▱▱▱▱ ······   +4.5K  17% 171.9K/1.0M │ planner │ claude-opus-9·medium │ running 12m04s │ $0.46 own │ 💾5m✓3m08s ?% │ pfm ✗ ${cause} │ Map the parser › Reading lexer.go`;

async function until(what: string, met: () => boolean): Promise<void> {
  for (let turn = 0; turn < 200; turn += 1) {
    if (met()) return;
    await new Promise((resolve) => setImmediate(resolve));
  }
  throw new Error(`never happened: ${what}`);
}

function engine(pfm: (argv: string[]) => Promise<Run> | Run = () => ok(ANSWER)) {
  const hooks = new Map<string, Hook>();
  const catches = new Map<string, Hook>();
  const filters: Record<string, unknown> = {};
  const screen: string[] = [];
  const doorRuns: string[][] = [];
  const logRuns: string[][] = [];
  const redraws: string[] = [];
  const sleeps: Array<{ ms: number; signal: AbortSignal | undefined; wake: () => void }> = [];
  const timers: Array<{ ms: number; fn: () => void; cancelled: boolean }> = [];
  const clock = { now: NOW };
  const $ = {
    clock: {
      now: async () => clock.now,
      sleep: (ms: number, options?: { signal?: AbortSignal }) => new Promise<void>((wake) => { sleeps.push({ ms, signal: options?.signal, wake }); }),
      every: (ms: number, fn: () => void) => {
        const timer = { ms, fn, cancelled: false };
        timers.push(timer);
        return { cancel: () => { timer.cancelled = true; } };
      },
    },
    env: { get: async (name: string) => ({ HOME: '/u' } as Record<string, string>)[name] },
    session: { id: async () => 'sess-1', cwd: async () => SESSION_CWD, info: async () => ({ transcriptPath: TRANSCRIPT }) },
    process: {
      run: async (argv: string[]) => {
        if (argv[0] === 'pfm') { doorRuns.push(argv); return pfm(argv); }
        logRuns.push(argv);
        return { exitCode: 0, stdout: '', stderr: '' };
      },
    },
    // Claude Code puts the plugin's name before each line.
    ui: { log: (text: string) => { screen.push(`professor: ${text}`); }, invalidate: (event: string) => { redraws.push(event); } },
  };
  const on = (name: string, filterOrHook: unknown, maybeHook?: Hook) => {
    hooks.set(name, (maybeHook ?? filterOrHook) as Hook);
    if (maybeHook) filters[name] = filterOrHook;
    return { catch: (handler: Hook) => { catches.set(name, handler); } };
  };
  register(on as never, {} as never);
  const event = (agent: RowAgent, rowIds: string[], columns: number | undefined) => ({ component: 'AgentRow', props: { ...agent, rowIds, columns, isHidden: false } });
  const pass = () => Object.assign(async (e: { props: { body?: string } }) => e, { signal: new AbortController().signal });
  /** The body the hook answers for `agent`'s row. */
  const draw = async (agent: RowAgent, rowIds: string[], columns: number | undefined): Promise<string> => (await hooks.get('ui.render')!($, event(agent, rowIds, columns), pass())).props.body;
  return {
    clock, filters, screen, doorRuns, redraws, sleeps, timers, draw,
    /** Every row of the four-agent list, asked at once, as the terminal shows them. */
    drawList: async (columns: number) => (await Promise.all(AGENTS.map((agent) => draw(agent, IDS, columns)))).map(plain),
    /** The body the hook's catch answers after the hook threw `error`. */
    caught: async (agent: RowAgent, error: { kind: string; message?: string }): Promise<string> =>
      (await catches.get('ui.render')!($, event(agent, [agent.id], undefined), Object.assign(pass(), { error: { ...error, budget: 100 } }))).props.body,
    end: async () => { await hooks.get('session.end')!($, {}, async () => ({})); },
    /** Settles the log the way session end does, then every row written, without its time. */
    rows: async () => {
      await hooks.get('session.end')!($, {}, async () => ({}));
      return logRuns.flatMap((argv) => {
        const out: Array<{ file: string; dir: string; row: Record<string, unknown> }> = [];
        for (let at = 6; at < argv.length; at += 2) {
          const { ts: _ts, ...row } = JSON.parse(argv[at + 1]!);
          out.push({ dir: argv[4]!, file: argv[at]!, row });
        }
        return out;
      });
    },
  };
}

describe('the agent list', () => {
  it('asks pfm once for every row of a list and draws each from its answer, to the same cells', async () => {
    const eng = engine();
    expect(eng.filters).toEqual({ 'ui.render': { component: 'AgentRow' } });
    expect(await eng.drawList(80)).toEqual([
      '1/3 ▰▱▱▱▱▱▱▱    17%│planner│running 12m04s│2 err│⟲1│Map the parser › Reading le…',
      '    ▰▰▰▰▰▰▰▱    86%│door·flight…│running 1m01s│idle 3m20s│Build the d… › Runnin…',
      '    ▰▱▱▱▱▱▱▱   5%│Explore│done│pfm ✗ open agent-a3.jsonl: pe…│Find the callers',
      '    ▱▱▱▱▱▱▱▱     ?%│general-pur…│pending│Wait for the build',
    ]);
    expect(eng.doorRuns).toEqual([DOOR]);
    expect(eng.redraws).toEqual(['ui.render']);
  });

  it('asks pfm again once four seconds have passed, and redraws the rows with its answer', async () => {
    const eng = engine();
    await eng.drawList(80);
    eng.clock.now = NOW + 3_999;
    await eng.drawList(80);
    expect(eng.doorRuns).toEqual([DOOR]);
    eng.clock.now = NOW + 4_000;
    await eng.drawList(80);
    await until('the second answer redraws the rows', () => eng.redraws.length === 2);
    expect(eng.doorRuns).toEqual([DOOR, DOOR]);
  });

  it('draws a first row without pfm\'s facts when its answer takes more than a moment, then redraws it with them', async () => {
    let answer!: (run: Run) => void;
    const eng = engine(() => new Promise<Run>((resolve) => { answer = resolve; }));
    const drawing = eng.draw(PLANNER, ['a1'], undefined);
    await until('the row waits for the first answer', () => eng.sleeps.length === 1);
    expect(eng.sleeps[0]!.ms).toBe(250);
    eng.sleeps[0]!.wake();
    expect(plain(await drawing)).toBe(UNANSWERED);
    answer(ok(ANSWER));
    await until('the answer redraws the rows', () => eng.redraws.length === 1);
    expect(eng.redraws).toEqual(['ui.render']);
    expect(plain(await eng.draw(PLANNER, ['a1'], undefined))).toBe(ANSWERED);
  });

  it.each<[string, () => Run, string]>([
    ['pfm is not installed', () => { throw new Error('professor: $.process.run(pfm) failed: ENOENT'); }, 'not on PATH'],
    ['the engine refuses the command', () => { throw new Error('professor: $.process.run(pfm) refused by policy'); }, '$.process.run(pfm) refused by policy'],
    ['pfm exits with its cause', () => ({ exitCode: 1, stdout: '', stderr: 'pfm statusline --agents: list /c/subagents: permission denied\n' }), 'list /c/subagents: permission denied'],
    ['pfm answers another shape', () => ok({ ...ANSWER, agents: {} }), 'answer.agents is not a list'],
  ])('names on the row why it has no pfm facts when %s', async (_name, pfm, cause) => {
    const eng = engine(pfm);
    expect(plain(await eng.draw(PLANNER, ['a1'], undefined))).toBe(refused(cause));
  });

  it('logs a pfm failure once while it lasts, and its return', async () => {
    let installed = false;
    const eng = engine(() => {
      if (!installed) throw new Error('professor: $.process.run(pfm) failed: ENOENT');
      return ok(ANSWER);
    });
    await eng.draw(PLANNER, ['a1'], undefined);
    eng.clock.now = NOW + 4_000;
    await eng.draw(PLANNER, ['a1'], undefined);
    await until('the second failure redraws the rows', () => eng.redraws.length === 2);
    installed = true;
    eng.clock.now = NOW + 8_000;
    await eng.draw(PLANNER, ['a1'], undefined);
    await until('the answer redraws the rows', () => eng.redraws.length === 3);
    expect(eng.doorRuns).toEqual([door('a1'), door('a1'), door('a1')]);
    expect(await eng.rows()).toEqual([
      { dir: LOGS, file: 'warnings.jsonl', row: { plugin: 'professor', level: 'warning', event: 'door-failed', msg: 'professor: pfm\'s agent facts failed: not on PATH; the rows show the engine\'s own fields and the cause', where: 'hooks/professor.ts#ask', session: 'sess-1', detail: { error: 'not on PATH' } } },
      { dir: LOGS, file: 'info.jsonl', row: { plugin: 'professor', level: 'info', event: 'door-answers', msg: 'professor: pfm answers again; the rows carry its facts', where: 'hooks/professor.ts#ask', session: 'sess-1' } },
    ]);
    expect(eng.screen).toEqual([]);
  });

  it('asks again at once after a call the engine abandoned, which is no failure', async () => {
    let calls = 0;
    const eng = engine(() => {
      calls += 1;
      if (calls === 1) throw new Error('professor: $.process.run(pfm) aborted');
      return ok(ANSWER);
    });
    expect(plain(await eng.draw(PLANNER, ['a1'], undefined))).toBe(UNANSWERED);
    expect(plain(await eng.draw(PLANNER, ['a1'], undefined))).toBe(ANSWERED);
    expect(eng.doorRuns).toEqual([door('a1'), door('a1')]);
    expect(await eng.rows()).toEqual([]);
  });

  it('lays a row out with the agents the list still holds', async () => {
    const eng = engine();
    await eng.drawList(80);
    expect(plain(await eng.draw(UNSTARTED, ['a4'], 80))).toBe(' ▱▱▱▱▱▱▱▱   ?% │ general-purpose │ pending │ Σ … │ 💾– │ Wait for the build');
  });

  it('asks pfm for the agent whose row is drawn though the list does not name it yet', async () => {
    const eng = engine();
    await eng.draw(PLANNER, ['a2'], undefined);
    expect(eng.doorRuns).toEqual([door('a2', 'a1')]);
  });

  it('redraws the rows each second while an agent runs, and stops once no row is asked for', async () => {
    const eng = engine();
    await eng.drawList(80);
    expect(eng.timers.map((timer) => timer.ms)).toEqual([1_000]);
    eng.timers[0]!.fn();
    expect(eng.redraws).toEqual(['ui.render', 'ui.render']);
    // Only an ended agent is left: its row has no clock to count.
    await eng.draw({ ...CHILD, parentId: undefined }, ['a3'], 80);
    for (let ticks = 0; ticks < 15; ticks += 1) eng.timers[0]!.fn();
    expect(eng.redraws).toEqual(['ui.render', 'ui.render']);
    expect(eng.timers[0]!.cancelled).toBe(false);
    eng.timers[0]!.fn();
    expect(eng.timers[0]!.cancelled).toBe(true);
    await eng.draw(PLANNER, ['a1'], 80);
    expect(eng.timers.map((timer) => [timer.ms, timer.cancelled])).toEqual([[1_000, true], [1_000, false]]);
    await eng.end();
    expect(eng.timers[1]!.cancelled).toBe(true);
  });
});

describe('a row whose drawing threw', () => {
  it('draws the cause before the engine\'s own words, and logs it', async () => {
    const eng = engine();
    expect(await eng.caught(PLANNER, { kind: 'throw', message: 'boom' })).toBe(`${ESC}[31mprofessor ✗ throw: boom${ESC}[0m ${ESC}[2m│${ESC}[0m Reading lexer.go`);
    expect(await eng.caught(PLANNER, { kind: 'timeout' })).toBe(`${ESC}[31mprofessor ✗ timeout: the engine gave no cause${ESC}[0m ${ESC}[2m│${ESC}[0m Reading lexer.go`);
    expect(await eng.rows()).toEqual([
      { dir: LOGS, file: 'errors.jsonl', row: { plugin: 'professor', level: 'error', event: 'hook-threw', msg: 'professor: drawing the row of agent a1 failed (throw: boom); the row shows the cause', where: 'hooks/professor.ts#ui.render', session: 'sess-1', detail: { kind: 'throw', error: 'boom', agentId: 'a1' } } },
      { dir: LOGS, file: 'errors.jsonl', row: { plugin: 'professor', level: 'error', event: 'hook-threw', msg: 'professor: drawing the row of agent a1 failed (timeout: the engine gave no cause); the row shows the cause', where: 'hooks/professor.ts#ui.render', session: 'sess-1', detail: { kind: 'timeout', agentId: 'a1' } } },
    ]);
    expect(eng.screen).toEqual([
      'professor: drawing the row of agent a1 failed (throw: boom); the row shows the cause',
      'professor: drawing the row of agent a1 failed (timeout: the engine gave no cause); the row shows the cause',
    ]);
  });
});
