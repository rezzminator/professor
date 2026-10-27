package action

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

func TestHeadlessExtensionEnvironments(t *testing.T) {
	machine := testMachineConfig(t.TempDir())
	machine.Extensions = map[string]pfmconfig.Extension{
		"ponytail": {
			Enabled: true,
			Engines: []string{"codex", "opencode"},
			Env:     map[string]string{"PONYTAIL_SUBAGENT_MATCHER": "^worker$"},
		},
	}
	for _, id := range []pfmengine.ID{pfmengine.Codex} {
		request := HeadlessRequest{
			Config:         machine,
			Home:           t.TempDir(),
			CWD:            t.TempDir(),
			PrimaryAccount: 1,
			Engine:         id,
			Name:           "fixture",
		}
		plan, err := HeadlessRun(request)
		if err != nil {
			t.Fatal(err)
		}
		assertExtensionPlanEnv(t, plan)
	}
	fork, err := HeadlessFork(
		HeadlessForkRequest{
			Config:         machine,
			Home:           t.TempDir(),
			CWD:            t.TempDir(),
			PrimaryAccount: 1,
			Engine:         pfmengine.Codex,
			Name:           "fixture",
			SessionID:      "thread",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	assertExtensionPlanEnv(t, fork)
}

func assertExtensionPlanEnv(t *testing.T, plan HeadlessPlan) {
	t.Helper()
	if plan.SeatRun == nil ||
		!strings.Contains(strings.Join(plan.SeatRun.Env, "\n"), "PONYTAIL_SUBAGENT_MATCHER=^worker$") {
		t.Fatalf("typed %s recipe lost extension env", plan.Engine)
	}
	if !strings.Contains(plan.Run, "PONYTAIL_SUBAGENT_MATCHER='^worker$'") {
		t.Fatalf("shell %s recipe lost extension env: %s", plan.Engine, plan.Run)
	}
}

func TestExtensionShellEnvironmentOwnership(t *testing.T) {
	t.Setenv("PONYTAIL_USER_EXPORT", "user")
	t.Setenv("PONYTAIL_SUBAGENT_MATCHER", "inherited")
	for _, mode := range []string{"on", "off", "dropped"} {
		for _, engine := range []string{"codex", "opencode"} {
			t.Run(mode+"/"+engine, func(t *testing.T) {
				ext := pfmconfig.Extension{
					Enabled: mode != "off",
					Engines: []string{engine},
					Env:     map[string]string{"PONYTAIL_SUBAGENT_MATCHER": "configured"},
				}
				if mode == "dropped" {
					ext.Engines = nil
				}
				machine := pfmconfig.Config{Extensions: map[string]pfmconfig.Extension{"ponytail": ext}}
				got, err := exec.Command("sh", "-c", "env"+ExtensionShellEnv(machine, engine)+" /usr/bin/printenv").
					CombinedOutput()
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(got), "PONYTAIL_USER_EXPORT=user") {
					t.Errorf("shell overlay lost unowned export: %s", got)
				}
				want := "inherited"
				if mode == "on" {
					want = "configured"
				}
				if !strings.Contains(string(got), "PONYTAIL_SUBAGENT_MATCHER="+want) {
					t.Errorf("shell overlay precedence wrong: %s", got)
				}
			})
		}
	}
}

func TestExtensionTypedLaunchOwnership(t *testing.T) {
	t.Setenv("PONYTAIL_USER_EXPORT", "user")
	t.Setenv("PONYTAIL_SUBAGENT_MATCHER", "inherited")
	machine := testMachineConfig(t.TempDir())
	machine.OpenCodeAccounts = []pfmconfig.OpenCodeAccount{{ID: 1, Home: t.TempDir()}}
	for _, mode := range []string{"on", "off", "dropped"} {
		t.Run(mode, func(t *testing.T) {
			ext := pfmconfig.Extension{
				Enabled: mode != "off",
				Engines: []string{"codex", "opencode"},
				Env:     map[string]string{"PONYTAIL_SUBAGENT_MATCHER": "configured"},
			}
			if mode == "dropped" {
				ext.Engines = nil
			}
			machine.Extensions = map[string]pfmconfig.Extension{"ponytail": ext}
			check := func(env []string) {
				t.Helper()
				got := strings.Join(env, "\n")
				if !strings.Contains(got, "PONYTAIL_USER_EXPORT=user") {
					t.Errorf("typed recipe lost unowned export: %s", got)
				}
				want := "inherited"
				if mode == "on" {
					want = "configured"
				}
				if !strings.Contains(got, "PONYTAIL_SUBAGENT_MATCHER="+want) {
					t.Errorf("typed recipe precedence wrong: %s", got)
				}
			}
			for _, kind := range []compose.Kind{compose.NewCodex, compose.ResumeCodex, compose.NewOpenCode, compose.ResumeOpenCode} {
				plan, err := Synthesize(
					Request{
						Config:         machine,
						Home:           t.TempDir(),
						PrimaryAccount: 1,
						FreshSocket:    "fixture",
						Row:            compose.Row{Kind: kind, ID: "session", CWD: t.TempDir()},
					},
				)
				if err != nil {
					t.Fatal(err)
				}
				check(plan.SeatRun.Env)
			}
			request := HeadlessRequest{
				Config:         machine,
				Home:           t.TempDir(),
				CWD:            t.TempDir(),
				PrimaryAccount: 1,
				Engine:         pfmengine.Codex,
				Name:           "fixture",
			}
			plan, err := HeadlessRun(request)
			if err != nil {
				t.Fatal(err)
			}
			check(plan.SeatRun.Env)
			plan, err = HeadlessFork(
				HeadlessForkRequest{
					Config:         machine,
					Home:           request.Home,
					CWD:            request.CWD,
					PrimaryAccount: 1,
					Engine:         pfmengine.Codex,
					Name:           "fixture",
					SessionID:      "thread",
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			check(plan.SeatRun.Env)
			request.Engine = pfmengine.OpenCode
			plan, err = PlanOpenCode(request)
			if err != nil {
				t.Fatal(err)
			}
			check(plan.SeatRun.Env)
		})
	}
}
