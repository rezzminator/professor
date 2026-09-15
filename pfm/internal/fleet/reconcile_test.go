package fleet

import (
	"testing"

	"hostops/pfm/internal/gather"
)

// TestCodexRolloutFingerprintsCaptureIdentityPerPID pins the shape the
// parked-poll gate compares: one fingerprint per live Codex PID, built only
// from the fields RefreshCodexHeldRollouts actually updates from procfs.
func TestCodexRolloutFingerprintsCaptureIdentityPerPID(t *testing.T) {
	codex := []gather.LiveCodex{
		{PID: 111, RolloutPath: "/codex/rollout-a.jsonl", RolloutHeld: true},
		{PID: 222, IdentityError: "read Codex descriptors: boom"},
	}
	fingerprints := CodexRolloutFingerprints(codex)
	if len(fingerprints) != 2 {
		t.Fatalf("fingerprints = %#v, want 2 entries", fingerprints)
	}
	if got := fingerprints[111]; got.rolloutPath != "/codex/rollout-a.jsonl" || !got.rolloutHeld {
		t.Fatalf("fingerprint[111] = %#v", got)
	}
	if got := fingerprints[222]; got.identityError != "read Codex descriptors: boom" {
		t.Fatalf("fingerprint[222] = %#v", got)
	}
}

// TestCodexRolloutFingerprintsEqualDetectsEveryKindOfChange is the direct
// proof behind the parked poll's gate (streamFleetRefreshesWith): the ONLY
// case that must read "unchanged" — and therefore skip the tmux
// capture-pane ReconcileCodexPanes would otherwise run — is bit-for-bit
// identical fingerprints for the identical PID set. A rollout swap, an error
// appearing or clearing, and a PID joining or leaving the live set must all
// read as changed (2026-09-08 measurement: an idle Limits tab was paying for
// capture-pane on every one of these polls regardless of whether any of
// them actually happened).
func TestCodexRolloutFingerprintsEqualDetectsEveryKindOfChange(t *testing.T) {
	base := map[int]CodexRolloutFingerprint{
		111: {rolloutPath: "/codex/rollout-a.jsonl", rolloutHeld: true},
	}
	tests := map[string]map[int]CodexRolloutFingerprint{
		"identical": {
			111: {rolloutPath: "/codex/rollout-a.jsonl", rolloutHeld: true},
		},
		"rollout path moved (a /clear)": {
			111: {rolloutPath: "/codex/rollout-b.jsonl", rolloutHeld: true},
		},
		"rollout no longer held": {
			111: {rolloutPath: "/codex/rollout-a.jsonl", rolloutHeld: false},
		},
		"identity error appeared": {
			111: {
				rolloutPath:   "/codex/rollout-a.jsonl",
				rolloutHeld:   true,
				identityError: "read Codex descriptors: boom",
			},
		},
		"a second PID joined": {
			111: {rolloutPath: "/codex/rollout-a.jsonl", rolloutHeld: true},
			222: {rolloutPath: "/codex/rollout-c.jsonl", rolloutHeld: true},
		},
		"the PID exited": {},
	}
	wantEqual := map[string]bool{
		"identical":                     true,
		"rollout path moved (a /clear)": false,
		"rollout no longer held":        false,
		"identity error appeared":       false,
		"a second PID joined":           false,
		"the PID exited":                false,
	}
	for name, current := range tests {
		if got := CodexRolloutFingerprintsEqual(base, current); got != wantEqual[name] {
			t.Errorf("%s: CodexRolloutFingerprintsEqual() = %v, want %v", name, got, wantEqual[name])
		}
	}
}

// TestCodexRolloutFingerprintsSkippableRequiresProcfsToFullyResolveEveryPID
// is the correctness half of the parked-poll gate: an unchanged fingerprint
// is only safe to trust WITHOUT a capture-pane when procfs's own signal
// already determines the outcome for every live Codex PID — a held rollout
// (the process's own claim wins outright) or an identity error
// (processConflicts overrides the pane text regardless). A rollout-less
// process with no error — DetectCodexThreads' own doc calls this "the
// normal shape of a paginated thread since Codex 0.146.1" — has NO procfs
// opinion at all, so it must never be treated as skippable: the pane's own
// screen (only reachable via capture-pane) is the only identity signal that
// exists for it. TestParkedPickerStillObservesCodexClear and
// TestParkedPickerRetriesRefreshAfterCodexClear are the end-to-end proof
// that this exact shape still gets reconciled while parked.
func TestCodexRolloutFingerprintsSkippableRequiresProcfsToFullyResolveEveryPID(t *testing.T) {
	tests := map[string]struct {
		codex []gather.LiveCodex
		want  bool
	}{
		"no live Codex processes": {
			codex: nil,
			want:  true,
		},
		"every process holds its own rollout": {
			codex: []gather.LiveCodex{
				{PID: 111, RolloutPath: "/codex/rollout-a.jsonl", RolloutHeld: true},
				{PID: 222, RolloutPath: "/codex/rollout-b.jsonl", RolloutHeld: true},
			},
			want: true,
		},
		"every process carries an identity error": {
			codex: []gather.LiveCodex{
				{PID: 111, IdentityError: "read Codex descriptors: boom"},
			},
			want: true,
		},
		"a mix of held rollouts and identity errors": {
			codex: []gather.LiveCodex{
				{PID: 111, RolloutPath: "/codex/rollout-a.jsonl", RolloutHeld: true},
				{PID: 222, IdentityError: "conflicting rollouts"},
			},
			want: true,
		},
		"one rollout-less process with no error": {
			codex: []gather.LiveCodex{
				{PID: 111, RolloutPath: "/codex/rollout-a.jsonl", RolloutHeld: true},
				{PID: 222},
			},
			want: false,
		},
	}
	for name, test := range tests {
		if got := CodexRolloutFingerprintsSkippable(test.codex); got != test.want {
			t.Errorf("%s: CodexRolloutFingerprintsSkippable() = %v, want %v", name, got, test.want)
		}
	}
}

// TestCodexRolloutFingerprintsEqualTreatsNilAndEmptyAsNoPriorState pins the
// first-poll boundary: streamFleetRefreshesWith seeds parkedRollouts as a
// nil map before the stream has ever gone through a full pass, and a nil map
// must compare equal to another empty map (no PIDs, no change) but never to
// a map that actually holds an entry.
func TestCodexRolloutFingerprintsEqualTreatsNilAndEmptyAsNoPriorState(t *testing.T) {
	if !CodexRolloutFingerprintsEqual(nil, map[int]CodexRolloutFingerprint{}) {
		t.Fatalf("nil vs empty map: want equal (both name zero PIDs)")
	}
	nonEmpty := map[int]CodexRolloutFingerprint{111: {rolloutPath: "/codex/rollout-a.jsonl", rolloutHeld: true}}
	if CodexRolloutFingerprintsEqual(nil, nonEmpty) {
		t.Fatalf("nil vs a populated map: want NOT equal")
	}
}
