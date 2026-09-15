package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	pfmconfig "hostops/pfm/internal/config"
	pfmengine "hostops/pfm/internal/engine"
)

func runConfig(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: pfm config init [--force] | show | validate")
		return 2
	}
	switch args[0] {
	case initCommand:
		return runConfigInit(args[1:], stdout, stderr, runtime)
	case "show":
		if len(args) != 1 {
			fmt.Fprintln(stderr, "usage: pfm config show")
			return 2
		}
		if runtime.ConfigError != nil {
			fmt.Fprintf(stderr, "pfm config show: configuration error: %v\n", runtime.ConfigError)
		}
		printResolvedConfig(stdout, runtime)
		return 0
	case "validate":
		if len(args) != 1 {
			fmt.Fprintln(stderr, "usage: pfm config validate")
			return 2
		}
		loaded, err := pfmconfig.Load(runtime.Config.Path, runtime.Paths.Home, runtime.Paths.Roots[pfmengine.Claude])
		if err != nil {
			fmt.Fprintf(stderr, "pfm config validate: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "config valid: %s\n", loaded.Path)
		return 0
	default:
		fmt.Fprintln(stderr, "usage: pfm config init [--force] | show | validate")
		return 2
	}
}

func runConfigInit(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	flags := newFlagSet("config init", "usage: pfm config init [--force]", stderr)
	force := flags.Bool("force", false, "overwrite an existing config")
	if code, ok := parseFlags(flags, args); !ok {
		return code
	}
	if flags.NArg() != 0 {
		flags.Usage()
		return 2
	}
	harvesterPath := pfmconfig.HarvesterPath(runtime.Config.Path)
	if !*force {
		// Refuse before writing either file, so init never leaves half a pair.
		for _, path := range []string{runtime.Config.Path, harvesterPath} {
			if _, err := os.Stat(path); err == nil {
				fmt.Fprintf(stderr, "pfm config init: %s already exists; use --force to overwrite\n", path)
				return 1
			} else if !errors.Is(err, fs.ErrNotExist) {
				fmt.Fprintf(stderr, "pfm config init: inspect %s: %v\n", path, err)
				return 1
			}
		}
	}
	if err := pfmconfig.WriteDefault(
		runtime.Config.Path,
		runtime.Paths.Home,
		runtime.Paths.Roots[pfmengine.Claude],
		*force,
	); err != nil {
		fmt.Fprintf(stderr, "pfm config init: %v\n", err)
		return 1
	}
	if err := pfmconfig.WriteDefaultHarvester(harvesterPath, *force); err != nil {
		fmt.Fprintf(stderr, "pfm config init: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "config initialized: %s\n", runtime.Config.Path)
	fmt.Fprintf(stdout, "harvester config initialized: %s\n", harvesterPath)
	fmt.Fprintln(stdout, "field documentation:")
	fmt.Fprintln(stdout, "  accounts: configured Claude account roster, configDir, and emoji badge")
	fmt.Fprintln(stdout, "  claude.permissionMode: bypass or prompted; account values override this default")
	fmt.Fprintln(
		stdout,
		"  claude.compactNudge: the milestone self-compact reminder — enabled, start %, step % (main Claude chat only; a reminder, never an order); account values override",
	)
	fmt.Fprintln(
		stdout,
		"  codex.yolo: whether Codex launches with approval bypass; account values override this default",
	)
	fmt.Fprintln(
		stdout,
		"  tmux.titles.enabled: whether pfm owns the outer terminal's title (set-titles on + pfm's set-titles-string); false leaves whatever the host set before tmux started",
	)
	fmt.Fprintln(
		stdout,
		"  nameSync.interval: how often the window-name backstop runs (Go duration, minimum 1m); rendered into the launchd StartInterval and the systemd OnUnitInactiveSec at install time",
	)
	fmt.Fprintln(stdout, "  theme: embedded palette name (default or tokyo-night)")
	fmt.Fprintln(stdout, "  mcp: enabled servers and the loopback HTTP port (chat + harvester, no auth)")
	fmt.Fprintln(stdout, "  ask: default one-shot engine, model, and effort")
	fmt.Fprintln(
		stdout,
		"harvester config fields (every Harvester setting lives here; keep it chmod 600 once it holds a key):",
	)
	fmt.Fprintln(stdout, "  enabled: serve the harvester MCP")
	fmt.Fprintln(
		stdout,
		"  external: the authenticated second port — enabled, host, port, publicURL, auth.passphrase / auth.staticToken, stateDir",
	)
	fmt.Fprintln(stdout, "  search: enabled, searxngURL (its origin is trusted — loopback/LAN is fine), braveApiKey")
	fmt.Fprintln(
		stdout,
		"  scholarly: contactEmail (enables Unpaywall), googleBooksApiKey, coreApiKey, semanticScholarApiKey",
	)
	fmt.Fprintln(stdout, "  scholarly: googleScholarURL — optional Google Scholar base URL; empty disables it")
	fmt.Fprintln(stdout, "  fetch: browser (the opt-in real-browser rung), userAgent, proxyURL")
	fmt.Fprintln(stdout, "  convert: pdfOcr, pdfLayout — handed to the pinned Python converter")
	fmt.Fprintln(
		stdout,
		"  cache: dir (default ~/.professor/.cache), ttlSeconds (0 = never expire), negativeTtlSeconds, negativeTransientTtlSeconds (0 = never cache failures)",
	)
	fmt.Fprintln(stdout, "  output: maxInlineChars")
	return 0
}

func printResolvedConfig(stdout io.Writer, runtime commandRuntime) {
	config := runtime.Config
	fmt.Fprintf(stdout, "config path=%s exists=%t\n", config.Path, config.Exists)
	fmt.Fprintf(
		stdout,
		"config version=%d effective (input=%d %s)\n",
		config.Version,
		config.InputVersion,
		config.Source(versionCommand),
	)
	fmt.Fprintf(stdout, "config theme=%s (%s)\n", config.Theme, config.Source("theme"))
	accounts := make([]string, 0, len(config.Accounts))
	for index, account := range config.Accounts {
		accounts = append(accounts, fmt.Sprintf("%d:%s:%s", account.ID, account.ConfigDir, config.EmojiFor(account.ID)))
		if config.Source(fmt.Sprintf("accounts[%d].emoji", index)) == pfmconfig.SourceFile {
			accounts[len(accounts)-1] += " (file)"
		} else {
			accounts[len(accounts)-1] += " (default)"
		}
	}
	fmt.Fprintf(stdout, "config accounts=%s (%s)\n", strings.Join(accounts, ","), config.Source("accounts"))
	fmt.Fprintf(
		stdout,
		"config claude.permissionMode=%s (%s)\n",
		config.Claude.PermissionMode,
		config.Source("claude.permissionMode"),
	)
	fmt.Fprintf(stdout, "config claude.binary=%s (%s)\n", config.Claude.Binary, config.Source("claude.binary"))
	fmt.Fprintf(
		stdout,
		"config claude.compactNudge.enabled=%t (%s)\n",
		config.Claude.CompactNudge.Enabled,
		config.Source("claude.compactNudge.enabled"),
	)
	fmt.Fprintf(
		stdout,
		"config claude.compactNudge.start=%d (%s)\n",
		config.Claude.CompactNudge.Start,
		config.Source("claude.compactNudge.start"),
	)
	fmt.Fprintf(
		stdout,
		"config claude.compactNudge.step=%d (%s)\n",
		config.Claude.CompactNudge.Step,
		config.Source("claude.compactNudge.step"),
	)
	fmt.Fprintf(
		stdout,
		"config tmux.titles.enabled=%t (%s)\n",
		config.Tmux.Titles.Enabled,
		config.Source("tmux.titles.enabled"),
	)
	fmt.Fprintf(
		stdout,
		"config nameSync.interval=%s (%s)\n",
		config.NameSync.Interval,
		config.Source("nameSync.interval"),
	)
	fmt.Fprintf(stdout, "config codex.yolo=%t (%s)\n", config.Codex.Yolo, config.Source("codex.yolo"))
	fmt.Fprintf(stdout, "config codex.binary=%s (%s)\n", config.Codex.Binary, config.Source("codex.binary"))
	fmt.Fprintf(stdout, "config mcp.http.port=%d (%s)\n", config.MCP.HTTP.Port, config.Source("mcp.http.port"))
	for _, name := range pfmconfig.RegisteredMCPServers() {
		fmt.Fprintf(
			stdout,
			"config %s=%t (%s)\n",
			mcpServerKey(name),
			config.MCPServers[name].Enabled,
			config.MCPServerSource(name),
		)
	}
	fmt.Fprintf(stdout, "config ask.engine=%s (%s)\n", config.Ask.Engine, config.Source("ask.engine"))
	for _, id := range pfmengine.All() {
		name := pfmengine.MustLookup(id).LongName
		prefs := config.Ask.PrefsFor(id)
		fmt.Fprintf(stdout, "config ask.%s.model=%s (%s)\n", name, prefs.Model, config.Source("ask."+name+".model"))
		fmt.Fprintf(stdout, "config ask.%s.effort=%s (%s)\n", name, prefs.Effort, config.Source("ask."+name+".effort"))
	}
	printResolvedHarvesterConfig(stdout, config)
}

// printResolvedHarvesterConfig renders harvester.config.json key by key with
// its source; every secret is redacted.
func printResolvedHarvesterConfig(stdout io.Writer, config pfmconfig.Config) {
	fmt.Fprintf(stdout, "config harvester path=%s exists=%t\n", config.Harvester.Path, config.Harvester.Exists)
	content, err := pfmconfig.MarshalHarvester(config.Harvester, true)
	if err != nil {
		fmt.Fprintf(stdout, "config harvester error=encode: %v\n", err)
		return
	}
	var tree map[string]any
	if err := json.Unmarshal(content, &tree); err != nil {
		fmt.Fprintf(stdout, "config harvester error=decode: %v\n", err)
		return
	}
	values := map[string]string{}
	flattenHarvesterConfig(harvesterServer, tree, values)
	for _, key := range pfmconfig.HarvesterSourceKeys() {
		fmt.Fprintf(stdout, "config %s=%s (%s)\n", key, values[key], config.Source(key))
	}
}

func flattenHarvesterConfig(prefix string, node map[string]any, into map[string]string) {
	for key, value := range node {
		path := prefix + "." + key
		if child, ok := value.(map[string]any); ok {
			flattenHarvesterConfig(path, child, into)
			continue
		}
		into[path] = fmt.Sprint(value)
	}
}
