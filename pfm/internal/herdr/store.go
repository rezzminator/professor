package herdr

// StoredOption is the tmux session option a pfm chat's server keeps its Herdr
// reporter state in: the JSON of one Stored.
const StoredOption = "@pfm_herdr"

// Client is one Herdr client attached to a pfm chat's tmux session.
type Client struct {
	Pane string `json:"pane"`
	// Terminal outlives a Herdr pane move, which assigns the pane a new id: it
	// names the chat's pane, and every send resolves it to the id first.
	Terminal string `json:"terminal,omitempty"`
	Socket   string `json:"socket"`
}

// Stored is the state kept in the chat session's StoredOption. Seq is the start
// time of the event that wrote it: a write never replaces a higher one. Clients
// are the attached Herdr clients, keyed by tmux client pid.
type Stored struct {
	// Agents is Claude's idempotent running-agent set; MainStoppedAt starts its
	// bounded lease after Stop. Native transports persist this state on disk.
	Agents        map[string]bool `json:"agents,omitempty"`
	MainStoppedAt int64           `json:"main_stopped_at,omitempty"`
	Engine        string          `json:"engine"`
	SessionID     string          `json:"session_id"`
	State         string          `json:"state"`
	Message       string          `json:"message"`
	Seq           uint64          `json:"seq"`
	// Tool names the tool call a Claude permission request's block asks about:
	// only that call's own working report leaves the block.
	Tool string `json:"tool,omitempty"`
	// Name is the chat name the panes were last named after.
	Name    string            `json:"name,omitempty"`
	Clients map[string]Client `json:"clients,omitempty"`
}
