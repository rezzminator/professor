package obs

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"hostops/pfm/internal/paths"
)

// TestComponentsPinsTheSpecRegistry: the eleven comp names of spec § Middleware,
// in its order and spelling — a wrapper naming anything else is refused.
func TestComponentsPinsTheSpecRegistry(t *testing.T) {
	want := []string{
		"cli", "mcp", "http.in", "http.out", "runner", "tmux", "hooks", "db", "harvestpy", "installer", "state",
	}
	if strings.Join(Components, ",") != strings.Join(want, ",") {
		t.Fatalf("Components = %v, want %v", Components, want)
	}
	for _, name := range want {
		if !KnownComponent(name) {
			t.Fatalf("KnownComponent(%q) = false", name)
		}
	}
	for _, name := range []string{"", "http", "HTTP.IN", "database", "installer "} {
		if KnownComponent(name) {
			t.Fatalf("KnownComponent(%q) = true", name)
		}
	}
	if FieldComp != "comp" || !Declared(FieldComp) {
		t.Fatalf("FieldComp = %q declared=%t, want the declared key comp", FieldComp, Declared(FieldComp))
	}
}

// TestResolveLevelsPrecedence pins § Control: build default < config log.level
// < PFM_LOG_LEVEL for the global; config log.components < PFM_LOG_COMPONENTS per
// component; a component without its own level inherits the global AND its
// source.
func TestResolveLevelsPrecedence(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		policy     Policy
		env        map[string]string
		wantGlobal LevelInForce
		wantMCP    LevelInForce
		wantDB     LevelInForce
	}{
		{
			name:       "alpha build defaults to debug everywhere",
			policy:     Policy{Version: "1.2.3-alpha"},
			wantGlobal: LevelInForce{slog.LevelDebug, SourceBuild},
			wantMCP:    LevelInForce{slog.LevelDebug, SourceBuild},
			wantDB:     LevelInForce{slog.LevelDebug, SourceBuild},
		},
		{
			name:       "release build defaults to info",
			policy:     Policy{Version: "1.2.3"},
			wantGlobal: LevelInForce{slog.LevelInfo, SourceBuild},
			wantMCP:    LevelInForce{slog.LevelInfo, SourceBuild},
			wantDB:     LevelInForce{slog.LevelInfo, SourceBuild},
		},
		{
			name:       "config level over the build, config component over the config level",
			policy:     Policy{Version: "1.2.3-alpha", Level: "warning", Components: map[string]string{"mcp": "debug"}},
			wantGlobal: LevelInForce{slog.LevelWarn, SourceConfig},
			wantMCP:    LevelInForce{slog.LevelDebug, SourceConfig},
			wantDB:     LevelInForce{slog.LevelWarn, SourceConfig},
		},
		{
			name: "env level over config level; env components over config components",
			policy: Policy{
				Version: "1.2.3", Level: "error", Components: map[string]string{"mcp": "debug", "db": "warn"},
			},
			env:        map[string]string{paths.EnvLogLevel: "info", paths.EnvLogComponents: " mcp = off ,db=debug,"},
			wantGlobal: LevelInForce{slog.LevelInfo, SourceEnv},
			wantMCP:    LevelInForce{LevelOff, SourceEnv},
			wantDB:     LevelInForce{slog.LevelDebug, SourceEnv},
		},
		{
			name:       "alpha debug applies only when the config carries no level",
			policy:     Policy{Version: "1.2.3-alpha", Level: "info"},
			wantGlobal: LevelInForce{slog.LevelInfo, SourceConfig},
			wantMCP:    LevelInForce{slog.LevelInfo, SourceConfig},
			wantDB:     LevelInForce{slog.LevelInfo, SourceConfig},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			levels, err := ResolveLevels(testCase.policy, &paths.MapEnv{Values: testCase.env})
			if err != nil {
				t.Fatalf("ResolveLevels error = %v", err)
			}
			if levels.Global != testCase.wantGlobal {
				t.Fatalf("global = %+v, want %+v", levels.Global, testCase.wantGlobal)
			}
			if got := levels.For("mcp"); got != testCase.wantMCP {
				t.Fatalf("mcp = %+v, want %+v", got, testCase.wantMCP)
			}
			if got := levels.For("db"); got != testCase.wantDB {
				t.Fatalf("db = %+v, want %+v", got, testCase.wantDB)
			}
			if got := levels.For(""); got != levels.Global {
				t.Fatalf("an unscoped record resolves to %+v, want the global %+v", got, levels.Global)
			}
			if len(levels.ByComponent) != len(Components) {
				t.Fatalf("ByComponent holds %d entries, want one per registered component", len(levels.ByComponent))
			}
		})
	}
}

