---
name: wave-builder
description: ORCHESTRATOR-ONLY — the {PROJECT_NAME} builder lane for Codex; acts only on an injected turn naming a wave BRIEF ("/wave:builder {path}", a "Use the wave-builder skill" turn carrying BRIEF {path}, or an orchestrator dispatch). Maps Codex mechanics onto the binding /wave:builder protocol; predecessor /wave:orchestrator, gate {project}-qa PRE-MERGE.
---

<!--
HAND-WRITTEN CARD — no generated marker. `pfm codex build` preserves it because
the binding builder protocol is machine-global, not a project-local generated
skill source. It maps Codex mechanics ONLY; it never restates protocol.

This is also the WORKED EXAMPLE for writing your own hand-written card: point at
the binding protocol first, then enumerate the deltas, then stop. If a card
starts explaining WHAT to build rather than HOW this harness differs, the content
belongs in `.claude/`, not here.
-->

# Codex Wave Builder — lane dialect card

**Binding protocol:** read `$HOME/.claude/commands/wave/builder.md` § Orchestrated mode FIRST — `pfm install` links that machine-global original, and every rule there binds you. This card ONLY maps Claude-harness mechanics onto the Codex harness. The compiled root `AGENTS.md` carries the project's MANDATORY rules in full, including: never edit code on `main`, only gitter runs git, never swallow exceptions, surgical changes, and the guarded paths (`.claude/**`, any `CLAUDE.md`) are stop-and-ping, never an edit.

## Toolset (read at its source)

Read each tool at its canonical path. Repo-relative paths resolve inside the lane's own worktree; host-level paths deliberately resolve to the shared machine surface:

- `$HOME/.claude/commands/wave/builder.md` — the binding protocol.
- `$HOME/.local/bin/pfm chat` — the chat CLI. Ping with `pfm chat inject {orchestrator-session} '{one-line msg}'`; resolve your identity with `pfm whoami`.
- `.claude/scripts/` — project scripts such as `filter-test-output.sh -p` and `worktree.sh`.
- `{project}/.claude/agents/` — per-project agent sources; the compiler registers their Codex roles.
- `.claude/agents/` — project-root agent sources; read only for role context because `spawn_agent` loads the registered protocol.

## Law enforcement (execpolicy — not optional)

Repo law is enforced at two layers — the kernel sandbox (workspace-write, the standing default) and `.codex/rules/*.rules`, which rejects direct infra tooling regardless of sandbox mode. A rejection quoting a justification is the LAW WORKING — never rephrase, wrap, or shell-trick around it; if a rejected command seems genuinely required, that is a SPEC-CONFLICT ping to the orchestrator. `git` and package installs sit OUTSIDE that layer and bind as law rather than as a pin: read-only git is yours, while commits, merges, pushes and dependency installs stay gitter's / the orchestrator's call.

## Harness deltas (the ONLY differences from builder.md)

1. **Hands & specialist roles** — builder.md's Agent-tool spawns map to the collaboration tools (`spawn_agent` / `wait_agent` / `followup_task`). Wherever a task's `Build agents:` line or the protocol names a specialist, dispatch the compiled `{role}-{project}` `agent_type` with the task's five-field briefing contract. Independent spans run in parallel; dependent spans run sequentially.

   The child shares your working directory and loads the role protocol compiled from `.claude/agents/`. Do not shell out to another agent CLI. If a named role is absent, stop with a registry failure; never substitute `default` or execute gitter inline.

2. **Pings** — native subagents use `collaboration.send_message` to their parent agent path; completion also arrives through the harness mailbox. A separately launched terminal builder reports through the chat MCP to the exact orchestrator target in its briefing. Report delivery errors; no spool or timer provides a fallback.

3. **Verdicts and steers inbound** — native subagents receive mailbox messages; separate terminal builders receive injected turns and end their turn after reporting. Use `wait_agent` when waiting for a native child.

4. **Goal / allow-list machinery** — there is no Claude `/goal` harness continuation. The discipline is identical by this card: act ONLY on injected turns (a BRIEF, a verdict, a boundary brief); never self-start work, never open `$WAVES` manifests un-briefed, never self-schedule timers or background waiters.

5. **Compact** — the orchestrator may send `/compact`; it is native in your TUI. After any compact, re-read this card and the current BRIEF before acting.

6. **End-of-wave GATE-1** — dispatch the registered `{project}-qa` role in PRE-MERGE mode (full suite, zero tolerance, filtered + timed per Toolset). It writes `$WAVES/{wave}/gate1.md` and stamps the verdict `Executor: codex-subagent/qa_{project}`. A missing registered QA role is a gate failure, never an inline substitute.

7. **Boundary mode** — GATE-2 suites + teardown are shell and therefore yours, exactly per builder.md § Boundary duties. The wave walk is NOT yours: the orchestrator runs `/wave:walker` where its census needs it. You never commit — that stays with registered gitter.

8. **Report cards** — identical, no adaptation: `$TASKS/task-{n}-report.md` per the BRIEF's env card, fixed headers, ≤1KB, `Expected:` / `Got:` deviations.

## Launch (operator / orchestrator side)

```bash
tmux -L codex-{lane} new-session -d -s codex-{lane} \
  'codex --cd {REPO_ROOT} -s workspace-write -a never'
```

Standing default: workspace-write (kernel sandbox on, spool-only pings). The full-toolset variant (`-s` with Codex's full-access sandbox plus `-a never`, where `pfm chat` works) requires explicit, plain-text human authorization per launch policy — never inferred, never menu-selected. **Launch flags live HERE, with the launcher — never in `.codex/config.toml`**, which interactive sessions also read.

The tmux session name `codex-{lane}` is the lane's address in `lanes.md` — `pfm chat` resolves exact tmux session names across every socket, and its busy-detection matches Codex's "Esc to interrupt" indicator. Dispatch = inject the pane with: `Use the wave-builder skill: BRIEF {path}`. Resume after a death: `codex exec resume {SESSION_ID}`, or relaunch and re-dispatch the BRIEF (the report cards on disk carry the position).
