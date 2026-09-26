package installer

import (
	"path/filepath"
	"strings"
	"testing"
)

// setTestImplementedSubcommands pins the registry SetImplementedSubcommands
// fills in from cmd/pfm — installer cannot import cmd/pfm (a main package,
// exactly the constraint the registry exists to cross), so this is a
// deliberately small, representative stand-in naming issue #24 F1's own
// examples (doctor, claude-version) alongside a few real names, while
// leaving "hook-from-a-newer-pfm" and "explore-deny"-shaped fixtures absent
// so the unknown-detection tests below keep exercising the real code path.
// t.Cleanup resets the registry to its unset (fail-closed) zero value so
// tests that never call this keep the never-unknown default.
func setTestImplementedSubcommands(t *testing.T) {
	t.Helper()
	SetImplementedSubcommands(
		[]string{"version", "ls", "doctor", "install", "update"},
		[]string{"claude-version", "launcher-repair", "clear-kill"},
	)
	t.Cleanup(func() { implementedSubcommands = subcommandRegistry{} })
}

// TestInstallRetiresAPFMHookThisBinaryDoesNotImplement pins issue #24
// finding 2's still-open half: a rollback to an older pfm (or a hook a
// newer-then-reverted pfm wrote) leaves an entry of pfm's own shape naming a
// subcommand THIS binary neither implements nor lists as retired. Unlike the
// table-retired shapes (already covered), nothing recognized this one at
// all before unknownPFMHookCommand — so the very next `pfm install` apply
// must strip it while every real template hook converges untouched.
func TestInstallRetiresAPFMHookThisBinaryDoesNotImplement(t *testing.T) {
	setTestImplementedSubcommands(t)
	home := filepath.Join("neutral", "home")
	pfm := home + "/.local/bin/pfm"
	unknown := pfm + " internal hook-from-a-newer-pfm"
	raw := []byte(`{
  "hooks": {
    "UserPromptSubmit": [
      {"matcher":"","hooks":[
        {"type":"command","command":"` + unknown + `"}
      ]}
    ]
  }
}`)

	updated, changed, _, err := updateSettings(raw, home, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("apply did not change a document containing an unknown pfm-shaped hook")
	}
	if strings.Contains(string(updated), "hook-from-a-newer-pfm") {
		t.Fatalf("unknown pfm-shaped hook survived an install apply:\n%s", updated)
	}
	for _, template := range claudeHookTemplates(home) {
		if got := hookCommandCount(t, string(updated), template.Event, template.Command); got != 1 {
			t.Fatalf("apply dropped installer template %q: count=%d\n%s", template.Name, got, updated)
		}
	}
	// The removal above is powered by exactly this naming rule — the
	// "unknown:<name>" identity ProbeExpectedHooks reports the same entry
	// under (TestProbeExpectedHooksReportsAnUnknownPFMHookAsStale) is the
	// name this call returns.
	if name, ok := unknownPFMHookCommand(unknown, pfm); !ok || name != "hook-from-a-newer-pfm" {
		t.Fatalf(
			"unknownPFMHookCommand(%q, %q) = (%q, %v), want (%q, true)",
			unknown,
			pfm,
			name,
			ok,
			"hook-from-a-newer-pfm",
		)
	}
}

