# Codex

- Native subagent coordination uses the collaboration tools: `spawn_agent` with a configured role, `wait_agent` for the result, `collaboration.send_message` with the parent agent path. The chat MCP addresses separate terminal chats; never use it as a substitute for a subagent mailbox and never pass `/root` agent paths to it.
- While waiting exclusively for delegated work, call `wait_agent` without a timeout override so the harness uses its configured default, and receive progress and completion through the mailbox; mailbox-interruptible `wait_agent` and `clock.sleep` calls are exempt from the general blocking-wait guidance. After a progress message, wait again for completion. Reserve `list_agents` and status-request messages for an explicit request to diagnose a stuck agent. If the long wait times out, report the timeout instead of starting a retry loop.
- A fresh or partial-history child whose role or custom instruction replaces this prompt gets the two orchestration sections below pasted into its briefing.
- If the collaboration messaging tool is unavailable, return the result and that limitation in the final answer; the harness delivers it to the parent.
- The tiers map to the configured Codex roles and their model/effort settings; the delegating parent owns the result and its correction.
