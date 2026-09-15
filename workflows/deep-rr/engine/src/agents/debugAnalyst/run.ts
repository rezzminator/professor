import { CONFIG } from '../../config.js';
import { debugAnalyst } from './index.js';
import { retryAgent, IO_LOG, LOG_BUFFER } from '../../runtime.js';
import { clip } from '../../utils/index.js';
import type { ResearchReport } from '../../engine.js';
import type { BrainerState } from '../../brainerState.js';
import type { DiagOut, Metrics } from '../../types/index.js';

// DEBUG & ANALYSIS (last phase, opt-in via arg.debug): an Opus agent consolidates the run's diagnostics — corner-by-corner,
// prospector→researcher venue utilization, and any arg.debugPrompt question — then JS appends the verbatim metrics, run log,
// and raw agent I/O (exact prompt in / exact output out) into one shippable _debug.md. Returns the _debug.md markdown (the engine writes it).
export async function runDebug(
  rr: ResearchReport,
  bs: BrainerState,
  metrics: Metrics,
): Promise<string> {
  phase(CONFIG.PHASE.debug);
  log(
    '· debug & analysis · ' +
      debugAnalyst.tier +
      ' · over ' +
      IO_LOG.length +
      ' agent calls + ' +
      LOG_BUFFER.length +
      ' log lines + ' +
      rr.laneRecords.length +
      ' lane records',
  );
  const diag = await retryAgent<DiagOut>(
    debugAnalyst.buildPrompt({
      query: CONFIG.query,
      focus: CONFIG.debugPrompt,
      metrics,
      waveLog: bs.waveLog,
      resultLog: bs.resultLog,
      highValueSources: rr.highValueSources,
      laneRecords: rr.laneRecords,
    }),
    {
      label: 'debug-analyst',
      phase: CONFIG.PHASE.debug,
      model: debugAnalyst.tier,
      effort: debugAnalyst.effort,
      schema: debugAnalyst.schema,
    },
  );
  const narrative = (diag && diag.diagnosis) || '_(debug analyst failed — see raw sections below)_';
  const rawIO = IO_LOG.map(
    (e, i) =>
      '### ' +
      (i + 1) +
      '. `' +
      e.label +
      '` · ' +
      e.model +
      ' · ' +
      e.phase +
      '\n\n**PROMPT**\n\n' +
      (e.prompt || '') +
      '\n\n**OUTPUT**' +
      (e.error ? ' _(' + e.error + ')_' : '') +
      '\n\n' +
      (e.output == null ? '_(null)_' : JSON.stringify(e.output, null, 2)),
  ).join('\n\n');
  const artifact =
    '# RR debug & analysis — ' +
    clip(CONFIG.query, 80) +
    (CONFIG.debugPrompt ? '\n\n**Debug prompt:** ' + CONFIG.debugPrompt : '') +
    '\n\n## Analysis (debug-analyst · ' +
    debugAnalyst.tier +
    ')\n\n' +
    narrative +
    '\n\n## Metrics\n\n```json\n' +
    JSON.stringify(metrics, null, 2) +
    '\n```' +
    '\n\n## Run log (' +
    LOG_BUFFER.length +
    ' lines)\n\n```\n' +
    LOG_BUFFER.join('\n') +
    '\n```' +
    '\n\n## Raw agent I/O — exact prompt in, exact output out (' +
    IO_LOG.length +
    ' calls)\n\n' +
    (rawIO || '_(none captured)_') +
    '\n';
  log('· debug DONE · _debug.md assembled');
  return artifact;
}
