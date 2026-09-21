package resolve

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// Seat is one resolved live destination. It is deliberately independent of
// chat and inject so both layers can use the same target ladder.
type Seat struct {
	SocketPath string `json:"socket_path"`
	Pane       string `json:"pane"`
	Engine     string `json:"engine"`
	Name       string `json:"name,omitempty"`
	ID         string `json:"id,omitempty"`
	Session    string `json:"session,omitempty"`
}

// LadderOptions select the raw namespaces and optional engine restriction.
type LadderOptions struct {
	RequiredEngine string
	Kinds          []Kind
}

// RosterResolver is the composed-fleet rung of Ladder.
type RosterResolver interface {
	ResolveRoster(context.Context, string, string) (Seat, int, string, error)
}

// RosterFunc adapts a caller-owned roster implementation without importing it.
type RosterFunc func(context.Context, string, string) (Seat, int, string, error)

// ResolveRoster implements RosterResolver.
func (fn RosterFunc) ResolveRoster(
	ctx context.Context,
	name, requiredEngine string,
) (Seat, int, string, error) {
	return fn(ctx, name, requiredEngine)
}

// RawResolver supplies the label/session/window namespaces.
type RawResolver interface {
	Resolve(context.Context, Kind, string) (Outcome, error)
}

// SelfIdentifier supplies an ambient or request-independent caller identity.
type SelfIdentifier interface {
	Identify(context.Context) (Identity, error)
}

// Environment is the narrow environment seam needed by the raw-pane rungs.
type Environment interface {
	Get(string) string
}

// SessionReader supplies the current session for a resolved socket.
type SessionReader interface {
	CurrentSession(context.Context, string) (string, error)
}

// Ladder owns the one target-resolution order shared by every chat surface.
type Ladder struct {
	Roster          RosterResolver
	Raw             RawResolver
	Self            SelfIdentifier
	CodexSelf       SelfIdentifier
	RequestIdentity *Identity
	Env             Environment
	Session         SessionReader
}

// Resolve applies empty, self, raw-pane, roster and raw namespace rungs.
func (ladder Ladder) Resolve(
	ctx context.Context,
	name string,
	options LadderOptions,
) (Seat, int, string, error) {
	name = NormalizeTarget(name)
	if name == "" {
		return Seat{}, CodeUnknown, "empty target", nil
	}
	if options.RequiredEngine != "" && options.RequiredEngine != string(pfmengine.Codex) {
		return Seat{}, CodeUndelivered, "", fmt.Errorf(
			"unsupported engine-scoped resolver %q", options.RequiredEngine,
		)
	}
	if options.RequiredEngine == "" && (name == "self" || name == "me") {
		return ladder.resolveSelf(ctx)
	}
	if options.RequiredEngine == "" && IsRawPane(name) {
		socket := ""
		if ladder.RequestIdentity != nil {
			socket = ladder.RequestIdentity.SocketPath
			if socket == "" {
				return Seat{}, CodeUnknown, "raw pane target request identity has no socket", nil
			}
		} else if ladder.Env != nil {
			socket = ladder.Env.Get("CHAT_INJECT_SOCKET")
			if socket == "" {
				socket = socketFromEnvironment(ladder.Env.Get("TMUX"))
			}
		}
		if socket == "" {
			return Seat{}, CodeUnknown, "raw pane target requires TMUX", nil
		}
		return SeatFromParts(socket, name, ladder.Env), 0, "", nil
	}
	if ladder.Roster != nil {
		seat, code, detail, err := ladder.Roster.ResolveRoster(ctx, name, options.RequiredEngine)
		if err != nil {
			return Seat{}, CodeUndelivered, "", err
		}
		switch code {
		case 0:
			return seat, 0, detail, nil
		case CodeAmbiguous:
			return Seat{}, CodeAmbiguous, detail, nil
		case CodeUnknown:
		default:
			return Seat{}, CodeUndelivered, "", fmt.Errorf(
				"roster resolver returned unsupported code %d", code,
			)
		}
	}
	if ladder.Raw == nil {
		return Seat{}, CodeUnknown, fmt.Sprintf("target %q matched no live chat", name), nil
	}
	kinds := options.Kinds
	if len(kinds) == 0 {
		kinds = []Kind{Session, Label, CxWindow}
	}
	if options.RequiredEngine != "" {
		kinds = []Kind{CxWindow}
	}
	for _, kind := range kinds {
		outcome, err := ladder.Raw.Resolve(ctx, kind, name)
		if err != nil {
			return Seat{}, CodeUndelivered, "", err
		}
		switch outcome.Code {
		case 0:
			socket, pane, ok := parseTarget(outcome.Stdout)
			if !ok {
				return Seat{}, CodeUndelivered, "", fmt.Errorf(
					"resolver %s returned malformed target", kind,
				)
			}
			seat := SeatFromParts(socket, pane, ladder.Env)
			seat.Name = name
			if ladder.Session != nil {
				if session, sessionErr := ladder.Session.CurrentSession(ctx, socket); sessionErr == nil {
					seat.Session = session
				}
			}
			if kind == CxWindow {
				seat.Engine = string(pfmengine.Codex)
			}
			return seat, 0, outcome.Stderr, nil
		case CodeAmbiguous:
			return Seat{}, CodeAmbiguous, outcome.Stderr, nil
		}
	}
	return Seat{}, CodeUnknown, fmt.Sprintf("target %q matched no live chat", name), nil
}

