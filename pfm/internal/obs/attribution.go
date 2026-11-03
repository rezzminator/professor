package obs

import (
	"context"
	"log/slog"
	"slices"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

const (
	FieldOp      = "op"
	FieldAction  = "action"
	FieldBeat    = "beat"
	FieldRun     = "run"
	FieldDoor    = "door"
	FieldSession = "session"
	FieldCaller  = "caller"
	FieldCallee  = "callee"
	FieldParent  = "parent"
	EnvBeat      = "PFM_BEAT"
	EnvRun       = "PFM_RUN"
	EnvDoor      = "PFM_DOOR"
	EnvChat      = "PFM_CHAT"
	EnvSession   = "PFM_SESSION"
	EnvBeatFile  = "PFM_BEAT_FILE"
	DoorTyped    = "typed"
	DoorCLI      = "cli"
	DoorPicker   = "picker"
	DoorMCP      = compMCP
	DoorHook     = "hook"
)

var attributionFields = []string{
	FieldOp,
	FieldAction,
	FieldBeat,
	FieldRun,
	FieldDoor,
	FieldChat,
	FieldSession,
	FieldCaller,
	FieldCallee,
	FieldParent,
}

func withTrace(ctx context.Context, trace map[string]any) context.Context {
	var attrs []any
	for _, key := range attributionFields {
		if key == FieldOp || key == FieldAction || key == FieldDoor {
			continue
		}
		if value, ok := trace[key].(string); ok {
			attrs = append(attrs, key, value)
		}
	}
	return With(ctx, attrs...)
}

// attributionHandler resolves scoped fields last, keeping one top-level key
// per identity even when a logger is reused across several requests.
type attributionHandler struct {
	next     slog.Handler
	attrs    []slog.Attr
	groups   []string
	beatFile string
	door     string
}

func attributed(next slog.Handler, env paths.Env, cmd string) slog.Handler {
	door := env.Get(EnvDoor)
	if !validDoor(door) {
		switch cmd {
		case DoorPicker:
			door = DoorPicker
		case "internal":
			door = DoorHook
		case DoorMCP:
			door = DoorMCP
		default:
			door = DoorCLI
		}
	}
	return attributionHandler{next: next, beatFile: env.Get(EnvBeatFile), door: door, attrs: []slog.Attr{
		slog.String(FieldBeat, env.Get(EnvBeat)), slog.String(FieldRun, env.Get(EnvRun)),
		slog.String(FieldChat, env.Get(EnvChat)),
		slog.String(FieldSession, env.Get(EnvSession)),
	}}
}

func validDoor(door string) bool {
	return slices.Contains([]string{DoorTyped, DoorCLI, DoorPicker, DoorMCP, DoorHook}, door)
}

func (handler attributionHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return handler.next.Enabled(ctx, level)
}

func (handler attributionHandler) Handle(ctx context.Context, record slog.Record) error {
	fields := make(map[string]slog.Attr, len(attributionFields))
	for _, key := range attributionFields {
		if key != FieldDoor {
			fields[key] = slog.String(key, "")
		}
	}
	fields[FieldOp] = slog.String(FieldOp, record.Message)
	output := slog.NewRecord(record.Time, record.Level, record.Message, record.PC)
	add := func(attr slog.Attr) {
		if slices.Contains(attributionFields, attr.Key) {
			fields[attr.Key] = attr
		} else {
			output.AddAttrs(attr)
		}
	}
	var local []slog.Attr
	record.Attrs(func(attr slog.Attr) bool {
		local = append(local, attr)
		return true
	})
	for _, attr := range appendGrouped(handler.attrs, handler.groups, local) {
		add(attr)
	}
	if fields[FieldAction].Value.String() != "" && !validDoor(fields[FieldDoor].Value.String()) {
		fields[FieldDoor] = slog.String(FieldDoor, handler.door)
	}
	if fields[FieldAction].Value.String() != "" && handler.beatFile != "" {
		beat, run, err := readActionBeat(handler.beatFile)
		fields[FieldBeat], fields[FieldRun] = slog.String(FieldBeat, beat), slog.String(FieldRun, run)
		if err != nil {
			failure := output.Clone()
			failure.Level, failure.Message = slog.LevelError, "action.unattributed"
			for _, key := range attributionFields {
				if key != FieldAction && key != FieldOp && key != FieldDoor && key != FieldBeat && key != FieldRun {
					failure.AddAttrs(fields[key])
				}
			}
			failure.AddAttrs(slog.String(FieldOp, "action.unattributed"))
			failure.AddAttrs(slog.String(FieldErr, err.Error()))
			if writeErr := handler.next.Handle(ctx, failure); writeErr != nil {
				return writeErr
			}
		}
	}
	for _, key := range attributionFields {
		if (key == FieldBeat || key == FieldRun) && fields[FieldAction].Value.String() == "" {
			continue
		}
		if key == FieldAction && fields[key].Value.String() == "" {
			continue
		}
		if attr, exists := fields[key]; exists {
			output.AddAttrs(attr)
		}
	}
	return handler.next.Handle(ctx, output)
}

func appendGrouped(attrs []slog.Attr, groups []string, local []slog.Attr) []slog.Attr {
	attrs = slices.Clone(attrs)
	if len(groups) == 0 {
		return append(attrs, local...)
	}
	for index := len(attrs) - 1; index >= 0; index-- {
		if attrs[index].Key == groups[0] && attrs[index].Value.Kind() == slog.KindGroup {
			attrs[index].Value = slog.GroupValue(appendGrouped(attrs[index].Value.Group(), groups[1:], local)...)
			return attrs
		}
	}
	return append(attrs, slog.Attr{Key: groups[0], Value: slog.GroupValue(appendGrouped(nil, groups[1:], local)...)})
}

func (handler attributionHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	handler.attrs = appendGrouped(handler.attrs, handler.groups, attrs)
	return handler
}

func (handler attributionHandler) WithGroup(name string) slog.Handler {
	if name != "" {
		handler.groups = append(slices.Clone(handler.groups), name)
	}
	return handler
}
