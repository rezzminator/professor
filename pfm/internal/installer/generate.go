package installer

import (
	"context"
	"errors"
	"fmt"
)

// Generate rebuilds every install output compiled from the source clone, by
// the same steps `pfm install` runs and nothing else: the harness prompts, the
// global command links, the engine-sync policy, the global agents and commands
// engine-sync compiles, and each Codex home's config.toml defaults with its
// developer_instructions fence. It records the clone it compiled as install
// does, so every reader anchors on that clone. No binary rebuild, plugin,
// service, shell or staged asset is touched, so an edited prompt part, agent
// or command reaches new chats with one command. ModeDryRun previews;
// ModeApply writes.
func Generate(ctx context.Context, options Options) (report Report, err error) {
	endRun := runSpan(ctx, options.Mode)
	defer func() { endRun(err) }()
	if options.Mode != ModeApply && options.Mode != ModeDryRun {
		return Report{}, fmt.Errorf("generate runs in apply or plan mode only, not installer mode %d", options.Mode)
	}
	options, err = normalizeInstallerOptions(options)
	if err != nil {
		return Report{}, fmt.Errorf("resolve generate options: %w", err)
	}
	installer := &engine{
		options:     options,
		apply:       options.Mode == ModeApply,
		stamp:       options.Now().Format("20060102-150405"),
		managedRoot: managedRootForHome(options.Home),
	}
	if installer.apply {
		unlock, lockErr := lockInstaller(installer.managedRoot)
		if lockErr != nil {
			return Report{}, lockErr
		}
		defer func() { err = errors.Join(err, unlock()) }()
	}
	installer.say("pfm generate: home=%s config=%s", options.Home, options.ConfigDir)
	if installer.apply {
		installer.say("MODE: apply")
	} else {
		installer.say("MODE: plan — nothing will change")
	}
	installer.say("")
	return installer.finish(installer.generate())
}

// generate runs the compile steps in install's order: the prompts first, since
// the Codex fence reads the generated codex.md, then the command links and the
// policy engine-sync global reads. A failing later step is named and the rest
// still run.
func (installer *engine) generate() error {
	if installer.options.SourceRepo != "" {
		if err := installer.recordSourceRepoMarker(); err != nil {
			return fmt.Errorf("record source clone: %w", err)
		}
	}
	if err := installer.generateHarnessPrompts(); err != nil {
		return fmt.Errorf("generate harness prompts: %w", err)
	}
	assets, err := assetFiles()
	if err != nil {
		return fmt.Errorf("enumerate embedded install assets: %w", err)
	}
	installer.deferFailure(namedStep("global commands", installer.wireGlobalCommands()))
	installer.deferFailure(namedStep("engine-sync policy", installer.wireEngineSyncConfig()))
	installer.deferFailure(namedStep("engine-sync global agents", installer.wireEngineSyncGlobal(assets)))
	if len(installer.codexHomes()) != 0 {
		installer.deferFailure(
			namedStep("Codex config defaults and developer_instructions", installer.wireCodexDefaults()),
		)
	}
	return nil
}

// namedStep prefixes a step's failure with the step's name; nil stays nil.
func namedStep(name string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", name, err)
}
