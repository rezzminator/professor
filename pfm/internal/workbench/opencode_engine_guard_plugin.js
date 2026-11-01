import { spawn } from "node:child_process"

// Engine guard for every OpenCode chat, main session and sub-agent alike:
// before a write tool runs, the pfm binary's guard (`pfm internal
// engine-guard --engine opencode`, the call as JSON on stdin) judges it, and
// a deny throws, which OpenCode hands the model as the tool's error. Git
// writes and framework-file writes are Claude's alone. The binary path is the
// absolute one pfm staged this file with. A guard that cannot run lets the
// call through, logged, as Claude's guards fail open.
const PFM_BINARY = "__PFM_BINARY__"
const ENTRY = ["internal", "engine-guard", "--engine", "opencode"]
const TOOLS = new Set(["bash", "edit", "write", "apply_patch", "multiedit", "patch"])
const GUARD_TIMEOUT_MS = 10000
const SERVICE = "pfm-engine-guard"

// runGuard runs the guard with payload on its stdin, off OpenCode's event
// loop, and resolves its error, or its status, signal, stdout and stderr;
// it never rejects. A guard past GUARD_TIMEOUT_MS is killed.
function runGuard(payload) {
  return new Promise((resolve) => {
    let child
    try {
      child = spawn(PFM_BINARY, ENTRY, { stdio: ["pipe", "pipe", "pipe"], timeout: GUARD_TIMEOUT_MS })
    } catch (error) {
      resolve({ error })
      return
    }
    let stdout = ""
    let stderr = ""
    child.stdout.setEncoding("utf8").on("data", (chunk) => { stdout += chunk })
    child.stderr.setEncoding("utf8").on("data", (chunk) => { stderr += chunk })
    child.on("error", (error) => resolve({ error }))
    child.on("close", (status, signal) => resolve({ status, signal, stdout, stderr }))
    // A guard that exits before reading its stdin is judged by its status.
    child.stdin.on("error", () => {})
    child.stdin.end(payload)
  })
}

// denyReason is the reason of the guard's PreToolUse deny, "" for an allow.
function denyReason(stdout) {
  if (!stdout || !stdout.trim()) return ""
  const output = JSON.parse(stdout)?.hookSpecificOutput
  return output?.permissionDecision === "deny" ? String(output.permissionDecisionReason || "denied") : ""
}

export const PfmEngineGuard = async ({ client, directory } = {}) => {
  const warn = (message) => {
    try {
      return Promise.resolve(client?.app?.log?.({ body: { service: SERVICE, level: "warn", message } })).catch(() => {})
    } catch {
      return Promise.resolve()
    }
  }
  return {
    "tool.execute.before": async (input, output) => {
      if (!TOOLS.has(input?.tool)) return
      const payload = JSON.stringify({ tool_name: input.tool, tool_input: output?.args ?? {}, cwd: directory ?? "" })
      const result = await runGuard(payload)
      if (result.error || result.status !== 0) {
        await warn(`engine guard did not run (fail-open): ${result.error ?? (result.stderr || `killed by ${result.signal}`)}`)
        return
      }
      let reason
      try {
        reason = denyReason(result.stdout)
      } catch (error) {
        await warn(`engine guard answer unreadable (fail-open): ${error}`)
        return
      }
      if (reason) throw new Error(reason)
    },
  }
}
