package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/call"
	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/spawn"
)

const tabStateUnknown = "unknown"

// errTabPaneList marks a socket-wide pane listing failure: it fails the tick,
// unlike one chat's view failure, which is named and skipped.
var errTabPaneList = errors.New("list Herdr tab panes")

type herdrTabProvider struct {
	clock         clock.Clock
	missingAgents map[string]time.Time
	scan          func(context.Context) (FleetScan, error)
	inspect       func(context.Context, headless.Chat, time.Time) (headless.Status, error)
	// rest reads the transcript turn an agent-less pane at rest shows, through
	// the reader inspect carries (SeatInspector.Rest); nil reads it whole.
	rest   func(context.Context, headless.Chat, time.Time) (headless.Status, error)
	views  func(context.Context, headless.Chat, string) ([]herdr.TabDecoration, error)
	screen func(context.Context, headless.Chat) (string, error)
	mode   func() (string, error)
	// glyphs reads the theme's tab glyphs each tick; nil draws the defaults.
	glyphs func() (herdr.TabGlyphs, error)
	warn   io.Writer
	// probe, when set, takes each enumeration's baseline for changed.
	probe *tabProbe
}

func (provider *herdrTabProvider) tabs(ctx context.Context, socket string) ([]herdr.TabDecoration, error) {
	if ticks, ok := provider.warn.(*tickWarnings); ok {
		ticks.tick()
	}
	clk := provider.clock
	if clk == nil {
		clk = clock.Real
	}
	pendingAgents := map[string]time.Time{}
	defer func() { provider.missingAgents = pendingAgents }()
	provider.probe.begin(ctx)
	// An unreadable configuration never stops the tabs' status: it is named, and
	// the tabs keep painting in the default mode while it lasts.
	mode, err := provider.mode()
	if err != nil {
		mode = pfmconfig.DefaultHerdrTabStatus
		if _, writeErr := fmt.Fprintf(
			provider.warn,
			"Herdr tab status configuration: %v (painting the default %s mode)\n",
			err, mode,
		); writeErr != nil {
			return nil, fmt.Errorf("persist tab status configuration failure: %w", writeErr)
		}
	}
	var glyphs herdr.TabGlyphs
	glyphRead := ""
	// Only emoji mode draws the theme's glyphs: off and text never read them,
	// so a broken theme state is never named for a mode that ignores it.
	if provider.drawsGlyphs(mode) {
		// An unreadable theme never stops the tabs' status: it is named, and
		// the tabs keep painting with the default glyphs while it lasts.
		glyphs, err = provider.glyphs()
		glyphRead = glyphToken(glyphs, err)
		if err != nil {
			glyphs = herdr.TabGlyphs{}
			if _, writeErr := fmt.Fprintf(
				provider.warn,
				"Herdr tab glyphs of the theme: %v (painting the default glyphs)\n",
				err,
			); writeErr != nil {
				return nil, fmt.Errorf("persist tab glyph failure: %w", writeErr)
			}
		}
	}
	scan, err := provider.scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("scan tab chats: %w", err)
	}
	provider.probe.record(ctx, socket, scan.Rows, mode, glyphRead)
	var decorations []herdr.TabDecoration
	for i := range scan.Rows {
		row := scan.Rows[i]
		target := FromRow(row)
		views, err := provider.views(ctx, target, socket)
		if errors.Is(err, errTabPaneList) {
			return nil, fmt.Errorf("locate tab views of %s: %w", target.ID, err)
		}
		if err != nil {
			// One chat's unreadable views never stop the other chats' tabs.
			if _, writeErr := fmt.Fprintf(
				provider.warn,
				"Herdr tab status of %s: %v\n",
				target.ID,
				err,
			); writeErr != nil {
				return nil, fmt.Errorf("persist tab view failure of %s: %w", target.ID, writeErr)
			}
			continue
		}
		if len(views) == 0 {
			continue
		}
		provider.probe.track(target)
		if !target.Live && len(scan.Unreadable) > 0 {
			return nil, fmt.Errorf("fleet inspection incomplete, refusing dead tab status: %v", scan.Unreadable)
		}
		if views[0].State == headless.StateDead {
			target.Live = false
		}
		state := tabStateUnknown
		if views[0].State == tabStateUnknown {
			// Ownership is proved, but foreground evidence is unavailable.
			// A transcript/cache must not overwrite that uncertainty.
		} else if status, err := provider.inspect(ctx, target, clk.Now()); err != nil || status.StateSource == herdrAgentUntracked {
			// Herdr tracks no agent on the pane: the engine still in its foreground
			// (herdrAgentUntracked), or a refusal whose foreground read failed.
			var refusal *herdr.Refusal
			if target.Backend == spawn.BackendHerdr &&
				(err == nil || errors.As(err, &refusal) && refusal.Code == herdr.AgentNotFound) {
				key := target.SeatID + "\x00" + target.ID + "\x00" + socket + "\x00" + views[0].Terminal
				since, found := provider.missingAgents[key]
				if !found {
					since = clk.Now()
				}
				confirmed, confirmErr := missingTabAgentState(
					ctx, target, views[0].Terminal, clk.Now().Sub(since), provider.screen, call.HerdrEngine,
				)
				if confirmErr == nil && confirmed == headless.StateIdle {
					// A pane at rest keeps its transcript's failed turn, as an idle Herdr agent does.
					restRead := provider.rest
					if restRead == nil {
						restRead = headless.Inspect
					}
					rest, restErr := restRead(ctx, target, clk.Now())
					if restErr != nil {
						confirmed, confirmErr = tabStateUnknown, fmt.Errorf(
							"read the turn %s rests on: %w",
							target.ID,
							restErr,
						)
					} else if rest.State == headless.StateError {
						confirmed = headless.StateError
					}
				}
				if confirmErr == nil {
					if confirmed == "" {
						continue
					}
					state = confirmed
					if confirmed != headless.StateDead {
						pendingAgents[key] = since
					}
					err = nil
				} else {
					pendingAgents[key] = since
					err = errors.Join(err, confirmErr)
				}
			}
			if err != nil {
				if _, writeErr := fmt.Fprintf(
					provider.warn,
					"Herdr tab status of %s: %v\n",
					target.ID,
					err,
				); writeErr != nil {
					return nil, fmt.Errorf("persist status inspection failure: %w", writeErr)
				}
			}
		} else {
			state = status.State
		}
		for i := range views {
			view := views[i]
			view.Name, view.UserName, view.UserKnown = target.Name, row.UserName, row.UserNameKnown
			view.State = state
			view.Mode = mode
			view.Glyphs = glyphs
			decorations = append(decorations, view)
		}
	}
	provider.probe.commit()
	return decorations, nil
}

