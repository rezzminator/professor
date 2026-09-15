#!/usr/bin/env node
/**
 * midrun.js — mid-run inspector for a LIVE (or crashed) RR workflow run.
 *
 * A Workflow's task output file stays EMPTY until the run completes — mid-run it tells you
 * nothing. The only mid-run truth is the workflow TRANSCRIPT DIR: <session>/subagents/workflows/
 * <runId>/journal.jsonl (one line per dispatch/completion event) plus one agent-<id>.jsonl
 * transcript per spawned agent. This script reads ONLY the journal — never the agent-*.jsonl
 * bodies (hundreds of KB each; reading them would flood any consumer). Transcript files are
 * touched with fs.stat ONLY, for mtime/size, to tell a still-running agent from a dead one.
 *
 * Journal lines are JSON: a DISPATCH entry ({ type, key, agentId }, no `result`) when an agent
 * starts, and a COMPLETION entry ({ type, key, agentId, result }, has `result`) when it returns.
 * `key` is an opaque memoization hash — agent IDENTITY (which pipeline role it played) is not
 * recorded anywhere and must be FINGERPRINTED from the shape of its result's key set (see
 * FINGERPRINTS below). A DEAD agent (retries exhausted, classifier-killed spawn) leaves ONLY a
 * dispatch entry — no result, no error string anywhere in the journal. Death is INFERRED from
 * that absence, combined with a stale transcript mtime — it is never read off an error message,
 * because there isn't one.
 *
 * Usage:
 *   node midrun.js [status|findings] [workflow-dir-or-journal-path]
 *
 * Mode (positional, case-insensitive; default "status"):
 *   status            progress · pipeline shape · wave/coord counts · phase inference ·
 *                      derived HEALTH FLAGS (the core value — degraded shape, brainer silence,
 *                      starved lanes, judge loops)
 *   findings          what the run has learned so far — the latest brainer coord's running
 *                      answer, or (when no coord ever ran) a reconstruction from the finalize
 *                      agents (initiator/refiner/judge) and the merged scout landscape
 *   Aliases: progress/error/errors/failure -> status; finding -> findings; also accepted as
 *   flags: --status / --findings.
 *
 * Path (positional, optional): a workflow run dir (containing journal.jsonl) or a direct path
 * to journal.jsonl. Omitted -> AUTO-DISCOVER the newest-mtime journal.jsonl under
 * <home>/.cc/{account}/projects/{project}/{sessionId}/subagents/workflows/wf_{id}/journal.jsonl
 * (plain fs.readdirSync walk, no glob dependency) and print which run was picked (to stderr,
 * so stdout stays clean JSON).
 *
 * Output: a markdown report written to {repo-root}/tmp/rr-midrun/<runId>-<mode>-<HHMMSS>.md
 * (repo-root = `git rev-parse --show-toplevel` from process.cwd(), falling back to __dirname's
 * repo, then to cwd); mkdir -p'd. A compact JSON summary is also printed to stdout:
 *   { report, run, mode, agents: { completed, inFlight, staleDead },
 *     waves: { scheduled, coordinated }, degraded, headline }
 *
 * Robustness: a truncated/corrupt journal line is skipped and counted, never thrown; a missing
 * run dir or journal prints one clear line to stderr and exits 1 — never a raw stack trace.
 */
const fs = require('fs')
const path = require('path')
const os = require('os')
const { execSync } = require('child_process')

const SIX_MIN_MS = 6 * 60 * 1000

