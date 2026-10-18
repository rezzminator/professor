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
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

const tabStateUnknown = "unknown"

// errTabPaneList marks a socket-wide pane listing failure: it fails the tick,
// unlike one chat's view failure, which is named and skipped.
var errTabPaneList = errors.New("list Herdr tab panes")

type herdrTabProvider struct {
	scan    func(context.Context) (FleetScan, error)
	inspect func(context.Context, headless.Chat, time.Time) (headless.Status, error)
	views   func(context.Context, headless.Chat, string) ([]herdr.TabDecoration, error)
	mode    func() (string, error)
	warn    io.Writer
}

func (provider *herdrTabProvider) tabs(ctx context.Context, socket string) ([]herdr.TabDecoration, error) {
	if ticks, ok := provider.warn.(*tickWarnings); ok {
		ticks.tick()
	}
	mode, err := provider.mode()
	if err != nil {
		return nil, fmt.Errorf("read tab status configuration: %w", err)
	}
	scan, err := provider.scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("scan tab chats: %w", err)
	}
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
		} else if status, err := provider.inspect(ctx, target, clock.Real.Now()); err != nil {
			if _, writeErr := fmt.Fprintf(
				provider.warn,
				"Herdr tab status of %s: %v\n",
				target.ID,
				err,
			); writeErr != nil {
				return nil, fmt.Errorf("persist status inspection failure: %w", writeErr)
			}
		} else {
			state = status.State
		}
		for i := range views {
			view := views[i]
			view.Name, view.UserName = target.Name, row.UserName
			view.State = state
			view.Mode = mode
			decorations = append(decorations, view)
		}
	}
	return decorations, nil
}

