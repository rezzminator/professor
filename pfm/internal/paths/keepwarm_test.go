package paths

import (
	"strings"
	"testing"
)

func TestCacheKeepwarmStateFile(t *testing.T) {
	for _, tc := range []struct{ name, home, session, override, xdg, want, fail string }{
		{name: "default", home: "/jail/home", session: "sess-1", want: "/jail/home/.local/state/cache-keepwarm/sessions/sess-1.json"},
		{name: "XDG", session: "S", xdg: "/jail/state", want: "/jail/state/cache-keepwarm/sessions/S.json"},
		{name: "override wins", session: "S", override: " /jail/override ", xdg: "/jail/state", want: "/jail/override/S.json"},
		{name: "blank override", home: "/jail/home", session: "S", override: "  ", xdg: " ", want: "/jail/home/.local/state/cache-keepwarm/sessions/S.json"},
		{name: "no home", session: "S", fail: "home directory is unknown"},
		{name: "no id", home: "/jail/home", fail: "unsafe session id"},
		{name: "traversal", home: "/jail/home", session: "../other", fail: "unsafe session id"},
		{name: "unicode", home: "/jail/home", session: "☀", fail: "unsafe session id"},
		{name: "relative override", home: "/jail/home", session: "S", override: "relative", fail: "not absolute"},
		{name: "relative XDG", home: "/jail/home", session: "S", xdg: "relative", fail: "not absolute"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := &MapEnv{Values: map[string]string{EnvCacheKeepwarmStateDir: tc.override, "XDG_STATE_HOME": tc.xdg}}
			got, err := CacheKeepwarmStateFile(env, tc.home, tc.session)
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
