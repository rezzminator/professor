package doctor

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func PrintConfig(stdout io.Writer, runtime config.Runtime) {
	fmt.Fprintf(stdout, "doctor: config path=%s exists=%t\n", runtime.Config.Path, runtime.Config.Exists)
	fmt.Fprintf(
		stdout,
		"doctor: config version=%d effective (input=%d %s)\n",
		runtime.Config.Version,
		runtime.Config.InputVersion,
		runtime.Config.Source(versionCommand),
	)
	fmt.Fprintf(stdout, "doctor: config theme=%s (%s)\n", runtime.Config.Theme, runtime.Config.Source("theme"))
	accounts := make([]string, 0, len(runtime.Config.Accounts))
	for _, account := range runtime.Config.Accounts {
		accounts = append(accounts, fmt.Sprintf("%d:%s", account.ID, account.ConfigDir))
	}
	fmt.Fprintf(
		stdout,
		"doctor: config accounts=%s (%s)\n",
		strings.Join(accounts, ","),
		runtime.Config.Source("accounts"),
	)
	fmt.Fprintf(
		stdout,
		"doctor: config claude.permissionMode=%s (%s)\n",
		runtime.Config.Claude.PermissionMode,
		runtime.Config.Source("claude.permissionMode"),
	)
	fmt.Fprintf(
		stdout,
		"doctor: config claude.binary=%s (%s)\n",
		runtime.Config.Claude.Binary,
		runtime.Config.Source("claude.binary"),
	)
	fmt.Fprintf(
		stdout,
		"doctor: config codex.yolo=%t (%s)\n",
		runtime.Config.Codex.Yolo,
		runtime.Config.Source("codex.yolo"),
	)
	fmt.Fprintf(
		stdout,
		"doctor: config codex.binary=%s (%s)\n",
		runtime.Config.Codex.Binary,
		runtime.Config.Source("codex.binary"),
	)
	for _, name := range config.RegisteredMCPServers() {
		fmt.Fprintf(
			stdout,
			"doctor: config %s=%t (%s)\n",
			config.MCPServerKey(name),
			runtime.Config.MCPServers[name].Enabled,
			runtime.Config.MCPServerSource(name),
		)
	}
	fmt.Fprintf(
		stdout,
		"doctor: config mcp.http.port=%d (%s)\n",
		runtime.Config.MCP.HTTP.Port,
		runtime.Config.Source("mcp.http.port"),
	)
	fmt.Fprintf(
		stdout,
		"doctor: config harvester path=%s exists=%t\n",
		runtime.Config.Harvester.Path,
		runtime.Config.Harvester.Exists,
	)
}

// RetiredHarvesterEnv maps every environment variable the harvester used to
// read to where that setting lives now. The harvester ignores them all, so a
// set one is a setting that silently stopped applying — doctor says so.
var RetiredHarvesterEnv = []struct{ Name, Now string }{
	{"SEARXNG_URL", "search.searxngURL"},
	{"BRAVE_API_KEY", "search.braveApiKey"},
	{"HARVESTER_DISABLE_SEARCH", "search.enabled"},
	{"HARVESTER_CONTACT_EMAIL", "scholarly.contactEmail"},
	{"GOOGLE_BOOKS_API_KEY", "scholarly.googleBooksApiKey"},
	{"CORE_API_KEY", "scholarly.coreApiKey"},
	{"SEMANTIC_SCHOLAR_API_KEY", "scholarly.semanticScholarApiKey"},
	{"HARVESTER_BROWSER", "fetch.browser"},
	{"HARVESTER_PDF_OCR", "convert.pdfOcr"},
	{"HARVESTER_PDF_LAYOUT", "convert.pdfLayout"},
	{"WEBFETCH_DIR", "cache.dir"},
	{"HARVESTER_CACHE_DIR", "cache.dir"},
	{"HARVESTER_CACHE_TTL", "cache.ttlSeconds"},
	{"HARVESTER_NEG_TTL", "cache.negativeTtlSeconds"},
	{"HARVESTER_NEG_TTL_TRANSIENT", "cache.negativeTransientTtlSeconds"},
	{"HARVESTER_MAX_INLINE_CHARS", "output.maxInlineChars"},
	{"HARVESTER_STATE_DIR", "external.stateDir"},
	{"HARVESTER_AUTH_PASSPHRASE", "external.auth.passphrase"},
	{"HARVESTER_STATIC_TOKEN", "external.auth.staticToken"},
	{"HARVESTER_LOCAL_ROOTS", ""},
	{"PFM_HARVEST_PYTHON", ""},
}

