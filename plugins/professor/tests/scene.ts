import type { RowAgent } from '../plugins/professor/src/row.ts';

// One invented list of four agents and pfm's answer for it, shared by the row and adapter tests:
// a busy planner, a young nested-parent foreman in a worktree whose cache lapsed, its finished
// child whose transcript pfm could not read, and an agent that has not started.

export const NOW = 1_800_000_000_000;
export const SESSION_CWD = '/w/proj';
export const TRANSCRIPT = '/c/projects/w-proj/5b1e7c2a-0000-4000-8000-00000000abcd.jsonl';

const E = '\x1b';
export const LOOK = {
  palette: {
    tokens: `${E}[1;97m`, window: `${E}[38;5;117m`, model: `${E}[38;5;51m`, effort: `${E}[38;5;220m`, elapsed: `${E}[38;5;215m`, tools: `${E}[38;5;87m`,
    label: `${E}[97m`, muted: `${E}[38;5;245m`, good: `${E}[38;5;118m`, warn: `${E}[38;5;226m`, bad: `${E}[1;38;5;203m`, dim: `${E}[2m`, reset: `${E}[0m`,
  },
  effortColors: { low: `${E}[38;5;117m`, medium: `${E}[38;5;118m`, high: `${E}[38;5;226m`, xhigh: `${E}[38;5;215m`, max: `${E}[1;38;5;203m` },
  gauge: { on: '▰', off: '▱', cells: 8 },
  pulse: { glyphs: ['·', '•', '●'], drop: '↓', steps: [1, 5_000], dropFrom: 1_000, stepMs: 5_000, cells: 6 },
  context: [
    { from: 120, pulse: true, growth: true, window: true }, { from: 100, pulse: true, growth: false, window: true },
    { from: 90, pulse: false, growth: false, window: true }, { from: 0, pulse: false, growth: false, window: false },
  ],
  tones: { usd: { warn: 2, bad: 10 }, share: { warn: 50, bad: 80 }, cacheHit: { warn: 50, good: 80 } },
};

export const PLANNER: RowAgent = {
  id: 'a1', type: 'planner', description: 'Map the parser', label: 'Reading lexer.go', status: 'running', taskStatus: 'running', startTime: NOW - 724_000, cwd: SESSION_CWD,
  model: 'claude-opus-9', effort: 'medium', contextWindowSize: 1_000_000, tokenCount: 171_928,
  tokenSamples: [140_003, 140_281, 140_386, 146_387, 149_340, 153_030, 158_300, 158_300, 162_886, 163_002, 163_183, 163_312, 163_460, 166_330, 167_414, 171_928],
  lastRequest: { at: NOW - 112_000, model: 'claude-opus-9', effort: 'medium', cacheTtl: '5m' }, cost: { usd: 0.46 },
};
export const FOREMAN: RowAgent = {
  id: 'a2', type: 'flights:foreman', name: 'door', description: 'Build the door', label: 'Running go test ./internal/...', status: 'running', taskStatus: 'running',
  startTime: NOW - 61_000, cwd: '/w/proj/.worktrees/door', model: 'claude-sonnet-9', effort: 'high', contextWindowSize: 200_000, tokenCount: 172_979,
  tokenSamples: [0, 40_853, 46_181, 46_267, 53_469, 62_464, 172_618, 172_716, 172_835, 172_979],
  lastRequest: { at: NOW - 400_000, model: 'claude-sonnet-9', effort: 'high', cacheTtl: '5m' }, cost: { usd: 12.5 },
};
export const CHILD: RowAgent = {
  id: 'a3', parentId: 'a2', type: 'Explore', description: 'Find the callers', label: 'Find the callers', status: 'completed', taskStatus: 'completed', startTime: NOW - 300_000,
  cwd: SESSION_CWD, model: 'claude-haiku-9', contextWindowSize: 200_000, tokenCount: 9_000, tokenSamples: [30_000, 31_000, 9_000],
  lastRequest: { at: NOW - 200_000, model: 'claude-haiku-9' }, cost: { usd: 0.02 },
};
export const UNSTARTED: RowAgent = {
  id: 'a4', type: 'general-purpose', description: 'Wait for the build', label: 'Wait for the build', status: 'pending', taskStatus: 'pending', startTime: NOW - 1_000,
  cwd: SESSION_CWD, tokenCount: 0, tokenSamples: [],
};
export const AGENTS: RowAgent[] = [PLANNER, FOREMAN, CHILD, UNSTARTED];

const SPEND = { usd: 3.41, usdKnown: true, in: 2_000_000, out: 24_300, tools: 57, advisorUsd: 0, advisorUnpriced: 0, floor: false, ledger: true, apiEquivalent: false };

/** pfm's answer as it prints it for the four ids; UNSTARTED is absent: it has no transcript yet. */
export const ANSWER = {
  sessionId: '5b1e7c2a-0000-4000-8000-00000000abcd', at: NOW,
  agents: [
    {
      id: 'a1', model: 'claude-opus-9', effort: 'medium', tools: 40, toolErrors: 2, compactions: 1, cacheHit: 94, cacheLifetime: true, context: 170_000, contextWindow: 0,
      lastActivity: NOW - 4_000, nested: { total: 3, working: 1, unknown: 0 }, spend: SPEND,
      text: { glyph: '◆', family: 'opus', effortBar: '▰▰', context: '170.0K', in: '2.0M', out: '24.3K', usd: '$3.41' },
    },
    {
      id: 'a2', model: 'claude-sonnet-9', effort: 'high', tools: 1, toolErrors: 0, compactions: 0, cacheHit: 31, cacheLifetime: true, context: 172_000, contextWindow: 0,
      lastActivity: NOW - 200_000, nested: { total: 0, working: 0, unknown: 0 },
      spend: { ...SPEND, usd: 12.5, in: 812_000, out: 9_300, tools: 1, advisorUsd: 0.3, floor: true, ledger: false, apiEquivalent: true },
      text: { glyph: '◇', family: 'sonnet', effortBar: '▰▰▰', context: '172.0K', in: '812.0K', out: '9.3K', usd: '$12.50' },
    },
    {
      id: 'a3', model: 'claude-haiku-9', effort: '', tools: 0, toolErrors: 0, compactions: 0, cacheHit: -1, cacheLifetime: true, context: 0, contextWindow: 0,
      lastActivity: 0, nested: { total: 0, working: 0, unknown: 0 }, spend: null, error: 'open agent-a3.jsonl: permission denied',
      text: { glyph: '○', family: 'haiku', effortBar: '', context: '0', in: '', out: '', usd: '' },
    },
    {
      id: 'a4', model: '', effort: '', tools: 0, toolErrors: 0, compactions: 0, cacheHit: -1, cacheLifetime: true, context: 0, contextWindow: 0,
      lastActivity: 0, nested: { total: 0, working: 0, unknown: 0 }, spend: null, error: 'open agent-a4.jsonl: no such file or directory', absent: true,
      text: { glyph: '', family: '', effortBar: '', context: '0', in: '', out: '', usd: '' },
    },
  ],
  look: LOOK,
};
