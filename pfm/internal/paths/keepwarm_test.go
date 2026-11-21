package paths

import (
	"strings"
	"testing"
)

func TestKeepwarmStateFile(t *testing.T) {
	for _, tc := range []struct{ name, home, session, override, xdg, want, fail string }{
		{name: "default", home: "/jail/home", session: "sess-1", want: "/jail/home/.local/state/cache-live-control/keepwarm/sessions/sess-1.json"},
		{name: "XDG", session: "S", xdg: "/jail/state", want: "/jail/state/cache-live-control/keepwarm/sessions/S.json"},
		{name: "override wins", session: "S", override: " /jail/override ", xdg: "/jail/state", want: "/jail/override/S.json"},
		{name: "blank override", home: "/jail/home", session: "S", override: "  ", xdg: " ", want: "/jail/home/.local/state/cache-live-control/keepwarm/sessions/S.json"},
		{name: "no home", session: "S", fail: "home directory is unknown"},
		{name: "no id", home: "/jail/home", fail: "unsafe session id"},
		{name: "traversal", home: "/jail/home", session: "../other", fail: "unsafe session id"},
		{name: "unicode", home: "/jail/home", session: "☀", fail: "unsafe session id"},
		{name: "relative override falls to XDG", home: "/jail/home", session: "S", override: "relative", xdg: "/jail/state", want: "/jail/state/cache-live-control/keepwarm/sessions/S.json"},
		{name: "relative XDG falls to home", home: "/jail/home", session: "S", xdg: "relative", want: "/jail/home/.local/state/cache-live-control/keepwarm/sessions/S.json"},
		{name: "nothing absolute", home: "rel/home", session: "S", override: "relative", xdg: "xdg", fail: "keepwarm state directory unresolved: CACHE_LIVE_CONTROL_KEEPWARM_STATE_DIR, XDG_STATE_HOME, the home directory not absolute"},
		{name: "relative override and no home", session: "S", override: "relative", fail: "keepwarm state directory unresolved: CACHE_LIVE_CONTROL_KEEPWARM_STATE_DIR not absolute"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := &MapEnv{
				Values: map[string]string{
					"CACHE_LIVE_CONTROL_KEEPWARM_STATE_DIR": tc.override,
					"XDG_STATE_HOME":                        tc.xdg,
				},
			}
			got, err := KeepwarmStateFile(env, tc.home, tc.session)
			if tc.fail != "" {
				if err == nil || !strings.Contains(err.Error(), tc.fail) {
					t.Fatalf("path=%q err=%v, want %q", got, err, tc.fail)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("path=%q err=%v, want %q", got, err, tc.want)
			}
		})
	}
}
