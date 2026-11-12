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
}

// Chat is one live chat as its seat and the process table hold it.
type Chat struct {
	Session string // the engine's session id
	Seat    string // the seat's id in the state database
	Engine  int    // the engine's pid
}
