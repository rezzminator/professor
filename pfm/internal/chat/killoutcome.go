package chat

import "fmt"

// KillOutcome is the one line a kill prints, naming the mechanism it actually
// used. "killed <id>" alone cannot tell a closed pane from a row that was
// merely de-listed, and that ambiguity is the whole defect this sentence
// exists to end — it is written to STDOUT, because a warning only stderr
// carries never reaches an MCP caller reading a zero exit.
//
// Three outcomes, one each:
//   - a live address AND a recorded kill: the pane is being closed;
//   - a recorded kill with no live address: the row was de-listed, nothing
//     was closed;
//   - a live address with NO record: an OpenCode seat keyed on its own socket
//     name, which is an address and never an identity (engine.SocketKeyedID),
//     so its pane closes and the kill ledger stays clean.
func KillOutcome(id, socket, pane string, recorded bool) string {
	if socket == "" || pane == "" {
		return fmt.Sprintf("killed %s\tde-listed only, no live pane closed", id)
	}
	if !recorded {
		return fmt.Sprintf(
			"closed %s\tclosing pane %s on socket %s — no kill recorded: this seat answers only to its socket",
			id, pane, socket,
		)
	}
	return fmt.Sprintf("killed %s\tclosing pane %s on socket %s", id, pane, socket)
}
