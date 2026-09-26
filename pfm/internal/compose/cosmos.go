package compose

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/resolve"
)

const (
	CosmosWindow            = 24 * time.Hour
	CosmosEventCap          = 5000
	CosmosTruncationWarning = "showing newest 5000 events of the window"

	// CosmosTrafficWindow is the lookback a star's temperature is measured
	// over: the events that touched a system inside this window, counted at
	// whatever clock BuildCosmos is handed, so a replayed past renders the
	// heat it had then.
	CosmosTrafficWindow = time.Hour

	// CosmosRowGrace is how long an identity with no matching row stays
	// PENDING rather than GONE: a freshly spawned chat looks identical to a
	// vanished one until the row layer catches up, so BuildCosmos waits
	// this long past the node's own last edge activity (nowNS - LastNS)
	// before calling a still-missing row dead — see the grace-window
	// resolution below, the one nowNS-dependent verdict this stateless
	// package makes on its own.
	//
	// Short on purpose: a chat that just died should read as gone quickly,
	// not linger as a ghost the row layer never explains.
	CosmosRowGrace = 2700 * time.Millisecond
)

type CosmosNode struct {
	Key string
	// Label is what a human reads. Its type is resolve.DisplayName, not a
	// string, and that is the point: outside internal/resolve there is no
	// literal that produces one, so a tmux session name or a socket path
	// cannot be assigned here by accident. It arrives from exactly one
	// place — the answer resolve.Directory gave for this node's identity.
	Label  resolve.DisplayName
	Engine string
	// Home is the star this chat belongs to — its Row.Project, the same
	// project identity the picker groups by, so the cosmos and the list can
	// never disagree about where a chat lives. Empty for a ghost with no
	// matching row: its home is genuinely unknown, and the renderer seats it
	// under the shared unknown star rather than guessing.
	Home string
	// RowKey is the fleet row this node resolved to — compose.RowKey of that
	// row, the join the picker uses to open a chat from the sky. Never the
	// bare ID: a live split window has none, and a running chat must never
	// read as a ghost. Empty only for a ghost — an identity no row answers to.
	RowKey string
	Live   bool
	Killed bool
	LastNS int64
	// Dead marks a node whose chat no longer has a live presence in the
	// roster. Two different questions decide it, and only one of them
	// needs a clock: a matched row explicitly Killed is positive evidence,
	// true immediately, no waiting required. A node with NO matching row
	// is ambiguous on its own — a freshly spawned chat looks identical to
	// a vanished one until the row layer catches up — so BuildCosmos gives
	// it a short grace window (nowNS - LastNS) before calling a still-
	// missing row GONE rather than PENDING.
	Dead bool
	// DiedNS, as this package sets it, is the node's own last edge activity
	// (LastNS), not a kill time — the store's kill table has no path into
	// this package (compose.Row carries only the Killed bool, not a
	// timestamp), and a chat that vanished without ever being recorded
	// killed has no timestamp at all. That is the most honest fact this
	// stateless package has; it is NOT when the chat died and this package
	// never claims otherwise. Only replay ever hands this value to a
	// renderer — the live graph drops every dead node before DiedNS could
	// reach one (see live in BuildCosmos's doc comment) — and even there it
	// stays labeled as last edge activity, never a kill moment.
	DiedNS int64
}

type CosmosEdge struct {
	From        string
	To          string
	Kind        string
	Count       int
	LastNS      int64
	LastMessage string
}

// CosmosStar is one project's sun as the ledger sees it. TrafficHour is how
// many events touched the system inside CosmosTrafficWindow — an event
// counts ONCE per distinct Home among its endpoints, so an intra-system
// message warms one star and a cross-system message warms both. Population
// is not here on purpose: the renderer counts the nodes it actually draws.
type CosmosStar struct {
	Home        string
	TrafficHour int
	LastNS      int64
}

type CosmosGraph struct {
	Nodes    []CosmosNode
	Edges    []CosmosEdge
	Stars    map[string]CosmosStar
	Err      string
	Warnings []string
}

