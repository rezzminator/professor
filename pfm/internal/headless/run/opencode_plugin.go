package run

// openCodePluginSource is deliberately dependency-free. OpenCode loads it
// from a file URL supplied in OPENCODE_CONFIG_CONTENT. The module has no
// package dependency, and sealed settings cannot add an inherited plugin.
const openCodePluginSource = `import fs from "node:fs"

let schemaFormat
let primarySessionID
let primaryMessageID

function readOptional(name) {
  const path = process.env[name]
  if (!path) return undefined
  return fs.readFileSync(path, "utf8")
}

function readSchema() {
  const text = readOptional("PFM_OPENCODE_SCHEMA_FILE")
  return text === undefined ? undefined : JSON.parse(text)
}

function readAllowedTools() {
  const text = process.env.PFM_OPENCODE_ALLOWED_TOOLS_JSON
  return text === undefined ? undefined : JSON.parse(text)
}

function readCatalogTools() {
  const path = process.env.PFM_OPENCODE_TOOL_IDS_FILE
  if (!path) return []
  const text = fs.readFileSync(path, "utf8")
  const tools = JSON.parse(text)
  if (!Array.isArray(tools) || tools.some((tool) => typeof tool !== "string")) throw new Error("pfm OpenCode tool catalog is invalid")
  return tools
}

function writeAtomic(path, text) {
  const temporary = path + ".tmp-" + process.pid + "-" + Math.random().toString(16).slice(2)
  try {
    fs.writeFileSync(temporary, text, { mode: 0o600 })
    fs.renameSync(temporary, path)
  } catch (error) {
    let cleanupError
    try {
      fs.unlinkSync(temporary)
    } catch (candidate) {
      if (candidate?.code !== "ENOENT") cleanupError = candidate
    }
    if (cleanupError) throw new Error("write " + path + " failed: " + error.message + "; remove temporary marker failed: " + cleanupError.message, { cause: error })
    throw new Error("write " + path + " failed: " + error.message, { cause: error })
  }
}

function writeJSONAtomic(path, value) {
  writeAtomic(path, JSON.stringify(value) + "\n")
}

export default async function pfmHeadlessPlugin({ client } = {}) {
  const ready = process.env.PFM_OPENCODE_PLUGIN_READY
  if (!ready) throw new Error("pfm OpenCode plugin handshake path is missing")

  // Read all controlled files before publishing readiness. OpenCode can log
  // and continue after a plugin failure; the marker makes that state visible
  // to the parent runner instead of allowing a false successful result.
  const system = readOptional("PFM_OPENCODE_SYSTEM_FILE")
  const schema = readSchema()
  const allowedTools = readAllowedTools()
  const strictMcp = process.env.PFM_OPENCODE_STRICT_MCP === "1"
  const noSessionPersistence = process.env.PFM_OPENCODE_NO_SESSION_PERSISTENCE === "1"
  const sessions = new Set()
  const autoPermission = process.env.PFM_OPENCODE_AUTO_PERMISSION === "1"
  const assistantPath = process.env.PFM_OPENCODE_ASSISTANTS_FILE

  function recordPrimary(input, output) {
    if (primarySessionID || !assistantPath) return
    primarySessionID = input.sessionID
    primaryMessageID = output.message.id
    writeJSONAtomic(assistantPath, { sessionID: primarySessionID, userMessageID: primaryMessageID, assistantIDs: [] })
  }

  function recordAssistant(event) {
    if (!assistantPath) return
    const properties = event?.properties
    if (event?.type === "session.error") {
      if (properties?.sessionID === primarySessionID) {
        let state = { sessionID: primarySessionID, userMessageID: primaryMessageID, assistantIDs: [] }
        try {
          state = JSON.parse(fs.readFileSync(assistantPath, "utf8"))
        } catch (error) {
          if (error?.code !== "ENOENT") throw error
        }
        state.error = properties?.error
        writeJSONAtomic(assistantPath, state)
      }
      return
    }
    if (event?.type !== "message.updated" || properties?.info?.role !== "assistant" || properties.info.sessionID !== primarySessionID || properties.info.parentID !== primaryMessageID) return
    let state = { sessionID: properties.info.sessionID, assistantIDs: [] }
    try {
      state = JSON.parse(fs.readFileSync(assistantPath, "utf8"))
    } catch (error) {
      if (error?.code !== "ENOENT") throw error
    }
    state.sessionID = properties.info.sessionID
    state.userMessageID = primaryMessageID
    state.assistantIDs ??= []
    if (!state.assistantIDs.includes(properties.info.id)) state.assistantIDs.push(properties.info.id)
    writeJSONAtomic(assistantPath, state)
  }

  return {
    event: async ({ event }) => {
      recordAssistant(event)
      const permission = event?.properties
      if (event?.type !== "permission.asked" || !sessions.has(permission?.sessionID)) return
      try {
        const response = await client.postSessionIdPermissionsPermissionId({ path: { id: permission.sessionID, permissionID: permission.id }, body: { response: autoPermission ? "once" : "reject" } })
        if (response.error) throw new Error(JSON.stringify(response.error))
      } catch (error) {
        const message = "pfm OpenCode permission reply failed: " + error.message
        recordAssistant({ type: "session.error", properties: { sessionID: primarySessionID, error: { name: "UnknownError", data: { message } } } })
        throw new Error(message, { cause: error })
      }
    },
    config(config) {
      if (strictMcp) config.mcp = {}
      if (noSessionPersistence) {
        config.share = "disabled"
        config.snapshot = false
      }
      if (schema !== undefined) {
        // Preserve every inherited guard and only permit OpenCode's synthetic
        // structured-output tool, which wildcard deny otherwise hides.
        config.permission = { ...(config.permission ?? {}), StructuredOutput: "allow" }
        if (config.agent && typeof config.agent === "object") {
          for (const agent of Object.values(config.agent)) {
            if (agent && typeof agent === "object") {
              agent.permission = { ...(agent.permission ?? {}), StructuredOutput: "allow" }
            }
          }
        }
      }
      // Publish readiness only after every controlled config mutation has
      // succeeded. OpenCode may log a plugin error and keep serving anyway.
	  writeAtomic(ready, "pfm-opencode-plugin-ready\n")
    },
    "experimental.chat.system.transform": async (_input, output) => {
      if (system !== undefined) output.system.splice(0, output.system.length, system)
    },
    "chat.message": async (input, output) => {
      let schemaSeed = false
      if (allowedTools !== undefined) {
        const builtinTools = [
          "invalid", "question", "bash", "read", "glob", "grep", "edit", "write",
          "task", "webfetch", "todowrite", "websearch", "skill", "apply_patch", "lsp", "plan", "execute", "StructuredOutput",
        ]
		const exact = new Set(allowedTools)
		output.message.tools = { ...(output.message.tools ?? {}) }
		for (const tool of Object.keys(output.message.tools)) output.message.tools[tool] = exact.has(tool)
		for (const tool of [...builtinTools, ...readCatalogTools()]) output.message.tools[tool] = exact.has(tool)
      }
      if (schema !== undefined) {
        // OutputFormatJsonSchema is an Effect Schema class. A plain object
        // produced here fails OpenCode's branded message validation. The
        // runner first sends a noReply REST prompt, whose public decoder
        // constructs the real class; reuse that instance for the CLI run.
        if (output.message.format?.type === "json_schema") {
          schemaSeed = true
          schemaFormat = output.message.format
          const schemaReady = process.env.PFM_OPENCODE_SCHEMA_READY
          if (schemaReady) writeAtomic(schemaReady, "pfm-opencode-schema-ready\n")
		} else if (schemaFormat !== undefined) {
			if (!primarySessionID || input.sessionID === primarySessionID) output.message.format = schemaFormat
        } else {
          throw new Error("pfm OpenCode schema format was not primed through the public REST decoder")
        }
      }
      if (!schemaSeed) {
        sessions.add(input.sessionID)
        recordPrimary(input, output)
      }
    },
    "chat.params": async (input, output) => {
      const effort = process.env.PFM_OPENCODE_EFFORT
      if (effort && input.model?.providerID === "openai") {
        // --variant only selects a configured OpenCode model variant. An
        // openai model without one still needs the provider option to carry
        // pfm's requested reasoning effort to the Responses API.
        output.options.reasoningEffort = effort
      }
    },
    "experimental.chat.messages.transform": async (_input, output) => {
      if (allowedTools === undefined) return
      const lastUser = output.messages.findLast((message) => message.info?.role === "user")
      if (!lastUser?.info) return
      const allowed = new Set(allowedTools)
      const original = lastUser.info.tools ?? {}
      lastUser.info.tools = new Proxy(original, {
        get(target, property, receiver) {
          if (typeof property !== "string") return Reflect.get(target, property, receiver)
          return allowed.has(property)
        },
      })
    },
  }
}
`
