//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
)

func TestHostLayoutMigratesLegacyHome(t *testing.T) {
	requireE2EFence(t)
	repo := sharedSourceRepo(t)
	h := &e2eHarness{t: t, repo: repo, goCache: requiredGoEnv(t, "GOCACHE"), goModCache: requiredGoEnv(t, "GOMODCACHE")}
	bin := os.Getenv(e2eScriptBinaryEnv)
	if bin == "" {
		t.Fatalf("%s: e2e binary was not built", e2eScriptBinaryEnv)
	}
	home := h.newHome(bin)
	marker := filepath.Join(home, e2eSourceMarker)
	if err := os.MkdirAll(filepath.Dir(marker), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte(repo+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) commandResult {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
		defer cancel()
		command := exec.CommandContext(ctx, filepath.Join(home, e2eCanonicalPFM), args...)
		command.Dir = repo
		command.Env = append(h.environment(home),
			"PFM_MANAGED_SETTINGS_DIR="+filepath.Join(home, "managed-settings.d"),
			"PFM_LOG_LEVEL=off")
		output, err := command.CombinedOutput()
		return commandResult{stdout: string(output), err: err}
	}
	// A current install supplies the non-layout assets. The fixture then puts
	// only the layout-governed paths back into their legacy shapes.
	h.requireSuccess("stage installed assets", run("install", "--yes", "--skip-harvest", "--skip-themes"))
	plantLegacyHostLayout(t, home, repo)
	plantHostInstallerDrift(t, home)
	// The migration apply must run the plugin door again; its settings write
	// makes rollback detect a missing plugin journal record.
	for _, relative := range []string{".claude/settings.json", ".cc/2/settings.json", ".cc/3/settings.json"} {
		raw, err := os.ReadFile(filepath.Join(home, relative))
		if err != nil {
			t.Fatal(err)
		}
		var settings map[string]any
		if err := json.Unmarshal(raw, &settings); err != nil {
			t.Fatal(err)
		}
		if _, present := settings["enabledPlugins"]; present {
			t.Fatalf("plant left %s plugin enabled; apply would not exercise the plugin door", relative)
		}
	}
	filesBefore := hostLayoutPayloads(t, home, "")
	stateBefore := hostLayoutTableCounts(t, filepath.Join(home, ".cc", "fleet.db"))
	cacheBefore := hostLayoutTableCounts(t, filepath.Join(home, ".local", "state", "pfm", "fleet.db"))
	stateBefore["hidden"] += cacheBefore["hidden"] // cache v8 kills move into shared state before its table drops.
	before := hostLayoutSnapshot(t, home)

	preview := run("install", "--skip-harvest", "--skip-themes")
	h.requireSuccess("layout preview", preview)
	hostLayoutSameSnapshot(t, "preview", before, hostLayoutSnapshot(t, home))

	apply := run("install", "--yes", "--skip-harvest", "--skip-themes")
	h.requireSuccess("layout apply", apply)
	journal := hostLayoutJournal(t, home, apply.stdout)
	if !strings.Contains(apply.stdout, "change  run CLAUDE_CONFIG_DIR=") {
		t.Fatalf("migration apply did not run the plugin door:\n%s", apply.stdout)
	}
	journalRaw, err := os.ReadFile(filepath.Join(journal, "journal.json"))
	if err != nil {
		t.Fatal(err)
	}
	var journalRecords []struct{ Row, Destination, Result string }
	if err := json.Unmarshal(journalRaw, &journalRecords); err != nil {
		t.Fatal(err)
	}
	// The journal records physical paths; on macOS /tmp is a link to /private/tmp.
	physicalHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatalf("resolve e2e home %s: %v", home, err)
	}
	pluginPaths := map[string]bool{}
	for _, record := range journalRecords {
		if record.Row == "install" && record.Result == "applied" {
			pluginPaths[record.Destination] = true
		}
	}
	for _, path := range []string{
		filepath.Join(physicalHome, ".claude", "settings.json"),
		filepath.Join(physicalHome, ".claude", "plugins"),
	} {
		if !pluginPaths[path] {
			t.Fatalf("plugin door did not journal %s", path)
		}
	}
	for _, line := range strings.Split(apply.stdout, "\n") {
		if strings.Contains(line, "refuse  layout") || strings.Contains(line, "UNREADABLE") {
			t.Fatalf("unexpected layout refusal: %s\n%s", line, apply.stdout)
		}
	}
	for _, row := range []string{"config", "state-db", "cache-db", "session-store", "memory-helpers", "account-settings", "account-mcp", "home-mcp", "zshrc", "staged-prompts", "shared-db", "stray-dir"} {
		if !strings.Contains(apply.stdout, "ok      layout "+row+" ") {
			t.Fatalf("apply did not verify layout row %s:\n%s", row, apply.stdout)
		}
	}
	hostLayoutMCPStripped(t, home)
	if !strings.Contains(apply.stdout, "conflict layout session-store") ||
		!strings.Contains(apply.stdout, filepath.Join(journal, "backup", "conflicts")) {
		t.Fatalf("session conflict was not listed and parked in %s:\n%s", journal, apply.stdout)
	}
	filesAfter := hostLayoutPayloads(t, home, journal)
	if !reflect.DeepEqual(filesBefore, filesAfter) {
		t.Fatalf("transcript/checkpoint/task SHA-256 multiset changed: before=%v after=%v", filesBefore, filesAfter)
	}
	hostLayoutSameCounts(t, "state", stateBefore,
		hostLayoutTableCounts(t, filepath.Join(home, ".local", "state", "pfm", "pfm.db")), "swap_event")
	hostLayoutSameCounts(t, "cache", cacheBefore,
		hostLayoutTableCounts(t, filepath.Join(home, ".local", "state", "pfm", "pfm-cache.db")), "hidden")
	hostLayoutStateHiddenIDs(t, filepath.Join(home, ".local", "state", "pfm", "pfm.db"), []string{"cached", "kept"})

	second := run("install", "--yes", "--skip-harvest", "--skip-themes")
	h.requireSuccess("layout idempotence", second)
	if !strings.Contains(second.stdout, "layout: nothing to do") ||
		strings.Contains(second.stdout, "install journal:") {
		t.Fatalf("second install changed layout:\n%s", second.stdout)
	}
	doctor := run("doctor")
	for _, line := range strings.Split(doctor.stdout, "\n") {
		trimmed := strings.TrimSpace(line)
		for _, prefix := range []string{"session-store:", "managed-cleanup:", "legacy:", "state:", "layout:"} {
			if strings.HasPrefix(trimmed, prefix) {
				t.Fatalf("doctor layout finding %q (exit %v)", line, doctor.err)
			}
		}
	}
	if doctor.err != nil {
		t.Logf("doctor has non-layout findings: %v\n%s", doctor.err, doctor.stdout)
	}

	rolled := run("install", "--rollback", filepath.Base(journal))
	h.requireSuccess("layout rollback", rolled)
	if runtime.GOOS == "darwin" &&
		!strings.Contains(rolled.stdout, "launchctl bootstrap gui/") {
		t.Fatalf("rollback did not print the launchd start lines for the jobs it stopped:\n%s", rolled.stdout)
	}
	hostLayoutSameSnapshot(t, "rollback", before, hostLayoutSnapshot(t, home))
}

