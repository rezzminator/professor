//go:build journey

package journey

// State is what a step hands the steps after it.
type State struct {
	Home    string // the owner's HOME
	Release string // the tag the snapshot INSTALL.md clones carries, as pfm --version prints it
	Session string // the branch's Herdr session; empty: the default session, the bare `herdr`
	Term    string // the terminal the branch's chat is typed in
	Project string // the directory the branch works in
	Name    string // the chat's pfm name, once a step named it
	Pane    string // the Herdr pane the branch's Herdr step opened
	Chat    Chat   // the chat the branch started
	Mesh    Mesh   // herdr-mesh's other chats: the child and its tmux peer
	Chats   []Chat // every chat of a branch that holds more than one, in the order it started them
	// Left is the segment the chat left when Claude Code continued it under a new session (a background job).
	Left string
	// Schedule is the id of the schedule a step armed on the chat.
	Schedule string
}

// Chat is one live chat as its seat and the process table hold it.
type Chat struct {
	Session string // the engine's session id
	Seat    string // the seat's id in the state database
	Engine  int    // the engine's pid
	Pane    string // the Herdr pane it runs in, for a branch that holds more than one chat
	Name    string // its pfm name, for a branch that holds more than one chat
	Account string // the CLAUDE_CONFIG_DIR its engine ran on, once a step read it
}
