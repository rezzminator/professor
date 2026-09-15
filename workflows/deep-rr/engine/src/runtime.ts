import { CONFIG } from './config.js';
import { PROMPT_LOG } from './utils/index.js';
import type { AgentOpts, IoLogEntry } from './types/index.js';

// ─────────────────────────────────────────────────────────────────────────────
// Agent runtime — the shared sub-agent caller + the debug capture buffers. Lives
// in its own module (bundled before the per-agent run.ts modules) so every run fn
// imports retryAgent without a cycle back through engine.ts; the engine no longer
// owns the agent-call plumbing, only the orchestration that consumes it.
// ─────────────────────────────────────────────────────────────────────────────

// L7 retry indirection — wraps every agent() call; the _agent alias keeps it from rewriting itself.
const _agent = agent;
// Debug capture (opt-in via arg.debug): the raw agent I/O + the full run-log stream, consumed by the end Debug & Analysis agent.
export const IO_LOG: IoLogEntry[] = [];
export const LOG_BUFFER: string[] = [];
const _log = globalThis.log;
try {
  globalThis.log = (m?: unknown) => {
    const s = typeof m === 'string' ? m : String(m);
    // per-wave checkpoint lines are a live-output/recovery mechanism, not a debug narrative — keep them
    // OUT of _debug.md's Run log so a long run's checkpoint spam never bloats it.
    if (CONFIG.debug && !s.startsWith(CONFIG.CHECKPOINT_MARK)) LOG_BUFFER.push(s);
    return _log(m);
  };
} catch (e) {
  /* log not writable → run-log just won't be buffered */
}

// run a sub-agent with AGENT_RETRIES retries, narrowing the result to its agent's typed `*Out` shape (T); degrades to null when exhausted.
export const retryAgent = async <T>(prompt: string, opts: AgentOpts): Promise<T | null> => {
  if (opts && opts.label) PROMPT_LOG[opts.label] = prompt;
  for (let attempt = 0; attempt <= CONFIG.AGENT_RETRIES; attempt++) {
    try {
      const out = (await _agent(prompt, opts)) as T;
      // The harness resolves to null WITHOUT throwing when the sub-agent dies on a
      // terminal API error (e.g. a safety-classifier block) or is skipped mid-run.
      // Route null through the same retry ladder as a thrown error — otherwise the
      // ladder never engages on exactly the failure class it exists for. A borderline
      // classifier block is often probabilistic; a fresh spawn frequently passes.
      if (out == null)
        throw new Error('agent returned null (terminal API error / skip / safety block)');
      if (CONFIG.debug)
        IO_LOG.push({
          label: (opts && opts.label) || '?',
          model: (opts && opts.model) || '?',
          phase: (opts && opts.phase) || '?',
          prompt,
          output: out,
        });
      return out;
    } catch (e) {
      log(
        '  ⚠ agent error (attempt ' +
          (attempt + 1) +
          '/' +
          (CONFIG.AGENT_RETRIES + 1) +
          '): ' +
          ((e && e.message) || e),
      );
      if (attempt === CONFIG.AGENT_RETRIES) {
        log('  ⚠ agent retries exhausted → degraded to null');
        if (CONFIG.debug)
          IO_LOG.push({
            label: (opts && opts.label) || '?',
            model: (opts && opts.model) || '?',
            phase: (opts && opts.phase) || '?',
            prompt,
            output: null,
            error: (e && e.message) || String(e),
          });
        return null;
      }
    }
  }
  return null;
};