// TestInstallLeavesAForeignHookThatMerelyMentionsPFM is the boundary pin for
// unknownPFMHookCommand's false-positive guard: a command whose first token
// is not one of pfm's own binary forms is never matched, even though its
// argument text contains "pfm". This assertion holds on unfixed code too —
// it pins the guard, not the new behavior.
func TestInstallLeavesAForeignHookThatMerelyMentionsPFM(t *testing.T) {
	home := filepath.Join("neutral", "home")
	foreign := "/usr/local/bin/notify --tag pfm"
	raw := []byte(`{
  "hooks": {
    "PostToolUse": [
      {"matcher":"","hooks":[
        {"type":"command","command":"` + foreign + `"}
      ]}
    ]
  }
}`)

	updated, _, _, err := updateSettings(raw, home, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := hookCommandCount(t, string(updated), "PostToolUse", foreign); got != 1 {
		t.Fatalf("foreign hook that merely mentions pfm was removed: count=%d\n%s", got, updated)
	}
}

// TestInstallKeepsAnOperatorHookNamingASubcommandThisBinaryImplements is a
// REGRESSION test for issue #24 F1: unknownPFMHookCommand used to classify
// ANY "pfm <x>" hook not among the installer's own automatic templates or
// the retiredHookCommands table as unknown, so removeRetiredHookCommands
// deleted an operator's own hand-wired hook — `~/.local/bin/pfm doctor` or
// `pfm internal claude-version` — on every `pfm install --yes`, even though
// this binary implements both. Unfixed (setTestImplementedSubcommands still
// called, so the registry IS set): both survive because the predicate now
// checks implementation, not "matches an automatic template".
func TestInstallKeepsAnOperatorHookNamingASubcommandThisBinaryImplements(t *testing.T) {
	setTestImplementedSubcommands(t)
	home := filepath.Join("neutral", "home")
	pfm := home + "/.local/bin/pfm"
	operatorDoctor := pfm + " doctor"
	operatorClaudeVersion := pfm + " internal claude-version"
	raw := []byte(`{
  "hooks": {
    "SessionStart": [
      {"matcher":"","hooks":[
        {"type":"command","command":"` + operatorDoctor + `"},
        {"type":"command","command":"` + operatorClaudeVersion + `"}
      ]}
    ]
  }
}`)

	updated, _, _, err := updateSettings(raw, home, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := hookCommandCount(t, string(updated), "SessionStart", operatorDoctor); got != 1 {
		t.Fatalf("operator hook %q survived count=%d, want 1:\n%s", operatorDoctor, got, updated)
	}
	if got := hookCommandCount(t, string(updated), "SessionStart", operatorClaudeVersion); got != 1 {
		t.Fatalf("operator hook %q survived count=%d, want 1:\n%s", operatorClaudeVersion, got, updated)
	}
	if name, ok := unknownPFMHookCommand(operatorDoctor, pfm); ok {
		t.Fatalf(
			"unknownPFMHookCommand(%q, %q) = (%q, true), want false — this binary implements doctor",
			operatorDoctor,
			pfm,
			name,
		)
	}
	if name, ok := unknownPFMHookCommand(operatorClaudeVersion, pfm); ok {
		t.Fatalf(
			"unknownPFMHookCommand(%q, %q) = (%q, true), want false — this binary implements internal claude-version",
			operatorClaudeVersion,
			pfm,
			name,
		)
	}
}

// TestInstallStillRemovesAnUnimplementedSubcommandWithTheRegistrySet checks
// the registry's other edge, alongside
// TestInstallRetiresAPFMHookThisBinaryDoesNotImplement: a name absent from
// BOTH the topLevel and internal lists set for this test is still removed,
// even though the registry is set (fail-closed only governs the UNSET
// case — see TestUnknownPFMHookCommandNeverReportsUnknownWithAnUnsetRegistry).
func TestInstallStillRemovesAnUnimplementedSubcommandWithTheRegistrySet(t *testing.T) {
	setTestImplementedSubcommands(t)
	home := filepath.Join("neutral", "home")
	pfm := home + "/.local/bin/pfm"
	unknown := pfm + " internal exit-close-from-a-stranger"
	raw := []byte(`{
  "hooks": {
    "SessionEnd": [
      {"matcher":"","hooks":[
        {"type":"command","command":"` + unknown + `"}
      ]}
    ]
  }
}`)

	updated, changed, _, err := updateSettings(raw, home, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("apply did not change a document containing an unimplemented pfm-shaped hook")
	}
	if strings.Contains(string(updated), "exit-close-from-a-stranger") {
		t.Fatalf("unimplemented pfm-shaped hook survived an install apply:\n%s", updated)
	}
}

// TestUnknownPFMHookCommandNeverReportsUnknownWithAnUnsetRegistry is a
// REGRESSION test for issue #24 F1's fail-closed default: a caller that
// never calls SetImplementedSubcommands (any process before main() sets it,
// or a test that never opts in) must never have unknownPFMHookCommand strip
// an operator's own hook on a guess — the registry unset means "assume every
// subcommand named is implemented", never "assume none are".
func TestUnknownPFMHookCommandNeverReportsUnknownWithAnUnsetRegistry(t *testing.T) {
	implementedSubcommands = subcommandRegistry{}
	home := filepath.Join("neutral", "home")
	pfm := home + "/.local/bin/pfm"
	for _, command := range []string{
		pfm + " doctor",
		pfm + " internal claude-version",
		pfm + " internal hook-from-a-newer-pfm",
	} {
		if name, ok := unknownPFMHookCommand(command, pfm); ok {
			t.Fatalf(
				"unknownPFMHookCommand(%q, %q) = (%q, true) with an unset registry, want false (fail closed toward keeping the hook)",
				command,
				pfm,
				name,
			)
		}
	}
}

// A hook document that does not parse is an unchecked file, never a clean
// one: UnknownPFMHookCommands returns the decode error, and a clean document
// returns no names and no error.
func TestUnknownPFMHookCommandsNamesAnUnparsableDocument(t *testing.T) {
	home := filepath.Join("neutral", "home")
	names, err := UnknownPFMHookCommands([]byte("{\"hooks\": "), home)
	if err == nil {
		t.Fatalf("UnknownPFMHookCommands(unparsable) = %v, nil; want the decode error", names)
	}
	names, err = UnknownPFMHookCommands([]byte("{\"hooks\": {}}"), home)
	if err != nil || len(names) != 0 {
		t.Fatalf("UnknownPFMHookCommands(clean) = %v, %v; want no names, no error", names, err)
	}
}