// PaintHerdrIndicators is the actual plugin command's chat-aware painter.
// The inspector persists across half-second polls and incrementally reads tails.
func PaintHerdrIndicators(ctx context.Context, socket, lockDir string, stderr io.Writer) error {
	persisted := tabIndicatorWarnings{dir: lockDir, stderr: stderr}
	warnings := &tickWarnings{out: persisted}
	runtime, err := pfmconfig.RuntimeOrDefault(nil)
	if err != nil {
		loadErr := fmt.Errorf("load tab indicator runtime: %w", err)
		_, writeErr := fmt.Fprintln(persisted, loadErr)
		return errors.Join(loadErr, writeErr)
	}
	inspector := NewSeatInspector(&runtime)
	provider := herdrTabProvider{inspect: inspector.Inspect, warn: warnings}
	provider.mode = func() (string, error) {
		latest, err := pfmconfig.LoadRuntime(runtime.Config.Path)
		if err != nil {
			return "", err
		}
		return latest.Config.Chat.HerdrTabStatus, nil
	}
	var panes []herdr.TabPane
	var loaded bool
	provider.scan = func(ctx context.Context) (FleetScan, error) {
		loaded = false
		inspector.Sweep()
		return ScanRows(ctx, persisted, &runtime)
	}
	addresses := tabViewReader{
		runtime: &runtime,
		warn:    warnings,
		tmux:    runTmux,
		resolve: herdr.WireResolver(
			gather.NewProcFS(runtime.Paths.ProcRoot).Environ,
			herdr.Call,
			func() uint64 { return uint64(clock.Real.Now().UnixNano()) },
		).Resolve,
	}
	provider.views = func(ctx context.Context, target headless.Chat, socket string) ([]herdr.TabDecoration, error) {
		if !loaded {
			var err error
			panes, err = herdr.ReadTabPanes(ctx, socket)
			if err != nil {
				return nil, fmt.Errorf("%w: %w", errTabPaneList, err)
			}
			loaded = true
		}
		return addresses.views(ctx, target, socket, panes)
	}
	return herdr.PaintIndicatorsWithTabs(ctx, socket, lockDir, persisted, provider.tabs, call.ProveHerdrPane)
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
// engine its foreground engine probe (nil: tabEngineForeground).
type tabViewReader struct {
	runtime *pfmconfig.Runtime
	tmux    func(context.Context, string, ...string) (string, error)
	resolve func(context.Context, int) (herdr.Location, bool, error)
	prove   func(context.Context, fleetdb.Seat, paths.Values) (fleetdb.Seat, bool, error)
	engine  func(context.Context, fleetdb.Seat) (bool, error)
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
		engine = tabEngineForeground
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

// tabEngineForeground reports whether the seat's engine is in its pane's
// foreground, from Herdr's pane.process_info.
func tabEngineForeground(ctx context.Context, seat fleetdb.Seat) (bool, error) {
	body, err := herdr.SeatRequest(
		ctx,
		seat.HerdrSocket,
		"pane.process_info",
		map[string]string{nativePaneIDKey: seat.HerdrPane},
	)
	if err != nil {
		return false, fmt.Errorf("read Herdr foreground processes of %s: %w", seat.HerdrPane, err)
	}
	return herdr.EngineForeground(body, seat.HerdrPane, pfmengine.ID(seat.Engine))
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
	if target.Backend == "herdr" {
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
			return nil, fmt.Errorf("decode tab client mappings: %w", err)
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
		pid, err := strconv.Atoi(pidText)
		if err != nil {
			return nil, fmt.Errorf("invalid tab client pid %q: %w", pidText, err)
		}
		location, found, err := reader.resolve(ctx, pid)
		if err != nil {
			return nil, fmt.Errorf("resolve tab client %d: %w", pid, err)
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
		Revision:    pane.Revision,
		Session:     pane.Session,
		AgentStatus: pane.State,
	}
}

func tabPaneSession(pane herdr.TabPane, target headless.Chat) (bool, error) {
	if len(pane.Session) == 0 || string(pane.Session) == "null" {
		return false, nil
	}
	var session struct{ Source, Agent, Kind, Value string }
	if err := json.Unmarshal(pane.Session, &session); err != nil {
		return false, fmt.Errorf("decode tab agent_session: %w", err)
	}
	if session.Source == "" || session.Agent == "" || session.Value == "" {
		return false, fmt.Errorf("incomplete tab agent_session: %s", pane.Session)
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
		return false, fmt.Errorf("unsupported tab agent_session kind %q", session.Kind)
	}
}

func (reader *tabViewReader) nativeView(
	ctx context.Context,
	target headless.Chat,
	socket string,
	pane herdr.TabPane,
) ([]herdr.TabDecoration, error) {
	// Herdr emits no agent_session for a pfm-native pane: absent, the seat's
	// own recorded terminal must be the pane's; present, it must name the chat.
	sessionNamed := len(pane.Session) != 0 && string(pane.Session) != "null"
	matches, err := tabPaneSession(pane, target)
	if err != nil {
		return nil, err
	}
	if sessionNamed && !matches {
		return nil, nil
	}
	seat, found, err := nativeSeat(ctx, *reader.runtime, target.ID, target.SeatID)
	if err != nil {
		return nil, fmt.Errorf("read native tab seat: %w", err)
	}
	if !found || seat.ReloadPending || seat.HerdrSocket != socket || seat.HerdrPane != pane.Pane ||
		!sessionNamed && (seat.HerdrTerminalID == "" || seat.HerdrTerminalID != pane.Terminal) {
		return nil, nil
	}
	state, proved, err := reader.liveness(ctx, target, seat)
	if err != nil || !proved {
		// An unproved owner never stops the other tabs.
		return nil, err
	}
	d := tabView(pane, target.SeatID+":"+target.ID)
	d.State = state
	d.Validate = func(ctx context.Context) (bool, error) {
		seat, found, err := nativeSeat(ctx, *reader.runtime, target.ID, target.SeatID)
		if err != nil {
			return false, fmt.Errorf("read native tab owner: %w", err)
		}
		if !found || seat.SessionID != target.ID || seat.HerdrSocket != socket || seat.HerdrPane != target.Pane ||
			seat.ReloadPending || !sessionNamed && seat.HerdrTerminalID != pane.Terminal {
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
