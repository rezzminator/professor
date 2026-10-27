import { spawn, spawnSync } from "node:child_process"

// Herdr reporter for a pfm OpenCode chat: every state change runs the pfm
// binary's one reporter entry (`pfm internal herdr-state --engine opencode
// <verb>`, JSON on stdin), which decides whether a Herdr pane is attached and
// is a silent no-op when none is. The binary path is the absolute one pfm
// staged this file with, never a PATH name.
const PFM_BINARY = "__PFM_BINARY__"
const ENTRY = ["internal", "herdr-state", "--engine", "opencode"]
const REPORT_TIMEOUT_MS = 5000
const SERVICE = "pfm-herdr"

const STATUS_VERB = new Map([
  ["busy", "working"],
  ["retry", "working"],
  ["idle", "idle"],
])
const EVENT_VERB = new Map([
  ["permission.asked", "blocked"],
  ["question.asked", "blocked"],
  ["permission.replied", "working"],
  ["question.replied", "working"],
  ["session.idle", "idle"],
])
const DISPOSE_EVENTS = new Set(["server.instance.disposed", "global.disposed"])
// OpenCode titles a session "New session - {ISO time}" (a sub-session "Child
// session - {ISO time}") until it is renamed; that placeholder is no title.
const DEFAULT_TITLE = /^(New session - |Child session - )\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$/

function sessionIDOf(properties) {
  if (typeof properties?.sessionID === "string" && properties.sessionID) return properties.sessionID
  if (typeof properties?.info?.id === "string" && properties.info.id) return properties.info.id
  return undefined
}

// askedMessage is what the pane shows while the chat waits on the user.
function askedMessage(type, properties) {
  if (type === "question.asked") {
    const question = properties?.questions?.[0]?.question
    return typeof question === "string" && question ? question : "question asked"
  }
  if (typeof properties?.title === "string" && properties.title) return properties.title
  if (typeof properties?.permission === "string" && properties.permission) {
    const patterns = Array.isArray(properties.patterns) ? properties.patterns.join(", ") : ""
    return "permission: " + properties.permission + (patterns ? " " + patterns : "")
  }
  return "permission requested"
}

export const PfmHerdr = async ({ client } = {}) => {
  // The entry decides, from the Herdr marker or the chat's tmux session
  // environment; with neither present no pane can be attached.
  if (process.env.HERDR_ENV !== "1" && !process.env.TMUX) return {}

  // A session is the chat's main one only when its info carries no parentID.
  // Parentage comes from the session's created event or, for a session that
  // was created before this plugin loaded (a resumed chat), from one
  // session.get; a session whose parentage is still unknown reports nothing.
  const children = new Set()
  const mains = new Set()
  // The last title forwarded per main session; cleared when the pane is
  // released, so a restarted pane gets the title again.
  const titles = new Map()
  let root
  let last = ""
  let queue = Promise.resolve()

  const log = (level, message) => {
    try {
      return Promise.resolve(client?.app?.log?.({ body: { service: SERVICE, level, message } })).catch(() => {})
    } catch {
      return Promise.resolve()
    }
  }

  const run = (verb, payload) =>
    new Promise((resolve) => {
      let settled = false
      let stderr = ""
      const done = () => {
        if (!settled) {
          settled = true
          resolve()
        }
      }
      let child
      try {
        child = spawn(PFM_BINARY, [...ENTRY, verb], { stdio: ["pipe", "ignore", "pipe"] })
      } catch (error) {
        log("error", "spawn " + PFM_BINARY + " " + verb + ": " + error.message).then(done)
        return
      }
      const timer = setTimeout(() => {
        child.kill("SIGKILL")
        log("error", PFM_BINARY + " " + verb + " did not finish in " + REPORT_TIMEOUT_MS + " ms").then(done)
      }, REPORT_TIMEOUT_MS)
      child.stderr.on("data", (chunk) => {
        stderr += chunk
      })
      child.on("error", (error) => {
        clearTimeout(timer)
        log("error", "spawn " + PFM_BINARY + " " + verb + ": " + error.message).then(done)
      })
      child.on("close", () => {
        clearTimeout(timer)
        if (stderr.trim()) log("warn", PFM_BINARY + " " + verb + ": " + stderr.trim()).then(done)
        else done()
      })
      child.stdin.on("error", () => {})
      child.stdin.end(JSON.stringify(payload))
    })

  // Reports run one at a time, in event order: the entry stamps each with
  // its start time, and Herdr drops a report older than the last it took.
  const report = (verb, sessionID, fields = {}) => {
    const key = verb + "\n" + sessionID + "\n" + JSON.stringify(fields)
    if (key === last) return queue
    last = key
    const payload = { session_id: sessionID, ...fields }
    queue = queue.then(() => run(verb, payload))
    return queue
  }

  const lookup = async (sessionID) => {
    if (typeof client?.session?.get !== "function") return false
    try {
      const response = await client.session.get({ path: { id: sessionID } })
      const info = response?.data ?? response
      if (!info || info.id !== sessionID) return false
      ;(info.parentID ? children : mains).add(sessionID)
      return true
    } catch (error) {
      await log("warn", "session.get " + sessionID + ": " + error.message)
      return false
    }
  }

  const start = (sessionID) => {
    if (root === sessionID) return queue
    root = sessionID
    last = ""
    return report("start", sessionID)
  }

  process.on("exit", () => {
    if (!root) return
    const result = spawnSync(PFM_BINARY, [...ENTRY, "end"], {
      input: JSON.stringify({ session_id: root }),
      timeout: 1500,
      stdio: ["pipe", "ignore", "ignore"],
    })
    root = undefined
    if (result.error) process.stderr.write("pfm-herdr: end report failed: " + result.error.message + "\n")
  })

  return {
    // OpenCode's chat.message is prompt creation, not history replay. Only
    // session identity leaves this hook; output can contain private text.
    "chat.message": async (input) => {
      const sessionID = input?.sessionID
      if (typeof sessionID !== "string" || !sessionID || children.has(sessionID)) return
      if (!mains.has(sessionID) && !(await lookup(sessionID))) return
      if (children.has(sessionID)) return
      await start(sessionID)
      // Submits are distinct even when consecutive state reports dedupe.
      last = ""
      await report("working", sessionID, { hook_event_name: "chat.message" })
    },
    event: async ({ event }) => {
      const type = event?.type
      const properties = event?.properties ?? {}
      const info = properties.info
      if (info?.id && type === "session.created") (info.parentID ? children : mains).add(info.id)
      if (DISPOSE_EVENTS.has(type)) {
        if (!root) return
        const ended = root
        root = undefined
        last = ""
        titles.clear()
        await report("end", ended)
        return
      }
      const sessionID = sessionIDOf(properties)
      if (!sessionID || children.has(sessionID)) return
      if (!mains.has(sessionID) && !(await lookup(sessionID))) return
      if (children.has(sessionID)) return
      if (type === "session.created") {
        await start(sessionID)
        return
      }
      if (type === "session.updated") {
        const title = info?.title
        if (typeof title !== "string" || !title || DEFAULT_TITLE.test(title) || titles.get(sessionID) === title) return
        titles.set(sessionID, title)
        await start(sessionID)
        await report("title", sessionID, { title })
        return
      }
      const verb = type === "session.status" ? STATUS_VERB.get(properties.status?.type) : EVENT_VERB.get(type)
      if (!verb) return
      await start(sessionID)
      await report(verb, sessionID, verb === "blocked" ? { message: askedMessage(type, properties) } : {})
    },
  }
}