// SessionEnd releases the agent before the engine exits, and a shell can keep
// its pane open afterwards. Neither a missing agent nor a listed pane proves
// death: refresh the pane identity and foreground, allowing a bounded transition.
// An engine still in the foreground past it runs in a pane Herdr tracks no agent
// for (a native seat), and its own screen tells its state.
const tabAgentExitGrace = 5 * time.Second

func missingTabAgentState(
	ctx context.Context,
	target headless.Chat,
	terminal string,
	elapsed time.Duration,
	screen func(context.Context, headless.Chat) (string, error),
	engine func(context.Context, fleetdb.Seat) (bool, error),
) (string, error) {
	panes, err := herdr.ReadTabPanes(ctx, target.Socket)
	// An incomplete listing still holds every complete pane: this chat's pane is
	// judged when it is among them, and stays unknown when it may be a skipped one.
	var incomplete *herdr.IncompleteTabPanesError
	if err != nil && !errors.As(err, &incomplete) {
		return tabStateUnknown, fmt.Errorf("confirm tab pane %s: %w", target.Pane, err)
	}
	for _, pane := range panes {
		if terminal != "" && pane.Terminal != terminal || terminal == "" && pane.Pane != target.Pane {
			continue
		}
		alive, err := engine(
			ctx,
			fleetdb.Seat{HerdrSocket: target.Socket, HerdrPane: pane.Pane, Engine: string(target.Engine)},
		)
		if err != nil {
			return tabStateUnknown, fmt.Errorf("confirm released tab agent %s: %w", pane.Pane, err)
		}
		if !alive {
			return headless.StateDead, nil
		}
		if elapsed < tabAgentExitGrace {
			return tabStateUnknown, nil
		}
		target.Pane = pane.Pane
		capture, err := screen(ctx, target)
		if err != nil {
			return tabStateUnknown, fmt.Errorf("read tab pane %s without its agent: %w", pane.Pane, err)
		}
		return paneState(target.Engine, capture), nil
	}
	if incomplete != nil {
		return tabStateUnknown, fmt.Errorf("confirm tab pane %s: %w", target.Pane, err)
	}
	return "", nil
}

