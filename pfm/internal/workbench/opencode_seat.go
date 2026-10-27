package workbench

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

const openCodeSeatPlugin = `import { readFile } from "node:fs/promises"

export const WorkbenchSeat = async () => {
  let fleetListed = false
  let launchAgent, launchModel, launchError
  const model = process.env.PFM_OPENCODE_MODEL
  const variant = process.env.PFM_OPENCODE_VARIANT
  const policy = process.env.PFM_OPENCODE_POLICY
  return {
    config: async (config) => {
      fleetListed = Array.isArray(config.instructions) &&
        config.instructions.includes(process.env.PFM_OPENCODE_FLEET_FILE)
      if (!model && !variant) return
      // Patch the selected agent, never substitute a new permissive primary.
      // Native TUI reads agent.model; agent.variant is only the native fallback.
      launchAgent = config.default_agent || "build"
      config.agent ??= {}
      if (!config.agent[launchAgent] && !["build", "plan"].includes(launchAgent)) {
        launchError = new Error("pfm OpenCode default agent " + launchAgent + " is not configured")
        throw launchError
      }
      const agent = config.agent[launchAgent] ??= {}
      // Native falls back to another primary when build is disabled/hidden.
      // Refuse rather than let that agent bypass the requested model/variant.
      if (agent.disable || agent.hidden || agent.mode === "subagent" || (agent.name && agent.name !== launchAgent)) {
        launchError = new Error("pfm OpenCode default agent " + launchAgent + " is not an available primary")
        throw launchError
      }
      launchModel = model ?? agent.model ?? config.model
      if (!launchModel) {
        launchError = new Error("pfm OpenCode effort requires an explicit or configured model")
        throw launchError
      }
      agent.model = launchModel
      if (variant) agent.variant = variant
    },
    "chat.message": async (_input, output) => {
      if (launchError) throw launchError
      // TUI remembered variants beat agent.variant. Pin the saved user message
      // before native request options and assistant metadata read its variant.
      if (variant && output.message.agent === launchAgent) output.message.model.variant = variant
    },
    "chat.params": async (input) => {
      // Native config-hook errors are logged and ignored; keep them visible at
      // the model-call boundary as well, never turn a failed pin into a default.
      if (launchError) throw launchError
      if (!launchAgent || input.agent !== launchAgent) return
      const resolved = input.model.providerID + "/" + input.model.id
      if (resolved !== launchModel) {
        throw new Error("pfm OpenCode requested model " + launchModel + " but resolved " + resolved)
      }
      if (variant && !Object.hasOwn(input.model.variants ?? {}, variant)) {
        throw new Error("pfm OpenCode model " + resolved + " does not support variant " + variant)
      }
      if (variant && input.message.model.variant !== variant) {
        throw new Error("pfm OpenCode requested variant " + variant + " but message carries " + input.message.model.variant)
      }
    },
    "experimental.chat.system.transform": async (_input, output) => {
      if (policy) output.system.push(policy)
      const path = process.env.PFM_OPENCODE_SYSTEM_FILE
      if (!path) return
      let system
      try {
        system = await readFile(path, "utf8")
      } catch (error) {
        throw new Error("read workbench system prompt " + path + ": " + error.message, { cause: error })
      }
      system = system.trim()
      if (!system) throw new Error("workbench system prompt " + path + " is empty")
      const fleetPath = process.env.PFM_OPENCODE_FLEET_FILE
      if (!fleetPath) return
      let fleet
      try {
        fleet = (await readFile(fleetPath, "utf8")).trim()
      } catch {
        return
      }
      if (!fleet) return
      for (let i = 0; i < output.system.length; i++) {
        const part = output.system[i]
        const offset = part.indexOf(fleet)
        if (offset < 0) continue
        let start = offset
        const lineEnd = part.lastIndexOf("\n", offset - 1)
        const lineStart = part.lastIndexOf("\n", lineEnd - 1) + 1
        if (lineEnd >= 0 && part.slice(lineStart, lineEnd).startsWith("Instructions from: ")) {
          start = lineStart
        }
        let end = offset + fleet.length
        while (end < part.length && !part[end].trim()) end++
        const remaining = part.slice(0, start) + part.slice(end)
        if (remaining.trim()) output.system[i] = remaining
        else output.system.splice(i, 1)
        return
      }
      if (fleetListed && output.system.some((part) => part.includes(system))) {
        throw new Error("pfm workbench seat: OpenCode's system prompt carries the workbench prompt " + path +
          " but not the fleet prompt " + fleetPath + " its instructions list; its text was reshaped and cannot be replaced")
      }
    },
  }
}

`

// StageOpenCodeSeatPlugin atomically replaces an absent or stale seat plugin.
func StageOpenCodeSeatPlugin(path string) error {
	body, err := os.ReadFile(path)
	if err == nil && string(body) == openCodeSeatPlugin {
		return nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		wrapped := fmt.Errorf("read OpenCode workbench plugin %s: %w", path, err)
		obs.Logger(context.Background()).Error("workbench seat plugin", "path", path, obs.FieldErr, wrapped)
		return wrapped
	}
	if err := atomicfile.Write(path, []byte(openCodeSeatPlugin), 0o600); err != nil {
		obs.Logger(context.Background()).Error("workbench seat plugin", "path", path, obs.FieldErr, err)
		return err
	}
	return nil
}
