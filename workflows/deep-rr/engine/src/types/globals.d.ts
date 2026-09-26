// Ambient globals the Workflow harness injects into the bundle's scope at runtime (and that the vitest
// setup / engine tests stub via globalThis). Declared with `var` so each is reachable both as a bare
// identifier (`agent`, `log`, …) and as `globalThis.agent`. `agent` resolves to `unknown` — the engine's
// retryAgent narrows each call to its agent's typed `*Out` shape.
import type { AgentOpts } from './run.js';

declare global {
  // the harness primitive: run a sub-agent with a prompt + options; returns its StructuredOutput.
  var agent: (prompt: string, opts: AgentOpts) => Promise<unknown>;
  // run thunks concurrently, preserving order.
  var parallel: <T>(thunks: Array<() => Promise<T>>) => Promise<T[]>;
  // two-stage map (only the tests stub this; the engine does not use it).
  var pipeline: (
    items: unknown[],
    s1: (item: unknown, i: number) => Promise<unknown>,
    s2: (a: unknown, item: unknown, i: number) => Promise<unknown>,
  ) => Promise<unknown[]>;
  // mark the current pipeline phase (UI only).
  var phase: (name: string) => void;
  // append a line to the run log.
  var log: (m?: unknown) => void;
  // the harness DOES inject this; the engine just has no use for it (kept for parity with validate-bundle.js's HARNESS_GLOBALS).
  var workflow: unknown;
  // the injected, untrusted run args (validated by Configs).
  var args: unknown;
  // the harness budget handle (unused by the engine logic).
  var budget: unknown;
}

export {};
