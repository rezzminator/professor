#!/usr/bin/env node
/**
 * persist.js — byte-exact persister for an RR workflow run.
 *
 * workflow.js runs in a sandbox with NO filesystem access, so it RETURNS its artifacts in
 * `result.files` (a { filename: content } map) instead of writing them. This script runs on
 * the HOST after the workflow completes, reads the run's output JSON, and writes every file
 * verbatim to {repo-root}/RR/{slug}/. No model, no re-reading — a deterministic parse-and-write
 * (the `cp` equivalent for files that are JSON-embedded rather than loose on disk).
 *
 * An aborted or killed run (unparseable output, or a completed run with an empty result.files)
 * never just vanishes: instead of an error-exit it writes an auditable tombstone under
 * RR/_aborted/ — run forensics: a query on its 4th retry had zero artifacts from attempts 1–3,
 * and without a tombstone there is nothing left to grep for what died where.
 *
 * Usage:  node persist.js <run-output-file.json>
 * The main chat calls this at the end of every RR run with the completion notification's
 * <output-file> path. Prints a JSON summary on success: { dir, written, files[], verdict,
 * confidence, resources: { copied, missing } }. On an aborted run: { tombstone, reason }.
 */
const fs = require('fs')
const path = require('path')
const { execSync } = require('child_process')

const outFile = process.argv[2]
if (!outFile) { console.error('usage: node persist.js <run-output-file>'); process.exit(1) }

const repoRoot = execSync('git rev-parse --show-toplevel', { cwd: __dirname }).toString().trim()

const rawText = fs.readFileSync(outFile, 'utf8')

let raw, parseError = null
try { raw = JSON.parse(rawText) }
catch (e) { parseError = e.message }

const res = parseError ? null : (raw && raw.result ? raw.result : raw)  // the workflow output is wrapped { …, result }
const files = (res && res.files) || {}

if (parseError || !Object.keys(files).length) {
  // TOMBSTONE SALVAGE — an aborted/killed run leaves an auditable trace instead of vanishing.
  const reason = parseError || 'workflow completed but result.files is empty'

  const dirMatch = rawText.match(/"dir"\s*:\s*"(RR\/[^"]+)"/)
  const slug = dirMatch ? dirMatch[1] : path.basename(outFile, path.extname(outFile))

  const abortedDir = path.join(repoRoot, 'RR', '_aborted')
  fs.mkdirSync(abortedDir, { recursive: true })
  const tombstonePath = path.join(abortedDir, slug.replace(/\//g, '-') + '-tombstone.md')

  const rawLines = rawText.split('\n')
  const lastCkpt = [...rawLines].reverse().find((l) => l.includes('⏺CKPT')) // the per-wave recovery checkpoint
  const tail = rawLines.slice(-30).join('\n')

  const tombstone = [
    '# RR aborted-run tombstone',
    '',
    `- outFile: ${outFile}`,
    `- reason: ${reason}`,
    `- last ⏺CKPT: ${lastCkpt || '(none found)'}`,
    '',
    '## Last 30 lines of raw output',
    '',
    '```',
    tail,
    '```',
  ].join('\n')

  fs.writeFileSync(tombstonePath, tombstone)
  console.log(JSON.stringify({ tombstone: tombstonePath, reason }, null, 2))
  process.exit(0)
}

const relDir = (res.dir && /^RR\//.test(res.dir)) ? res.dir : ('RR/' + (res.slug || 'run'))
const dir = path.join(repoRoot, relDir)                    // {repo-root}/RR/{slug}
fs.mkdirSync(dir, { recursive: true })

const written = []
for (const [name, content] of Object.entries(files)) {
  const filePath = path.join(dir, name)
  fs.mkdirSync(path.dirname(filePath), { recursive: true }) // namespaced keys (multi-brainer: root/wave-0.md, b1-x/wave-1.md) need their subdir first
  fs.writeFileSync(filePath, typeof content === 'string' ? content : JSON.stringify(content, null, 2))
  written.push(name)
}

// RESOURCE ARCHIVING — provenance-filtered: ONLY cache files a ledger claim actually pins to
// are archived here (an ad-hoc cache sweep once archived 136 foreign files out of 191); a
// missing cache file is counted, not fatal.
const resources = { copied: 0, missing: 0 }
if (files['_sources.json']) {
  const sourcesDoc = typeof files['_sources.json'] === 'string' ? JSON.parse(files['_sources.json']) : files['_sources.json']
  const resourcesDir = path.join(dir, 'resources')
  fs.mkdirSync(resourcesDir, { recursive: true })
  for (const { cachePath } of (sourcesDoc && sourcesDoc.sources) || []) {
    if (cachePath && fs.existsSync(cachePath)) {
      fs.copyFileSync(cachePath, path.join(resourcesDir, path.basename(cachePath)))
      resources.copied++
    } else {
      resources.missing++
    }
  }
}

console.log(JSON.stringify({
  dir,
  written: written.length,
  files: written.sort(),
  verdict: res.verdict || null,
  confidence: res.confidence || null,
  resources,
}, null, 2))