// tickWarnings names each per-chat tab diagnostic once while it recurs on
// every half-second tick: a body written in the previous tick is not written
// again, and one that stopped recurring is named afresh when it returns.
type tickWarnings struct {
	out        io.Writer
	last, this map[string]bool
}

func (writer *tickWarnings) tick() {
	writer.last, writer.this = writer.this, map[string]bool{}
}

func (writer *tickWarnings) Write(body []byte) (int, error) {
	if writer.this == nil {
		writer.this = map[string]bool{}
	}
	key := string(body)
	writer.this[key] = true
	if writer.last[key] {
		return len(body), nil
	}
	return writer.out.Write(body)
}

type tabIndicatorWarnings struct {
	dir    string
	stderr io.Writer
}

func (writer tabIndicatorWarnings) Write(body []byte) (int, error) {
	if writer.stderr != nil {
		if _, err := writer.stderr.Write(body); err != nil {
			return 0, fmt.Errorf("write tab diagnostic: %w", err)
		}
	}
	if err := herdr.AppendError(writer.dir, "tab-status", clock.Real.Now(), string(body)); err != nil {
		return 0, fmt.Errorf("persist tab diagnostic: %w", err)
	}
	return len(body), nil
}

// tabViewReader's prove is the native seat ownership proof (nil: nativeAlive),
// engine its foreground engine probe (nil: call.HerdrEngine), state the
// shared state native seats are read from (nil: opened per read, nativeSeat).
type tabViewReader struct {
	runtime *pfmconfig.Runtime
	tmux    func(context.Context, string, ...string) (string, error)
	resolve func(context.Context, int) (herdr.Location, bool, error)
	prove   func(context.Context, fleetdb.Seat, paths.Values) (fleetdb.Seat, bool, error)
	engine  func(context.Context, fleetdb.Seat) (bool, error)
	state   func(context.Context) (*fleetdb.Store, error)
	// bridge proves a pfm tmux client in a Herdr pane's foreground; nil is
	// call.ProveHerdrBridge.
	bridge   func(context.Context, string, string) (call.HerdrBridge, error)
	warn     io.Writer
	previous map[string][]herdr.TabDecoration
}

// liveness returns a native tab's dead/unknown state over a proved owner; an
// empty state leaves live status to the inspector. proved=false
// leaves the tab untouched: pfm never paints a pane whose owner is unproved.
func (reader *tabViewReader) liveness(
	ctx context.Context,
	target headless.Chat,
	seat fleetdb.Seat,
) (state string, proved bool, err error) {
	prove := reader.prove
	if prove == nil {
		prove = nativeAlive
	}
	canonical, owned, proofErr := prove(ctx, seat, reader.runtime.Paths)
	if proofErr != nil {
		return "", false, reader.unproved(target, proofErr)
	}
	if !owned {
		return headless.StateDead, true, nil
	}
	engine := reader.engine
	if engine == nil {
		engine = call.HerdrEngine
	}
	// The supervisor outlives an abruptly dead engine until it retires the seat.
	alive, err := engine(ctx, canonical)
	if err != nil {
		if _, writeErr := fmt.Fprintf(
			reader.warn,
			"Herdr tab engine of %s unknown: %v\n",
			target.ID,
			err,
		); writeErr != nil {
			return "", true, fmt.Errorf("persist tab engine failure: %w", errors.Join(err, writeErr))
		}
		return tabStateUnknown, true, nil
	}
	if !alive {
		return headless.StateDead, true, nil
	}
	return "", true, nil
}

