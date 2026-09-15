// validate-bundle.js — enforces the Workflow sandbox contract on the built ../workflow.js.
// Run by build.js after every bundle. Any violation throws → `npm run build` fails loudly.

import { readFileSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'
import { execSync } from 'node:child_process'
import { parse } from 'acorn'

const SRC = dirname(fileURLToPath(import.meta.url))
const BUNDLE = join(SRC, '..', 'workflow.js')
const src = readFileSync(BUNDLE, 'utf8')
const fail = (msg) => { console.error('✗ bundle invalid — ' + msg); process.exit(1) }

// 1) must parse as JS
try { execSync('node --check ' + JSON.stringify(BUNDLE), { stdio: 'pipe' }) }
catch (e) { fail('node --check failed:\n' + (e.stderr || e.stdout || e.message)) }

// 1b) must ALSO compile as the BODY of the async function the harness wraps it in. `node --check` above
//     parses the file as an ES MODULE, which tolerates dangling `} from '…'` fragments that a function
//     scope rejects — the exact failure a multi-line-import drop produced. Compile (don't run) it the way
//     Workflow loads it: de-export meta (illegal in a function body); the top-level `return await` needs async.
const AsyncFunction = Object.getPrototypeOf(async () => {}).constructor
const HARNESS_GLOBALS = ['agent', 'parallel', 'pipeline', 'log', 'phase', 'workflow', 'args', 'budget']
try { new AsyncFunction(...HARNESS_GLOBALS, src.replace(/^export const meta/, 'const meta')) }
catch (e) { fail('not valid as the harness function body (how Workflow loads it): ' + e.message) }

// 2) no surviving module system at runtime (the harness wraps it in a function scope).
//    The ONLY allowed export is the top `export const meta`; no import/require/module.exports.
if (/\brequire\s*\(/.test(src)) fail('contains require( — must be a self-contained single file')
if (/^\s*import\b/m.test(src)) fail('contains a surviving `import` statement')
if (/module\.exports|exports\./.test(src)) fail('contains CommonJS exports')
const exportHits = src.match(/^\s*export\b/gm) || []
if (exportHits.length !== 1) fail('expected exactly ONE export (`export const meta`), found ' + exportHits.length)

// 3) `export const meta = { … }` must be the first statement (top of file) and a pure DATA literal —
//    an AST check (acorn already parses the same syntax build.js's bundler does), not a string heuristic:
//    body[0] must be exactly `export const meta = <ObjectExpression>` (acorn never counts comments as
//    statements, so this is "after comments" for free), then the object is walked recursively and
//    rejects any SpreadElement, function/arrow, call/new expression, or interpolated template literal —
//    pure data only, no computation.
// allowReturnOutsideFunction: the bundle deliberately ends in a top-level `return` (the harness wraps it
// in an async function at runtime, per check 1b above) — not valid standalone module syntax, by design.
const ast = parse(src, { ecmaVersion: 'latest', sourceType: 'module', allowReturnOutsideFunction: true })
const first = ast.body[0]
const decl =
  first && first.type === 'ExportNamedDeclaration' && first.declaration &&
  first.declaration.type === 'VariableDeclaration' && first.declaration.kind === 'const'
    ? first.declaration.declarations[0]
    : null
if (!decl || decl.id.name !== 'meta' || !decl.init || decl.init.type !== 'ObjectExpression')
  fail('file must BEGIN with `export const meta = { … }` (a pure object literal)')

const FORBIDDEN_NODE_TYPES = new Set([
  'SpreadElement', 'FunctionExpression', 'ArrowFunctionExpression', 'FunctionDeclaration',
  'CallExpression', 'NewExpression', 'TaggedTemplateExpression',
])
function walkPureData(node) {
  if (node === null || typeof node !== 'object') return
  if (Array.isArray(node)) { for (const n of node) walkPureData(n); return }
  if (typeof node.type === 'string') {
    if (FORBIDDEN_NODE_TYPES.has(node.type))
      fail('meta object contains a forbidden construct (' + node.type + ') — it must be pure data')
    if (node.type === 'TemplateLiteral' && node.expressions && node.expressions.length > 0)
      fail('meta object contains an interpolated template literal — it must be pure data')
  }
  for (const key of Object.keys(node)) {
    if (key === 'start' || key === 'end' || key === 'loc' || key === 'range' || key === 'type') continue
    walkPureData(node[key])
  }
}
walkPureData(decl.init)

// 4) determinism guard — none of these non-deterministic / host globals (usage-level, so prose
//    words like "url"/"WebFetch"/"process" do not trip it). Mirrors the harness static guard.
const FORBIDDEN = [
  [/\bDate\.now\b/, 'Date.now()'],
  [/\bMath\.random\b/, 'Math.random()'],
  [/new\s+Date\s*\(\s*\)/, 'argless new Date()'],
  [/\bBuffer\s*[.(]|new\s+Buffer\b/, 'Buffer'],
  [/\bprocess\./, 'process.*'],
  [/(^|[^A-Za-z.])fetch\s*\(/m, 'fetch('],
  [/\bcrypto\b/, 'crypto'],
  [/new\s+URL\s*\(/, 'new URL('],
  [/\bURL\./, 'URL.'],
]
for (const [re, label] of FORBIDDEN) {
  if (re.test(src)) fail('contains forbidden non-deterministic global: ' + label)
}

console.log('✓ bundle valid — single-file, `export const meta` literal at top, no forbidden globals')