// ---------------------------------------------------------------------------------------------
// FINGERPRINTS — a completed agent's ROLE is read off its result's key set, in this order.
// (see header: identity is never recorded directly, only these shapes.)
// ---------------------------------------------------------------------------------------------
function fingerprint(result) {
  if (!result || typeof result !== 'object') return { kind: 'unknown', keys: [] }
  const keys = Object.keys(result)
  const has = (k) => keys.includes(k)

  if (has('decomposition') && has('angles')) return { kind: 'scoutPlanner' }
  if (has('landscape') && has('pages')) return { kind: 'scoutSweep' } // probe OR merger — indistinguishable by shape; see mergedLandscape()
  if (has('highValueSources')) return { kind: 'prospector' }
  if (has('resultSoFar') && has('stop')) return { kind: 'brainerCoord' } // the crawl's heartbeat
  if (has('resultSoFar')) return { kind: 'brainCompute' }
  if (has('lanes')) return { kind: 'scheduler' }
  if (has('runningAnswer')) return { kind: 'laneReader' }
  if (has('checks') && has('enough')) return { kind: 'validator' }
  if (has('checks')) return { kind: 'claimAuditorBatch' }
  if (has('links')) return { kind: 'lineageClerk' }
  if (has('refinement') && has('synthesiser')) return { kind: 'initiator' }
  if (has('verdict') && has('report')) return { kind: 'synthesiser' }
  if (has('queriesTried') && has('counterFound')) return { kind: 'refiner' }
  if (has('goalMet') && has('verificationSound')) return { kind: 'judge' }
  if (has('diagnosis')) return { kind: 'debugAnalyst' }
  if (has('ok')) return { kind: 'rerunner' }
  return { kind: 'unknown', keys }
}

const PIPELINE_ORDER = [
  'scoutPlanner', 'scoutSweep', 'prospector',
  'scheduler', 'laneReader', 'claimAuditorBatch', 'lineageClerk', 'validator',
  'brainerCoord', 'brainCompute',
  'initiator', 'refiner', 'judge', 'synthesiser', 'rerunner', 'debugAnalyst', 'unknown',
]

// ---------------------------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------------------------
function clip(v, n) {
  if (v == null) return ''
  const s = typeof v === 'string' ? v : JSON.stringify(v)
  return s.length > n ? s.slice(0, n) + '…' : s
}

function fmtAge(ms) {
  if (ms == null) return 'unknown (transcript missing)'
  const m = ms / 60000
  return m < 1 ? `${Math.round(ms / 1000)}s` : `${m.toFixed(1)}m`
}

function statMtime(p) {
  try { return fs.statSync(p).mtime } catch (e) { return null }
}

function hhmmss(d) {
  const p2 = (n) => String(n).padStart(2, '0')
  return `${p2(d.getHours())}${p2(d.getMinutes())}${p2(d.getSeconds())}`
}

function listSection(label, arr) {
  if (!Array.isArray(arr) || !arr.length) return `- ${label}: (none)`
  const items = arr.slice(0, 12).map((x) => clip(x, 200))
  return `- ${label} (${arr.length}${arr.length > 12 ? ', showing 12' : ''}):\n` + items.map((i) => `  - ${i}`).join('\n')
}

// ---------------------------------------------------------------------------------------------
// argv
// ---------------------------------------------------------------------------------------------
function parseArgs(argv) {
  const MODE_ALIASES = {
    status: 'status', progress: 'status', error: 'status', errors: 'status', failure: 'status',
    findings: 'findings', finding: 'findings',
  }
  let mode = 'status'
  let pathArg = null
  for (const a of argv) {
    if (a === '--status') { mode = 'status'; continue }
    if (a === '--findings') { mode = 'findings'; continue }
    const alias = MODE_ALIASES[a.toLowerCase()]
    if (alias) { mode = alias; continue }
    pathArg = a
  }
  return { mode, pathArg }
}

// ---------------------------------------------------------------------------------------------
// path resolution / auto-discovery
// ---------------------------------------------------------------------------------------------
function resolveGivenPath(p) {
  const resolved = path.resolve(p)
  if (fs.existsSync(resolved) && fs.statSync(resolved).isDirectory()) {
    return path.join(resolved, 'journal.jsonl')
  }
  return resolved
}

