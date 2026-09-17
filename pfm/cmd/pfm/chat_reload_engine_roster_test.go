package main

import (
	"strings"
	"testing"

	pfmconfig "hostops/pfm/internal/config"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/paths"
)

func TestValidateReloadAccountUsesTheSeatEngineRoster(t *testing.T) {
	machine := pfmconfig.Config{
		Version:       pfmconfig.Version,
		CodexAccounts: []pfmconfig.CodexAccount{{ID: 3, Home: "/codex/3"}},
	}
	if _, err := validateReloadAccount(machine, "cx", 3); err != nil {
		t.Fatalf("requested Codex account rejected: %v", err)
	}
	if _, err := validateReloadAccount(machine, "cc", 1); err == nil ||
		!strings.Contains(err.Error(), "no Claude accounts configured") {
		t.Fatalf("empty Claude roster error = %v", err)
	}
	if _, err := validateReloadAccount(machine, "cx", 4); err == nil ||
		!strings.Contains(err.Error(), "requested Codex account 4") {
		t.Fatalf("off-roster Codex error = %v", err)
	}
}

func TestReloadExplicitlyRejectsOpenCode(t *testing.T) {
	machine := pfmconfig.Config{OpenCodeAccounts: []pfmconfig.OpenCodeAccount{{ID: 1, Home: "/opencode"}}}
	if _, err := validateReloadAccount(machine, pfmengine.OpenCode, 1); err == nil ||
		!strings.Contains(err.Error(), "OpenCode") {
		t.Fatalf("validateReloadAccount(OpenCode) error=%v, want product-level refusal", err)
	}
	_, err := findEngineTranscript(paths.Values{Roots: map[pfmengine.ID][]string{
		pfmengine.Claude: {t.TempDir()}, pfmengine.OpenCode: {t.TempDir()},
	}}, machine, pfmengine.OpenCode, "ses-fixture")
	if err == nil || !strings.Contains(err.Error(), "OpenCode") {
		t.Fatalf("findEngineTranscript(OpenCode) error=%v, want product-level refusal", err)
	}
}
