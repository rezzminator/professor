package installer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/herdr"
)

// herdrClaudeInstallBound caps one `herdr integration install claude` run.
const herdrClaudeInstallBound = 30 * time.Second

// wireHerdrClaudeIntegration makes Herdr's own Claude integration present on a
// Herdr host: one SessionStart hook per physical settings.json the Claude
// config dirs share (herdr.ClaudeStores), guarded so it does nothing inside a
// herdr-shield pane (herdr.GuardClaudeHook). A store without the hook gets one
// install under its chosen dir, then the guard; a store with one unguarded hook
// is refreshed under the dir that hook names (idempotent) and guarded in place;
// a guarded one is left alone, since Herdr's install over it adds an unguarded
// duplicate; a store with more is left alone and warned.
// Once every store holds the hook, pfm's own native Claude reports are
// released (Options.ReleaseNativeClaudeReports).
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
	if failed != nil || !installer.apply || installer.options.ReleaseNativeClaudeReports == nil {
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
	switch len(store.Hooks) {
	case 0:
		err := installer.change(
			"herdr claude integration in "+store.Settings+": "+
				herdr.ClaudeIntegrationCommand(herdr.HostBinary(installer.env().Get), store.Chosen),
			func() error {
				if err := installer.runHerdrClaudeInstall(ctx, runner, store.Chosen); err != nil {
					return err
				}
				return guardHerdrClaudeStore(store.Settings)
			},
		)
		if err != nil {
			return installer.fail(err)
		}
		return nil
	case 1:
		if herdr.ClaudeHookGuarded(store.Hooks[0]) {
			installer.ok("herdr claude integration in " + store.Settings + ": " + store.Hooks[0])
			return nil
		}
		err := installer.change(
			"herdr claude integration in "+store.Settings+": guard "+store.Hooks[0]+" against herdr-shield panes",
			func() error {
				if err := installer.runHerdrClaudeInstall(ctx, runner, store.Chosen); err != nil {
					return err
				}
				return guardHerdrClaudeStore(store.Settings)
			},
		)
		if err != nil {
			return installer.fail(err)
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
	if slices.ContainsFunc(store.Hooks, func(hook string) bool { return !herdr.ClaudeHookGuarded(hook) }) {
		// An unguarded one stores herdr:claude in every herdr-shield pane it starts in.
		return installer.fail(fmt.Errorf("%s; an unguarded one runs in herdr-shield panes", message))
	}
	installer.say("  warn    %s", message)
	installer.record("warn", message, nil)
	return nil
}

// guardHerdrClaudeStore rewrites the one Herdr hook in the physical settings.json
// to its guarded form, in place through the physical path so a symlinked account
// settings.json stays a link; a store whose hook is already guarded is not written.
func guardHerdrClaudeStore(settings string) error {
	hooks, err := herdr.ClaudeIntegrationHooks(settings)
	if err != nil {
		return fmt.Errorf("herdr claude integration: %w", err)
	}
	if len(hooks) != 1 {
		return fmt.Errorf(
			"herdr claude integration: %s holds %d Herdr hooks after install, want 1", settings, len(hooks))
	}
	if herdr.ClaudeHookGuarded(hooks[0]) {
		return nil
	}
	raw, err := os.ReadFile(settings)
	if err != nil {
		return fmt.Errorf("herdr claude integration: read %s: %w", settings, err)
	}
	info, err := os.Stat(settings)
	if err != nil {
		return fmt.Errorf("herdr claude integration: stat %s: %w", settings, err)
	}
	guarded, err := herdr.GuardClaudeSettings(raw, hooks[0])
	if err != nil {
		return fmt.Errorf("herdr claude integration: %s: %w", settings, err)
	}
	if err := atomicfile.Write(settings, guarded, info.Mode().Perm()); err != nil {
		return fmt.Errorf("herdr claude integration: write %s: %w", settings, err)
	}
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
