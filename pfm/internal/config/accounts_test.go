package config

import (
	"reflect"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// TestAccountProjectionsFollowTheRoster pins every per-engine projection of the
// roster, and the zero answers with an engine that has no accounts.
func TestAccountProjectionsFollowTheRoster(t *testing.T) {
	machine := Config{
		Accounts:         []Account{{ID: 1, Emoji: "🥇"}, {ID: 2, Emoji: "·"}},
		CodexAccounts:    []CodexAccount{{ID: 3, Home: "/c/codex-3", Emoji: "🟢"}, {ID: 4, Home: "/c/codex-4"}},
		OpenCodeAccounts: []OpenCodeAccount{{ID: 5}},
	}
	if got := machine.CodexHomes(); !reflect.DeepEqual(got, []string{"/c/codex-3", "/c/codex-4"}) {
		t.Errorf("CodexHomes() = %v", got)
	}
	if got := machine.PrimaryCodexAccount(); got != 3 {
		t.Errorf("PrimaryCodexAccount() = %d", got)
	}
	if got := machine.OpenCodeAccountIDs(); !reflect.DeepEqual(got, []int{5}) {
		t.Errorf("OpenCodeAccountIDs() = %v", got)
	}
	if got := machine.PrimaryOpenCodeAccount(); got != 5 {
		t.Errorf("PrimaryOpenCodeAccount() = %d", got)
	}
	emojis := machine.AccountEmojis()
	if len(emojis) != 2 || emojis[1] != machine.EmojiFor(1) || emojis[2] != machine.EmojiFor(2) {
		t.Errorf("AccountEmojis() = %v", emojis)
	}
	if got := machine.CodexAccountEmojis(); len(got) != 2 || got[3] != machine.CodexEmojiFor(3) {
		t.Errorf("CodexAccountEmojis() = %v", got)
	}
	for _, emoji := range machine.LabelEmojis() {
		if emoji == "" || emoji == "·" {
			t.Errorf("LabelEmojis() carries a non-label %q", emoji)
		}
	}
	var empty Config
	if empty.PrimaryCodexAccount() != 0 || empty.PrimaryOpenCodeAccount() != 0 || len(empty.CodexHomes()) != 0 {
		t.Error("an engine with no accounts must project to zero values")
	}
}

// A row opens on ITS engine's primary account. The Claude primary is only
// Claude's: `pfm chat open` handed it to a Codex resume, and a host with
// Claude accounts 1 and 2 but one Codex account refused every Codex resume
// as "Codex account 2 is not in the configured roster".
func TestPrimaryAccountForPicksTheRowsOwnEngine(t *testing.T) {
	machine := Config{
		Accounts:         []Account{{ID: 1}, {ID: 2}},
		CodexAccounts:    []CodexAccount{{ID: 1, Home: "/c/codex"}},
		OpenCodeAccounts: []OpenCodeAccount{{ID: 7}},
	}
	for engine, want := range map[pfmengine.ID]int{
		pfmengine.Claude:   2,
		pfmengine.Codex:    1,
		pfmengine.OpenCode: 7,
	} {
		if got := machine.PrimaryAccountFor(engine, 2); got != want {
			t.Fatalf("PrimaryAccountFor(%s, claude primary 2) = %d, want %d", engine, got, want)
		}
	}
}