type cosmosBuilder struct {
	// directory is the ONE index every identity in this graph resolves
	// through. Both halves of a conversation ask it the same question in
	// the same way; the three hand-rolled maps that used to sit here were
	// two divergent ladders, and the asymmetry between them was the root
	// of the ghost-node family.
	directory *resolve.Directory[Row]
	nodes     map[string]CosmosNode
	edges     map[string]CosmosEdge
	warnings  []string
	// unmatched holds every node key built with no matching row — no
	// positive evidence of death, just its absence. BuildCosmos resolves
	// these once every event has been walked and each key's final LastNS
	// is known, via the grace window below; a resolved row's Dead (set in
	// chatNode from row.Killed) is never touched here.
	unmatched map[string]bool
	// nowNS is the clock BuildCosmos was handed, threaded onto the builder
	// so warm can answer "did this touch fall inside CosmosTrafficWindow"
	// without every call site re-deriving it.
	nowNS int64
	// stars accumulates one CosmosStar per distinct Home touched by warm.
	// BuildCosmos folds it against the node set at the end so a home with
	// zero traffic still gets a cold star, and a home no node belongs to
	// never gets one it was not asked for.
	stars map[string]CosmosStar
}

// BuildCosmos derives the complete presentation graph from fleet rows and a
// caller-bounded event window. It performs no I/O and remembers nothing
// between calls, but it DOES read nowNS, for exactly one question it can
// answer truthfully: whether a no-row-matched node's absence has had long
// enough to mean the row layer will never catch up (see the grace-window
// resolution below). That is a different question from "when did this
// chat die" — this package still refuses that one, because LastNS cannot
// answer it and DiedNS below stays honestly labeled as last edge activity,
// never a kill time.
//
// live controls two linked things: whether every live pane row (liveRow)
// earns a node up front, before the event walk runs at all — the user's
// ruling is that an active pane IS a node whether or not it ever spoke
// through the chat MCP, so a quiet live chat must not read as absent just
// because chatNode never had an event to resolve it from — and whether a
// dead or hidden chat's node survives into the returned graph at all.
// live=true is the sky of now: seed every live pane row, and drop every
// node whose chat is dead or hidden (a matched row explicitly Killed, or an
// unmatched identity past its grace window) along with every edge naming
// one — the live sky never shows a dead or hidden chat, because a node the
// picker would not list is not a chat the picker could open.
// live=false is the chronoscope replay: events only, no seeding, every
// ghost kept — the past is the ledger, not the roster as it stands today.
//
// A seeded node carries no edges and is stamped with the row's own last
// activity (Row.ActivityNS), or nowNS when the row carries none — never
// nowNS outright, or a chat idle for hours would read as having just
// spoken; the event walk beneath still advances a node's LastNS past its
// seed the moment real traffic is newer, since touch only ever moves a
// node's clock forward.
func BuildCosmos(rows []Row, events []fleetdb.CommsEvent, nowNS int64, live bool) CosmosGraph {
	builder := cosmosBuilder{
		directory: resolve.NewDirectory(rows, rowAddress),
		nodes:     make(map[string]CosmosNode),
		edges:     make(map[string]CosmosEdge),
		nowNS:     nowNS,
	}

	if live {
		for index := range rows {
			row := rows[index]
			if row.Killed || row.BG || !liveRow(row) {
				continue
			}
			at := row.ActivityNS
			if at == 0 {
				at = nowNS
			}
			builder.touch(builder.chatNode(rowAddress(row)), at)
		}
	}

	unsigned := 0
	for index := range events {
		event := events[index]
		switch event.Kind {
		case fleetdb.KindInject:
			if event.SenderSession == "" && event.SenderUUID == "" && event.SenderLabel == "" {
				unsigned++
			}
			from := builder.chatNode(senderAddress(event))
			to := builder.chatNode(receiverAddress(event))
			builder.touch(from, event.AtNS)
			builder.touch(to, event.AtNS)
			builder.addEdge(from.Key, to.Key, event)
			builder.warm(event.AtNS, from, to)
		case fleetdb.KindSpawn:
			child := builder.chatNode(receiverAddress(event))
			builder.touch(child, event.AtNS)
			if event.SenderSession == "" && event.SenderUUID == "" && event.SenderLabel == "" {
				unsigned++
				builder.warm(event.AtNS, child)
				continue
			}
			parent := builder.chatNode(senderAddress(event))
			builder.touch(parent, event.AtNS)
			builder.addEdge(parent.Key, child.Key, event)
			builder.warm(event.AtNS, child, parent)
		case "group":
			// Retired chat-group events: a historical fleet.db row of this
			// kind is known history, not an unknown one — skip it silently,
			// with no node, no edge, and no warning.
		default:
			builder.warnings = append(builder.warnings, fmt.Sprintf(
				"comms event %d has unknown kind %q", event.ID, event.Kind,
			))
		}
	}

	if unsigned > 0 {
		// Error is never absence: an unsigned event has no sender node, so
		// its line and its lineage CANNOT be drawn — and a universe missing
		// lines must say why, not render a quieter sky and let the gap read
		// as "no traffic".
		builder.warnings = append(builder.warnings, fmt.Sprintf(
			"%d events carry no sender identity — their lines cannot be drawn", unsigned,
		))
	}
	rowGrace := int64(CosmosRowGrace)
	graph := CosmosGraph{Warnings: builder.warnings}
	graph.Nodes = make([]CosmosNode, 0, len(builder.nodes))
	for key, node := range builder.nodes {
		if builder.unmatched[key] {
			// No row matched. On its own that is ambiguous — a freshly
			// spawned chat looks identical to a vanished one until the row
			// layer catches up — so give it rowGrace before calling a
			// still-missing row GONE rather than PENDING. This is the one
			// nowNS-dependent verdict this stateless package makes, and it
			// answers "has enough time passed for absence to mean gone",
			// never "when did it die".
			node.Dead = nowNS-node.LastNS > rowGrace
		}
		if live && node.Dead {
			// The live sky never shows a dead or hidden chat — no blink,
			// no fade, no debris; a node the picker would not list is not
			// in the graph. A row explicitly Killed and an unmatched
			// identity past rowGrace are both "dead" for this purpose, and
			// this is the only place either verdict is acted on. Replay
			// never takes this branch — live is false there — so a chat
			// that has since died still renders as the ghost it was.
			continue
		}
		if node.Dead {
			// The only death time this package can honestly report is the
			// node's own last edge activity: compose.Row carries a Killed
			// bool but no kill timestamp, and a chat that vanished without
			// ever being recorded killed has no timestamp at all.
			// Fabricating a more precise one would be a lie the graph
			// cannot back up. Reaching here means live is false — the
			// branch above already dropped this node when live was true —
			// so DiedNS only ever reaches a replay renderer.
			node.DiedNS = node.LastNS
		}
		graph.Nodes = append(graph.Nodes, node)
	}
	// Stars gets one entry per distinct Home among the nodes just built —
	// zero traffic included, a cold star is still a star — and only for
	// those homes: a home warm() touched but that ended up with no
	// surviving node (there is none such today, but the rule is the graph's
	// node set decides, not the builder's touch history) does not get one.
	homes := make(map[string]bool, len(graph.Nodes))
	// present is the live-only membership test the edge filter below uses:
	// an edge naming a node the live drop just removed cannot be drawn
	// either, and the graph's own node set — not the builder's edge or
	// touch history — is what decides who survived.
	var present map[string]bool
	if live {
		present = make(map[string]bool, len(graph.Nodes))
	}
	for _, node := range graph.Nodes {
		homes[node.Home] = true
		if present != nil {
			present[node.Key] = true
		}
	}
	graph.Stars = make(map[string]CosmosStar, len(homes))
	for home := range homes {
		star := builder.stars[home]
		star.Home = home
		graph.Stars[home] = star
	}
	sort.Slice(graph.Nodes, func(left, right int) bool {
		leftLabel := graph.Nodes[left].Label.String()
		rightLabel := graph.Nodes[right].Label.String()
		leftFold := strings.ToLower(leftLabel)
		rightFold := strings.ToLower(rightLabel)
		if leftFold != rightFold {
			return leftFold < rightFold
		}
		if leftLabel != rightLabel {
			return leftLabel < rightLabel
		}
		return graph.Nodes[left].Key < graph.Nodes[right].Key
	})
	graph.Edges = make([]CosmosEdge, 0, len(builder.edges))
	for _, edge := range builder.edges {
		if present != nil && (!present[edge.From] || !present[edge.To]) {
			continue
		}
		graph.Edges = append(graph.Edges, edge)
	}
	sort.Slice(graph.Edges, func(left, right int) bool {
		if graph.Edges[left].LastNS != graph.Edges[right].LastNS {
			return graph.Edges[left].LastNS > graph.Edges[right].LastNS
		}
		if graph.Edges[left].From != graph.Edges[right].From {
			return graph.Edges[left].From < graph.Edges[right].From
		}
		if graph.Edges[left].To != graph.Edges[right].To {
			return graph.Edges[left].To < graph.Edges[right].To
		}
		return graph.Edges[left].Kind < graph.Edges[right].Kind
	})
	return graph
}

