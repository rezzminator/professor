package main

import (
	"os"
	"testing"

	pfmconfig "hostops/pfm/internal/config"
)

// clearCache1HEnv unsets CC_ARM_1H and ENABLE_PROMPT_CACHING_1H — which
// initialCache1H detects by PRESENCE via os.LookupEnv, not by value — and
// restores whatever the shell running the suite had after the test. A dev
// shell that exports these to arm real Claude Code caching would otherwise
// leak into every "config only" case below and pass it for the wrong
// reason. CLAUDECODE is read with plain os.Getenv, where unset and empty
// are indistinguishable, so t.Setenv("CLAUDECODE", "") is the honest way to
// say "unset" for that one.
func clearCache1HEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"CC_ARM_1H", "ENABLE_PROMPT_CACHING_1H"} {
		original, had := os.LookupEnv(key)
		if had {
			if err := os.Unsetenv(key); err != nil {
				t.Fatalf("unset %s: %v", key, err)
			}
		}
		t.Cleanup(func(key, original string, had bool) func() {
			return func() {
				if had {
					if err := os.Setenv(key, original); err != nil {
						t.Errorf("restore %s: %v", key, err)
					}
				} else {
					if err := os.Unsetenv(key); err != nil {
						t.Errorf("keep %s unset: %v", key, err)
					}
				}
			}
		}(key, original, had))
	}
	t.Setenv("CLAUDECODE", "")
}

// TestInitialCache1HEnvOverridesConfig pins that CC_ARM_1H and
// ENABLE_PROMPT_CACHING_1H, when PRESENT at all, win over config in both
// directions — synth.go's NewClaude route depends on this to force caching
// off downstream by re-exporting "CC_ARM_1H=0 ENABLE_PROMPT_CACHING_1H=0"
// into a spawned shell.
func TestInitialCache1HEnvOverridesConfig(t *testing.T) {
	t.Setenv("TMUX_TMPDIR", t.TempDir())

	for _, testCase := range []struct {
		name       string
		env        map[string]string
		configTrue bool
		want       bool
	}{
		{
			name:       "CC_ARM_1H=1 overrides config false",
			env:        map[string]string{"CC_ARM_1H": "1"},
			configTrue: false,
			want:       true,
		},
		{
			name:       "CC_ARM_1H=0 overrides config true",
			env:        map[string]string{"CC_ARM_1H": "0"},
			configTrue: true,
			want:       false,
		},
		{
			name:       "ENABLE_PROMPT_CACHING_1H=1 without CLAUDECODE arms",
			env:        map[string]string{"ENABLE_PROMPT_CACHING_1H": "1"},
			configTrue: false,
			want:       true,
		},
		{
			name:       "ENABLE_PROMPT_CACHING_1H=1 with CLAUDECODE set does not arm",
			env:        map[string]string{"ENABLE_PROMPT_CACHING_1H": "1", "CLAUDECODE": "1"},
			configTrue: true,
			want:       false,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			clearCache1HEnv(t)
			for key, value := range testCase.env {
				t.Setenv(key, value)
			}
			config := pfmconfig.Config{Claude: pfmconfig.Claude{Cache1H: testCase.configTrue}}
			if got := initialCache1H(config, 0); got != testCase.want {
				t.Fatalf(
					"initialCache1H() = %v, want %v (env=%v, config.Cache1H=%v)",
					got,
					testCase.want,
					testCase.env,
					testCase.configTrue,
				)
			}
		})
	}
}

// TestInitialCache1HHonorsPerAccountOverrideNoEnv is the regression test:
// before the fix, initialCache1H read config.Claude.Cache1H directly and
// ignored the account entirely, so accounts[N].claude.cache1h parsed and
// marshalled while changing nothing a launched chat could observe.
func TestInitialCache1HHonorsPerAccountOverrideNoEnv(t *testing.T) {
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	clearCache1HEnv(t)

	config := pfmconfig.Config{
		Claude: pfmconfig.Claude{Cache1H: true},
		Accounts: []pfmconfig.Account{
			{ID: 7, Claude: &pfmconfig.ClaudePrefs{Cache1H: false}},
		},
	}
	if got := initialCache1H(config, 7); got {
		t.Fatal("initialCache1H(config, 7) = true, want false: accounts[7].claude.cache1h override was ignored")
	}
	if !config.Claude.Cache1H {
		t.Fatal("top-level config.Claude.Cache1H must stay true; only the account's effective value changes")
	}
	if got := initialCache1H(config, 0); !got {
		t.Fatal(
			"initialCache1H(config, 0) = false, want true: account 0 (no chat chosen yet) must fall back to the top-level posture",
		)
	}
}
