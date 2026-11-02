package update

// chatlogSuccessor is what replaced the /tokens command and the transcript skill.
const chatlogSuccessor = "the chatlog skill"

// forwardCompactionSuccessor is what the forward-compaction plugin serves in
// place of sub-agent-compact's tools.
const forwardCompactionSuccessor = "mcp__forward-compaction__forward_compaction / mcp__forward-compaction__look_back"

// flightsSuccessor takes new work since /flights:spec retired, so the /wave
// family, the wave-builder skill and /imagine:build-spec point there too.
const flightsSuccessor = "the flights-foreman agent"

// pcmSuccessor is what replaced the /pcm command: dispatching its agent.
const pcmSuccessor = "the pcm agent"

// imagineSuccessor is what replaced /imagine:build, /imagine:build-fast,
// /imagine:callmeter, /ultimate-spec and the ultimate visualization skill.
const imagineSuccessor = "/imagine"

// retiredNames is the one registry of retired names `pfm doctor
// --project-updates` scans a project's files for. Every entry cites
// the release note or commit that retired it. A retired name that is also a
// live one, or an everyday word or route, is left out with its reason: a
// scan that false-hits trains the reader to skip it.
//
// Left out: bare `/pfm` (live again as the machine-global pfm CLI guide,
// releases/v0.78.0.md "Project: /pfm becomes /pcm" — the retired change
// manager is matched by its project file instead); `/pm` and `/git`
// (releases/v0.78.0.md "Project: retired project surface") and the
// `developer`, `qa`, `architect` and `scheduler` agents (releases/v0.78.0.md
// "Global: flights replaces wave", "Project: flights cast replaces
// per-project developer and qa"), all ordinary words, paths or routes.
var retiredNames = []retiredName{
	// chat-call: split family registrations and rename chat delivery.
	{
		name:      "mcp__professor__",
		kind:      retiredTool,
		successor: "mcp__chat-professor__* / mcp__plugin_harvester_harvester__*",
		match:     matchPrefix,
	},
	{name: "pfm chat inject", kind: retiredCommand, successor: "pfm chat call", match: matchExact},
	{name: "chat_inject", kind: retiredTool, successor: "mcp__chat-professor__call", match: matchExact},
	// releases/v0.78.0.md "Global: flights replaces wave": every /wave:*
	// command and the wave-builder skill are removed. The bare /wave pair
	// retired earlier (releases/v0.43.0.md). New work started with
	// /flights:spec until it retired too (below): flights-foreman takes it now.
	{name: "/wave", kind: retiredCommand, successor: flightsSuccessor, match: matchFamily},
	{name: "wave-builder", kind: retiredSkill, successor: flightsSuccessor, match: matchExact},
	// releases/v0.77.0.md "B: templates — /jc and /qa:live are gone"; commit
	// e7c8b6a2 "retire /jc, /qa:live and /pfm:refresh". /jc:wave was its
	// namespace (releases/v0.32.0.md).
	{name: "/jc", kind: retiredCommand, match: matchFamily},
	{name: "/qa:live", kind: retiredCommand, match: matchExact},
	// Same note: "gitter renames JC-COMMIT to COMMIT"; "/documenter's
	// JC-UPDATE mode is renamed FIX-UPDATE", and /documenter itself retired
	// in releases/v0.78.0.md.
	{name: "JC-COMMIT", kind: retiredPhase, successor: "COMMIT", match: matchExact},
	{name: "JC-UPDATE", kind: retiredPhase, match: matchExact},
	// releases/v0.77.0.md "C: pfm — /pfm:refresh is gone as a command": the
	// refresh pass is pcm's, run on request (the live form per CLAUDE.md
	// § Vocabulary "refresh").
	{
		name:      "/pfm:refresh",
		kind:      retiredCommand,
		successor: pcmSuccessor,
		match:     matchExact,
	},
	// releases/v0.77.0.md "/quality:md-forlint (renamed from /quality:forlint)".
	{name: "/quality:forlint", kind: retiredCommand, successor: "/md-forlint:use", match: matchExact},
	// The /quality:* family became the /{x}:use commands: each /quality:{x}
	// is now /{x}:use. A name is matched exactly, so the longer
	// /quality:md-forlint never reads as a shorter one.
	{name: "/quality:claude-md", kind: retiredCommand, successor: "/claude-md:use", match: matchExact},
	{
		name: "/quality:claude-selector", kind: retiredCommand,
		successor: "/claude-selector:use", match: matchExact,
	},
	{name: "/quality:description", kind: retiredCommand, successor: "/description:use", match: matchExact},
	{name: "/quality:doc", kind: retiredCommand, successor: "/doc:use", match: matchExact},
	{
		name: "/quality:integration-suite", kind: retiredCommand,
		successor: "/integration-suite:use", match: matchExact,
	},
	{name: "/quality:llm-codebase", kind: retiredCommand, successor: "/llm-codebase:use", match: matchExact},
	{name: "/quality:md-forlint", kind: retiredCommand, successor: "/md-forlint:use", match: matchExact},
	{name: "/quality:prompt", kind: retiredCommand, successor: "/prompt:use", match: matchExact},
	{name: "/quality:rules", kind: retiredCommand, successor: "/rules:use", match: matchExact},
	{
		name: "/iterative-improvement", kind: retiredCommand,
		successor: "/iterative-improvement:use", match: matchExact,
	},
	// releases/v0.78.0.md "Project: /pfm becomes /pcm": commands/pfm.md is
	// renamed commands/pcm.md with its audit scopes inline; that /pcm command
	// retired in turn (below), so both point at the agent it dispatched.
	{name: ".claude/commands/pfm.md", kind: retiredPath, successor: pcmSuccessor, match: matchExact},
	{
		name: ".claude/commands/pfm/references/audit-scopes.md", kind: retiredPath,
		successor: pcmSuccessor, match: matchExact,
	},
	// Commit "remove the /pcm command end to end": the pcm agent is dispatched
	// directly; a project keeping its own commands/pcm.md is not a hit.
	{name: "/pcm", kind: retiredCommand, successor: pcmSuccessor, match: matchExact},
	// releases/v0.78.0.md retired the former chat tool namespace.
	{name: "mcp__chat__", kind: retiredTool, successor: "mcp__chat-professor__*", match: matchPrefix},
	// Standalone plugin registration replaces the server pfm installed.
	{name: "harvester-professor", kind: retiredTool, successor: "harvester", match: matchExact},
	{
		name: "mcp__harvester-professor__", kind: retiredTool,
		successor: "mcp__plugin_harvester_harvester__*", match: matchPrefix,
	},
	// The forward-compaction plugin replaces sub-agent-compact: its server and
	// both of its tools are renamed (forward_handoff is forward_compaction).
	{name: "mcp__sub-agent-compact__", kind: retiredTool, successor: forwardCompactionSuccessor, match: matchPrefix},
	// releases/v0.62.0.md "Removed: the host-level /chat:* Claude
	// slash-command family"; "drive chat through the MCP tools instead".
	{name: "/chat:", kind: retiredCommand, successor: "mcp__chat-professor__*", match: matchPrefix},
	// releases/v0.78.0.md "Project: retired project surface": /documenter and
	// /documenter:archive ("Doc consolidation is the main session's work
	// under /quality:doc"), /km, /audit:ai-output and six agents.
	{name: "/documenter", kind: retiredCommand, successor: "/doc:use", match: matchFamily},
	{name: "/km", kind: retiredCommand, match: matchExact},
	{name: "/audit:ai-output", kind: retiredCommand, match: matchExact},
	// Commit "remove /rnd end to end": the project-scope research command and
	// its former /rnd:hammer and /rnd:referee sub-commands have no successor.
	{name: "/rnd", kind: retiredCommand, match: matchFamily},
	// Commit db16bb63 "replace context-meter and context-audit commands with
	// one user-only initial-context audit skill".
	{name: "/context-meter", kind: retiredCommand, successor: "/context-audit", match: matchExact},
	// Commit "prune project templates": the project-tier /mentor, /marketer and
	// /officer command templates are gone, with no successor.
	{name: "/mentor", kind: retiredCommand, match: matchExact},
	{name: "/marketer", kind: retiredCommand, match: matchExact},
	{name: "/officer", kind: retiredCommand, match: matchExact},
	{name: "mono-architect", kind: retiredAgent, match: matchExact},
	{name: "mono-documenter", kind: retiredAgent, match: matchExact},
	{name: "mono-planner", kind: retiredAgent, match: matchExact},
	{name: "rndier", kind: retiredAgent, match: matchExact},
	{name: "role-wrapper", kind: retiredAgent, match: matchExact},
	{name: "qa-wrapper", kind: retiredAgent, match: matchExact},
	// releases/v0.78.0.md "Global: rr family": "rr-super is retired"; its
	// stronger variant is super-rr.
	{name: "rr-super", kind: retiredAgent, successor: "super-rr", match: matchExact},
	// Commit 4bb529c7 "one tracer — the tracer-pro and tracer-pro-max tiers
	// are gone". Two rows: a match followed by `-` is a longer name, so
	// tracer-pro never hits tracer-pro-max.
	{name: "tracer-pro", kind: retiredAgent, successor: "tracer", match: matchExact},
	{name: "tracer-pro-max", kind: retiredAgent, successor: "tracer", match: matchExact},
	// Commit f0c88ffb "tracer-rr is clone-distiller".
	{name: "tracer-rr", kind: retiredAgent, successor: "clone-distiller", match: matchExact},
	// Commit 3b401f24: the twin agents take their originals' full names; terra retires.
	// No successor: gpt-flights-lander, the name gpt-lander once took, retired with the twins.
	{name: "gpt-lander", kind: retiredAgent, match: matchExact},
	{name: "gpt-executor", kind: retiredAgent, match: matchExact},
	// The flights family cut (docs/design/flights/flights.md § The family): the
	// foreman-pro twins, the mechanical executors and the gpt speccer retire, and
	// so do /flights:spec, /flights:init and /flights:orchestrate-cross-harness.
	// None has a successor: the agents they once pointed at retired too.
	// flights-speccer is no row: commit 5c7d67f1 restored it, and it ships.
	{name: "foreman-pro", kind: retiredAgent, match: matchExact},
	{name: "gpt-foreman-pro", kind: retiredAgent, match: matchExact},
	{name: "flights-mechanical-executor", kind: retiredAgent, match: matchExact},
	{name: "gpt-flights-mechanical-executor", kind: retiredAgent, match: matchExact},
	{name: "gpt-flights-speccer", kind: retiredAgent, match: matchExact},
	{name: "/flights:spec", kind: retiredCommand, match: matchExact},
	{name: "/flights:init", kind: retiredCommand, match: matchExact},
	{name: "/flights:orchestrate-cross-harness", kind: retiredCommand, match: matchExact},
	// The one lander (docs/design/flights/flights-lander.md): its size tiers
	// retire, flights-lander landing every worktree.
	{name: "flights-lander-mini", kind: retiredAgent, successor: "flights-lander", match: matchExact},
	{name: "flights-lander-pro", kind: retiredAgent, successor: "flights-lander", match: matchExact},
	retiredAgentRow("terra", "", `subagent_type: "terra"`),
	retiredAgentRow("terra", "", `subagent_type:"terra"`),
	retiredAgentRow("terra", "", `subagent_type: 'terra'`),
	retiredAgentRow("terra", "", "subagent_type: terra"),
	retiredAgentRow("terra", "", `subagent_type="terra"`),
	retiredAgentRow("terra", "", `subagent_type": "terra"`),
	retiredAgentRow("terra", "", `subagent_type":"terra"`),
	retiredAgentRow("terra", "", "agents/terra.md"),
	retiredAgentRow("terra", "", "`terra` agent"),
	retiredAgentRow("terra", "", "`terra` sub-agent"),
	retiredAgentRow("terra", "", "`terra` subagent"),
	// Commit 73f2ae2b "/reminder becomes /pfm:schedule".
	{name: "pfm chat reminder", kind: retiredCommand, successor: "pfm chat schedule", match: matchExact},
	{name: "build-codex.mjs", kind: retiredPath, successor: "engine-sync codex build|check", match: matchExact},
	{name: "scripts/codex-sync.sh", kind: retiredPath, successor: "engine-sync codex", match: matchExact},
	{name: "/reminder", kind: retiredCommand, successor: "/pfm:schedule", match: matchFamily},
	// Commit b9f530c9 "/tokens and the transcript skill become one skill":
	// chatlog, over `pfm tokens` and `pfm transcript` (both live). The
	// transcript skill is named by its path or as "transcript skill" only —
	// the bare word is everyday prose.
	{name: "/tokens", kind: retiredCommand, successor: chatlogSuccessor, match: matchFamily},
	{name: "skills/transcript", kind: retiredSkill, successor: chatlogSuccessor, match: matchExact},
	{name: "transcript skill", kind: retiredSkill, successor: chatlogSuccessor, match: matchExact},
	// Commit 3fb6eda4 "retire pfm transcript counts|types and pfm tokens --timeline":
	// the chatlog kit's prep reads Claude, Codex and OpenCode itself (TOOLS rows,
	// timeline.txt). pfm transcript show|locate|agents and pfm tokens stay live.
	// Known gap: the scan matches the exact phrase `pfm tokens --timeline`; the flag split from
	// `pfm tokens` by other flags is not flagged.
	{name: "pfm transcript counts", kind: retiredCommand, successor: chatlogSuccessor, match: matchExact},
	{name: "pfm transcript types", kind: retiredCommand, successor: chatlogSuccessor, match: matchExact},
	{name: "pfm tokens --timeline", kind: retiredCommand, successor: chatlogSuccessor, match: matchExact},
	// Commit 8bde61ab "/close-out is /done-herewith"; the skill by its path
	// too, the bare word "close-out" being prose.
	{name: "/close-out", kind: retiredCommand, successor: "/done-herewith", match: matchFamily},
	{name: "skills/close-out", kind: retiredSkill, successor: "/done-herewith", match: matchExact},
	// `pfm codex|opencode` retire for `engine-sync codex|opencode`, which
	// `pfm install` puts on PATH. The same rows answer the retired dispatch
	// itself (RetiredPfmCommand).
	{name: "pfm codex", kind: retiredCommand, successor: "engine-sync codex", match: matchFamily},
	{name: "pfm opencode", kind: retiredCommand, successor: "engine-sync opencode", match: matchFamily},
	// Commit abbcf405 "`pfm price` is gone": `pfm model-cost` is the one
	// price command (a following `s` or `-` is another name, never a hit).
	{name: "pfm price", kind: retiredCommand, successor: "pfm model-cost", match: matchExact},
	// Commit c867316c "retire docs/agents": the docs hub scaffolds to docs/.
	{
		name: "docs/agents", kind: retiredPath, successor: "docs/_index.md and docs/standards.md",
		match: matchExact,
	},
	// The machine-global writing-code rule is renamed coding: a project file
	// citing rules/writing-code.md or the `writing-code` rule names a link
	// pfm install retires.
	{name: "writing-code", kind: retiredPath, successor: "rules/coding.md", match: matchExact},
	// The stamp-gated PreToolUse guard hook is removed with its stamp script:
	// framework files are edited by the pcm agent, which the launch hook `pfm
	// internal pcm-guard` exempts by agent_type. A settings.json hook entry or
	// a script still naming either one is dead wiring.
	{name: "pfm-guard.sh", kind: retiredPath, match: matchExact},
	{name: "guard-stamp.sh", kind: retiredPath, match: matchExact},
	// Commit 09d16b64 "the ultimate family becomes /imagine:build, build-spec,
	// build-fast, evolve and callmeter" retired /ultimate-spec and the ultimate
	// agent, and callmeter commit 156f558 "remove ultimate-visualization; it
	// moved to professor as /imagine:callmeter" its skill. /ultimate itself is
	// live again as a standalone skill, so it is no row and no agent row below
	// matches a reference to that skill (`/ultimate`, skills/ultimate/).
	// Commit 471d4b63 "the imagine family collapses to /imagine, the imagination
	// agent and the ultimate skill" retired every /imagine: subcommand, the
	// imagine agent and the imagine-core skill (its law lives in the imaginator
	// agent); /imagine itself is live, so it is no row. A spec without a build
	// went to /flights:spec, which retired since: flights-foreman takes it.
	// Commit 196ec68b "retire /imagine:evolve with its imagine-historian agent"
	// left both with no successor.
	{name: "/ultimate-spec", kind: retiredCommand, successor: imagineSuccessor, match: matchExact},
	{
		name: "/callmeter:ultimate-visualization", kind: retiredSkill, successor: imagineSuccessor,
		match: matchExact,
	},
	{name: "/imagine:build", kind: retiredCommand, successor: imagineSuccessor, match: matchExact},
	{name: "/imagine:build-fast", kind: retiredCommand, successor: imagineSuccessor, match: matchExact},
	{name: "/imagine:build-spec", kind: retiredCommand, successor: flightsSuccessor, match: matchExact},
	{name: "/imagine:callmeter", kind: retiredCommand, successor: imagineSuccessor, match: matchExact},
	{name: "imagine-core", kind: retiredSkill, successor: "the imaginator agent", match: matchExact},
	{name: "/imagine:evolve", kind: retiredCommand, match: matchExact},
	{name: "imagine-historian", kind: retiredAgent, match: matchExact},
	// The agent is named by its context only — the bare word is everyday prose.
	// One row per spelling an agent reference takes: the Agent tool's
	// subagent_type in YAML, call, keyword and JSON form, the agent file's path,
	// and the backticked name beside the word agent or sub-agent.
	retiredAgentRow("ultimate", "imaginator", `subagent_type: "ultimate"`),
	retiredAgentRow("ultimate", "imaginator", `subagent_type:"ultimate"`),
	retiredAgentRow("ultimate", "imaginator", `subagent_type: 'ultimate'`),
	retiredAgentRow("ultimate", "imaginator", "subagent_type: ultimate"),
	retiredAgentRow("ultimate", "imaginator", `subagent_type="ultimate"`),
	retiredAgentRow("ultimate", "imaginator", `subagent_type": "ultimate"`),
	retiredAgentRow("ultimate", "imaginator", `subagent_type":"ultimate"`),
	retiredAgentRow("ultimate", "imaginator", "agents/ultimate.md"),
	retiredAgentRow("ultimate", "imaginator", "`ultimate` agent"),
	retiredAgentRow("ultimate", "imaginator", "`ultimate` sub-agent"),
	retiredAgentRow("ultimate", "imaginator", "`ultimate` subagent"),
	// The imagine agent, named by its context for the same reason: imaginator,
	// /imagine and imagine-core are other names and never reach these rows.
	retiredAgentRow("imagine", "imaginator", `subagent_type: "imagine"`),
	retiredAgentRow("imagine", "imaginator", `subagent_type:"imagine"`),
	retiredAgentRow("imagine", "imaginator", `subagent_type: 'imagine'`),
	retiredAgentRow("imagine", "imaginator", "subagent_type: imagine"),
	retiredAgentRow("imagine", "imaginator", `subagent_type="imagine"`),
	retiredAgentRow("imagine", "imaginator", `subagent_type": "imagine"`),
	retiredAgentRow("imagine", "imaginator", `subagent_type":"imagine"`),
	retiredAgentRow("imagine", "imaginator", "agents/imagine.md"),
	retiredAgentRow("imagine", "imaginator", "`imagine` agent"),
	retiredAgentRow("imagine", "imaginator", "`imagine` sub-agent"),
	retiredAgentRow("imagine", "imaginator", "`imagine` subagent"),
}

// retiredAgentRow is one contextual spelling of a retired agent reference.
// A project keeping its own agent definition is not naming the retired one.
func retiredAgentRow(name, successor, spelling string) retiredName {
	return retiredName{
		name: spelling, kind: retiredAgent, successor: successor, match: matchExact,
		probe: ".claude/agents/" + name + ".md",
	}
}
