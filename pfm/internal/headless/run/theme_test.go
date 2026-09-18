package run

import (
	"testing"

	pfmconfig "hostops/pfm/internal/config"
	pfmengine "hostops/pfm/internal/engine"
)

// TestArgumentsCarriesThemedSettingsForAnAccountWithATheme pins arguments()
// to the same EffectiveClaude(Account) binary-pattern read the two
// action.ClaudeSpawn renderers make — a headless request on an account
// carrying a theme must merge it into --settings.
func TestArgumentsCarriesThemedSettingsForAnAccountWithATheme(t *testing.T) {
	config := pfmconfig.Config{
		Accounts: []pfmconfig.Account{{ID: 7, Claude: &pfmconfig.ClaudePrefs{Theme: "custom:professor-silver"}}},
	}
	args, err := arguments(Request{Engine: pfmengine.Claude, Account: 7, Config: config})
	if err != nil {
		t.Fatalf("arguments() error = %v", err)
	}
	want := pfmengine.ClaudeSettingsPayload("custom:professor-silver")
	if !containsPair(args, "--settings", want) {
		t.Fatalf("Claude args %#v lack the themed --settings payload %s", args, want)
	}
}

// TestArgumentsCarriesThePlainConstWithoutATheme is the companion negative:
// a request with no configured theme keeps the byte-identical const.
func TestArgumentsCarriesThePlainConstWithoutATheme(t *testing.T) {
	args, err := arguments(Request{Engine: pfmengine.Claude})
	if err != nil {
		t.Fatalf("arguments() error = %v", err)
	}
	if !containsPair(args, "--settings", pfmengine.OutputStyleDefaultSettings) {
		t.Fatalf("Claude args %#v lack the plain settings const", args)
	}
}

// TestArgumentsWithoutAccountDerivesNoTheme pins the explicit carve-out:
// WithoutAccount never reads the configured roster, so even an Account field
// left at a real id must not leak that account's theme into a diagnostic
// capture request.
func TestArgumentsWithoutAccountDerivesNoTheme(t *testing.T) {
	config := pfmconfig.Config{
		Claude: pfmconfig.ClaudePrefs{Theme: "dark"},
	}
	args, err := arguments(Request{Engine: pfmengine.Claude, WithoutAccount: true, Config: config})
	if err != nil {
		t.Fatalf("arguments() error = %v", err)
	}
	if !containsPair(args, "--settings", pfmengine.OutputStyleDefaultSettings) {
		t.Fatalf("WithoutAccount Claude args %#v should carry the plain const, not the top-level theme", args)
	}
}