// liveRow reports whether row carries a live pane — a real process a human
// can be looking at right now — the exact Kind set compose.defaultEligible
// already treats as self-evidently real: LiveSplit, Agent, and Booting are
// wholly exempt from its transcript-emptiness test for that reason, and
// LiveCodex is exempt from its size/prompt half for the same one. LiveClaude
// joins them here because a live Claude pane is equally real evidence on its
// own; BuildCosmos's seeding only asks "is there a pane", never "has this
// pane's transcript accumulated content yet" — that emptiness question
// belongs to the picker's default view, not to whether the cosmos shows a
// chat the user is actively looking at.
func liveRow(row Row) bool {
	switch row.Kind {
	case LiveClaude, LiveCodex, LiveOpenCode, LiveSplit, Agent, Booting:
		return true
	default:
		return false
	}
}

// rowAddress projects one fleet row into the shared identity address space.
// It is the ONLY place this package states which of a Row's fields are
// identities, and Row.Socket is deliberately handed over as Session: a live
// row carries the BARE socket name tmux is addressed by (-L), while the
// comms ledger records full -S paths, and resolve.Address normalises both to
// the same tmux session name. Keying one side bare and the other by path is
// what made the pane index unmatchable.
func rowAddress(row Row) resolve.Address {
	return resolve.Address{
		ID:      row.ID,
		Session: row.Socket,
		Pane:    row.PaneID,
		Label:   row.Name,
	}
}

