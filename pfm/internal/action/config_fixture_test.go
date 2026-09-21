package action

import (
	"context"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

func testMachineConfig(home string) pfmconfig.Config {
	machine := pfmconfig.Defaults(home, []string{
		pfmconfig.DefaultAccountProjectDir(home, 1),
		pfmconfig.DefaultAccountProjectDir(home, 2),
		pfmconfig.DefaultAccountProjectDir(home, 3),
	})
	machine.CodexAccounts = []pfmconfig.CodexAccount{
		{ID: 1, Home: home + "/.codex"},
		{ID: 2, Home: home + "/.codex-2"},
		{ID: 3, Home: home + "/.codex-3"},
	}
	return machine
}

func synthesizeWithTestConfig(request Request) (Plan, error) {
	if len(request.Config.Accounts) == 0 {
		request.Config = testMachineConfig(request.Home)
	}
	return Synthesize(request)
}

func headlessWithTestConfig(request HeadlessRequest) (HeadlessPlan, error) {
	if len(request.Config.Accounts) == 0 {
		request.Config = testMachineConfig(request.Home)
	}
	return HeadlessRun(request)
}

func openWithTestConfig(executor *Executor, ctx context.Context, request Request) (string, error) {
	if len(request.Config.Accounts) == 0 {
		request.Config = testMachineConfig(request.Home)
	}
	return executor.Open(ctx, request)
}
