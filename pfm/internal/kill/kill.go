// Package kill implements permanent kills and the
// detached graceful-exit finisher for Claude and Codex chats.
//
// Entry points: New builds a Manager; Manager.Kill hides a chat and ends its
// pane, Manager.Unkill lifts a hide; NewFinisher runs the detached close.
//
// types.go: Target, Request, Dependencies and Manager.
// manager.go: New, Kill, ConfirmExit, Unkill and the kill target lookup.
// herdr.go: a native Herdr seat's kill target, ownership proof and pane close.
// herdr_dead.go: a kill named by a dead native seat's id hides its conversation.
// tmux.go: TmuxKiller, the tmux pane probes and closes.
// finisher.go: the detached exit finisher (NewFinisher, Run).
// spawn.go: CommandSpawner, which starts that finisher.
// lineage.go: an unindexed Codex member resolved to its lineage root.
// obs.go: the kill's activity-log trail.
package kill