// senderAddress and receiverAddress are the ledger's two identity
// projections. Every column lands in the field whose flavour it actually
// has — except Target, which is whatever a caller typed and therefore goes
// into Text, the untyped slot resolution sweeps across every namespace. That
// is not a hedge: the reply footer used to hand callers a raw session id to
// address a chat by, so the target column genuinely holds session names,
// labels and stale labels alike.
func senderAddress(event fleetdb.CommsEvent) resolve.Address {
	return resolve.Address{
		ID:      event.SenderUUID,
		Session: event.SenderSession,
		Label:   event.SenderLabel,
	}
}

func receiverAddress(event fleetdb.CommsEvent) resolve.Address {
	return resolve.Address{
		Session: event.ReceiverSocket,
		Pane:    event.ReceiverPane,
		Text:    event.Target,
	}
}

// chatNode is the ONE path from any identity a recorded event carries to the
// node it belongs to. Both halves of a conversation come through here, which
// is the whole fix: a sender and a receiver describing the SAME chat used to
// walk two different ladders and land on two different nodes.
//
// A node whose identity resolved to no row is still a node — the graph must
// show that somebody talked — but it is marked for the grace-window
// resolution BuildCosmos performs once every event has been walked. It
// carries no Dead verdict here: the ABSENCE of a row is not the same
// evidence as row.Killed, and whether a still-missing row means PENDING or
// GONE cannot be decided until this key's final LastNS is known.
func (builder *cosmosBuilder) chatNode(query resolve.Address) CosmosNode {
	answer := builder.directory.Lookup(query)
	key := cosmosKey(answer)
	if key == "" {
		return CosmosNode{}
	}
	node := CosmosNode{Key: key, Label: answer.Display}
	if answer.Found {
		node.Engine = string(EngineForKind(answer.Chat.Kind))
		node.Home = answer.Chat.Project
		node.RowKey = RowKey(answer.Chat)
		node.Live = answer.Chat.Socket != ""
		node.Killed = answer.Chat.Killed
		node.Dead = answer.Chat.Killed
		return node
	}
	if builder.unmatched == nil {
		builder.unmatched = make(map[string]bool)
	}
	builder.unmatched[key] = true
	return node
}