// Newest-mtime journal.jsonl under <home>/.cc/{account}/projects/{project}/{sessionId}/subagents/
// workflows/wf_{id}/journal.jsonl — one extra {sessionId} level sits between a project and its
// subagents dir (each project dir mixes loose per-session *.jsonl transcript files with one
// subdirectory per session that spawned sub-agents). Plain fs.readdirSync walk, no glob dep.
function autoDiscoverJournal() {
  const ccDir = path.join(os.homedir(), '.cc')
  let best = null
  let accounts
  try { accounts = fs.readdirSync(ccDir) } catch (e) { return null }
  for (const acct of accounts) {
    const projectsDir = path.join(ccDir, acct, 'projects')
    let projects
    try { projects = fs.readdirSync(projectsDir) } catch (e) { continue }
    for (const proj of projects) {
      const projDir = path.join(projectsDir, proj)
      let sessions
      try { sessions = fs.readdirSync(projDir) } catch (e) { continue }
      for (const session of sessions) {
        const workflowsDir = path.join(projDir, session, 'subagents', 'workflows')
        let runs
        try { runs = fs.readdirSync(workflowsDir) } catch (e) { continue }
        for (const run of runs) {
          if (!run.startsWith('wf_')) continue
          const journalPath = path.join(workflowsDir, run, 'journal.jsonl')
          const mtime = statMtime(journalPath)
          if (mtime && (!best || mtime.getTime() > best.mtimeMs)) {
            best = { journalPath, mtimeMs: mtime.getTime() }
          }
        }
      }
    }
  }
  return best ? best.journalPath : null
}

function getRepoRoot() {
  try { return execSync('git rev-parse --show-toplevel', { cwd: process.cwd() }).toString().trim() } catch (e) { /* fall through */ }
  try { return execSync('git rev-parse --show-toplevel', { cwd: __dirname }).toString().trim() } catch (e) { /* fall through */ }
  return process.cwd()
}

// ---------------------------------------------------------------------------------------------
// journal loading
// ---------------------------------------------------------------------------------------------
function loadJournal(journalPath) {
  const rawText = fs.readFileSync(journalPath, 'utf8')
  const lines = rawText.split('\n')
  const entries = []
  let corrupt = 0
  for (const line of lines) {
    const t = line.trim()
    if (!t) continue
    try { entries.push(JSON.parse(t)) }
    catch (e) { corrupt++ }
  }
  return { entries, corrupt }
}

function classify(entries) {
  const records = entries.map((e, idx) => {
    const isCompletion = Object.prototype.hasOwnProperty.call(e, 'result')
    return {
      idx,
      agentId: e.agentId || `unknown-${idx}`,
      key: e.key,
      isCompletion,
      fp: isCompletion ? fingerprint(e.result) : null,
      result: e.result,
    }
  })

  const started = new Map() // agentId -> first dispatch idx
  const completedIds = new Set()
  for (const r of records) {
    if (r.isCompletion) completedIds.add(r.agentId)
    else if (!started.has(r.agentId)) started.set(r.agentId, r.idx)
  }
  const pendingAgentIds = [...started.keys()].filter((id) => !completedIds.has(id))
  const completions = records.filter((r) => r.isCompletion)

  const tally = {}
  const unknownList = []
  for (const c of completions) {
    tally[c.fp.kind] = (tally[c.fp.kind] || 0) + 1
    if (c.fp.kind === 'unknown') unknownList.push({ agentId: c.agentId, keys: c.fp.keys })
  }

  return { records, started, pendingAgentIds, completions, tally, unknownList }
}

function splitPending(pendingAgentIds, started, runDir) {
  const now = Date.now()
  const inFlight = []
  const staleDead = []
  for (const agentId of pendingAgentIds) {
    const transcriptPath = path.join(runDir, `agent-${agentId}.jsonl`)
    const mtime = statMtime(transcriptPath)
    const ageMs = mtime ? now - mtime.getTime() : null
    const entry = { agentId, ageMs, mtime: mtime ? mtime.toISOString() : null, dispatchIdx: started.get(agentId) }
    if (mtime && ageMs <= SIX_MIN_MS) inFlight.push(entry)
    else staleDead.push(entry)
  }
  return { inFlight, staleDead }
}

