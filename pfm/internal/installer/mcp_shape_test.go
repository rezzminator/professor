package installer

import "testing"

// An env that is present but empty ({} or null) is shape-neutral — Claude Code
// adds it when it rewrites its config — while a non-empty env, or any other
// value under the key, keeps the entry someone else's.
func TestWithoutEmptyEnvDropsOnlyAnEmptyEnv(t *testing.T) {
	t.Parallel()
	base := func(env any) map[string]any {
		return map[string]any{"type": "stdio", "command": "pfm", configEnvKey: env}
	}
	for name, tc := range map[string]struct {
		registration map[string]any
		wantEnv      bool
	}{
		"empty object":  {base(map[string]any{}), false},
		"null":          {base(nil), false},
		"non-empty":     {base(map[string]any{"A": "1"}), true},
		"not an object": {base("x"), true},
		"absent":        {map[string]any{"type": "stdio", "command": "pfm"}, false},
	} {
		got := withoutEmptyEnv(tc.registration)
		if _, has := got[configEnvKey]; has != tc.wantEnv {
			t.Fatalf("%s: env kept=%v, want %v (%v)", name, has, tc.wantEnv, got)
		}
		if _, still := tc.registration[configEnvKey]; name != "absent" && !still {
			t.Fatalf("%s: the caller's map lost its env; want a copy", name)
		}
	}
}
