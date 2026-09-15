package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "hostops/pfm/internal/config"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/paths"
	pfmstats "hostops/pfm/internal/stats"
)

func TestLimitAccountsKeepCodexIndependentFromClaudeRoster(t *testing.T) {
	home := t.TempDir()
	t.Setenv(paths.EnvHome, home)
	runtime := commandRuntime{
		Paths: paths.Values{
			Home:  home,
			Roots: map[pfmengine.ID][]string{pfmengine.Codex: {filepath.Join(home, ".codex")}},
		},
		Config: pfmconfig.Config{
			Claude: pfmconfig.Claude{Binary: "claude"},
			Accounts: []pfmconfig.Account{
				{ID: 1, ConfigDir: pfmconfig.DefaultAccountDir(home, 1), Emoji: "🥇"},
				{ID: 2, ConfigDir: pfmconfig.DefaultAccountDir(home, 2), Emoji: "🥈"},
			},
			AccountSkips: []pfmconfig.AccountSkip{{
				ID: 4, ConfigDir: pfmconfig.DefaultAccountDir(home, 4), Reason: "no valid credentials",
			}},
			CodexAccounts: []pfmconfig.CodexAccount{
				{ID: 1, Home: filepath.Join(home, ".codex"), Emoji: "⬢"},
				{ID: 2, Home: filepath.Join(home, ".codex-2"), Emoji: "🔷"},
			},
		},
	}
	accounts := limitAccounts(runtime)
	if len(accounts) != 6 {
		t.Fatalf(
			"limitAccounts()=%#v, want two Claude rows, one skip, two Codex rows, and named OpenCode absence",
			accounts,
		)
	}
	if accounts[0].ID != 1 || accounts[1].ID != 2 || accounts[2].SkipReason != "no valid credentials" {
		t.Fatalf("Claude accounts/skips=%#v", accounts[:3])
	}
	for index, want := range []struct {
		id, offset         int
		label, emoji, auth string
	}{
		{id: 1, offset: 3, label: "Codex 1", emoji: "⬢", auth: filepath.Join(home, ".codex", "auth.json")},
		{id: 2, offset: 4, label: "Codex 2", emoji: "🔷", auth: filepath.Join(home, ".codex-2", "auth.json")},
	} {
		codex := accounts[want.offset]
		if codex.ID != want.id || codex.Engine != pfmengine.Codex || codex.Label != want.label ||
			codex.Emoji != want.emoji ||
			codex.CodexAuthPath != want.auth {
			t.Fatalf(
				"Codex account %d=%#v, want id=%d label=%q emoji=%q auth=%q",
				index,
				codex,
				want.id,
				want.label,
				want.emoji,
				want.auth,
			)
		}
	}
	if account := accounts[5]; account.Engine != pfmengine.Opencode || !account.Absent ||
		account.Label != "no OpenCode accounts configured" {
		t.Fatalf("OpenCode absence=%#v, want named absent row", account)
	}
}

func TestLimitAccountsNameBothEmptyRosters(t *testing.T) {
	accounts := limitAccounts(commandRuntime{Config: pfmconfig.Config{}})
	if len(accounts) != 3 || !accounts[0].Absent || !accounts[1].Absent || !accounts[2].Absent ||
		accounts[0].Label != "no Claude accounts configured" ||
		accounts[1].Label != "no Codex accounts configured" ||
		accounts[2].Label != "no OpenCode accounts configured" {
		t.Fatalf("limitAccounts()=%#v, want one named absence row per engine", accounts)
	}
}

func TestLimitAccountsIncludesConfiguredOpencodeSeat(t *testing.T) {
	accounts := limitAccounts(commandRuntime{Config: pfmconfig.Config{
		OpencodeAccounts: []pfmconfig.OpenCodeAccount{{ID: 7, Home: "/jail/opencode"}},
	}})
	var found bool
	for _, account := range accounts {
		if account.Engine == pfmengine.Opencode {
			found = account.ID == 7 && account.Label == "OpenCode 7" && !account.Absent
		}
	}
	if !found {
		t.Fatalf("limitAccounts()=%#v, want configured OpenCode seat", accounts)
	}
}

func TestConfiguredOpencodeLimitIsUnsupportedNotAbsent(t *testing.T) {
	accounts := limitAccounts(commandRuntime{Config: pfmconfig.Config{
		OpencodeAccounts: []pfmconfig.OpenCodeAccount{{ID: 7, Home: "/jail/opencode"}},
	}})
	var configured pfmstats.LimitAccount
	for _, account := range accounts {
		if account.Engine == pfmengine.Opencode {
			configured = account
			break
		}
	}
	if configured.Engine != pfmengine.Opencode || configured.Absent {
		t.Fatalf("configured OpenCode limit row=%#v, want a configured (not absent) row", configured)
	}
	limits, warnings := pfmstats.NewLimitsSampler([]pfmstats.LimitAccount{configured}).Sample(context.Background())
	if len(limits) != 1 || limits[0].Absent || !strings.Contains(limits[0].Status, "no usage source registered") {
		t.Fatalf("configured OpenCode limits=%#v warnings=%v, want visible unsupported status", limits, warnings)
	}
}