// TestResolveLevelsRefusesUnknownValuesNamingWhatStays: an unknown component
// or level is an error naming the accepted values AND the setting that stays in
// force; the other settings still resolve.
func TestResolveLevelsRefusesUnknownValuesNamingWhatStays(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		policy Policy
		env    map[string]string
		want   []string
	}{
		{
			name:   "bad env level keeps the config level",
			policy: Policy{Version: "1.2.3", Level: "warn"}, env: map[string]string{paths.EnvLogLevel: "chatty"},
			want: []string{
				paths.EnvLogLevel, `"chatty"`, "debug, info, warn, warning, error, off", "keeping warn from config",
			},
		},
		{
			name:   "unknown env component keeps the components map",
			policy: Policy{Version: "1.2.3"}, env: map[string]string{paths.EnvLogComponents: "database=off"},
			want: []string{
				paths.EnvLogComponents, `"database"`, strings.Join(Components, ", "), "keeping info from build",
			},
		},
		{
			name:   "env component without a level",
			policy: Policy{Version: "1.2.3"}, env: map[string]string{paths.EnvLogComponents: "mcp"},
			want: []string{paths.EnvLogComponents, `"mcp"`, "<comp>=<level>", "keeping info from build"},
		},
		{
			name:   "bad config component level keeps the global",
			policy: Policy{Version: "1.2.3", Components: map[string]string{"db": "loud"}},
			want: []string{
				"log.components.db", `"loud"`, "debug, info, warn, warning, error, off", "keeping info from build",
			},
		},
		{
			name:   "unknown config component",
			policy: Policy{Version: "1.2.3", Components: map[string]string{"sql": "off"}},
			want:   []string{"log.components", `"sql"`, strings.Join(Components, ", ")},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			levels, err := ResolveLevels(testCase.policy, &paths.MapEnv{Values: testCase.env})
			if err == nil {
				t.Fatalf("ResolveLevels accepted %+v %v", testCase.policy, testCase.env)
			}
			for _, want := range testCase.want {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error %q does not carry %q", err, want)
				}
			}
			if levels.For("db").Level == LevelOff {
				t.Fatalf("a refused value was applied: %+v", levels)
			}
		})
	}
	// Two bad settings: both are reported, neither silences the other.
	_, err := ResolveLevels(Policy{Version: "1.2.3"}, &paths.MapEnv{Values: map[string]string{
		paths.EnvLogLevel: "chatty", paths.EnvLogComponents: "db=loud",
	}})
	if err == nil || !strings.Contains(err.Error(), "chatty") || !strings.Contains(err.Error(), "loud") {
		t.Fatalf("error = %v, want both refused values named", err)
	}
}

// TestPerComponentControl runs § Proof's per-component level test through the
// real file: `off` writes nothing for its component, `debug` dumps for its
// component alone, an ERROR passes a component set to `warn`, and a component
// without its own level takes the global.
func TestPerComponentControl(t *testing.T) {
	finish, path := openIn(t, t.TempDir(), Settings{
		Cmd: "chat", Version: "1.2.3", Level: "info",
		Components: map[string]string{"mcp": "debug", "db": "off", "tmux": "warn"},
	})
	ctx := context.Background()
	Logger(Component(ctx, "mcp")).Debug("mcp.dumped")
	Logger(Component(ctx, "db")).Error("db.silenced")
	Logger(Component(ctx, "tmux")).Error("tmux.error.passes")
	Logger(Component(ctx, "tmux")).Info("tmux.info.dropped")
	Logger(Component(ctx, "runner")).Debug("runner.debug.dropped")
	Logger(Component(ctx, "runner")).Info("runner.info.passes")
	// A record that names its component as a plain attribute, not through
	// Component(ctx): the component's level still applies.
	Logger(ctx).Error("db.attr.silenced", FieldComp, "db")
	Logger(ctx).Debug("unscoped.debug.dropped")
	finish(0)

	text := recordsText(readLog(t, path))
	for _, want := range []string{"mcp.dumped", "tmux.error.passes", "runner.info.passes", "cmd.start", "cmd.exit"} {
		if !strings.Contains(text, want) {
			t.Fatalf("record %q missing:\n%s", want, text)
		}
	}
	for _, refused := range []string{
		"db.silenced", "tmux.info.dropped", "runner.debug.dropped", "db.attr.silenced", "unscoped.debug.dropped",
	} {
		if strings.Contains(text, refused) {
			t.Fatalf("record %q was written past its component level:\n%s", refused, text)
		}
	}
	if !strings.Contains(text, `"comp":"mcp"`) {
		t.Fatalf("the scoped record carries no comp field:\n%s", text)
	}
}

// TestEnabledAnswersPerComponent lets a middleware skip building a DEBUG dump
// nobody will keep.
func TestEnabledAnswersPerComponent(t *testing.T) {
	finish, _ := openIn(t, t.TempDir(), Settings{
		Cmd: "chat", Version: "1.2.3", Level: "info", Components: map[string]string{"mcp": "debug", "db": "off"},
	})
	defer finish(0)
	ctx := context.Background()
	for _, testCase := range []struct {
		comp  string
		level Level
		want  bool
	}{
		{"mcp", slog.LevelDebug, true},
		{"runner", slog.LevelDebug, false},
		{"runner", slog.LevelInfo, true},
		{"db", slog.LevelError, false},
		{"", slog.LevelInfo, true},
	} {
		if got := Enabled(ctx, testCase.comp, testCase.level); got != testCase.want {
			t.Fatalf("Enabled(%q, %v) = %t, want %t", testCase.comp, testCase.level, got, testCase.want)
		}
	}
	if Enabled(context.Background(), "mcp", slog.LevelDebug) != true {
		t.Fatal("Enabled ignored the process logger")
	}
}

func TestEnabledIsFalseBeforeTheLogOpens(t *testing.T) {
	// The process scope this test sees is whatever an earlier test left; pin
	// the discarding one explicitly.
	processMutex.Lock()
	previous := process
	process = &scope{logger: slog.New(discardHandler{}), timing: previous.timing}
	processMutex.Unlock()
	t.Cleanup(func() {
		processMutex.Lock()
		process = previous
		processMutex.Unlock()
	})
	if Enabled(context.Background(), "mcp", slog.LevelError) {
		t.Fatal("Enabled = true against the discarding logger")
	}
}
