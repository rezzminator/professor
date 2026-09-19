package engine

// SocketKeyedID reports whether id is not a chat identity at all but the name
// of the tmux socket the chat happens to run on.
//
// Only OpenCode produces one. A running OpenCode TUI exports no session
// variable, so a seat whose session could not be pinned down (gather's
// identification ladder) is still a live, addressable chat — keyed on the one
// handle it provably owns, its socket. That handle is an ADDRESS: it names
// where the chat is, never which conversation it is, and it stops meaning
// anything the moment the seat's session is finally pinned down.
//
// This is the ONE test of that (K3): the fleet composer refuses to apply a
// kill written against such an id (compose.applyKill), and the kill manager
// refuses to write one (kill.Manager.Kill) — a tombstone keyed on a socket
// name would outlive the socket and name nothing at all.
func SocketKeyedID(engine ID, id, socket string) bool {
	return engine == OpenCode && id != "" && id == socket
}
