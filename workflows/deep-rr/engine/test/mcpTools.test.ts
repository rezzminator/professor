// @ts-nocheck — this file reads the shipped root files (workflow.js, SKILL.md) from disk. The engine
// package has no @types/node (its own src/ never touches the filesystem), so Node builtins have no ambient
// types here; ts-nocheck keeps that a test-file-local concern (as in midrun.test.ts and persist.test.ts).
import { describe, it, expect } from 'vitest';
import fs from 'node:fs';
import path from 'node:path';

describe('MCP tool names — only what the professor server serves', () => {
  // A worker model calls exactly the tool a prompt names; a name the server does not expose fails every fetch
  // and silently degrades the run to snippet-only. Snapshots pin the text, not its validity, so this scans the
  // shipped bundle (verify.js keeps it equal to src/, CONFIG.NET included) and SKILL.md's preflight.
  const SERVED = new Set([
    'mcp__professor__harvester_read',
    'mcp__professor__harvester_download_file',
    'mcp__professor__harvester_search_literature',
    'mcp__professor__harvester_search_web',
    'mcp__professor__harvester_*', // SKILL.md's ToolSearch preflight pattern
  ]);
  for (const file of ['workflow.js', 'SKILL.md']) {
    it(`${file} names only professor harvester tools`, () => {
      const text = fs.readFileSync(path.join(__dirname, '..', '..', file), 'utf8');
      const named = [...new Set(text.match(/mcp__[A-Za-z0-9_]+\*?/g) || [])];
      expect(named.length).toBeGreaterThan(0);
      expect(named.filter((n) => !SERVED.has(n))).toEqual([]);
    });
  }
});
