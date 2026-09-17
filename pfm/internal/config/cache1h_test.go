package config

import (
	"os"
	"testing"
)

func clearCache1HEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"CC_ARM_1H", "ENABLE_PROMPT_CACHING_1H"} {
		original, had := os.LookupEnv(key)
		if had {
			if err := os.Unsetenv(key); err != nil {
				t.Fatalf("unset %s: %v", key, err)
			}
		}
		t.Cleanup(func() {
			if had {
				if err := os.Setenv(key, original); err != nil {
					t.Errorf("restore %s: %v", key, err)
				}
			} else if err := os.Unsetenv(key); err != nil {
				t.Errorf("keep %s unset: %v", key, err)
			}
		})
	}
	t.Setenv("CLAUDECODE", "")
}

func TestInitialCache1HEnvOverridesConfig(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		env        map[string]string
		configTrue bool
		want       bool
	}{
		{name: "CC_ARM_1H=1 overrides config false", env: map[string]string{"CC_ARM_1H": "1"}, want: true},
		{name: "CC_ARM_1H=0 overrides config true", env: map[string]string{"CC_ARM_1H": "0"}, configTrue: true},
		{name: "ENABLE_PROMPT_CACHING_1H=1 without CLAUDECODE arms", env: map[string]string{"ENABLE_PROMPT_CACHING_1H": "1"}, want: true},
		{name: "ENABLE_PROMPT_CACHING_1H=1 with CLAUDECODE set does not arm", env: map[string]string{"ENABLE_PROMPT_CACHING_1H": "1", "CLAUDECODE": "1"}, configTrue: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			clearCache1HEnv(t)
			for key, value := range testCase.env {
				t.Setenv(key, value)
			}
			config := Config{Claude: Claude{Cache1H: testCase.configTrue}}
			if got := config.InitialCache1H(0); got != testCase.want {
				t.Fatalf("InitialCache1H() = %v, want %v", got, testCase.want)
			}
		})
	}
}

func TestInitialCache1HHonorsPerAccountOverrideNoEnv(t *testing.T) {
	clearCache1HEnv(t)
	config := Config{
		Claude:   Claude{Cache1H: true},
		Accounts: []Account{{ID: 7, Claude: &ClaudePrefs{Cache1H: false}}},
	}
	if config.InitialCache1H(7) {
		t.Fatal("InitialCache1H(config, 7) = true, want false")
	}
	if !config.InitialCache1H(0) {
		t.Fatal("InitialCache1H(config, 0) = false, want true")
	}
}