// The scout probe/merger fingerprint is shape-identical for every sweep of the angle; the LAST
// one before the prospector's completion is, by pipeline order, the merged landscape.
function mergedLandscape(completions) {
  const sweeps = completions.filter((c) => c.fp.kind === 'scoutSweep')
  if (!sweeps.length) return null
  const prospectors = completions.filter((c) => c.fp.kind === 'prospector')
  let pool = sweeps
  if (prospectors.length) {
    const before = sweeps.filter((s) => s.idx < prospectors[0].idx)
    if (before.length) pool = before
  }
  return pool[pool.length - 1].result.landscape || null
}

// ---------------------------------------------------------------------------------------------
// health flags
// ---------------------------------------------------------------------------------------------
function computeFlags(completions, tally) {
  const flags = []
  const coordCount = tally.brainerCoord || 0
  const schedulerCount = tally.scheduler || 0
  const finalizeCount = (tally.initiator || 0) + (tally.refiner || 0) + (tally.judge || 0)
  const judgeEntries = completions.filter((c) => c.fp.kind === 'judge')
  const judgeFalseCount = judgeEntries.filter((c) => c.result && c.result.goalMet === false).length
  const synthCount = tally.synthesiser || 0

  if (finalizeCount > 0 && coordCount === 0) {
    flags.push({
      mark: '⚠', label: 'DEGRADED SHAPE',
      detail: 'finalize agents (initiator/refiner/judge) present but ZERO brainer coords → the crawl never coordinated; wave-0 brainer death (classifier/schema-size kill or API death) — check COORD serialized size vs the 3,966/5,311 known-good ceilings.',
    })
  }

  if (coordCount > 0 && schedulerCount > 0) {
    const schedIdxs = completions.filter((c) => c.fp.kind === 'scheduler').map((c) => c.idx)
    const lastSchedIdx = Math.max(...schedIdxs)
    const coordAfter = completions.some((c) => c.fp.kind === 'brainerCoord' && c.idx > lastSchedIdx)
    const finalizeAfter = completions.some((c) => ['initiator', 'refiner', 'judge'].includes(c.fp.kind) && c.idx > lastSchedIdx)
    if (!coordAfter && !finalizeAfter) {
      flags.push({
        mark: '⚠', label: 'BRAINER SILENT',
        detail: 'a scheduler + lane readers ran for the latest wave but no coord followed within the journal → brainer death mid-crawl.',
      })
    }
  }

  for (const s of completions.filter((c) => c.fp.kind === 'scheduler')) {
    const lanes = (s.result && s.result.lanes) || []
    if (lanes.length && lanes.every((l) => !((l.sources || []).length))) {
      flags.push({ mark: '⚠', label: 'STARVED', detail: 'a scheduler result whose lanes all have empty sources.' })
      break
    }
  }

  if (judgeFalseCount > 1) {
    flags.push({ mark: '⚠', label: 'JUDGE LOOP', detail: `${judgeFalseCount} judge results with goalMet=false → remediation cycling.` })
  }

  if (synthCount > 0) {
    flags.push({ mark: '✓', label: 'LANDING', detail: 'synthesiser result present → run is writing its report.' })
  }

  return { flags, coordCount, schedulerCount, finalizeCount, judgeEntries, judgeFalseCount, synthCount }
}

function inferPhases(tally) {
  const has = (k) => (tally[k] || 0) > 0
  const phases = []
  if (has('scoutPlanner') || has('scoutSweep') || has('prospector')) phases.push('scout')
  if (has('scheduler') || has('laneReader')) phases.push(`crawl wave (${tally.scheduler || 0} scheduled)`)
  if (has('initiator') || has('refiner') || has('judge')) phases.push('finalize')
  if (has('synthesiser')) phases.push('synthesis')
  if (has('debugAnalyst')) phases.push('debug')
  return phases
}

