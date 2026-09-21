package obs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// FieldComp names the component a record belongs to (spec § Middleware): the
// key a middleware scopes with (Component) and `pfm log --comp` filters on.
const FieldComp = "comp"

// Components is the registry of component names, in the spec's order and
// spelling. A level set for a name outside it is refused, so a typo in
// pfm.config.json can never be a silent no-op.
var Components = []string{
	"cli", "mcp", "http.in", "http.out", "runner", "tmux", "hooks", "db", "harvestpy", "installer", "state",
}

// Where a level in force came from, as `pfm doctor` reports it.
const (
	SourceBuild  = "build"
	SourceConfig = "config"
	SourceEnv    = "env"
)

// KnownComponent reports whether name is one Components registers.
func KnownComponent(name string) bool {
	for _, known := range Components {
		if known == name {
			return true
		}
	}
	return false
}

// Policy is the level control as the build and pfm.config.json hand it over:
// the VERSION (an -alpha suffix defaults to debug), log.level and
// log.components. The environment overrides are read by ResolveLevels itself.
type Policy struct {
	Version    string
	Level      string
	Components map[string]string
}

// LevelInForce is one resolved level and where it came from.
type LevelInForce struct {
	Level  Level
	Source string
}

// Levels is the control resolved: the global level and one entry per
// registered component — a component without its own setting carries the
// global level AND its source, so doctor's per-component row is never blank.
type Levels struct {
	Global      LevelInForce
	ByComponent map[string]LevelInForce
}

// For is the level in force for comp: its own entry, else the global (also
// the answer for an unscoped record, comp == "").
func (levels Levels) For(comp string) LevelInForce {
	if own, found := levels.ByComponent[comp]; found {
		return own
	}
	return levels.Global
}

// ResolveLevels applies § Control's precedence: build default < config
// log.level < PFM_LOG_LEVEL for the global; config log.components <
// PFM_LOG_COMPONENTS per component. Every refused value is an error naming the
// accepted values and the setting that stays in force; the refusals are
// joined, and the returned Levels are what stands — never a silently ignored
// override, never a log silenced by a typo.
func ResolveLevels(policy Policy, env paths.Env) (Levels, error) {
	var refused []error
	global := LevelInForce{Level: slog.LevelInfo, Source: SourceBuild}
	if strings.HasSuffix(strings.TrimSpace(policy.Version), AlphaSuffix) {
		global.Level = slog.LevelDebug
	}
	for _, candidate := range []struct{ source, setting, value string }{
		{SourceConfig, "config log.level", policy.Level},
		{SourceEnv, paths.EnvLogLevel, env.Get(paths.EnvLogLevel)},
	} {
		if strings.TrimSpace(candidate.value) == "" {
			continue
		}
		parsed, err := ParseLevel(candidate.value)
		if err != nil {
			refused = append(refused, fmt.Errorf("%s: %w; keeping %s", candidate.setting, err, global))
			continue
		}
		global = LevelInForce{Level: parsed, Source: candidate.source}
	}
	levels := Levels{Global: global, ByComponent: make(map[string]LevelInForce, len(Components))}
	for _, name := range Components {
		levels.ByComponent[name] = global
	}
	for name, value := range policy.Components {
		if err := levels.set(SourceConfig, "config log.components", name, value); err != nil {
			refused = append(refused, err)
		}
	}
	for _, item := range strings.Split(env.Get(paths.EnvLogComponents), ",") {
		if strings.TrimSpace(item) == "" {
			continue
		}
		name, value, found := strings.Cut(item, "=")
		if !found {
			refused = append(refused, fmt.Errorf(
				"%s: %q is not <comp>=<level>; keeping %s",
				paths.EnvLogComponents, strings.TrimSpace(item), levels.For(strings.TrimSpace(name)),
			))
			continue
		}
		if err := levels.set(SourceEnv, paths.EnvLogComponents, strings.TrimSpace(name), value); err != nil {
			refused = append(refused, err)
		}
	}
	return levels, errors.Join(refused...)
}

// set applies one component override, refusing an unknown component (naming
// the registry) or level (naming the spellings) and saying what stays.
func (levels Levels) set(source, setting, name, value string) error {
	if !KnownComponent(name) {
		return fmt.Errorf(
			"%s: component %q is not one of %s; keeping %s",
			setting, name, strings.Join(Components, ", "), levels.For(name),
		)
	}
	parsed, err := ParseLevel(value)
	if err != nil {
		return fmt.Errorf("%s.%s: %w; keeping %s", setting, name, err, levels.For(name))
	}
	levels.ByComponent[name] = LevelInForce{Level: parsed, Source: source}
	return nil
}

// String renders a level in force for an error message: `warn from config`.
func (inForce LevelInForce) String() string {
	return LevelName(inForce.Level) + " from " + inForce.Source
}

// compKey is the context key Component stores the component name under, so
// Enabled and the handler can answer for a component before any record exists.
type compKey struct{}

// Component scopes ctx to one registered component: every record logged
// through it carries comp=name and is filtered at that component's level.
// This is how a middleware joins the log — never by spelling FieldComp itself.
func Component(ctx context.Context, name string) context.Context {
	return With(context.WithValue(ctx, compKey{}, name), FieldComp, name)
}

// Enabled reports whether a record at level for comp would be written, so a
// middleware can skip building a DEBUG dump nobody keeps. Before OpenLog, and
// while the log is off, it is false.
func Enabled(ctx context.Context, comp string, level Level) bool {
	return Logger(ctx).Handler().Enabled(context.WithValue(ctx, compKey{}, comp), level)
}

// componentOf reads the component Component stored on ctx.
func componentOf(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	name, _ := ctx.Value(compKey{}).(string)
	return name
}

// levelHandler is the control stage in front of the destination: it answers
// Enabled and drops a record below the level in force for its component — the
// one bound by Component (WithAttrs), else the one on ctx, else the one the
// record carries as a plain FieldComp attribute, else the global.
type levelHandler struct {
	next   slog.Handler
	levels Levels
	comp   string
}

func (handler levelHandler) Enabled(ctx context.Context, level slog.Level) bool {
	comp := handler.comp
	if comp == "" {
		comp = componentOf(ctx)
	}
	return level >= handler.levels.For(comp).Level
}

func (handler levelHandler) Handle(ctx context.Context, record slog.Record) error {
	comp := handler.comp
	if comp == "" {
		comp = componentOf(ctx)
	}
	if comp == "" {
		record.Attrs(func(attr slog.Attr) bool {
			if attr.Key == FieldComp {
				comp = attr.Value.String()
				return false
			}
			return true
		})
	}
	if record.Level < handler.levels.For(comp).Level {
		return nil
	}
	return handler.next.Handle(ctx, record)
}

func (handler levelHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	bound := handler
	for _, attr := range attrs {
		if attr.Key == FieldComp {
			bound.comp = attr.Value.String()
		}
	}
	bound.next = handler.next.WithAttrs(attrs)
	return bound
}

func (handler levelHandler) WithGroup(name string) slog.Handler {
	bound := handler
	bound.next = handler.next.WithGroup(name)
	return bound
}
