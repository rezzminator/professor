// Each loop's agent type by id, pure: who a loop is, and which loops were
// already reported as nobody.
// This file is a vendored copy of the plugin-log canonical source (Professor's plugins/plugin-log/ts/agent-types.ts),
// kept byte-identical in every plugin by a sync check: change the canonical source, then re-vendor every copy.

/**
 * Each agent loop's type by id. The session's agent list comes first, once
 * per id it names: a teammate's SubagentStart and SubagentStop carry its name
 * as `agent_type`, not its type. An id no list row names takes the type the
 * engine announced (SubagentStart, SubagentStop): a workflow's agents are
 * never in `$.agent.list()`, so the announcement is their only source. An id
 * neither names is an engine loop (a fork of its own: compaction, memory)
 * that no rule governs: the API documents that the engine's own forks carry
 * ids no list names (`AgentLoop.agentId`), and they fire no SubagentStart.
 */
export class AgentTypes {
  private readonly listed = new Map<string, string>();
  private readonly announced = new Map<string, string>();
  private readonly seen = new Set<string>();
  private readonly missed = new Set<string>();

  learn(id: string, type: string): void {
    if (!id) return;
    this.seen.add(id);
    if (type) this.announced.set(id, type);
  }

  /** True once a list row or an announcement named the id, typed or not: a sub-agent of the session, not an engine loop. */
  named(id: string): boolean {
    return this.seen.has(id);
  }

  async of(id: string, list: () => Promise<readonly { id: string; type: string }[]>): Promise<string | undefined> {
    const known = this.listed.get(id);
    if (known) return known;
    const agent = (await list()).find((info) => info.id === id);
    if (agent) this.seen.add(id);
    if (agent?.type) {
      this.listed.set(id, agent.type);
      return agent.type;
    }
    return this.announced.get(id);
  }

  /** True the first time an id is reported as resolving to no type, false on every later report: one log line per loop. */
  firstMiss(id: string): boolean {
    if (this.missed.has(id)) return false;
    this.missed.add(id);
    return true;
  }
}