// ---------------------------------------------------------------------------------------------
// report builders
// ---------------------------------------------------------------------------------------------
function buildStatusReport(ctx) {
  const { runId, journalPath, entries, corrupt, firstTs, lastTs, completions, tally,
    inFlight, staleDead, flagInfo, unknownList } = ctx
  const L = []
  L.push(`# RR mid-run status — ${runId}`)
  L.push('')
  L.push(`- journal: ${journalPath}`)
  L.push(`- entries: ${entries.length}${corrupt ? ` (${corrupt} corrupt/truncated line(s) skipped)` : ''}`)
  L.push(`- first entry (approx, first agent's dispatch mtime): ${firstTs ? firstTs.toISOString() : 'unknown'}`)
  L.push(`- last entry (journal mtime): ${lastTs ? lastTs.toISOString() : 'unknown'}`)
  L.push('')
  L.push('## Agents')
  L.push('')
  L.push(`- completed: ${completions.length}`)
  L.push(`- in-flight (dispatched, transcript touched within ~6 min): ${inFlight.length}`)
  for (const a of inFlight) L.push(`  - ${a.agentId} — transcript age ${fmtAge(a.ageMs)}`)
  L.push(`- stale/likely-dead (dispatched >6 min ago, transcript stale or missing): ${staleDead.length}`)
  for (const a of staleDead) L.push(`  - ${a.agentId} — transcript age ${fmtAge(a.ageMs)}`)
  if (staleDead.length) L.push('  - caveat: a dead agent leaves NO error text — only the absence of a result after dispatch; this is inferred, never read.')
  L.push('')
  L.push('## Pipeline shape')
  L.push('')
  for (const kind of PIPELINE_ORDER) {
    if (tally[kind]) L.push(`- ${kind}: ${tally[kind]}`)
  }
  if (unknownList.length) {
    L.push('')
    L.push('unknown result shapes seen:')
    for (const u of unknownList.slice(0, 8)) L.push(`  - ${u.agentId}: [${u.keys.join(', ')}]`)
  }
  L.push('')
  L.push('## Waves')
  L.push('')
  L.push(`- schedulers seen (waves scheduled, incl. judge-reopen mini-waves): ${flagInfo.schedulerCount}`)
  L.push(`- brainer coords seen: ${flagInfo.coordCount}`)
  const latestCoord = completions.filter((c) => c.fp.kind === 'brainerCoord').slice(-1)[0]
  L.push(`- latest coord stop: ${latestCoord ? clip(latestCoord.result.stop, 300) : 'n/a — no coord seen'}`)
  L.push('')
  L.push('## Phase inference')
  L.push('')
  const phases = inferPhases(tally)
  L.push(phases.length ? phases.join(' → ') : '(no recognizable phase yet)')
  L.push('')
  L.push('## Health flags')
  L.push('')
  if (!flagInfo.flags.length) L.push('(none)')
  for (const f of flagInfo.flags) L.push(`${f.mark} **${f.label}** — ${f.detail}`)
  const latestJudge = flagInfo.judgeEntries.slice(-1)[0]
  if (latestJudge) {
    L.push('')
    L.push('## Judge snapshot (latest)')
    L.push('')
    L.push(`- goalMet: ${latestJudge.result.goalMet} · verificationSound: ${latestJudge.result.verificationSound}`)
    L.push(`- reasoning: ${clip(latestJudge.result.reasoning, 300)}`)
    const reopenKeywords = (latestJudge.result.reopenRabbitHoles || []).map((r) => r.keyword).filter(Boolean)
    if (reopenKeywords.length) L.push(`- reopen lane keywords: ${clip(reopenKeywords.join('; '), 300)}`)
  }
  return L.join('\n') + '\n'
}