// unproved records, through the painter's diagnostic writer (the Herdr plugin
// log and the persistent Herdr error log), why a chat's tab was left untouched.
func (reader *tabViewReader) unproved(target headless.Chat, proof error) error {
	if _, err := fmt.Fprintf(
		reader.warn,
		"Herdr tab of %s left untouched: seat %s ownership unproved: %v\n",
		target.ID,
		target.SeatID,
		proof,
	); err != nil {
		return fmt.Errorf("persist tab ownership failure: %w", errors.Join(proof, err))
	}
	return nil
}

func (reader *tabViewReader) views(
	ctx context.Context,
	target headless.Chat,
	socket string,
	panes []herdr.TabPane,
) ([]herdr.TabDecoration, error) {
	if reader.previous == nil {
		reader.previous = map[string][]herdr.TabDecoration{}
	}
	if target.Backend == spawn.BackendHerdr {
		if target.Socket != socket {
			return nil, nil
		}
		for _, pane := range panes {
			if pane.Pane != target.Pane {
				continue
			}
			return reader.nativeView(ctx, target, socket, pane)
		}
		return nil, nil
	}
	if !target.Live {
		var dead []herdr.TabDecoration
		for i := range reader.previous[target.ID] {
			old := reader.previous[target.ID][i]
			for _, pane := range panes {
				if pane.Terminal == old.Terminal {
					matches, err := tabPaneSession(pane, target)
					if errors.Is(err, errUnusableTabClaim) {
						// The terminal is the one this chat's tab proved: an unreadable claim vetoes nothing.
						if err := reader.warnUnusableClaim(target, pane, err); err != nil {
							return nil, err
						}
						matches, err = true, nil
					}
					if err != nil {
						return nil, err
					}
					if matches {
						// No live client remains to prove: the tab is acted on only
						// through pfm's own label record of this identity.
						view := tabView(pane, old.Identity)
						view.Retained = true
						dead = append(dead, view)
					}
				}
			}
		}
		return dead, nil
	}
	// A live row with no tmux socket has no live tmux server, so no tab of it.
	if target.Socket == "" {
		return nil, nil
	}
	// headless.Chat carries the tmux socket name; every tmux read and the
	// bridge proof's server comparison take its path under the TmuxDir.
	if reader.runtime == nil {
		return nil, fmt.Errorf("resolve tmux socket %q: no runtime paths", target.Socket)
	}
	tmuxSocket, err := reader.runtime.Paths.SocketUnder(target.Socket)
	if err != nil {
		return nil, fmt.Errorf("resolve tmux socket %q: %w", target.Socket, err)
	}
	raw, err := reader.stored(ctx, tmuxSocket, target)
	if err != nil {
		return nil, err
	}
	var stored herdr.Stored
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &stored); err != nil {
			// A mapping pfm cannot read is rebuilt from the live clients, which the bridge proof checks.
			if _, writeErr := fmt.Fprintf(
				reader.warn,
				"Herdr tab client mappings of %s unreadable (%v); using the live clients\n",
				target.ID,
				err,
			); writeErr != nil {
				return nil, fmt.Errorf("persist tab client mappings failure: %w", errors.Join(err, writeErr))
			}
			stored = herdr.Stored{}
		}
	}
	if stored.SessionID != "" && stored.SessionID != target.ID {
		return nil, nil
	}
	output, err := reader.tmux(ctx, tmuxSocket, "list-clients", "-t", tabTmuxTarget(target), "-F", "#{client_pid}")
	if err != nil {
		return nil, fmt.Errorf("list tab tmux clients: %w", err)
	}
	seen := map[string]bool{}
	var views []herdr.TabDecoration
	for _, pidText := range strings.Fields(output) {
		// One client that cannot be read costs that client's view, named, never its siblings'.
		pid, err := strconv.Atoi(pidText)
		if err != nil {
			if _, writeErr := fmt.Fprintf(
				reader.warn,
				"Herdr tab client %q of %s skipped: not a pid\n",
				pidText,
				target.ID,
			); writeErr != nil {
				return nil, fmt.Errorf("persist tab client failure: %w", writeErr)
			}
			continue
		}
		location, found, err := reader.resolve(ctx, pid)
		if err != nil {
			if _, writeErr := fmt.Fprintf(
				reader.warn,
				"Herdr tab client %d of %s skipped: %v\n",
				pid,
				target.ID,
				err,
			); writeErr != nil {
				return nil, fmt.Errorf("persist tab client failure: %w", errors.Join(err, writeErr))
			}
			continue
		}
		if !found || location.Socket != socket || seen[location.Terminal] {
			continue
		}
		seen[location.Terminal] = true
		for _, pane := range panes {
			if pane.Terminal == location.Terminal && pane.Pane == location.Pane {
				// Resolve live client pids, not a possibly stale registered mapping.
				d := tabView(pane, target.Socket+":"+target.Session+":"+target.ID)
				d.Validate = func(ctx context.Context) (bool, error) {
					current, err := reader.stored(ctx, tmuxSocket, target)
					if err != nil {
						return false, err
					}
					if current != raw {
						return false, nil
					}
					now, found, err := reader.resolve(ctx, pid)
					if err != nil {
						return false, err
					}
					if !found || now != location {
						return false, nil
					}
					return reader.bridged(ctx, target, tmuxSocket, socket, location.Pane, pid)
				}
				views = append(views, d)
			}
		}
	}
	reader.previous[target.ID] = views
	return views, nil
}

