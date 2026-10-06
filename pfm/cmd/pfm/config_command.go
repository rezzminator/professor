package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/cli"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func runConfig(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: pfm config init [--force] | show | validate | claude [--account N]")
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
	case pfmengine.MustLookup(pfmengine.Claude).LongName:
		return runConfigClaude(args[1:], stdout, stderr, runtime)
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
		fmt.Fprintln(stderr, "usage: pfm config init [--force] | show | validate | claude [--account N]")
		return 2
	}
}

func runConfigClaude(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	flags := cli.NewFlagSet("config claude", "usage: pfm config claude [--account N]", stderr)
	account := flags.Int("account", 0, "Claude account roster ID")
	if code, ok := cli.ParseFlags(flags, args); !ok {
		return code
	}
	if flags.NArg() != 0 || *account < 0 {
		flags.Usage()
		return 2
	}
	if *account != 0 {
		if _, found := runtime.Config.AccountByID(*account); !found {
			ids := make([]int, 0, len(runtime.Config.Accounts))
			for _, entry := range runtime.Config.Accounts {
				ids = append(ids, entry.ID)
			}
			sort.Ints(ids)
			configured := make([]string, 0, len(ids))
			for _, id := range ids {
				configured = append(configured, strconv.Itoa(id))
			}
			fmt.Fprintf(
				stderr,
				"pfm config claude: account %d is not configured (configured: %s)\n",
				*account,
				strings.Join(configured, ","),
			)
			return 2
		}
	}
	for _, row := range claudelaunch.Resolve(runtime.Config, *account) {
		fmt.Fprintf(
			stdout,
			"%s wire=%s target=%s value=%s source=%s\n",
			row.Knob.Name,
			row.Knob.Wire,
			row.Knob.Target,
			row.Value,
			row.Won,
		)
	}
	return 0
}

func runConfigInit(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	flags := cli.NewFlagSet("config init", "usage: pfm config init [--force]", stderr)
	force := flags.Bool("force", false, "overwrite an existing config")
	if code, ok := cli.ParseFlags(flags, args); !ok {
		return code
	}
	if flags.NArg() != 0 {
		flags.Usage()
		return 2
	}
	if runtime.Config.Path == "" {
		_, markerErr := pfmconfig.ResolvePath(runtime.Paths.Home)
		fmt.Fprintf(stderr, "pfm config init: %v\n", pfmconfig.NoConfigPathError(markerErr))
		return 1
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
		"  cache: dir (default ~/.professor/.harvester-cache), ttlSeconds (0 = never expire), negativeTtlSeconds, negativeTransientTtlSeconds (0 = never cache failures)",
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
	for index, value := range []string{runtime.Paths.StateDB, runtime.Paths.CacheDB} {
		source := config.Source([]string{"state.db", "state.cacheDb"}[index])
		if (paths.OSEnv{}).Get([]string{paths.EnvStateDB, paths.EnvCacheDB}[index]) != "" {
			source = "env"
		}
		fmt.Fprintf(stdout, "config state.%s=%s (%s)\n", []string{"db", "cacheDb"}[index], value, source)
	}
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
	printClaude := func(key string, value any) {
		fmt.Fprintf(stdout, "config claude.%s=%v (%s)\n", key, value, config.Source("claude."+key))
	}
	printClaude("permissionMode", config.Claude.PermissionMode)
	printClaude("binary", config.Claude.Binary)
	printClaude("theme", config.Claude.Theme)
	printClaude("webSearchesPerSession", config.Claude.WebSearchesPerSession)
	printClaude("autoCompactWindow", config.Claude.AutoCompactWindow)
	printClaude("tmuxTruecolor", config.Claude.TmuxTruecolor)
	printClaude("cleanupPeriodDays", config.Claude.CleanupPeriodDays)
	printClaude("requireManagedCleanup", config.Claude.RequireManagedCleanup)
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
	ignored := strings.Join(config.Doctor.IgnoreWarnings, ",")
	if ignored == "" {
		ignored = "none"
	}
	fmt.Fprintf(stdout, "config doctor.ignoreWarnings=%s (%s)\n", ignored, config.Source("doctor.ignoreWarnings"))
	fmt.Fprintf(stdout, "config codex.yolo=%t (%s)\n", config.Codex.Yolo, config.Source("codex.yolo"))
	fmt.Fprintf(stdout, "config codex.binary=%s (%s)\n", config.Codex.Binary, config.Source("codex.binary"))
	fmt.Fprintf(stdout, "config mcp.http.port=%d (%s)\n", config.MCP.HTTP.Port, config.Source("mcp.http.port"))
	thirdParty := make([]string, 0, len(config.MCP.ThirdParty))
	for name := range config.MCP.ThirdParty {
		thirdParty = append(thirdParty, name)
	}
	sort.Strings(thirdParty)
	names, source := strings.Join(thirdParty, ","), config.Source("mcp.thirdParty")
	if len(thirdParty) == 0 {
		names = "none"
	}
	fmt.Fprintf(stdout, "config mcp.thirdParty=%s (%s)\n", names, source)
	for _, name := range pfmconfig.RegisteredMCPServers() {
		fmt.Fprintf(
			stdout,
			"config %s=%t (%s)\n",
			pfmconfig.MCPServerKey(name),
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
	flattenHarvesterConfig(pfmconfig.MCPServerHarvester, tree, values)
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
