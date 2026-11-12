package picker

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleet"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/paths"
	pfmstats "github.com/rezzminator/professor/pfm/internal/stats"
	"github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/testjail"
	"github.com/rezzminator/professor/pfm/internal/ui"
)

func TestLimitAccountsKeepCodexIndependentFromClaudeRoster(t *testing.T) {
	home := t.TempDir()
	t.Setenv(paths.EnvHome, home)
	runtime := pfmconfig.Runtime{
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
	if account := accounts[5]; account.Engine != pfmengine.OpenCode || !account.Absent ||
		account.Label != "no OpenCode accounts configured" {
		t.Fatalf("OpenCode absence=%#v, want named absent row", account)
	}
}

func TestLimitAccountsNameBothEmptyRosters(t *testing.T) {
	accounts := limitAccounts(pfmconfig.Runtime{Config: pfmconfig.Config{}})
	if len(accounts) != 3 || !accounts[0].Absent || !accounts[1].Absent || !accounts[2].Absent ||
		accounts[0].Label != "no Claude accounts configured" ||
		accounts[1].Label != "no Codex accounts configured" ||
		accounts[2].Label != "no OpenCode accounts configured" {
		t.Fatalf("limitAccounts()=%#v, want one named absence row per engine", accounts)
	}
}

func TestLimitAccountsIncludesConfiguredOpenCodeSeat(t *testing.T) {
	accounts := limitAccounts(pfmconfig.Runtime{Config: pfmconfig.Config{
		OpenCodeAccounts: []pfmconfig.OpenCodeAccount{{ID: 7, Home: "/jail/opencode"}},
	}})
	var found bool
	for _, account := range accounts {
		if account.Engine == pfmengine.OpenCode {
			found = account.ID == 7 && account.Label == "OpenCode 7" && !account.Absent
		}
	}
	if !found {
		t.Fatalf("limitAccounts()=%#v, want configured OpenCode seat", accounts)
	}
}

func TestConfiguredOpenCodeLimitIsUnsupportedNotAbsent(t *testing.T) {
	accounts := limitAccounts(pfmconfig.Runtime{Config: pfmconfig.Config{
		OpenCodeAccounts: []pfmconfig.OpenCodeAccount{{ID: 7, Home: "/jail/opencode"}},
	}})
	var configured pfmstats.LimitAccount
	for _, account := range accounts {
		if account.Engine == pfmengine.OpenCode {
			configured = account
			break
		}
	}
	if configured.Engine != pfmengine.OpenCode || configured.Absent {
		t.Fatalf("configured OpenCode limit row=%#v, want a configured (not absent) row", configured)
	}
	limits, warnings := pfmstats.NewLimitsSampler([]pfmstats.LimitAccount{configured}).Sample(context.Background())
	if len(limits) != 1 || limits[0].Absent || !strings.Contains(limits[0].Status, "no usage source registered") {
		t.Fatalf("configured OpenCode limits=%#v warnings=%v, want visible unsupported status", limits, warnings)
	}
}

// A launch is remembered only when a new chat was really chosen: a resume, a
// reboot and a cancelled picker leave the memory as it was.
func TestRememberLaunchRecordsOnlyANewChatLaunch(t *testing.T) {
	for _, test := range []struct {
		name    string
		outcome ui.Outcome
		want    fleetdb.LaunchMemory
	}{
		{
			"a new Claude chat keeps engine, account and cache",
			ui.Outcome{Kind: ui.OutcomeSelected, Row: compose.Row{Kind: compose.NewClaude}, PrimaryAccount: 2, Cache1H: false},
			fleetdb.LaunchMemory{
				Engine: string(pfmengine.Claude), Accounts: map[string]int{string(pfmengine.Claude): 2},
				Cache1H: map[int]bool{2: false},
			},
		},
		{
			"a new Codex chat keeps engine and account, no cache",
			ui.Outcome{Kind: ui.OutcomeSelected, Row: compose.Row{Kind: compose.NewCodex}, PrimaryAccount: 3, Cache1H: true},
			fleetdb.LaunchMemory{Engine: string(pfmengine.Codex), Accounts: map[string]int{string(pfmengine.Codex): 3}},
		},
		{
			"resuming a chat records nothing",
			ui.Outcome{Kind: ui.OutcomeSelected, Row: compose.Row{Kind: compose.ResumeCodex}, PrimaryAccount: 3},
			fleetdb.LaunchMemory{},
		},
		{
			"a cancelled picker records nothing",
			ui.Outcome{Kind: ui.OutcomeCancelled, Row: compose.Row{Kind: compose.NewClaude}, PrimaryAccount: 2},
			fleetdb.LaunchMemory{},
		},
		{
			"a reboot records nothing",
			ui.Outcome{Kind: ui.OutcomeReboot, Row: compose.Row{Kind: compose.LiveClaude}, PrimaryAccount: 2},
			fleetdb.LaunchMemory{},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			ctx := context.Background()
			state := fleetdb.OpenSharedState(ctx, paths.Values{Home: home, StateDB: filepath.Join(home, "pfm.db")})
			defer func() {
				if err := state.Close(); err != nil {
					t.Errorf("close state: %v", err)
				}
			}()
			if err := rememberLaunch(ctx, state, test.outcome); err != nil {
				t.Fatalf("rememberLaunch: %v", err)
			}
			got, err := state.LaunchMemory(ctx)
			if err != nil || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("memory after %s = %+v, %v; want %+v", test.name, got, err, test.want)
			}
		})
	}
}