function buildFindingsReport(ctx) {
  const { runId, journalPath, completions, flagInfo } = ctx
  const L = []
  L.push(`# RR mid-run findings — ${runId}`)
  L.push('')
  L.push(`- journal: ${journalPath}`)

  const latestSynth = completions.filter((c) => c.fp.kind === 'synthesiser').slice(-1)[0]
  const latestCoord = completions.filter((c) => c.fp.kind === 'brainerCoord').slice(-1)[0]
  const degraded = flagInfo.finalizeCount > 0 && flagInfo.coordCount === 0

  L.push(`- reconstruction mode: ${latestCoord ? 'primary (brainer coord memory)' : 'DEGRADED fallback (no brainer coord ever seen)'}`)
  L.push('')

  if (latestSynth) {
    L.push('## Synthesis — run is effectively done')
    L.push('')
    L.push(`- verdict: ${clip(latestSynth.result.verdict, 500)}`)
    L.push(`- confidence: ${latestSynth.result.confidence}`)
    L.push(listSection('openQuestions', latestSynth.result.openQuestions))
    L.push('')
  }

  if (latestCoord) {
    const r = latestCoord.result
    const rsf = r.resultSoFar || {}
    L.push('## Primary — latest brainer coord memory')
    L.push('')
    L.push(`- answer: ${clip(rsf.answer, 4000)}`)
    L.push(`- confidence: ${rsf.confidence}`)
    L.push(`- keyClaimIds: ${(rsf.keyClaimIds || []).length}`)
    L.push(listSection('resolved', rsf.resolved))
    L.push(listSection('openGaps', rsf.openGaps))
    L.push(listSection('tensions', rsf.tensions))
    L.push(`- stop: ${clip(r.stop, 300)}`)
  } else {
    L.push('## DEGRADED fallback — reconstructed from finalize agents (learned on the maiden v3.2.0 flight)')
    L.push('')
    const initiatorEntry = completions.filter((c) => c.fp.kind === 'initiator').slice(-1)[0]
    if (initiatorEntry) {
      const facts = (initiatorEntry.result.refinement && initiatorEntry.result.refinement.facts) || []
      L.push(`### Initiator fact groups (${facts.length})`)
      L.push('')
      for (const f of facts) L.push(`- ${clip(f.fact, 220)}`)
      L.push('')
    }
    const refinerEntries = completions.filter((c) => c.fp.kind === 'refiner')
    if (refinerEntries.length) {
      L.push(`### Refiner passes (${refinerEntries.length})`)
      L.push('')
      for (const rf of refinerEntries) {
        const firstLine = ((rf.result.report || '').split('\n').find((l) => l.trim()) || '').trim()
        L.push(`- ${clip(firstLine, 150)} — counterFound: ${rf.result.counterFound}`)
      }
      L.push('')
    }
    const latestJudge = flagInfo.judgeEntries.slice(-1)[0]
    if (latestJudge) {
      L.push('### Judge')
      L.push('')
      L.push(`- goalMet: ${latestJudge.result.goalMet} · verificationSound: ${latestJudge.result.verificationSound}`)
      L.push(`- reasoning: ${clip(latestJudge.result.reasoning, 500)}`)
      const reopenKeywords = (latestJudge.result.reopenRabbitHoles || []).map((r) => r.keyword).filter(Boolean)
      if (reopenKeywords.length) L.push(`- reopen lane keywords: ${clip(reopenKeywords.join('; '), 300)}`)
      if (latestJudge.result.reopenDirective) L.push(`- reopenDirective: ${clip(latestJudge.result.reopenDirective, 300)}`)
      L.push('')
    }
    const landscape = mergedLandscape(completions)
    if (landscape) {
      L.push('### Merged scout landscape')
      L.push('')
      L.push(clip(landscape, 800))
      L.push('')
    }
  }

  L.push('')
  L.push('## Claims tally (pre-dedup emissions, not the ledger count)')
  L.push('')
  let claimsTotal = 0
  let claimsFrom = 0
  for (const c of completions) {
    if ((c.fp.kind === 'scoutSweep' || c.fp.kind === 'laneReader') && Array.isArray(c.result.claims)) {
      claimsTotal += c.result.claims.length
      claimsFrom++
    }
  }
  L.push(`- total: ${claimsTotal} (across ${claimsFrom} scout sweeps + lane readers)`)

  return L.join('\n') + '\n'
}

