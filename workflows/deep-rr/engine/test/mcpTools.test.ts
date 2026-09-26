// @ts-nocheck — this file reads the shipped root files (workflow.js, SKILL.md) from disk, plus the Go
// registry four levels up (pfm/internal/harvestmcp/toolnames.go, pfm/internal/config/mcp_server.go). The engine
// package has no @types/node (its own src/ never touches the filesystem), so Node builtins have no ambient
// types here; ts-nocheck keeps that a test-file-local concern (as in midrun.test.ts and persist.test.ts).
import { describe, it, expect } from 'vitest';
import fs from 'node:fs';
import path from 'node:path';

// The repo root, four levels above engine/test/ (workflows/deep-rr/engine/test -> workflows/deep-rr -> workflows -> repo root).
const REPO_ROOT = path.join(__dirname, '..', '..', '..', '..');
const PFM_DIR = path.join(REPO_ROOT, 'pfm');
const TOOLNAMES_GO = path.join(PFM_DIR, 'internal', 'harvestmcp', 'toolnames.go');
const MCP_SERVER_GO = path.join(PFM_DIR, 'internal', 'config', 'mcp_server.go');
const CONDITIONAL_NOTE = '(registered only when a search backend is configured';

// Extracts `const identifier = "value"` (or `identifier = "value"` inside a const block) bindings from Go
// source text as a name -> value map.
function extractGoStringConsts(text) {
  const out = new Map();
  const re = /(\w+)\s*=\s*"([^"]*)"/g;
  let m;
  while ((m = re.exec(text))) {
    out.set(m[1], m[2]);
  }
  return out;
}

// Reads a file the registry must hold; a pfm/ tree without it is Go-side drift, never a skip.
function readRequired(file) {
  if (!fs.existsSync(file)) {
    throw new Error(
      `${path.relative(REPO_ROOT, file)} not found — the Go registry moved or renamed it`,
    );
  }
  return fs.readFileSync(file, 'utf8');
}

// The served set, from RegisteredToolNames in toolnames.go: the `names := []string{…}` literal is served
// always, each `names = append(names, …)` only behind its runtime gate (conditional).
function readGoRegistry() {
  if (!fs.existsSync(PFM_DIR)) return null; // a standalone deep-rr checkout carries no pfm/
  const toolnamesText = readRequired(TOOLNAMES_GO);
  const toolConsts = extractGoStringConsts(toolnamesText);
  const serverConsts = extractGoStringConsts(readRequired(MCP_SERVER_GO));
  if (!serverConsts.has('MCPServerProfessor')) {
    throw new Error(
      'mcp_server.go: constant "MCPServerProfessor" not found — the Go registry renamed or removed it',
    );
  }
  const always = toolnamesText.match(/names\s*:=\s*\[\]string\{([^}]*)\}/);
  const gated = [...toolnamesText.matchAll(/names\s*=\s*append\(names,\s*(\w+)\)/g)].map(
    (m) => m[1],
  );
  if (!always || !gated.length) {
    throw new Error(
      'toolnames.go: RegisteredToolNames no longer reads `names := []string{…}` plus a gated append',
    );
  }
  const server = serverConsts.get('MCPServerProfessor');
  const toolName = (ident) => {
    if (!toolConsts.has(ident)) {
      throw new Error(
        `toolnames.go: constant "${ident}" not found — the Go registry renamed or removed it`,
      );
    }
    return `mcp__${server}__${toolConsts.get(ident)}`;
  };
  const conditionalNames = new Set(gated.map(toolName));
  const served = new Set([
    ...always[1]
      .split(',')
      .map((s) => s.trim())
      .filter(Boolean)
      .map(toolName),
  ]);
  for (const n of conditionalNames) served.add(n);
  served.add(`mcp__${server}__harvester_*`); // SKILL.md's ToolSearch preflight pattern
  return { served, conditionalNames };
}

describe('MCP tool names — only what the professor server serves', () => {
  // A worker model calls exactly the tool a prompt names; a name the server does not expose fails every fetch
  // and silently degrades the run to snippet-only. Snapshots pin the text, not its validity, so this scans the
  // shipped bundle (verify.js keeps it equal to src/, CONFIG.NET included) and SKILL.md's preflight. The
  // served set itself is derived from the Go registry (toolnames.go + mcp_server.go) so a rename there is
  // caught here instead of silently drifting from the prompts.
  const registry = readGoRegistry();

  if (registry === null) {
    it.skip('skipped — standalone deep-rr checkout has no pfm/ Go registry to read', () => {});
  } else {
    const { served, conditionalNames } = registry;
    const bundle = fs.readFileSync(path.join(__dirname, '..', '..', 'workflow.js'), 'utf8');

    for (const file of ['workflow.js', 'SKILL.md']) {
      it(`${file} names only professor harvester tools`, () => {
        const text = fs.readFileSync(path.join(__dirname, '..', '..', file), 'utf8');
        const named = [...new Set(text.match(/mcp__[A-Za-z0-9_]+\*?/g) || [])];
        expect(named.length).toBeGreaterThan(0);
        expect(named.filter((n) => !served.has(n))).toEqual([]);
      });
    }

    it('harvester_search_web is gated in the Go registry, and the bundle names it only as conditional', () => {
      expect([...conditionalNames]).toContain('mcp__professor__harvester_search_web');
      for (const name of conditionalNames) {
        const first = bundle.indexOf(name);
        if (first === -1) continue; // a prompt that never names it owes no note
        expect(
          bundle
            .slice(first + name.length)
            .trimStart()
            .startsWith(CONDITIONAL_NOTE),
        ).toBe(true);
      }
    });
  }
});