// OpenRow execs tmux or zsh on a terminal and never returns, so a launch is
// remembered before its open runs; an open that fails puts the memory back.
func TestOpenAndRememberRecordsBeforeTheOpenThatMayNotReturn(t *testing.T) {
	claude, codex := string(pfmengine.Claude), string(pfmengine.Codex)
	on := true
	previous := fleetdb.LaunchMemory{
		Engine: claude, Accounts: map[string]int{claude: 2}, Cache1H: map[int]bool{2: true},
	}
	launched := fleetdb.LaunchMemory{
		Engine: codex, Accounts: map[string]int{claude: 2, codex: 3}, Cache1H: map[int]bool{2: true},
	}
	outcome := ui.Outcome{Kind: ui.OutcomeSelected, Row: compose.Row{Kind: compose.NewCodex}, PrimaryAccount: 3}
	for _, test := range []struct {
		name  string
		code  int
		after fleetdb.LaunchMemory
	}{
		{"an open that execs finds the launch already remembered", 0, launched},
		{"a failed open leaves the memory as it was", 1, previous},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			ctx := context.Background()
			state := fleetdb.OpenSharedState(ctx, paths.Values{Home: home, StateDB: filepath.Join(home, "pfm.db")})
			defer func() {
				if err := state.Close(); err != nil {
					t.Errorf("close state: %v", err)
				}
			}()
			if err := state.RememberLaunch(ctx, claude, 2, &on, 1); err != nil {
				t.Fatalf("seed memory: %v", err)
			}
			var during fleetdb.LaunchMemory
			var duringErr error
			code := openAndRemember(ctx, state, outcome, io.Discard, func() int {
				during, duringErr = state.LaunchMemory(ctx)
				return test.code
			})
			if code != test.code || duringErr != nil || !reflect.DeepEqual(during, launched) {
				t.Fatalf("code %d; memory while opening = %+v, %v; want %+v", code, during, duringErr, launched)
			}
			if after, err := state.LaunchMemory(ctx); err != nil || !reflect.DeepEqual(after, test.after) {
				t.Fatalf("memory after the open = %+v, %v; want %+v", after, err, test.after)
			}
		})
	}
}

