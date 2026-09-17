package inject

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/rearm"
)

// TestRearmThresholdBytesUnderBothSpillLinesAndDefault pins behaviour 3 (the
// channel budget fix): the self-compact channel's own re-arm budget must sit
// STRICTLY BELOW inlineThreshold for BOTH engines — the point above which
// prepareMessage spills the body into ~/.local/state/pfm/inject-bodies and
// replaces it with a pointer at that SNAPSHOT file (body.go's
// inlineThreshold check) — and STRICTLY BELOW rearm.DefaultThresholdBytes,
// never handed rearm.Pointer's day-one design ceiling unchecked.
func TestRearmThresholdBytesUnderBothSpillLinesAndDefault(t *testing.T) {
	engine := &Engine{options: withDefaults(Options{})}

	for _, test := range []struct {
		name       string
		target     Target
		spillLimit int
	}{
		{"claude", Target{Engine: string(pfmengine.Claude)}, ClaudeInlineMax},
		{"codex", Target{Engine: string(pfmengine.Codex)}, CodexInlineMax},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := engine.rearmThresholdBytes(test.target)
			if got >= test.spillLimit {
				t.Fatalf("rearmThresholdBytes(%s) = %d, want strictly under this channel's own spill line %d",
					test.name, got, test.spillLimit)
			}
			if got >= rearm.DefaultThresholdBytes {
				t.Fatalf("rearmThresholdBytes(%s) = %d, want strictly under rearm.DefaultThresholdBytes (%d)",
					test.name, got, rearm.DefaultThresholdBytes)
			}
		})
	}
}

// TestRolePointerChoosesShortPointerUnderChannelBudget is the regression
// test for the real defect T1's channel budget fixes: a role constitution
// sized comfortably under rearm.DefaultThresholdBytes (so reload's own
// channel would re-arm it FULL TEXT) must still land as the SHORT POINTER
// on the self-compact channel, because that channel's own inlineThreshold
// spill line sits far below 4KB. Handing rearm.Pointer the wrong (larger)
// budget here would have this body attempt to go out full text, get
// silently spilled by prepareMessage into a snapshot file, and point the
// seat at a frozen copy instead of re-reading the live artifact — the exact
// drift T1 exists to prevent.
func TestRolePointerChoosesShortPointerUnderChannelBudget(t *testing.T) {
	sidDir := t.TempDir()
	artifactPath := filepath.Join(t.TempDir(), "dev.md")
	// 1000 bytes: comfortably under rearm.DefaultThresholdBytes (4096), so
	// chat_reload_command.go's own channel would re-arm this FULL TEXT — but
	// comfortably over this channel's derived budget (ClaudeInlineMax 720
	// minus rearmPreamblePadding 200 = 520).
	body := strings.Repeat("x", 1000) + "\n"
	if err := os.WriteFile(artifactPath, []byte(body), 0o600); err != nil {
		t.Fatalf("write fixture artifact: %v", err)
	}
	socket := "cc-1800000000-1-9"
	if err := rearm.WriteCrumb(sidDir, socket, rearm.Crumb{
		Role:         "dev",
		ArtifactPath: artifactPath,
		TOMLKey:      false,
	}); err != nil {
		t.Fatalf("WriteCrumb() error = %v", err)
	}

	engine := &Engine{sidDir: sidDir, options: withDefaults(Options{})}
	target := Target{
		SocketPath: filepath.Join(t.TempDir(), socket),
		Pane:       "",
		Engine:     string(pfmengine.Claude),
	}

	pointer, hasRole, err := engine.rolePointer(target)
	if err != nil {
		t.Fatalf("rolePointer() error = %v", err)
	}
	if !hasRole {
		t.Fatal("rolePointer() hasRole = false, want true for a live crumb")
	}
	if strings.Contains(pointer, strings.Repeat("x", 100)) {
		t.Fatalf("rolePointer() = %q, the full body leaked through the self-compact channel's own budget", pointer)
	}
	if !strings.Contains(pointer, artifactPath) {
		t.Fatalf("rolePointer() = %q, want the short pointer naming the live artifact %q", pointer, artifactPath)
	}

	// Sanity check that this fixture actually proves the asymmetry: the SAME
	// body, on reload's unmodified DefaultThresholdBytes budget, re-arms
	// FULL TEXT — the two channels genuinely disagree on this body, not just
	// on paper.
	reloadWouldBe := rearm.Pointer(
		rearm.Crumb{Role: "dev", ArtifactPath: artifactPath, TOMLKey: false},
		rearm.DefaultThresholdBytes,
	)
	if !strings.Contains(reloadWouldBe, strings.Repeat("x", 100)) {
		t.Fatalf(
			"sanity: reload's own budget did not re-arm this body full text (%q) — the fixture is not sized to prove the asymmetry",
			reloadWouldBe,
		)
	}
}
