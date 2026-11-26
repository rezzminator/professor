import type { EngineInterface, Register } from 'claude-code';
import { Door, DOOR_TIMEOUT_MS, doorArgv, readDoorAnswer } from '../src/door';
import { levelRow, PLUGIN_LOG_HOLD_ROWS, pluginLogDir, PluginLogQueue, runAborted, type PluginLogLevel } from '../src/plugin-log';
import { drawRow, failureRow, layout, type Layout, type RowAgent } from '../src/row';
import { clean } from '../src/text';

// Thin adapter: every decision lives in src/. One hook draws the body of each
// sub-agent's row. Drawing never waits on pfm: a row is drawn from the props
// the engine hands and from what pfm's door last answered, one door call per
// DOOR_EVERY_MS for every row of the list, running or not, whose answer
// redraws the rows. Only the
// list's first drawing waits, FIRST_ANSWER_MS at most, so it lands whole.
// While an agent runs the rows are redrawn each second, so their clocks count.
// A failure is logged with its context and drawn on the row, never an empty row.

const PLUGIN = 'professor';
/** How long the first drawing of a list waits for pfm's first answer, inside the 300 ms the engine holds a new row. */
const FIRST_ANSWER_MS = 250;
const TICK_MS = 1_000;
/** Ticks with no row asked for after which the redraw timer stops: the list is gone. */
const TICK_QUIET = 15;

/** `held`: rows built while no queue could be made, written by the first that can, oldest first. */
type LogState = { work: Promise<void>; queue?: PluginLogQueue; session?: Promise<string | undefined>; held: string[]; failing: boolean };
type State = {
  /** Every agent that has a row, as its last props gave it. */
  rows: Map<string, RowAgent>;
  door: Door;
  /** The door call in flight. */
  asking?: Promise<void>;
  sessionCwd?: string;
  tick?: { cancel: () => void };
  quietTicks: number;
  /** The layout of the rows asked for in one burst, so every row of it is drawn to the same columns. */
  frame?: { key: string; layout: Layout };
  /** Problems already logged, one row each per module life. */
  reported: Set<string>;
  log: LogState;
};

function message(error: unknown): string {
  return typeof error === 'object' && error !== null && 'message' in error && typeof error.message === 'string' ? error.message : String(error);
}

// Claude Code puts the plugin's name before each line itself.
function show($: EngineInterface, text: string): void {
  try { $.ui.log(text.startsWith(`${PLUGIN}: `) ? text.slice(PLUGIN.length + 2) : text); } catch {
    // No other screen door exists when ui.log itself is unavailable.
  }
}

// Error rows show on screen too; every row goes to the plugin log, written in order behind the ones before it.
function plog($: EngineInterface, state: State, level: PluginLogLevel, event: string, msg: string, hook: string, detail?: Record<string, unknown>): void {
  if (level === 'error') show($, msg);
  const at = Date.now();
  const log = state.log;
  log.work = log.work.then(async () => {
    log.session ??= $.session.id().catch(() => undefined);
    log.held.push(levelRow({ plugin: PLUGIN, level, event, msg, where: `hooks/professor.ts#${hook}`, session: await log.session, detail }, at));
    // shortcut: past the hold the oldest held row goes uncounted; count it if a queue ever stays unmakeable for long.
    if (log.held.length > PLUGIN_LOG_HOLD_ROWS) log.held.shift();
    log.queue ??= new PluginLogQueue(pluginLogDir(await $.env.get('PLUGIN_LOG_DIR'), await $.env.get('HOME'), PLUGIN), PLUGIN);
    for (const row of log.held.splice(0)) log.queue.add(row);
    const result = await log.queue.flush((argv) => $.process.run(argv, { timeoutMs: 5000 }));
    if (result.screen !== undefined) show($, result.screen);
    log.failing = false;
  }).catch((error) => {
    const held = log.held.length;
    if (!log.failing) show($, held > 0 ? `plugin log unavailable: ${message(error)}; ${held} ${held === 1 ? 'row is' : 'rows are'} held for the next write` : `plugin log unavailable: ${message(error)}; the ${event} row is lost`);
    log.failing = true;
  });
}

// One row per distinct problem per module life: a door that stays shut would repeat it every call.
function report($: EngineInterface, state: State, level: PluginLogLevel, event: string, msg: string, hook: string, detail?: Record<string, unknown>): void {
  const key = `${event}\n${msg}`;
  if (state.reported.has(key)) return;
  state.reported.add(key);
  plog($, state, level, event, msg, hook, detail);
}

/** Why a door call failed, in the words a row shows. */
function doorCause(error: unknown): string {
  const said = message(error);
  // Claude Code words a command that cannot start `<plugin>: $.process.run(pfm) failed: ENOENT`, with no code.
  if (/\bENOENT\b/.test(said)) return 'not on PATH';
  return said.startsWith(`${PLUGIN}: `) ? said.slice(PLUGIN.length + 2) : said;
}