// cosmosKey is the graph's node identity, and it keys on machine addresses,
// never on a label: chat_name renames a chat at will and a freed label is
// reused by a later one, so a label-keyed node would retroactively rewrite
// who said what in an append-only ledger.
//
// The unresolved branch prefers the SESSION over anything else for one
// specific reason: it is the only identity both halves of a conversation
// carry independently for the same chat — a sender states its own
// sender_session, a receiver is recorded by socket — so a chat that has
// vanished from the roster converges on ONE ghost instead of one per side.
func cosmosKey(answer resolve.Answer[Row]) string {
	address := answer.Address
	name := firstNonEmpty(address.Label, address.Text)
	if answer.Found {
		switch {
		case address.ID != "":
			return "chat:id:" + address.ID
		case address.Session != "":
			return "chat:pane:" + address.Session + "\t" + address.Pane
		case name != "":
			return "chat:name:" + name
		}
		return ""
	}
	switch {
	case address.Session != "":
		return "chat:session:" + address.Session
	case address.ID != "":
		return "chat:id:" + address.ID
	case name != "":
		return "chat:name:" + name
	}
	return ""
}

func (builder *cosmosBuilder) touch(node CosmosNode, atNS int64) {
	if node.Key == "" {
		return
	}
	current, exists := builder.nodes[node.Key]
	if !exists {
		current = node
	}
	if atNS > current.LastNS {
		current.LastNS = atNS
	}
	builder.nodes[node.Key] = current
}

// warm folds one event's timestamp into every distinct Home among the given
// endpoint nodes — an intra-system event (both endpoints share a Home) warms
// that one star once, never twice, and a cross-system event warms both. A
// node with no Key (an unresolved lookup) never reaches a star: it carries
// no identity to warm anything with. TrafficHour only increments inside
// CosmosTrafficWindow of nowNS, but LastNS always advances, so a star this
// package has never seen fresh still remembers when it last was.
func (builder *cosmosBuilder) warm(atNS int64, nodes ...CosmosNode) {
	if builder.stars == nil {
		builder.stars = make(map[string]CosmosStar)
	}
	seen := make(map[string]bool, len(nodes))
	for _, node := range nodes {
		if node.Key == "" || seen[node.Home] {
			continue
		}
		seen[node.Home] = true
		star := builder.stars[node.Home]
		star.Home = node.Home
		if atNS > builder.nowNS-int64(CosmosTrafficWindow) {
			star.TrafficHour++
		}
		if atNS > star.LastNS {
			star.LastNS = atNS
		}
		builder.stars[node.Home] = star
	}
}

func (builder *cosmosBuilder) addEdge(from, to string, event fleetdb.CommsEvent) {
	if from == "" || to == "" {
		return
	}
	key := from + "\x00" + to + "\x00" + event.Kind
	edge := builder.edges[key]
	if edge.Count == 0 {
		edge = CosmosEdge{
			From: from, To: to, Kind: event.Kind,
			LastNS: event.AtNS, LastMessage: event.Message,
		}
	} else if event.AtNS > edge.LastNS {
		edge.LastNS = event.AtNS
		edge.LastMessage = event.Message
	}
	edge.Count++
	builder.edges[key] = edge
}