func (reader *tabViewReader) stored(ctx context.Context, tmuxSocket string, target headless.Chat) (string, error) {
	raw, err := reader.tmux(ctx, tmuxSocket, "show-options", "-qv", "-t", tabTmuxTarget(target), herdr.StoredOption)
	if err != nil {
		return "", fmt.Errorf("read tab client generation: %w", err)
	}
	return raw, nil
}

// bridged proves the Herdr pane hosts the listed client of this chat's tmux
// server: a proof error is visible, a client of another server, session or
// pid leaves the tab untouched.
func (reader *tabViewReader) bridged(
	ctx context.Context,
	target headless.Chat,
	tmuxSocket, socket, pane string,
	pid int,
) (bool, error) {
	prove := reader.bridge
	if prove == nil {
		prove = call.ProveHerdrBridge
	}
	proof, err := prove(ctx, socket, pane)
	if err != nil {
		return false, err
	}
	if proof.ClientPID != pid || filepath.Clean(proof.TmuxSocket) != filepath.Clean(tmuxSocket) {
		return false, nil
	}
	return target.Session == "" || proof.Session == target.Session, nil
}

func tabTmuxTarget(target headless.Chat) string {
	if target.Session != "" {
		return target.Session
	}
	return target.Pane
}

func tabView(pane herdr.TabPane, identity string) herdr.TabDecoration {
	return herdr.TabDecoration{
		Pane:        pane.Pane,
		Terminal:    pane.Terminal,
		Tab:         pane.Tab,
		Identity:    identity,
		Session:     pane.Session,
		AgentStatus: pane.State,
	}
}

// staleTabClaim says the pane's agent_session names a chat whose own native
// seat was born in another terminal: Herdr kept a replaced chat's claim, so
// the claim never vetoes the seat that proves the pane's terminal. A claim
// pfm cannot place (a path, an unknown chat) stands.
func (reader *tabViewReader) staleTabClaim(ctx context.Context, pane herdr.TabPane) (bool, error) {
	var session struct{ Kind, Value string }
	if err := json.Unmarshal(pane.Session, &session); err != nil {
		return false, fmt.Errorf("decode tab agent_session: %w", err)
	}
	if session.Kind != "id" {
		return false, nil
	}
	claimed, found, err := reader.seat(ctx, session.Value, "")
	if err != nil {
		return false, fmt.Errorf("read the native seat Herdr names in tab %s: %w", pane.Tab, err)
	}
	return found && claimed.HerdrTerminalID != "" && claimed.HerdrTerminalID != pane.Terminal, nil
}

// errUnusableTabClaim marks an agent_session pfm cannot read (undecodable,
// valueless or of an unknown kind): it proves nothing and vetoes nothing.
var errUnusableTabClaim = errors.New("unusable tab agent_session")