// Starts the one door call for the whole list when one is due, for every agent of `ids`, running or not, each with the
// model its last props name; its answer, or its failure, redraws every row.
function ask($: EngineInterface, state: State, now: number, ids: readonly string[]): void {
  if (!state.door.begin(now)) return;
  state.asking = (async () => {
    try {
      const info = await $.session.info();
      state.sessionCwd = await $.session.cwd();
      const failed = state.door.state.state === 'failed';
      const asks = ids.map((id) => {
        const agent = state.rows.get(id);
        return { id, model: clean(agent?.lastRequest?.model ?? agent?.model ?? '') };
      });
      state.door.land(readDoorAnswer(await $.process.run(doorArgv(info.transcriptPath, asks), { timeoutMs: DOOR_TIMEOUT_MS })));
      if (failed) { state.reported.clear(); plog($, state, 'info', 'door-answers', `${PLUGIN}: pfm answers again; the rows carry its facts`, 'ask'); }
    } catch (error) {
      const cause = doorCause(error);
      // A call its hook dispatch abandoned failed at nothing: the next drawing asks again.
      if (runAborted(cause)) { state.door.drop(); return; }
      state.door.fail(cause);
      report($, state, 'warning', 'door-failed', `${PLUGIN}: pfm's agent facts failed: ${cause}; the rows show the engine's own fields and the cause`, 'ask', { error: cause });
    }
    state.frame = undefined;
    try { $.ui.invalidate('ui.render'); } catch (error) {
      report($, state, 'warning', 'redraw-refused', `${PLUGIN}: the engine refused the redraw after pfm's answer: ${message(error)}; the rows show it at their next drawing`, 'ask', { error: message(error) });
    }
  })();
}

// Redraws the rows each second while an agent runs, so elapsed time and the cache countdown count; stops once no row is asked for.
function tick($: EngineInterface, state: State): void {
  state.quietTicks = 0;
  if (state.tick) return;
  state.tick = $.clock.every(TICK_MS, () => {
    state.quietTicks += 1;
    if (state.quietTicks > TICK_QUIET) { state.tick?.cancel(); state.tick = undefined; return; }
    for (const agent of state.rows.values()) {
      if (agent.status === 'running' || agent.status === 'waiting') { $.ui.invalidate('ui.render'); return; }
    }
  });
}

// The first drawing of a list waits for pfm's first answer, a moment at most; a wait cut short draws what there is.
async function firstAnswer($: EngineInterface, asking: Promise<void>, signal: AbortSignal): Promise<void> {
  await Promise.race([asking, $.clock.sleep(FIRST_ANSWER_MS, { signal }).catch(() => undefined)]);
}

/** Keeps `agent`'s props and drops every agent the list no longer holds. */
function remember(state: State, agent: RowAgent, rowIds: readonly string[]): void {
  state.rows.set(agent.id, agent);
  const held = new Set([...rowIds, agent.id]);
  for (const id of state.rows.keys()) { if (!held.has(id)) state.rows.delete(id); }
  state.door.keep(held);
}

/** The ids of the list in the engine's order, `agent`'s among them. */
function listIds(agent: RowAgent, rowIds: readonly string[]): readonly string[] {
  return rowIds.includes(agent.id) ? rowIds : [...rowIds, agent.id];
}

/** The list in the engine's order: `agent` as its props stand, every other agent as its last props gave it. */
function listed(state: State, agent: RowAgent, rowIds: readonly string[]): RowAgent[] {
  return listIds(agent, rowIds).flatMap((id) => (id === agent.id ? agent : state.rows.get(id) ?? []));
}

function body(state: State, agents: readonly RowAgent[], id: string, columns: number | undefined, now: number): string {
  const scene = { agents, pfm: state.door, now, columns, sessionCwd: state.sessionCwd };
  // One layout per burst of asks (the engine asks every row within a moment), so no row moves its columns alone.
  const key = `${Math.floor(now / 250)} ${columns} ${agents.map((agent) => agent.id).join(' ')}`;
  if (state.frame?.key !== key) state.frame = { key, layout: layout(scene) };
  return drawRow(scene, id, state.frame.layout);
}

function newState(): State {
  return { rows: new Map(), door: new Door(), quietTicks: 0, reported: new Set(), log: { work: Promise.resolve(), held: [], failing: false } };
}

export const register: Register = (on) => {
  const state = newState();

  on('ui.render', { component: 'AgentRow' }, async ($, e, next) => {
    const now = await $.clock.now();
    remember(state, e.props, e.props.rowIds);
    ask($, state, now, listIds(e.props, e.props.rowIds));
    tick($, state);
    if (state.door.state.state === 'waiting' && state.asking) await firstAnswer($, state.asking, next.signal);
    // The list is read after the wait: by then every row of a first burst has given its props, so all are laid out together.
    return next({ ...e, props: { ...e.props, body: body(state, listed(state, e.props, e.props.rowIds), e.props.id, e.props.columns, now) } });
  }).catch(($, e, next) => {
    const cause = `${next.error.kind}: ${next.error.message ?? 'the engine gave no cause'}`;
    report($, state, 'error', 'hook-threw', `${PLUGIN}: drawing the row of agent ${e.props.id} failed (${cause}); the row shows the cause`, 'ui.render', { kind: next.error.kind, error: next.error.message, agentId: e.props.id });
    return next({ ...e, props: { ...e.props, body: failureRow(cause, e.props.label) } });
  });

  on('session.end', async ($, e, next) => {
    state.tick?.cancel();
    await state.log.work;
    return next(e);
  });
};
