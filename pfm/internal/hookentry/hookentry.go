// Package hookentry holds the body of every pfm internal <entry>: the harness hooks (store-guard, pcm-guard, epic-inject, reload-intercept, exit-intercept, exit-close, resume-unkill, prompt block) and the entries the installer wires as units and shim calls (launch, then, chat-server, claude-version, clear-kill, agent-open, codex-launch, launcher-repair, update-check). Argv and stdin in, exit code out — the harness's own contract.
//
// herdr_activity_record.go keeps each Claude session's Herdr activity record and hands the
// native Claude panes an older build reported under herdr:claude back to Herdr (pfm install's cutover).
// herdr_shell_resume.go answers the shim's auto-open whether Herdr resumes the pane's agent itself.
// herdr_settle_process.go is the detached settle process: the mode a hook hands it and its
// bounded, backing-off retry of a Herdr state the host's load left undelivered.
// herdr_tmux_seat.go binds and ends a tmux chat's seat on its engine's session start and end.
// seathost_image.go re-execs a seat-host from the installed pfm when an install replaced its binary.
package hookentry
