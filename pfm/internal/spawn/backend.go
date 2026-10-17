package spawn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/herdr"
)

const (
	BackendAuto  = "auto"
	BackendTmux  = "tmux"
	BackendHerdr = "herdr"
)

// BackendChoice is the immutable birth location chosen for a chat.
type BackendChoice struct{ Backend, HerdrSocket, HerdrPane, HerdrWorkspace, Warning string }

type backendEnvironment struct {
	Requested, Marker, Socket, Pane, TMUX string
	Requester                             *fleetdb.Seat
}

// ChooseBackend applies an explicit/config backend or the auto birth rule.
func ChooseBackend(
	ctx context.Context,
	requested string,
	environ []string,
	requester *fleetdb.Seat,
) (BackendChoice, error) {
	values := make(map[string]string)
	for _, entry := range environ {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			values[key] = value
		}
	}
	env := backendEnvironment{
		Requested: requested,
		Marker:    values[herdr.EnvMarker],
		Socket:    values[herdr.EnvSocket],
		Pane:      values[herdr.EnvPane],
		TMUX:      values["TMUX"],
		Requester: requester,
	}
	return selectBackend(env, func(socket, pane string) error {
		probe, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		defer cancel()
		body, err := herdr.SeatRequest(probe, socket, "pane.get", map[string]string{"pane_id": pane})
		var dial *net.OpError
		if errors.As(err, &dial) {
			return dial
		}
		if err != nil {
			return err
		}
		var result struct {
			Pane struct {
				ID string `json:"pane_id"`
			} `json:"pane"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			return fmt.Errorf("decode Herdr pane probe: %w", err)
		}
		if result.Pane.ID != pane {
			return fmt.Errorf("herdr pane probe for %s returned %q", pane, result.Pane.ID)
		}
		return nil
	})
}

func selectBackend(env backendEnvironment, probe func(string, string) error) (BackendChoice, error) {
	requested := env.Requested
	if requested == "" {
		requested = BackendAuto
	}
	if requested != BackendAuto && requested != BackendTmux && requested != BackendHerdr {
		return BackendChoice{}, fmt.Errorf("backend %q: want auto, tmux or herdr", requested)
	}
	tmux := BackendChoice{Backend: BackendTmux}
	if requested == BackendTmux {
		return tmux, nil
	}
	workspace := ""
	if env.Requester != nil {
		if env.Requester.Backend == BackendTmux && requested == BackendAuto {
			return tmux, nil
		}
		if env.Requester.Backend == BackendHerdr {
			env.Marker = "1"
			env.Socket = env.Requester.HerdrSocket
			env.Pane = env.Requester.HerdrPane
			env.TMUX = ""
			workspace = env.Requester.HerdrWorkspace
		}
	}
	if requested == BackendAuto && (env.TMUX != "" || env.Marker != "1" || env.Socket == "" || env.Pane == "") {
		return tmux, nil
	}
	if env.Socket == "" || env.Pane == "" {
		return BackendChoice{}, fmt.Errorf("herdr backend requires HERDR_SOCKET_PATH and HERDR_PANE_ID")
	}
	if err := probe(env.Socket, env.Pane); err != nil {
		if requested == BackendHerdr {
			return BackendChoice{}, fmt.Errorf("herdr pane %s unreachable (%w)", env.Pane, err)
		}
		tmux.Warning = fmt.Sprintf("pfm: Herdr pane %s unreachable (%v) — starting in tmux", env.Pane, err)
		return tmux, nil
	}
	if workspace == "" {
		workspace, _, _ = strings.Cut(env.Pane, ":")
	}
	return BackendChoice{
		Backend:        BackendHerdr,
		HerdrSocket:    env.Socket,
		HerdrPane:      env.Pane,
		HerdrWorkspace: workspace,
	}, nil
}
