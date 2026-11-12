package installer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/herdr"
)

// herdrClaudeInstallBound caps one `herdr integration install claude` run.
const herdrClaudeInstallBound = 30 * time.Second

// wireHerdrClaudeIntegration makes Herdr's own Claude integration present on a
// Herdr host: one SessionStart hook per physical settings.json the Claude
// config dirs share (herdr.ClaudeStores). A store without the hook gets one
// install under its chosen dir; a store with one is left as it is, unless it
// is herdr-shield's guarded form (RetireHerdrShieldHook); a store with more is
// left alone and warned.
// Once every store holds the hook, pfm's own native Claude reports are
// released (Options.ReleaseNativeClaudeReports). Uninstall takes back only
// pfm's wrapping: a guarded hook unwraps even while Herdr still runs
// herdr-shield, whose guard handed its panes to the pfm now leaving; Herdr's
// own hooks, none or many, stay as they are and nothing is released.
func (installer *engine) wireHerdrClaudeIntegration(ctx context.Context) error {
	runner := installer.processRunner()
	env := installer.env()
	if !herdr.HostHasHerdr(env.Get, runner.LookPath) {
		installer.skip("herdr claude integration: herdr absent")
		return nil
	}
	dirs := pfmconfig.Config{Accounts: installer.options.ClaudeAccounts}.ClaudeConfigDirs()
	if len(dirs) == 0 {
		dirs = []string{installer.options.ConfigDir}
	}
	var failed error
	for _, store := range herdr.ClaudeStores(dirs) {
		failed = errors.Join(failed, installer.wireHerdrClaudeStore(ctx, runner, store))
	}
	if failed != nil || !installer.apply || installer.options.ReleaseNativeClaudeReports == nil ||
		installer.options.Mode == ModeUninstall {
		return failed
	}
	if err := installer.options.ReleaseNativeClaudeReports(ctx, installer.options.Home); err != nil {
		return installer.fail(fmt.Errorf("herdr claude integration: release pfm's native Claude reports: %w", err))
	}
	return nil
}

func (installer *engine) wireHerdrClaudeStore(ctx context.Context, runner deps.Runner, store herdr.ClaudeStore) error {
	if store.Err != nil {
		return installer.fail(fmt.Errorf("herdr claude integration: %w", store.Err))
	}
	uninstall := installer.options.Mode == ModeUninstall
	if uninstall && len(store.Hooks) != 1 {
		return nil
	}
	switch len(store.Hooks) {
	case 0:
		err := installer.change(
			"herdr claude integration in "+store.Settings+": "+
				herdr.ClaudeIntegrationCommand(herdr.HostBinary(installer.env().Get), store.Chosen),
			func() error { return installer.runHerdrClaudeInstall(ctx, runner, store.Chosen) },
		)
		if err != nil {
			return installer.fail(err)
		}
		return nil
	case 1:
		retirement := RetireHerdrShieldHook(store, installer.options.Home, installer.env())
		if uninstall {
			retirement.Skip = ""
		}
		switch {
		case retirement.Err != nil:
			return installer.fail(retirement.Err)
		case retirement.Skip != "":
			installer.skip(retirement.Skip)
		case retirement.Guarded:
			err := installer.change(
				"herdr claude integration in "+store.Settings+": unwrap "+store.Hooks[0]+" to Herdr's plain hook",
				func() error { return unwrapHerdrShieldHook(store.Settings) },
			)
			if err != nil {
				return installer.fail(err)
			}
		default:
			installer.ok("herdr claude integration in " + store.Settings + ": " + store.Hooks[0])
		}
		return nil
	}
	quoted := make([]string, len(store.Hooks))
	for at, hook := range store.Hooks {
		quoted[at] = fmt.Sprintf("%q", hook)
	}
	message := fmt.Sprintf(
		"herdr claude integration: %s holds %d Herdr hooks, each run on every Claude session start: %s — "+
			"delete all but one, then rerun pfm install",
		store.Settings, len(store.Hooks), strings.Join(quoted, ", "),
	)
	installer.say("  warn    %s", message)
	installer.record("warn", message, nil)
	return nil
}

// runHerdrClaudeInstall runs Herdr's Claude integration install under dir.
func (installer *engine) runHerdrClaudeInstall(ctx context.Context, runner deps.Runner, dir string) error {
	name := herdr.HostBinary(installer.env().Get)
	command := herdr.ClaudeIntegrationCommand(name, dir)
	binary, err := runner.LookPath(name)
	if err != nil {
		return fmt.Errorf("herdr claude integration: %s: resolve %s: %w", command, name, err)
	}
	bounded, cancel := context.WithTimeout(ctx, herdrClaudeInstallBound)
	defer cancel()
	argv := herdr.ClaudeIntegrationArgv(binary)
	result, err := runner.Run(bounded, argv, deps.RunOptions{
		Env: deps.EnvironmentWith(herdr.ClaudeConfigDirEnv, dir), WaitDelay: 2 * time.Second, ProcessGroup: true,
	})
	if err == nil && result.ExitCode != 0 {
		err = fmt.Errorf("exit %d", result.ExitCode)
	}
	if err != nil {
		return fmt.Errorf("herdr claude integration: %s (argv %q): %w; stderr: %q",
			command, argv, err, strings.TrimSpace(string(result.Stderr)))
	}
	return nil
}