// printHarvesterExternalDoctor reports the external gateway whenever it is
// configured on: the daemon's live state, or why it cannot be running. A
// configured gateway that never opened is a warning, never an absent line.
func printHarvesterExternalDoctor(stdout io.Writer, harvester config.HarvesterConfig, reported string) int {
	if !harvester.External.Enabled {
		return 0
	}
	state := reported
	switch {
	case !harvester.Enabled:
		state = "off (external.enabled is true but harvester.enabled is false; the gateway only runs with the harvester)"
	case state == "":
		state = "not reported (daemon predates the external gateway; restart it)"
	}
	fmt.Fprintf(stdout, "doctor: mcp harvester_external=%s\n", state)
	if strings.HasPrefix(state, "listening") {
		return 0
	}
	return 1
}

// printHarvesterConfigDoctor reports the two ways a harvester setting can stop
// applying without an error: a config migration still pending (pre-split
// layout, an interrupted migration's leftover, the old default port), and a
// retired environment variable still set. Each is a warning.
func printHarvesterConfigDoctor(stdout io.Writer, runtime config.Runtime) int {
	return printHarvesterConfigDoctorWithEnv(stdout, runtime, paths.OSEnv{})
}

// printDuplicateSeatLogins reads the user-scope Claude registries that the
// installer already owns and names seats sharing one OAuth login. A shared
// login shares one provider usage cap, so reloading onto the second seat does
// not provide fresh capacity.
func printDuplicateSeatLogins(stdout io.Writer, runtime config.Runtime, env paths.Env) int {
	type seat struct {
		id   int
		path string
	}
	byEmail := map[string][]seat{}
	warnings := 0
	for _, registry := range installer.ClaudeUserRegistries(
		runtime.Paths.Home,
		runtime.Config.Accounts,
		config.AmbientClaudeConfigDirFrom(env),
	) {
		if registry.Account == 0 {
			continue
		}
		raw, err := os.ReadFile(registry.Path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			fmt.Fprintf(
				stdout,
				"doctor: duplicate-seat-login registry=%s state=unavailable error=%v\n",
				registry.Path,
				err,
			)
			warnings++
			continue
		}
		var document struct {
			OAuthAccount struct {
				EmailAddress string `json:"emailAddress"`
			} `json:"oauthAccount"`
		}
		if err := json.Unmarshal(raw, &document); err != nil {
			fmt.Fprintf(
				stdout,
				"doctor: duplicate-seat-login registry=%s state=unreadable error=%v\n",
				registry.Path,
				err,
			)
			warnings++
			continue
		}
		email := strings.TrimSpace(document.OAuthAccount.EmailAddress)
		if email == "" {
			continue
		}
		byEmail[email] = append(byEmail[email], seat{id: registry.Account, path: registry.Path})
	}
	emails := make([]string, 0, len(byEmail))
	for email := range byEmail {
		emails = append(emails, email)
	}
	sort.Strings(emails)
	for _, email := range emails {
		seats := byEmail[email]
		if len(seats) < 2 {
			continue
		}
		labels := make([]string, 0, len(seats))
		for _, value := range seats {
			labels = append(labels, fmt.Sprintf("%d:%s", value.id, value.path))
		}
		sort.Strings(labels)
		warnings++
		fmt.Fprintf(
			stdout,
			"doctor: duplicate-seat-login email=%s seats=%s advisory=these seats share one OAuth usage cap; reload onto one of them does not change capacity\n",
			email,
			strings.Join(labels, ","),
		)
	}
	return warnings
}

func printHarvesterConfigDoctorWithEnv(stdout io.Writer, runtime config.Runtime, env paths.Env) int {
	warnings := 0
	if migration, err := config.PlanMigration(runtime.Config); err != nil {
		warnings++
		fmt.Fprintf(stdout, "doctor: config layout=unknown error=%v\n", err)
	} else if !migration.Empty() {
		warnings++
		fmt.Fprintf(stdout, "doctor: config layout=pre-split path=%s remediation=run pfm install --yes (%s)\n",
			runtime.Config.Path, strings.Join(migration.Steps(), "; "))
	}
	for _, retired := range RetiredHarvesterEnv {
		if strings.TrimSpace(env.Get(retired.Name)) == "" {
			continue
		}
		warnings++
		if retired.Now == "" {
			fmt.Fprintf(
				stdout,
				"doctor: harvester retired_env=%s is set but ignored (removed; pfm never honored it)\n",
				retired.Name,
			)
			continue
		}
		fmt.Fprintf(
			stdout,
			"doctor: harvester retired_env=%s is set but ignored — move it to %s in %s\n",
			retired.Name,
			retired.Now,
			runtime.Config.Harvester.Path,
		)
	}
	return warnings
}