func TestPickerNativeDiscoveryFailures(t *testing.T) {
	for _, stateName := range []string{"live", "lost"} {
		for _, mode := range []string{"--plain", "--tsv", "refresh", "refreshing"} {
			t.Run(stateName+"/"+mode, func(t *testing.T) {
				testjail.Fleet(t)
				ctx := context.Background()
				values, err := pfmconfig.ResolvePaths()
				if err != nil {
					t.Fatal(err)
				}
				runtime := pfmconfig.Runtime{
					Config: pfmconfig.Defaults(values.Home, values.Roots[pfmengine.Claude]),
					Paths:  values,
				}
				state := fleetdb.OpenSharedState(ctx, values)
				t.Cleanup(func() {
					if err := state.Close(); err != nil {
						t.Error(err)
					}
				})
				// A socket file nothing listens on: Herdr cannot be read, never proved gone.
				dead := filepath.Join(values.Home, "dead.sock")
				listener, err := net.Listen("unix", dead)
				if err != nil {
					t.Fatal(err)
				}
				listener.(*net.UnixListener).SetUnlinkOnClose(false)
				if err := listener.Close(); err != nil {
					t.Fatal(err)
				}
				if err := state.PutSeat(ctx, fleetdb.Seat{
					SeatID:      "native-failure",
					SessionID:   "native-sid",
					Engine:      "cc",
					Backend:     "herdr",
					State:       stateName,
					Name:        "NATIVE",
					HerdrPane:   "w1:p1",
					HerdrSocket: dead,
				}); err != nil {
					t.Fatal(err)
				}
				if strings.HasPrefix(mode, "--") {
					database, err := store.Open()
					if err != nil {
						t.Fatal(err)
					}
					transcript := filepath.Join(values.Roots[pfmengine.Claude][0], "healthy.jsonl")
					body := []byte(
						"{\"type\":\"custom-title\",\"customTitle\":\"HEALTHY\"}\n{\"type\":\"user\",\"message\":{\"content\":\"keep healthy\"}}\n",
					)
					if err := os.WriteFile(transcript, body, 0o600); err != nil {
						t.Fatal(err)
					}
					if err := database.UpsertTranscript(
						ctx,
						store.Transcript{
							UUID:        "healthy",
							Path:        transcript,
							Size:        int64(len(body)),
							PromptCount: 1,
							CustomTitle: "HEALTHY",
						},
					); err != nil {
						t.Fatal(err)
					}
					if err := database.Close(); err != nil {
						t.Fatal(err)
					}
					var stdout, stderr bytes.Buffer
					rc := Run([]string{mode}, &stdout, &stderr, runtime)
					want := 0
					if stateName == "live" {
						want = 1
					}
					if rc != want || (want == 1 && !strings.Contains(stderr.String(), "dead.sock")) {
						t.Errorf("native discovery rc=%d want=%d stderr=%q", rc, want, stderr.String())
					}
					if !strings.Contains(stdout.String(), "HEALTHY") {
						t.Errorf("healthy partial rows lost: %s", stdout.String())
					}
					return
				}
				updates := make(chan ui.Snapshot, 1)
				last, ok := sendRefresh(
					ctx,
					nil,
					fleet.Env{Paths: values, Config: runtime.Config},
					scanRequest{},
					fleet.Data{},
					gather.Snapshot{},
					mode == "refreshing",
					updates,
				)
				if !ok {
					t.Fatal("refresh not published")
				}
				snapshot := <-updates
				if stateName == "live" {
					if !last.failed || snapshot.RefreshFailure == nil ||
						snapshot.RefreshFailure.Stage != "native seats" ||
						!strings.Contains(snapshot.RefreshFailure.Err, "dead.sock") {
						t.Fatalf(
							"native discovery accepted as current: last.failed=%t snapshot=%+v",
							last.failed,
							snapshot,
						)
					}
				} else if last.failed || snapshot.RefreshFailure != nil || len(snapshot.Rows) == 0 {
					t.Fatalf("lost diagnostic seat must remain a normal row: %+v", snapshot)
				}
			})
		}
	}
}

// A resume walk's choice reaches the open: the row's account a Claude resume
// launches under, the primary account a Codex resume launches under, and the
// cache; without one, or on a chat turned live since, the open is as before.
func TestResumeUnderAppliesTheWalksChoice(t *testing.T) {
	choice := &ui.ResumeChoice{Account: 2, Cache1H: false}
	for _, test := range []struct {
		name        string
		row         compose.Row
		resume      *ui.ResumeChoice
		wantAccount int
		wantPrimary int
		wantCache   bool
	}{
		{
			"a Claude resume launches on the chosen account and cache",
			compose.Row{Kind: compose.ResumeClaude, ID: "s", Account: 1},
			choice, 2, 2, false,
		},
		{
			"a Codex resume launches on the chosen account",
			compose.Row{Kind: compose.ResumeCodex, ID: "s"},
			choice, 2, 2, false,
		},
		{
			"no walk keeps the recorded account, the primary and the picker's cache",
			compose.Row{Kind: compose.ResumeClaude, ID: "s", Account: 1},
			nil, 1, 3, true,
		},
		{
			"a chat turned live since opens as it is",
			compose.Row{Kind: compose.LiveClaude, ID: "s", Account: 1},
			choice, 1, 3, true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			outcome := ui.Outcome{Kind: ui.OutcomeSelected, Row: test.row, PrimaryAccount: 3, Resume: test.resume}
			row, primary, cache := resumeUnder(test.row, outcome, true)
			if row.Account != test.wantAccount || row.ID != test.row.ID || row.Kind != test.row.Kind ||
				primary != test.wantPrimary || cache != test.wantCache {
				t.Fatalf(
					"resumeUnder = row %s account %d, primary %d, cache %t; want row %s account %d, primary %d, cache %t",
					row.ID,
					row.Account,
					primary,
					cache,
					test.row.ID,
					test.wantAccount,
					test.wantPrimary,
					test.wantCache,
				)
			}
		})
	}
}