// warnUnusableClaim names a pane's unusable claim, which never stops its tab.
func (reader *tabViewReader) warnUnusableClaim(target headless.Chat, pane herdr.TabPane, claimErr error) error {
	if _, err := fmt.Fprintf(
		reader.warn,
		"Herdr tab %s of %s: %v; the pane's terminal proves it\n",
		pane.Tab,
		target.ID,
		claimErr,
	); err != nil {
		return fmt.Errorf("persist tab claim failure: %w", errors.Join(claimErr, err))
	}
	return nil
}

func tabPaneSession(pane herdr.TabPane, target headless.Chat) (bool, error) {
	if len(pane.Session) == 0 || string(pane.Session) == "null" {
		return false, nil
	}
	var session struct{ Kind, Value string }
	if err := json.Unmarshal(pane.Session, &session); err != nil {
		return false, fmt.Errorf("%w: decode: %w", errUnusableTabClaim, err)
	}
	if session.Value == "" {
		return false, fmt.Errorf("%w: incomplete: %s", errUnusableTabClaim, pane.Session)
	}
	switch session.Kind {
	case "id":
		return session.Value == target.ID, nil
	case "path":
		if target.Path == "" {
			return false, fmt.Errorf("cannot verify tab session path %q without transcript path", session.Value)
		}
		return filepath.Clean(session.Value) == filepath.Clean(target.Path), nil
	default:
		return false, fmt.Errorf("%w: unsupported kind %q", errUnusableTabClaim, session.Kind)
	}
}

func (reader *tabViewReader) nativeView(
	ctx context.Context,
	target headless.Chat,
	socket string,
	pane herdr.TabPane,
) ([]herdr.TabDecoration, error) {
	// A seat's recorded terminal must be the pane's. Herdr emits no
	// agent_session for a pfm-native pane: absent, that terminal is the only
	// proof; present, it must name the chat, unless it is a replaced chat's
	// claim Herdr kept (reader.staleTabClaim).
	sessionNamed := len(pane.Session) != 0 && string(pane.Session) != "null"
	matches, claimErr := tabPaneSession(pane, target)
	if claimErr != nil && !errors.Is(claimErr, errUnusableTabClaim) {
		return nil, claimErr
	}
	seat, found, err := reader.seat(ctx, target.ID, target.SeatID)
	if err != nil {
		return nil, fmt.Errorf("read native tab seat: %w", err)
	}
	if !found || seat.ReloadPending || seat.HerdrSocket != socket || seat.HerdrPane != pane.Pane ||
		seat.HerdrTerminalID != pane.Terminal && (seat.HerdrTerminalID != "" || !sessionNamed) {
		return nil, nil
	}
	if sessionNamed && !matches && claimErr != nil {
		// An unreadable claim vetoes nothing, but only the seat's own terminal can stand in for it.
		if seat.HerdrTerminalID == "" {
			return nil, claimErr
		}
		if err := reader.warnUnusableClaim(target, pane, claimErr); err != nil {
			return nil, err
		}
	} else if sessionNamed && !matches {
		stale, err := reader.staleTabClaim(ctx, pane)
		if err != nil || !stale || seat.HerdrTerminalID == "" {
			return nil, err
		}
	}
	state, proved, err := reader.liveness(ctx, target, seat)
	if err != nil || !proved {
		// An unproved owner never stops the other tabs.
		return nil, err
	}
	d := tabView(pane, target.SeatID+":"+target.ID)
	d.State = state
	d.Validate = func(ctx context.Context) (bool, error) {
		seat, found, err := reader.seat(ctx, target.ID, target.SeatID)
		if err != nil {
			return false, fmt.Errorf("read native tab owner: %w", err)
		}
		if !found || seat.SessionID != target.ID || seat.HerdrSocket != socket || seat.HerdrPane != target.Pane ||
			seat.ReloadPending || seat.HerdrTerminalID != pane.Terminal && (seat.HerdrTerminalID != "" || !sessionNamed) {
			return false, nil
		}
		currentState, proved, err := reader.liveness(ctx, target, seat)
		if err != nil || !proved {
			return false, err
		}
		return currentState == state, nil
	}
	reader.previous[target.ID] = []herdr.TabDecoration{d}
	return []herdr.TabDecoration{d}, nil
}