function buildHeadline(ctx) {
  const { completions, flagInfo } = ctx
  const latestSynth = completions.filter((c) => c.fp.kind === 'synthesiser').slice(-1)[0]
  const degraded = flagInfo.finalizeCount > 0 && flagInfo.coordCount === 0
  if (degraded) return 'DEGRADED — finalize agents seen (initiator/refiner/judge) but zero brainer coords; wave-0 brainer likely dead.'
  if (latestSynth) return `LANDING — synthesiser has produced a report (confidence ${latestSynth.result.confidence}).`
  if (flagInfo.judgeFalseCount > 1) return `JUDGE LOOP — ${flagInfo.judgeFalseCount} judge rejections, remediation cycling.`
  if (flagInfo.coordCount > 0) return `Healthy — ${flagInfo.coordCount} brainer coord(s) seen, ${flagInfo.schedulerCount} wave(s) scheduled.`
  return `Early — ${completions.length} agent(s) completed so far, no coord/finalize seen yet.`
}

// ---------------------------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------------------------
function main() {
  const { mode, pathArg } = parseArgs(process.argv.slice(2))

  let journalPath
  if (pathArg) {
    journalPath = resolveGivenPath(pathArg)
  } else {
    journalPath = autoDiscoverJournal()
    if (!journalPath) {
      console.error('no workflow run found under <home>/.cc/{account}/projects/{project}/{sessionId}/subagents/workflows/wf_{id}/journal.jsonl')
      process.exit(1)
    }
    console.error(`[auto-discovered] ${journalPath}`)
  }

  if (!fs.existsSync(journalPath)) {
    console.error(`journal not found: ${journalPath}`)
    process.exit(1)
  }

  const runDir = path.dirname(journalPath)
  const runId = path.basename(runDir)

  const { entries, corrupt } = loadJournal(journalPath)
  if (!entries.length) {
    console.error(`journal is empty: ${journalPath}`)
    process.exit(1)
  }

  const { records, started, pendingAgentIds, completions, tally, unknownList } = classify(entries)
  const { inFlight, staleDead } = splitPending(pendingAgentIds, started, runDir)
  const flagInfo = computeFlags(completions, tally)

  const firstAgentId = records[0].agentId
  const firstTs = statMtime(path.join(runDir, `agent-${firstAgentId}.meta.json`)) || statMtime(path.join(runDir, `agent-${firstAgentId}.jsonl`))
  const lastTs = statMtime(journalPath)

  const ctx = { runId, journalPath, entries, corrupt, firstTs, lastTs, completions, tally, inFlight, staleDead, flagInfo, unknownList }

  const reportMd = mode === 'findings' ? buildFindingsReport(ctx) : buildStatusReport(ctx)
  const degraded = flagInfo.finalizeCount > 0 && flagInfo.coordCount === 0
  const headline = buildHeadline(ctx)

  const repoRoot = getRepoRoot()
  const outDir = path.join(repoRoot, 'tmp', 'rr-midrun')
  fs.mkdirSync(outDir, { recursive: true })
  const reportPath = path.join(outDir, `${runId}-${mode}-${hhmmss(new Date())}.md`)
  fs.writeFileSync(reportPath, reportMd)

  console.log(JSON.stringify({
    report: reportPath,
    run: runId,
    mode,
    agents: { completed: completions.length, inFlight: inFlight.length, staleDead: staleDead.length },
    waves: { scheduled: flagInfo.schedulerCount, coordinated: flagInfo.coordCount },
    degraded,
    headline,
  }, null, 2))
}

try {
  main()
} catch (e) {
  console.error(`midrun.js error: ${e.message}`)
  process.exit(1)
}
