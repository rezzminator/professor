//go:build journey

package journey

// State is what a step hands the steps after it.
type State struct {
	Home    string // the owner's HOME
	Release string // the tag the snapshot INSTALL.md clones carries, as pfm --version prints it
	Pane    string // the Herdr pane the herdr step opened
	Holder  string // the herdr-shield holder of that pane
	Chat    Chat   // the chat claude-from-picker started
}

// Chat is one live chat as its seat and the process table hold it.
type Chat struct {
	Session string // the engine's session id
	Seat    string // the seat's id in the state database
	Engine  int    // the engine's pid
}
