// vitest setupFiles — set the ambient globals the engine reads at MODULE LOAD (`new
// Configs(args)`, `const _agent = agent`, `log('▶ RR START')`) BEFORE any module imports.
// The engine test overrides agent/parallel/pipeline (and args, to switch modes) per-test
// via vi.resetModules() + a fresh dynamic import; these are the safe defaults.
globalThis.args = { query: 'best vector database for production RAG at scale', mode: 'goal' };
globalThis.log = () => {};
globalThis.phase = () => {};
globalThis.agent = async () => ({});
globalThis.parallel = async <T>(thunks: Array<() => Promise<T>>): Promise<T[]> =>
  Promise.all(thunks.map((t) => t()));
globalThis.pipeline = async (
  items: unknown[],
  s1: (item: unknown, i: number) => Promise<unknown>,
  s2: (a: unknown, item: unknown, i: number) => Promise<unknown>,
): Promise<unknown[]> => {
  const out: unknown[] = [];
  for (let i = 0; i < items.length; i++) {
    const a = await s1(items[i], i);
    out.push(await s2(a, items[i], i));
  }
  return out;
};
globalThis.budget = { total: null, spent: () => 0, remaining: () => Infinity };
