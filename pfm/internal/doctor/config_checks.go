package doctor

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func PrintConfig(stdout io.Writer, runtime config.Runtime) (int, int) {
	warnings, failures := printConfigFileRows(stdout, runtime)
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
	for _, account := range runtime.Config.CodexAccounts {
		path := filepath.Join(account.Home, "auth.json")
		if err := config.CodexLoginError(account.Home); err != nil {
			failures++
			var pathErr *os.PathError
			_, statErr := os.Stat(path)
			switch {
			case errors.Is(statErr, os.ErrNotExist) && !errors.Is(err, config.ErrCodexLoggedOut):
				fmt.Fprintf(
					stdout,
					"doctor: codex-login codex[%d] %s UNREADABLE error=%v\n",
					account.ID,
					filepath.Join(account.Home, "config.toml"),
					err,
				)
			case errors.Is(statErr, os.ErrNotExist) && errors.Is(err, config.ErrCodexLoggedOut):
				fmt.Fprintf(stdout, "doctor: codex-login codex[%d] %s missing — run codex login\n", account.ID, path)
			case errors.As(err, &pathErr):
				fmt.Fprintf(
					stdout,
					"doctor: codex-login codex[%d] %s UNREADABLE error=%v\n",
					account.ID,
					path,
					pathErr.Err,
				)
			case !errors.Is(err, config.ErrCodexLoggedOut):
				// A present auth.json Codex cannot decode (corrupt, or a field of the wrong type)
				// is a failure to read the login, never "has no tokens".
				cause := errors.Unwrap(err)
				if cause == nil {
					cause = err
				}
				fmt.Fprintf(
					stdout,
					"doctor: codex-login codex[%d] %s UNREADABLE error=%v — run codex login\n",
					account.ID,
					path,
					cause,
				)
			default:
				fmt.Fprintf(
					stdout,
					"doctor: codex-login codex[%d] %s has no tokens.access_token and tokens.account_id — run codex login\n",
					account.ID,
					path,
				)
			}
			continue
		}
		fmt.Fprintf(stdout, "doctor: codex-login codex[%d] ok\n", account.ID)
	}
	return warnings, failures
}

func printConfigFileRows(stdout io.Writer, runtime config.Runtime) (warnings, failures int) {
	path := runtime.Config.Path
	if path == "" {
		resolved, markerErr := config.ResolvePath(runtime.Paths.Home)
		switch {
		case markerErr == nil:
			fmt.Fprintf(stdout, "doctor: config: missing (resolves to %s, not loaded) — run pfm install\n", resolved)
		case errors.Is(markerErr, paths.ErrNoSourceRepoMarker):
			fmt.Fprintln(stdout, "doctor: config: missing (no source repo recorded) — run pfm install")
		default:
			fmt.Fprintf(stdout, "doctor: config: missing (%v) — run pfm install\n", markerErr)
		}
		return 1, 0
	}
	if runtime.ConfigError == nil {
		failures = 1
	}
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(stdout, "doctor: config: missing %s — run pfm install\n", path)
		// The host gate refuses pfm install while a legacy config waits, so the
		// row names the host check's fix; under an explicit --config the check
		// skips the file, and so does the row.
		if runtime.Paths.Home != "" && !runtime.ConfigExplicit {
			legacy := filepath.Join(config.LegacyConfigDir(paths.OSEnv{}, runtime.Paths.Home), config.FileName)
			if _, err := os.Stat(legacy); err == nil {
				fmt.Fprintf(
					stdout,
					"doctor: config: legacy file %s is not read — apply the host-check legacy-config fix\n",
					legacy,
				)
			}
		}
		return 1, 0
	}
	if err != nil {
		fmt.Fprintf(stdout, "doctor: config: unreadable %s error=%v\n", path, err)
		return 0, failures
	}
	var object map[string]any
	err = json.Unmarshal(content, &object)
	if err == nil && object == nil {
		err = errors.New("config is JSON null, not an object")
	}
	if err != nil {
		fmt.Fprintf(stdout, "doctor: config: unreadable %s error=%v\n", path, err)
		return 0, failures
	}
	for _, entry := range config.Keys() {
		parts := strings.Split(entry.Key, ".")
		var current any = object
		found := true
		for _, part := range parts {
			values, ok := current.(map[string]any)
			if !ok {
				found = false
				break
			}
			current, ok = values[part]
			if !ok {
				found = false
				break
			}
		}
		if !found {
			fmt.Fprintf(
				stdout,
				"doctor: config: missing key %s (default %v) — informational: unset, its default applies\n",
				entry.Key,
				entry.Default,
			)
		}
	}
	return 0, 0
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