func plantLegacyHostLayout(t *testing.T, home, repo string) {
	t.Helper()
	configPath := filepath.Join(home, "pfm.config.json")
	configRaw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(configRaw, &config); err != nil {
		t.Fatal(err)
	}
	delete(config, "log")
	configRaw, err = json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, configRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"systemctl", "launchctl"} {
		path := filepath.Join(home, ".local", "bin", name)
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		quiet := strings.ReplaceAll(string(body), "printf '"+name+" %s\\n' \"$*\" >> \"$HOME/scheduler-calls\"\n", "")
		if quiet == string(body) {
			t.Fatalf("scheduler fixture %s has no audit line to disable", path)
		}
		if name == "systemctl" {
			quiet = strings.Replace(quiet, "case \"$*\" in\n", "case \"$*\" in\n  *stop*|*start*) exit 0 ;;\n", 1)
		}
		if err := os.WriteFile(path, []byte(quiet), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	write := func(relative string, body []byte) {
		t.Helper()
		path := filepath.Join(home, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	remove := func(relative string) {
		t.Helper()
		if err := os.RemoveAll(filepath.Join(home, relative)); err != nil {
			t.Fatal(err)
		}
	}
	link := func(relative, target string) {
		t.Helper()
		remove(relative)
		if err := os.Symlink(target, filepath.Join(home, relative)); err != nil {
			t.Fatal(err)
		}
	}
	link(".cc/1", filepath.Join(home, ".claude"))
	for _, entry := range []string{"projects", "file-history", "tasks", "session-env"} {
		write(filepath.Join(".claude", entry, "same-id.jsonl"), []byte("store-"+entry+"\n"))
		for _, account := range []string{"2", "3"} {
			base := filepath.Join(".cc", account, entry)
			remove(base)
			if entry == "projects" {
				link(base, filepath.Join(home, ".claude", "projects"))
				continue
			}
			write(filepath.Join(base, account+"-checkpoint.jsonl"), []byte(account+"-"+entry+"\n"))
		}
	}
	write(".cc/2/tasks/same-id.jsonl", []byte("account-conflict\n"))
	plantLegacyAccountFiles(t, home, repo, write)
	write(".config/pfm/pfm.config.json", configRaw)
	remove("pfm.config.json")
	write(".local/share/pfm/install/source-repo", []byte(repo+"\n"))
	write(".zshrc", []byte("source "+filepath.Join(home, ".local", "share", "pfm", "install", "shim", "pfm.zsh")+"\n"))
	write(".local/state/pfm/shared.db", nil)
	if err := os.MkdirAll(filepath.Join(home, ".cc", ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "sid"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(
		filepath.Join(home, ".local", "share", "pfm", "install", "harness-prompts"),
		0o700,
	); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(home, ".local", "state", "pfm", "pfm.db")
	cache := filepath.Join(home, ".local", "state", "pfm", "pfm-cache.db")
	remove(".local/state/pfm/pfm.db")
	remove(".local/state/pfm/pfm-cache.db")
	hostLayoutLegacyDBs(
		t,
		repo,
		filepath.Join(home, ".cc", "fleet.db"),
		filepath.Join(home, ".local", "state", "pfm", "fleet.db"),
	)
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatalf("state target remains: %v", err)
	}
	if _, err := os.Stat(cache); !os.IsNotExist(err) {
		t.Fatalf("cache target remains: %v", err)
	}
}

func plantHostInstallerDrift(t *testing.T, home string) {
	t.Helper()
	asset := filepath.Join(home, ".local", "share", "pfm", "install", "reload.command.md")
	body, err := os.ReadFile(asset)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(asset, append(body, []byte("# drift\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "darwin" {
		// launchd has no enablement link: the plist is both unit and
		// enablement, so its drifted body is what install must rewrite.
		plist := filepath.Join(home, "Library", "LaunchAgents", "com.professor.pfm.name-sync.plist")
		body, err := os.ReadFile(plist)
		if err != nil {
			t.Fatal(err)
		}
		drifted := strings.Replace(string(body), "</plist>", "<!-- drift -->\n</plist>", 1)
		if drifted == string(body) {
			t.Fatalf("launch agent %s has no </plist> to drift", plist)
		}
		if err := os.WriteFile(plist, []byte(drifted), 0o644); err != nil {
			t.Fatal(err)
		}
	} else if err := os.Remove(
		filepath.Join(home, ".config", "systemd", "user", "default.target.wants", "pfm-name-sync.path"),
	); err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{".claude/commands", ".claude/agents", ".codex/agents"} {
		entries, err := os.ReadDir(filepath.Join(home, directory))
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) == 0 {
			t.Fatalf("no installed entry in %s", directory)
		}
		path := filepath.Join(home, directory, entries[0].Name())
		if entries[0].Type()&os.ModeSymlink != 0 {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := os.WriteFile(path, []byte("drift\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func plantLegacyAccountFiles(t *testing.T, home, repo string, write func(string, []byte)) {
	t.Helper()
	oldTemplate, err := os.ReadFile(filepath.Join(repo, "templates", "project", "scripts", "memory-wire.sh"))
	if err != nil {
		t.Fatal(err)
	}
	oldTemplate = bytes.Replace(oldTemplate, []byte("# memory-wire.sh —"), []byte("# cc-memory-wire.sh —"), 1)
	oldTemplate = bytes.Replace(oldTemplate, []byte("{MEMORY_VAULT_DIR}"), []byte("fixture-vault"), 1)
	write(".claude/scripts/cc-memory-wire.sh", oldTemplate)
	owned := home + "/private-ledger-hook"
	pfm := filepath.Join(home, ".local", "bin", "pfm")
	for _, account := range []string{"1", "2", "3"} {
		settings := filepath.Join(".cc", account, "settings.json")
		if account == "1" {
			settings = filepath.Join(".claude", "settings.json")
		}
		commands := []any{
			map[string]any{"type": "command", "command": pfm + " internal launcher-repair"},
			map[string]any{"type": "command", "command": "pfm internal clear-hide"},
			map[string]any{"type": "command", "command": "echo operator"},
		}
		if account == "1" {
			commands = append(
				commands,
				map[string]any{"type": "command", "command": "bash '$HOME/.claude/scripts/cc-memory-wire.sh'"},
			)
		}
		if account == "2" {
			commands = append(commands, map[string]any{"type": "command", "command": owned})
		}
		settingsRaw, err := json.Marshal(map[string]any{
			"operatorSetting": account,
			"hooks":           map[string]any{"SessionStart": []any{map[string]any{"hooks": commands}}},
			"statusLine":      map[string]any{"type": "command", "command": claudelaunch.StatusLineCommand(home)},
		})
		if err != nil {
			t.Fatal(err)
		}
		write(settings, settingsRaw)
	}
	settingsLedger, err := json.Marshal(map[string]any{
		"version": 1,
		"hooks": []any{map[string]any{
			"path":  filepath.Join(home, ".cc", "2", "settings.json"),
			"event": "SessionStart", "matcher": "", "command": owned, "count": 1,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	write(".local/share/pfm/install/settings-hook-ownership.json", settingsLedger)
	registrations := map[string]any{}
	harvesterShape := map[string]any{
		"type": "http",
		"url":  fmt.Sprintf("http://127.0.0.1:%d/mcp/harvester", hostLayoutMCPPort(t, home)),
	}
	for _, account := range []string{"1", "2", "3"} {
		registry := filepath.Join(".cc", account, ".claude.json")
		if account == "1" {
			registry = ".claude.json"
		}
		servers := map[string]any{
			"chat":      map[string]any{"command": "pfm", "args": []string{"mcp", "chat", "serve"}},
			"harvester": map[string]any{"command": "pfm", "args": []string{"mcp", "harvester", "serve"}},
			"operator":  map[string]any{"command": "operator-mcp"},
		}
		owned := map[string]any{"chat": servers["chat"], "harvester": servers["harvester"]}
		if account == "2" {
			// Shape-only entries an install older than the ledger wrote: no ledger record names them.
			servers["professor"] = map[string]any{
				"type":    "stdio",
				"command": pfm,
				"args":    []string{"mcp", "serve", "--stdio"},
			}
			servers["harvester"] = harvesterShape
			owned = map[string]any{"chat": servers["chat"]}
		}
		registryRaw, err := json.Marshal(map[string]any{"mcpServers": servers})
		if err != nil {
			t.Fatal(err)
		}
		write(registry, registryRaw)
		registrations[filepath.Join(home, registry)] = owned
	}
	homeMCP, err := json.Marshal(map[string]any{"mcpServers": map[string]any{
		"harvester": harvesterShape,
		"operator":  map[string]any{"command": "operator-mcp"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	write(".mcp.json", homeMCP)
	mcpLedger, err := json.Marshal(map[string]any{"registrations": registrations, "clients": []string{"harvester"}})
	if err != nil {
		t.Fatal(err)
	}
	write(".local/share/pfm/install/mcp-ownership.json", mcpLedger)
}

// hostLayoutMCPStripped proves the MCP plants left: the shape-only entries in
// account 2, the pfm entry in ~/.mcp.json and the ledger's clients list, while
// the foreign operator entries stay.
func hostLayoutMCPStripped(t *testing.T, home string) {
	t.Helper()
	ledger := filepath.Join(home, ".local", "share", "pfm", "install", "mcp-ownership.json")
	for path, want := range map[string]struct{ gone, kept []string }{
		filepath.Join(home, ".cc", "2", ".claude.json"): {
			gone: []string{`"professor"`, `"harvester"`, `"chat"`},
			kept: []string{`"operator"`},
		},
		filepath.Join(home, ".mcp.json"): {gone: []string{`"harvester"`}, kept: []string{`"operator"`}},
		ledger:                           {gone: []string{`"clients"`}},
	} {
		raw, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) && want.kept == nil {
			continue // the install's own MCP pass may retire an emptied ledger
		}
		if err != nil {
			t.Fatal(err)
		}
		for _, gone := range want.gone {
			if bytes.Contains(raw, []byte(gone)) {
				t.Errorf("%s still carries %s after apply: %s", path, gone, raw)
			}
		}
		for _, kept := range want.kept {
			if !bytes.Contains(raw, []byte(kept)) {
				t.Errorf("%s lost %s after apply: %s", path, kept, raw)
			}
		}
	}
}

// hostLayoutMCPPort is the loopback port the planted config gives pfm's MCP
// server: the one the layout's shape test reads.
func hostLayoutMCPPort(t *testing.T, home string) int {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(home, "pfm.config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		MCP struct {
			HTTP struct {
				Port int `json:"port"`
			} `json:"http"`
		} `json:"mcp"`
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	if config.MCP.HTTP.Port == 0 {
		return pfmconfig.DefaultMCPPort
	}
	return config.MCP.HTTP.Port
}

func hostLayoutLegacyDBs(t *testing.T, repo, state, cache string) {
	t.Helper()
	ctx := context.Background()
	for _, spec := range []struct {
		path     string
		sqlFiles []string
		extra    string
		version  int
	}{
		{state, []string{"pfm/internal/fleetdb/fleetdb.go"}, `CREATE TABLE swap_event(id INTEGER PRIMARY KEY, detail TEXT); INSERT INTO swap_event(detail) VALUES('legacy'); INSERT INTO hidden(uuid,hidden_at) VALUES('kept',77); INSERT INTO meta(key,val,updated_at) VALUES('fixture','kept',1);`, 1},
		{cache, []string{"pfm/internal/store/schema.sql", "pfm/internal/store/migration_v2.sql", "pfm/internal/store/migration_v3.sql", "pfm/internal/store/migration_v4.sql", "pfm/internal/store/migration_v5.sql", "pfm/internal/store/migration_v6.sql", "pfm/internal/store/migration_v7.sql", "pfm/internal/store/migration_v8.sql"}, `INSERT INTO hidden(id,engine,hidden_at) VALUES('cached','cc',88); INSERT INTO transcripts(uuid,path) VALUES('transcript-row','/fixture/transcript.jsonl'); INSERT INTO meta(key,value) VALUES('shared_hidden_adopted','0');`, 8},
	} {
		db, err := sqlitedb.OpenStore(ctx, spec.path)
		if err != nil {
			t.Fatal(err)
		}
		for _, relative := range spec.sqlFiles {
			body, err := os.ReadFile(filepath.Join(repo, relative))
			if err != nil {
				t.Fatal(err)
			}
			ddl := string(body)
			if strings.HasSuffix(relative, "fleetdb.go") {
				var found bool
				_, ddl, found = strings.Cut(ddl, "const schemaDDL = `")
				if !found {
					t.Fatal("fleetdb v1 schema not found")
				}
				ddl, _, found = strings.Cut(ddl, "`")
				if !found {
					t.Fatal("fleetdb v1 schema unterminated")
				}
			}
			if _, err := db.ExecContext(ctx, ddl); err != nil {
				t.Fatalf("apply %s: %v", relative, err)
			}
		}
		if _, err := db.ExecContext(ctx, spec.extra); err != nil {
			t.Fatalf("seed %s: %v", spec.path, err)
		}
		if _, err := db.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version=%d", spec.version)); err != nil {
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func hostLayoutSnapshot(t *testing.T, home string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(home, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == home {
			return nil
		}
		rel, err := filepath.Rel(home, path)
		if err != nil {
			return err
		}
		if rel == filepath.Join(".local", "state", "pfm", "migrations") {
			// The install journal itself: created by the apply, kept or
			// removed by the rollback — never part of the host it restores.
			return fs.SkipDir
		}
		if rel == filepath.Join(".config", "go") {
			return fs.SkipDir
		}
		if rel == "scheduler-calls" {
			return nil
		}
		if rel == "fixture-launchd" {
			// The launchctl fixture's loaded-job state: a rollback leaves the
			// jobs it stopped down and prints their bootstrap lines instead.
			return fs.SkipDir
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		mode := fmt.Sprintf(":%04o", info.Mode()&(os.ModePerm|os.ModeSetuid|os.ModeSetgid|os.ModeSticky))
		if entry.IsDir() {
			result[rel] = "dir" + mode
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			result[rel] = "link:" + target + mode
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(body)
		result[rel] = "sha256:" + hex.EncodeToString(sum[:]) + mode
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func hostLayoutSameSnapshot(t *testing.T, phase string, before, after map[string]string) {
	t.Helper()
	keys := map[string]bool{}
	for key := range before {
		keys[key] = true
	}
	for key := range after {
		keys[key] = true
	}
	ordered := make([]string, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)
	for _, key := range ordered {
		if before[key] != after[key] {
			t.Fatalf("%s changed %s: before=%q after=%q", phase, key, before[key], after[key])
		}
	}
}

func hostLayoutPayloads(t *testing.T, home, journal string) map[string]int {
	t.Helper()
	result := map[string]int{}
	for _, root := range []string{filepath.Join(home, ".claude"), filepath.Join(home, ".cc", "2"), filepath.Join(home, ".cc", "3"), filepath.Join(journal, "backup", "conflicts")} {
		if root == "backup/conflicts" {
			continue
		}
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if os.IsNotExist(walkErr) {
				return nil
			}
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(path, ".jsonl") {
				return nil
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(body)
			result[hex.EncodeToString(sum[:])]++
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return result
}

func hostLayoutTableCounts(t *testing.T, path string) map[string]int {
	t.Helper()
	db, err := sqlitedb.OpenReadOnly(path, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("close %s: %v", path, err)
		}
	}()
	rows, err := db.Query(
		"SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name",
	)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, name := range names {
		var count int
		if err := db.QueryRow("SELECT count(*) FROM \"" + name + "\"").Scan(&count); err != nil {
			t.Fatalf("%s.%s: %v", path, name, err)
		}
		counts[name] = count
	}
	return counts
}

func hostLayoutSameCounts(t *testing.T, label string, before, after map[string]int, dropped string) {
	t.Helper()
	if _, ok := before[dropped]; !ok {
		t.Fatalf("%s fixture lacks retired table %s", label, dropped)
	}
	if _, ok := after[dropped]; ok {
		t.Fatalf("%s.%s survived migration", label, dropped)
	}
	delete(before, dropped)
	for name, count := range before {
		if after[name] != count {
			t.Fatalf("%s.%s rows before=%d after=%d", label, name, count, after[name])
		}
	}
	for name := range after {
		if _, ok := before[name]; !ok && after[name] != 0 {
			t.Fatalf("%s.%s appeared with %d rows", label, name, after[name])
		}
	}
}

func hostLayoutStateHiddenIDs(t *testing.T, path string, want []string) {
	t.Helper()
	db, err := sqlitedb.OpenReadOnly(path, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("close %s: %v", path, err)
		}
	}()
	rows, err := db.Query("SELECT uuid FROM hidden ORDER BY uuid")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		got = append(got, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("state hidden IDs=%v, want %v", got, want)
	}
}

func hostLayoutJournal(t *testing.T, home, output string) string {
	t.Helper()
	for _, line := range strings.Split(output, "\n") {
		path, ok := strings.CutPrefix(line, "install journal: ")
		if !ok {
			continue
		}
		if !strings.HasPrefix(
			path,
			filepath.Join(home, ".local", "state", "pfm", "migrations")+string(os.PathSeparator),
		) {
			t.Fatalf("journal escaped home: %s", path)
		}
		var records []struct{ Row, Result string }
		body, err := os.ReadFile(filepath.Join(path, "journal.json"))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(body, &records); err != nil {
			t.Fatal(err)
		}
		if len(records) == 0 {
			t.Fatal("install journal has no records")
		}
		return path
	}
	t.Fatalf("apply omitted install journal:\n%s", output)
	return ""
}
