package action

import (
	"slices"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/workbench"
)

// Each adapter must deliver the interactive policy, including the misleadingly
// named HeadlessFork (a detached interactive seat, not a print-mode query).
func TestRemoteControlInteractiveActionDoors(t *testing.T) {
	home := t.TempDir()
	machine := testMachineConfig(home)
	for _, door := range []string{"native birth", "detached birth", "dead row", "agent fallback", "fork", "launcher", "query"} {
		t.Run(door, func(t *testing.T) {
			var command string
			var err error
			switch door {
			case "native birth":
				run, runErr := (ClaudeSpawn{Purpose: PurposeInteractive, Account: 1, Home: home, Machine: machine, Name: "desk"}).SeatRun(
					home,
				)
				if runErr != nil {
					t.Fatal(runErr)
				}
				at := slices.Index(run.Args, "--remote-control")
				if at < 0 || at+1 == len(run.Args) || run.Args[at+1] != "desk" {
					t.Fatalf("native birth omitted Remote Control: %q", run.Args)
				}
				return
			case "detached birth":
				plan, planErr := HeadlessRun(
					HeadlessRequest{
						Engine:         pfmengine.Claude,
						Name:           "desk",
						CWD:            home,
						Home:           home,
						PrimaryAccount: 1,
						Config:         machine,
					},
				)
				command, err = plan.Run, planErr
			case "dead row", "agent fallback":
				kind := compose.ResumeClaude
				if door == "agent fallback" {
					kind = compose.Agent
				}
				plan, planErr := Synthesize(
					Request{
						Row: compose.Row{
							Kind: kind,
							ID:   "old-session",
							CWD:  home,
							Name: "desk",
						},
						Home:           home,
						PrimaryAccount: 1,
						FreshSocket:    "cc-fixture",
						Config:         machine,
					},
				)
				command, err = plan.Run, planErr
			case "fork":
				plan, planErr := HeadlessFork(
					HeadlessForkRequest{
						Engine:         pfmengine.Claude,
						SessionID:      "parent-session",
						Name:           "desk",
						CWD:            home,
						Home:           home,
						PrimaryAccount: 1,
						Config:         machine,
					},
				)
				command, err = plan.Run, planErr
			case "launcher":
				command, _, err = LauncherLaunchAs(
					workbench.Persona{},
					"/fixture/claude",
					[]string{"initial prompt"},
					machine.Accounts[0].ConfigDir,
					home,
					machine,
					machine.EffectiveClaude(1),
					"desk",
				)
			case "query":
				command, err = (ClaudeSpawn{Purpose: PurposeQuery, Account: 1, Home: home, Machine: machine, Args: []string{"-p", "query"}}).ShellCommand()
			}
			if err != nil {
				t.Fatal(err)
			}
			if door != "query" && !strings.Contains(command, "'--remote-control' 'desk'") {
				t.Fatalf("interactive door lost known Remote Control name: %s", command)
			}
			if (door == "dead row" || door == "agent fallback") && strings.Contains(command, "'--name'") {
				t.Fatalf("resume retitles the session: %s", command)
			}
			if got, want := strings.Contains(command, "'--remote-control'"), door != "query"; got != want {
				t.Fatalf("%s Remote Control=%t, want %t: %s", door, got, want, command)
			}
		})
	}
}