func (ladder Ladder) resolveSelf(ctx context.Context) (Seat, int, string, error) {
	identity := Identity{}
	var err error
	if ladder.Self != nil {
		identity, err = ladder.Self.Identify(ctx)
	}
	if (err != nil || identity.Session == "") && ladder.CodexSelf != nil &&
		ladder.Env != nil && ladder.Env.Get(CodexThreadEnv) != "" {
		identity, err = ladder.CodexSelf.Identify(ctx)
	}
	if err != nil && !errors.Is(err, ErrNoTmux) {
		return Seat{}, CodeUndelivered, "", err
	}
	if err != nil || identity.Session == "" || identity.SocketPath == "" {
		return Seat{}, CodeUnknown, "self target has no live tmux seat", nil
	}
	pane := identity.Pane
	if pane == "" {
		pane = identity.Session
	}
	seat := SeatFromParts(identity.SocketPath, pane, ladder.Env)
	seat.ID = identity.ID
	seat.Session = identity.Session
	if identity.Engine != "" {
		seat.Engine = identity.Engine
	}
	return seat, 0, "", nil
}

// SeatFromParts derives the engine namespace from a socket address.
func SeatFromParts(socketPath, pane string, env Environment) Seat {
	base := filepath.Base(socketPath)
	id, ok := pfmengine.FromSocket(base)
	if !ok && env != nil && env.Get("PFM_TEST_PROBE_SOCKETS") == "1" {
		id, ok = pfmengine.FromSocket(strings.TrimPrefix(base, "probe-"))
	}
	if !ok {
		return Seat{SocketPath: socketPath, Pane: pane, Engine: "unknown"}
	}
	return Seat{SocketPath: socketPath, Pane: pane, Engine: string(id)}
}

// NormalizeTarget applies the target spelling accepted by Ladder before its
// namespace classifiers run.
func NormalizeTarget(name string) string {
	trimmed := strings.TrimSpace(name)
	if len(trimmed) >= 2 && strings.HasPrefix(trimmed, `"`) && strings.HasSuffix(trimmed, `"`) {
		return strings.TrimSpace(trimmed[1 : len(trimmed)-1])
	}
	return trimmed
}

func socketFromEnvironment(value string) string {
	if comma := strings.IndexByte(value, ','); comma >= 0 {
		return value[:comma]
	}
	return value
}

// IsRawPane reports whether value is an exact tmux pane id: % followed by
// decimal digits.
func IsRawPane(value string) bool {
	if len(value) < 2 || value[0] != '%' {
		return false
	}
	for _, character := range value[1:] {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func parseTarget(line string) (string, string, bool) {
	fields := strings.Split(strings.TrimSpace(line), "\t")
	if len(fields) != 2 || fields[0] == "" || fields[1] == "" {
		return "", "", false
	}
	return fields[0], fields[1], true
}
