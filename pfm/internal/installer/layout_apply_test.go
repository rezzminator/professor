package installer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"syscall"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
)

func TestLayoutPreviewIsReadOnlyAndNamesRows(t *testing.T) {
	env := layoutFixture(t)
	zshrc := filepath.Join(env.Home, ".zshrc")
	old := sourceLine(filepath.Join(env.ManagedRoot, "shim", "pfm.zsh")) + "\n"
	layoutWrite(t, zshrc, old)
	shared := filepath.Join(env.Home, ".local", "state", "pfm", "shared.db")
	layoutWrite(t, shared, "")
	var output bytes.Buffer
	dir, err := ApplyLayout(context.Background(), env, nil, false, &output)
	if err != nil || dir != "" {
		t.Fatalf("preview journal=%q err=%v", dir, err)
	}
	for _, line := range []string{
		"  change  layout zshrc repoint " + zshrc,
		"  change  layout shared-db remove " + shared,
	} {
		if !strings.Contains(output.String(), line) {
			t.Errorf("preview omitted %q: %s", line, output.String())
		}
	}
	if got, err := os.ReadFile(zshrc); err != nil || string(got) != old {
		t.Fatalf("preview changed zshrc=%q err=%v", got, err)
	}
	if _, err := os.Lstat(shared); err != nil {
		t.Fatalf("preview removed shared.db: %v", err)
	}
}

func TestLayoutApplyConfigMoveSeedAndNoMarker(t *testing.T) {
	for _, scenario := range []string{"move", "seed", "no marker"} {
		t.Run(scenario, func(t *testing.T) {
			env := layoutFixture(t)
			if err := os.Remove(env.ConfigPath); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "move":
				layoutWrite(t, filepath.Join(env.LegacyConfigDir, "pfm.config.json"), `{"version":2}`)
				layoutWrite(t, filepath.Join(env.LegacyConfigDir, "harvester.config.json"), `{}`)
			case "seed":
				layoutWrite(t, filepath.Join(env.Clone, "example.pfm.config.json"), `{"version":2}`)
			case "no marker":
				env.Clone = ""
			}
			var output bytes.Buffer
			dir, err := ApplyLayout(context.Background(), env, nil, true, &output)
			if scenario == "no marker" {
				// Without a clone pfm runs on its defaults: the row is named, never a failed install.
				refusal := "  refuse  layout config " + env.ConfigPath + " — no source repo recorded"
				if err != nil || !strings.Contains(output.String(), refusal) {
					t.Fatalf("no-marker seed: err=%v output=%q, want the refusal named", err, output.String())
				}
				if dir != "" {
					t.Fatalf("refused seed created journal %q", dir)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if dir == "" || strings.Contains(output.String(), "layout: nothing to do") {
				t.Fatalf("journal=%q output=%s", dir, output.String())
			}
			if got, err := os.ReadFile(env.ConfigPath); err != nil || string(got) != `{"version":2}` {
				t.Fatalf("config=%q err=%v", got, err)
			}
			if scenario == "move" {
				if _, err := os.Lstat(filepath.Join(env.LegacyConfigDir, "pfm.config.json")); !os.IsNotExist(err) {
					t.Fatalf("legacy config remains: %v", err)
				}
				if _, err := os.Stat(filepath.Join(env.Clone, "harvester.config.json")); err != nil {
					t.Fatalf("harvester did not move: %v", err)
				}
			}
		})
	}
}

// A first `pfm install` runs before any source-repo marker exists: the clone
// it runs from seeds the resolved config path (PFM_CONFIG here, outside the
// clone) from example.pfm.config.json, and an existing file is never touched.
// The preview classifies against the config the apply reloads.
func TestInstallLayoutSeedsResolvedConfigOnFirstInstall(t *testing.T) {
	for _, scenario := range []string{"absent", "existing", "legacy-move"} {
		t.Run(scenario, func(t *testing.T) {
			fixture := layoutFixture(t)
			if err := os.Remove(fixture.ConfigPath); err != nil {
				t.Fatal(err)
			}
			example := `{"version":2,"theme":"seeded"}`
			layoutWrite(t, filepath.Join(fixture.Clone, "example.pfm.config.json"), example)
			resolved := filepath.Join(fixture.Home, "override", pfmconfig.FileName)
			if scenario == "existing" {
				layoutWrite(t, resolved, `{"version":2,"theme":"mine"}`)
			}
			if scenario == "legacy-move" {
				layoutWrite(
					t,
					filepath.Join(fixture.LegacyConfigDir, pfmconfig.FileName),
					`{"version":2,"mcp":{"servers":{"chat":{"enabled":true}}}}`,
				)
			}
			config := fixture.Config
			config.Path = resolved
			runtime := pfmconfig.Runtime{
				Config: config,
				Paths: paths.Values{
					Home: fixture.Home, StateDB: fixture.StateDB, CacheDB: fixture.CacheDB,
					ManagedSettingsDir: fixture.ManagedDir, ProcRoot: fixture.ProcRoot,
				},
			}
			env, err := NewInstallLayoutEnv(runtime, &paths.MapEnv{}, fixture.Clone)
			if err != nil {
				t.Fatal(err)
			}
			env.runner = fixture.runner
			if env.ConfigPath != resolved {
				t.Fatalf("config=%q, want the resolved path %q", env.ConfigPath, resolved)
			}
			preview, err := env.InstallConfig(runtime, false)
			if err != nil {
				t.Fatalf("preview config: %v", err)
			}
			if scenario == "legacy-move" && !preview.MCPServers["chat"].Enabled {
				t.Fatalf("preview plans disabled chat MCP server: %+v", preview.MCPServers)
			}
			var output bytes.Buffer
			if _, err := ApplyLayout(context.Background(), env, nil, true, &output); err != nil {
				t.Fatalf("apply: %v\n%s", err, output.String())
			}
			applied, err := env.InstallConfig(runtime, true)
			if err != nil {
				t.Fatalf("apply config: %v", err)
			}
			if !reflect.DeepEqual(preview, applied) || applied.Path != resolved || !applied.Exists {
				t.Fatalf("preview and --yes classify different configs:\npreview=%#v\napply=%#v", preview, applied)
			}
			want := example
			switch scenario {
			case "existing":
				want = `{"version":2,"theme":"mine"}`
			case "legacy-move":
				want = `{"version":2,"mcp":{"servers":{"chat":{"enabled":true}}}}`
			}
			if got, err := os.ReadFile(resolved); err != nil || string(got) != want {
				t.Fatalf("config=%q err=%v want %q\n%s", got, err, want, output.String())
			}
			if _, err := os.Lstat(filepath.Join(fixture.Clone, pfmconfig.FileName)); !os.IsNotExist(err) {
				t.Fatalf("seed ignored the resolved path and wrote the clone: %v", err)
			}
		})
	}
}

func TestLayoutMovedConfigIsMigratedAndRollbackRestoresLegacyBytes(t *testing.T) {
	env := layoutFixture(t)
	if err := os.Remove(env.ConfigPath); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(env.LegacyConfigDir, "pfm.config.json")
	before := `{"version":2,"mcp":{"http":{"port":8377}}}`
	layoutWrite(t, legacy, before)
	var output bytes.Buffer
	dir, err := ApplyLayout(context.Background(), env, nil, true, &output)
	if err != nil || dir == "" {
		t.Fatalf("config migration journal=%q err=%v output=%s", dir, err, output.String())
	}
	got, err := os.ReadFile(env.ConfigPath)
	if err != nil || !strings.Contains(string(got), "18377") {
		t.Fatalf("moved config=%s err=%v", got, err)
	}
	if err := RollbackLayout(context.Background(), env, filepath.Base(dir), false, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(legacy)
	if err != nil || string(got) != before {
		t.Fatalf("legacy config restored=%s err=%v want=%s", got, err, before)
	}
	if _, err := os.Lstat(env.ConfigPath); !os.IsNotExist(err) {
		t.Fatalf("target config survived rollback: %v", err)
	}
}

func TestLayoutApplyManagedShellLeftoversAndIdempotence(t *testing.T) {
	env := layoutFixture(t)
	if err := os.Remove(filepath.Join(env.ManagedDir, "pfm.json")); err != nil {
		t.Fatal(err)
	}
	zshrc := filepath.Join(env.Home, ".zshrc")
	layoutWrite(t, zshrc, sourceLine(filepath.Join(env.ManagedRoot, "shim", "pfm.zsh"))+"\n")
	shared := filepath.Join(env.Home, ".local", "state", "pfm", "shared.db")
	layoutWrite(t, shared, "")
	staged := filepath.Join(env.ManagedRoot, "harness-prompts")
	if err := os.MkdirAll(staged, 0o700); err != nil {
		t.Fatal(err)
	}
	stray := filepath.Join(env.Home, ".cc", ".git")
	if err := os.Mkdir(stray, 0o700); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	dir, err := ApplyLayout(context.Background(), env, nil, true, &output)
	if err != nil || dir == "" {
		t.Fatalf("apply journal=%q err=%v output=%s", dir, err, output.String())
	}
	for _, finding := range ClassifyLayout(env) {
		if finding.Verdict != VerdictOK || finding.Err != nil {
			t.Errorf("remaining finding %+v", finding)
		}
	}
	var second bytes.Buffer
	again, err := ApplyLayout(context.Background(), env, nil, true, &second)
	if err != nil || again != "" || !strings.Contains(second.String(), "layout: nothing to do") {
		t.Fatalf("second journal=%q err=%v output=%s", again, err, second.String())
	}
	if err := RollbackLayout(context.Background(), env, filepath.Base(dir), false, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{shared, staged, stray} {
		if _, err := os.Lstat(path); err != nil {
			t.Errorf("rollback did not restore %s: %v", path, err)
		}
	}
}

func TestLayoutApplyDatabaseCountsAndRollback(t *testing.T) {
	env := layoutFixture(t)
	if err := os.Remove(env.StateDB); err != nil {
		t.Fatal(err)
	}
	legacy := paths.LegacyStateDB(env.Home)
	db, err := sqlitedb.OpenStore(context.Background(), legacy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`CREATE TABLE items (id INTEGER PRIMARY KEY); INSERT INTO items (id) VALUES (1), (2)`,
	); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	dir, err := ApplyLayout(context.Background(), env, nil, true, &output)
	if err != nil || dir == "" {
		t.Fatalf("db move journal=%q err=%v output=%s", dir, err, output.String())
	}
	counts, err := layoutDBCounts(context.Background(), env.StateDB)
	if err != nil || counts["items"] != 2 {
		t.Fatalf("moved counts=%v err=%v", counts, err)
	}
	if err := RollbackLayout(context.Background(), env, filepath.Base(dir), false, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	counts, err = layoutDBCounts(context.Background(), legacy)
	if err != nil || counts["items"] != 2 {
		t.Fatalf("restored counts=%v err=%v", counts, err)
	}
	if _, err := os.Stat(env.StateDB); !os.IsNotExist(err) {
		t.Fatalf("moved database survived rollback: %v", err)
	}
}

func TestLayoutRollbackRemovesDatabaseMigrationBackup(t *testing.T) {
	env := layoutFixture(t)
	if err := os.Remove(env.CacheDB); err != nil {
		t.Fatal(err)
	}
	legacy := paths.LegacyCacheDB(env.Home)
	db, err := sqlitedb.OpenStore(context.Background(), legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	dir, err := ApplyLayout(context.Background(), env, nil, true, &output)
	if err != nil || dir == "" {
		t.Fatalf("move cache journal=%q err=%v output=%s", dir, err, output.String())
	}
	backup := env.CacheDB + ".bak-before-v1"
	layoutWrite(t, backup, "created by cache migration")
	if err := RollbackLayout(context.Background(), env, filepath.Base(dir), false, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(backup); !os.IsNotExist(err) {
		t.Fatalf("cache migration backup survived rollback: %v", err)
	}
}

func TestLayoutRollbackRemovesNumberedDatabaseMigrationBackup(t *testing.T) {
	env := layoutFixture(t)
	if err := os.Remove(env.CacheDB); err != nil {
		t.Fatal(err)
	}
	legacy := paths.LegacyCacheDB(env.Home)
	db, err := sqlitedb.OpenStore(context.Background(), legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	base := env.CacheDB + ".bak-before-v1"
	layoutWrite(t, base, "operator's earlier backup")
	var output bytes.Buffer
	dir, err := ApplyLayout(context.Background(), env, nil, true, &output)
	if err != nil || dir == "" {
		t.Fatalf("move cache journal=%q err=%v output=%s", dir, err, output.String())
	}
	numbered := base + ".1"
	layoutWrite(t, numbered, "created by cache migration")
	if err := RollbackLayout(context.Background(), env, filepath.Base(dir), false, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(base); err != nil || string(got) != "operator's earlier backup" {
		t.Fatalf("preexisting backup=%q err=%v", got, err)
	}
	if _, err := os.Lstat(numbered); !os.IsNotExist(err) {
		t.Fatalf("numbered migration backup survived rollback: %v", err)
	}
}

func TestLayoutApplyStripsAccountFilesAndLedgers(t *testing.T) {
	env := layoutFixture(t)
	account := env.Config.Accounts[1].ConfigDir
	settings := filepath.Join(account, "settings.json")
	raw, owned := accountSettingsFixture(env.Home)
	layoutWrite(t, settings, string(raw))
	encoded, err := encodeSettingsHookOwnership(map[string]settingsHookCounts{physicalSettingsPath(settings): owned})
	if err != nil {
		t.Fatal(err)
	}
	settingsLedger := settingsHookOwnershipPath(env.ManagedRoot)
	layoutWrite(t, settingsLedger, string(encoded))
	registry := filepath.Join(account, ".claude.json")
	layoutWrite(t, registry, `{"mcpServers":{"chat":{"command":"pfm"},"operator":{"command":"own"}}}`)
	mcpLedger := filepath.Join(env.ManagedRoot, mcpOwnershipName)
	mcpRecord := mcpOwnership{
		Registrations: map[string]map[string]any{
			physicalSettingsPath(registry): {"chat": map[string]any{"command": "pfm"}},
		},
	}
	mcpRaw, err := json.Marshal(mcpRecord)
	if err != nil {
		t.Fatal(err)
	}
	layoutWrite(t, mcpLedger, string(mcpRaw))
	var output bytes.Buffer
	dir, err := ApplyLayout(context.Background(), env, nil, true, &output)
	if err != nil || dir == "" {
		t.Fatalf("strip journal=%q err=%v output=%s", dir, err, output.String())
	}
	updated, err := os.ReadFile(settings)
	if err != nil || strings.Contains(string(updated), "private-ledger-hook") ||
		!strings.Contains(string(updated), "operator-hook") {
		t.Fatalf("settings=%s err=%v", updated, err)
	}
	updated, err = os.ReadFile(registry)
	if err != nil || strings.Contains(string(updated), `"chat"`) || !strings.Contains(string(updated), `"operator"`) {
		t.Fatalf("registry=%s err=%v", updated, err)
	}
	settingsOwnership, _, err := readSettingsHookOwnership(settingsLedger)
	if err != nil || len(settingsOwnership[physicalSettingsPath(settings)]) != 0 {
		t.Fatalf("settings ledger still owns stripped entries: %v err=%v", settingsOwnership, err)
	}
	mcpOwnershipAfter, err := readMCPOwnership(mcpLedger)
	if err != nil || len(mcpOwnershipAfter.Registrations[physicalSettingsPath(registry)]) != 0 {
		t.Fatalf("MCP ledger still owns stripped entries: %v err=%v", mcpOwnershipAfter, err)
	}
	if err := RollbackLayout(context.Background(), env, filepath.Base(dir), false, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(settings); err != nil || !bytes.Equal(got, raw) {
		t.Fatalf("settings rollback=%s err=%v", got, err)
	}
	if got, err := os.ReadFile(mcpLedger); err != nil || !bytes.Equal(got, mcpRaw) {
		t.Fatalf("ledger rollback=%s err=%v", got, err)
	}
}

func TestLayoutLiveChatRefusesSessionMerge(t *testing.T) {
	env := layoutFixture(t)
	account := env.Config.Accounts[1].ConfigDir
	path := filepath.Join(account, "file-history")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	layoutWrite(t, filepath.Join(path, "session", "checkpoint"), "keep")
	layoutWrite(t, filepath.Join(account, "sessions", "4242.json"), `{}`)
	if err := os.Mkdir(filepath.Join(env.ProcRoot, "4242"), 0o700); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	dir, err := ApplyLayout(context.Background(), env, nil, true, &output)
	if err == nil ||
		!strings.Contains(
			err.Error(),
			"refused before any change:\n  refuse  layout session-store "+path+" — live chats: 4242",
		) ||
		dir != "" ||
		output.Len() != 0 {
		t.Fatalf("live guard journal=%q err=%v output=%s", dir, err, output.String())
	}
	if got, err := os.ReadFile(filepath.Join(path, "session", "checkpoint")); err != nil || string(got) != "keep" {
		t.Fatalf("guarded session changed: %q err=%v", got, err)
	}
}

func TestLayoutCrossFilesystemMoveChecksSpaceBeforeCopy(t *testing.T) {
	for _, available := range []uint64{2, 20} {
		t.Run(fmt.Sprint(available), func(t *testing.T) {
			env := layoutFixture(t)
			if err := os.Remove(env.ConfigPath); err != nil {
				t.Fatal(err)
			}
			legacy := filepath.Join(env.LegacyConfigDir, "pfm.config.json")
			layoutWrite(t, legacy, `{"version":2}`)
			before, err := os.Stat(legacy)
			if err != nil {
				t.Fatal(err)
			}
			probes := 0
			env.moveProbe = func(source, destination string) (bool, uint64, error) {
				if source != legacy || destination != env.ConfigPath {
					return false, 0, nil
				}
				probes++
				if _, err := os.Stat(legacy); err != nil {
					t.Fatalf("space probe ran after source removal: %v", err)
				}
				return true, available, nil
			}
			var output bytes.Buffer
			_, err = ApplyLayout(context.Background(), env, nil, true, &output)
			if probes == 0 {
				t.Fatal("device and free-space probe did not run")
			}
			if available == 2 {
				if err == nil || !strings.Contains(err.Error(), "insufficient free space: need 13 bytes, have 2") ||
					output.Len() != 0 {
					t.Fatalf("missing gate refusal: err=%v output=%s", err, output.String())
				}
				if _, err := os.Stat(legacy); err != nil {
					t.Fatalf("refused move removed source: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			after, err := os.Stat(env.ConfigPath)
			if err != nil {
				t.Fatal(err)
			}
			if before.Sys().(*syscall.Stat_t).Ino == after.Sys().(*syscall.Stat_t).Ino {
				t.Fatal("different-device move renamed source instead of copying")
			}
			if _, err := os.Stat(legacy); !os.IsNotExist(err) {
				t.Fatalf("source remains after copy: %v", err)
			}
		})
	}
}

func TestLayoutManagedSudoDeclinedWarnsAndContinues(t *testing.T) {
	env := layoutFixture(t)
	managed := filepath.Join(env.ManagedDir, "pfm.json")
	if err := os.Remove(managed); err != nil {
		t.Fatal(err)
	}
	env.writeManaged = func(path string, _ []byte) error {
		if path != managed {
			t.Fatalf("unexpected managed write %s", path)
		}
		return os.ErrPermission
	}
	runner := &layoutTestRunner{failSudo: true}
	env.runner = runner
	zshrc := filepath.Join(env.Home, ".zshrc")
	layoutWrite(t, zshrc, sourceLine(filepath.Join(env.ManagedRoot, "shim", "pfm.zsh"))+"\n")
	var output bytes.Buffer
	journal := NewJournal(context.Background(), env)
	_, err := ApplyLayout(context.Background(), env, journal, true, &output)
	if err != nil {
		t.Fatal(err)
	}
	want := "  warn    layout managed-cleanup " + managed +
		" — sudo -n needs cached credentials; run: sudo mkdir -p " + filepath.Dir(managed) +
		" && printf '%s\\n' '{\"cleanupPeriodDays\":36500}' | sudo tee " + managed + " >/dev/null"
	if !strings.Contains(output.String(), "sudo -n install -D -m 0644") ||
		!strings.Contains(
			output.String(),
			want,
		) || strings.Contains(output.String(), "refuse  layout managed-cleanup") {
		t.Fatalf("sudo warning missing: %s", output.String())
	}
	if !slices.ContainsFunc(runner.calls, layoutManagedSudoCall) {
		t.Fatalf("sudo command not injected: %v", runner.calls)
	}
	if finding := layoutFindingByPath(ClassifyLayout(env), "zshrc", zshrc); finding.Verdict != VerdictOK {
		t.Fatalf("later shell row did not apply: %+v", finding)
	}
	for _, record := range journal.records {
		if record.Row == layoutRowManagedCleanup {
			t.Fatalf("advisory row retained a journal record: %+v", record)
		}
	}
}

func TestLayoutManagedSudoWithCachedCredentialsApplies(t *testing.T) {
	env := layoutFixture(t)
	managed := filepath.Join(env.ManagedDir, "pfm.json")
	if err := os.Remove(managed); err != nil {
		t.Fatal(err)
	}
	env.writeManaged = func(string, []byte) error { return os.ErrPermission }
	runner := &layoutTestRunner{}
	runner.onRun = func(call string) {
		if !strings.HasPrefix(call, "sudo -n install -D -m 0644 ") {
			return
		}
		parts := strings.Fields(call)
		data, err := os.ReadFile(parts[len(parts)-2])
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(managed, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	env.runner = runner
	var output bytes.Buffer
	if _, err := ApplyLayout(context.Background(), env, nil, true, &output); err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(runner.calls, layoutManagedSudoCall) {
		t.Fatalf("runner calls=%v", runner.calls)
	}
	if finding := layoutFindingByPath(
		ClassifyLayout(env),
		layoutRowManagedCleanup,
		managed,
	); finding.Verdict != VerdictOK {
		t.Fatalf("managed row=%+v", finding)
	}
}

// layoutManagedSudoCall is the managed-cleanup drop-in's sudo install; the
// apply's scheduler stop runs before it.
func layoutManagedSudoCall(call string) bool {
	return strings.HasPrefix(call, "sudo -n install -D -m 0644 ")
}

type layoutTestRunner struct {
	calls     []string
	failSudo  bool
	failStop  bool
	failStart bool
	onRun     func(string)
	// unloaded units answer ActiveState inactive, and a stop or start naming
	// one fails as systemctl does for a unit that is not loaded (exit 5).
	unloaded []string
	// states is each unit's ActiveState (default active); a stop makes a unit
	// inactive and a start active, unless afterStart holds it in a state.
	states     map[string]string
	afterStart map[string]string
	// failProbe makes every ActiveState probe fail; noManager makes
	// show-environment fail.
	failProbe bool
	noManager bool
	// mainPIDs is each service's running PID, keyed by systemd unit or launchd
	// label (absent: not running); a stopped unit reads MainPID 0. garbagePID
	// makes every PID probe answer something that is not a PID.
	mainPIDs   map[string]int
	garbagePID bool
	// bootedOut are the launchd labels a bootout unloaded and no bootstrap
	// loaded again: `launchctl print` answers them exit 113, as launchd does.
	bootedOut map[string]bool
}

const layoutMainPIDProbe = "systemctl --user show --property=MainPID --value "

const layoutActiveStateProbe = "systemctl --user show --property=ActiveState --value "

func (runner *layoutTestRunner) Run(_ context.Context, name string, args ...string) error {
	runner.calls = append(runner.calls, name+" "+strings.Join(args, " "))
	if runner.onRun != nil {
		runner.onRun(runner.calls[len(runner.calls)-1])
	}
	if name == "sudo" && runner.failSudo {
		return os.ErrPermission
	}
	if name == "systemctl" && slices.Contains(args, "show-environment") && runner.noManager {
		return errors.New("Failed to connect to bus")
	}
	for _, unit := range runner.unloaded {
		if slices.Contains(args, unit) {
			return errors.New("exit status 5: unit " + unit + " not loaded")
		}
	}
	if last := runner.calls[len(runner.calls)-1]; runner.failStop &&
		(strings.Contains(last, " stop ") || strings.Contains(last, " bootout ")) {
		return os.ErrPermission
	}
	if strings.Contains(runner.calls[len(runner.calls)-1], " start ") && runner.failStart {
		return os.ErrPermission
	}
	if name == "systemctl" && len(args) > 1 && (args[1] == "stop" || args[1] == "start") {
		if runner.states == nil {
			runner.states = map[string]string{}
		}
		for _, unit := range args[2:] {
			runner.states[unit] = "inactive"
			if args[1] == "start" {
				runner.states[unit] = "active"
				if held, ok := runner.afterStart[unit]; ok {
					runner.states[unit] = held
				}
			}
		}
	}
	if name == "launchctl" && len(args) > 1 {
		label := strings.TrimSuffix(filepath.Base(args[len(args)-1]), ".plist")
		switch args[0] {
		case "bootout":
			if runner.bootedOut == nil {
				runner.bootedOut = map[string]bool{}
			}
			runner.bootedOut[label] = true
		case "bootstrap":
			delete(runner.bootedOut, label)
		case "print":
			if runner.bootedOut[label] {
				return launchdExit(launchctlNotLoaded)
			}
		}
	}
	return nil
}

func (runner *layoutTestRunner) Output(_ context.Context, name string, args ...string) ([]byte, error) {
	call := name + " " + strings.Join(args, " ")
	runner.calls = append(runner.calls, call)
	if label, ok := strings.CutPrefix(call, fmt.Sprintf("launchctl print gui/%d/", os.Getuid())); ok {
		if runner.garbagePID {
			return []byte("\tpid = not-a-pid\n"), nil
		}
		if pid, ok := runner.mainPIDs[label]; ok {
			return []byte(fmt.Sprintf("%s = {\n\tstate = running\n\tpid = %d\n}\n", label, pid)), nil
		}
		return []byte(label + " = {\n\tstate = not running\n}\n"), nil
	}
	if unit, ok := strings.CutPrefix(call, layoutMainPIDProbe); ok {
		if runner.garbagePID {
			return []byte("[not set]\n"), nil
		}
		if pid, ok := runner.mainPIDs[unit]; ok && runner.states[unit] != "inactive" {
			return []byte(fmt.Sprintf("%d\n", pid)), nil
		}
		return []byte("0\n"), nil
	}
	unit, ok := strings.CutPrefix(call, layoutActiveStateProbe)
	if !ok {
		return nil, errors.New("layoutTestRunner: no output for " + call)
	}
	if runner.failProbe {
		return nil, errors.New("exit status 1: Failed to connect to bus")
	}
	if state, ok := runner.states[unit]; ok {
		return []byte(state + "\n"), nil
	}
	// The oneshot name-sync job is idle unless states names it.
	if slices.Contains(runner.unloaded, unit) || unit == nameSyncServiceUnit {
		return []byte("inactive\n"), nil
	}
	return []byte("active\n"), nil
}

// layoutServiceLifecycle keeps the stop and start calls, dropping the manager
// check and the ActiveState probes.
func layoutServiceLifecycle(calls []string) []string {
	var lifecycle []string
	for _, call := range calls {
		if !strings.Contains(call, " show") {
			lifecycle = append(lifecycle, call)
		}
	}
	return lifecycle
}

func TestLayoutDatabaseServiceCommandFailureIsReported(t *testing.T) {
	for _, phase := range []string{"stop", "start"} {
		t.Run(phase, func(t *testing.T) {
			env := layoutFixture(t)
			if err := os.Remove(env.StateDB); err != nil {
				t.Fatal(err)
			}
			legacy := paths.LegacyStateDB(env.Home)
			db, err := sqlitedb.OpenStore(context.Background(), legacy)
			if err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			runner := &layoutTestRunner{failStop: phase == "stop", failStart: phase == "start"}
			env.runner = runner
			_, err = ApplyLayout(context.Background(), env, nil, true, &bytes.Buffer{})
			if err == nil || !strings.Contains(err.Error(), "systemctl --user "+phase) {
				t.Fatalf("%s failure was silent: %v", phase, err)
			}
			if phase == "stop" {
				if _, err := os.Stat(legacy); err != nil {
					t.Fatalf("database moved without stopped services: %v", err)
				}
			}
			if lifecycle := layoutServiceLifecycle(runner.calls); len(lifecycle) != 2 {
				t.Fatalf("services not restarted after %s failure: %v", phase, runner.calls)
			}
		})
	}
}

func TestLayoutDatabaseServicesRestartAfterOutcome(t *testing.T) {
	for _, outcome := range []string{"moved", "holder", "failure"} {
		t.Run(outcome, func(t *testing.T) {
			env := layoutFixture(t)
			if err := os.Remove(env.StateDB); err != nil {
				t.Fatal(err)
			}
			legacy := paths.LegacyStateDB(env.Home)
			if outcome == "failure" {
				layoutWrite(t, legacy, "invalid sqlite")
			} else {
				db, err := sqlitedb.OpenStore(context.Background(), legacy)
				if err != nil {
					t.Fatal(err)
				}
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if outcome == "holder" {
				fd := filepath.Join(env.ProcRoot, "4242", "fd")
				if err := os.MkdirAll(fd, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(legacy, filepath.Join(fd, "3")); err != nil {
					t.Fatal(err)
				}
			}
			runner := &layoutTestRunner{}
			if outcome == "holder" {
				// The MCP service holds it at the gate; the holder outlives the stop.
				runner.mainPIDs = map[string]int{mcpUnitName: 4242}
			}
			runner.onRun = func(call string) {
				if strings.Contains(call, " stop ") {
					if _, err := os.Stat(legacy); err != nil {
						t.Fatalf("services stopped after source moved: %v", err)
					}
				}
				if strings.Contains(call, " start ") && outcome == "moved" {
					if _, err := os.Stat(env.StateDB); err != nil {
						t.Fatalf("services restarted before target move: %v", err)
					}
				}
			}
			env.runner = runner
			var output bytes.Buffer
			_, err := ApplyLayout(context.Background(), env, nil, true, &output)
			if outcome == "failure" && err == nil {
				t.Fatal("invalid database did not fail")
			}
			if outcome == "holder" &&
				(err == nil || !strings.Contains(err.Error(), "held by pid 4242 with the pfm services stopped")) {
				t.Fatalf("holder refusal error=%v", err)
			}
			if outcome == "moved" && err != nil {
				t.Fatal(err)
			}
			lifecycle := layoutServiceLifecycle(runner.calls)
			if len(lifecycle) != 2 ||
				lifecycle[0] != "systemctl --user stop pfm-mcp.service pfm-name-sync.path pfm-name-sync.timer" ||
				lifecycle[1] != "systemctl --user start pfm-mcp.service" {
				t.Fatalf("service lifecycle %s: %v", outcome, runner.calls)
			}
		})
	}
}

func TestLayoutApplyStripsMCPByShapeAndHomeFile(t *testing.T) {
	env := layoutFixture(t)
	env.Config.MCP.HTTP.Port = layoutTestMCPPort
	registry := filepath.Join(env.Config.Accounts[1].ConfigDir, ".claude.json")
	home := filepath.Join(env.Home, ".mcp.json")
	ledger := filepath.Join(env.ManagedRoot, mcpOwnershipName)
	replace := layoutMCPReplacer(env, registry)
	registryRaw := replace.Replace(`{"counter":9007199254740993,"theme":"dark","mcpServers":{` +
		`"professor":{"type":"stdio","command":"{bin}","args":["mcp","serve","--stdio"]},` +
		`"operator":{"command":"own"}}}`)
	homeRaw := replace.Replace(`{"mcpServers":{` +
		`"harvester":{"type":"http","url":"http://127.0.0.1:{port}/mcp/harvester"},` +
		`"operator":{"command":"own"}}}`)
	ledgerRaw := `{"clients":["harvester"],"opencodeRegistrations":{"/srv/opencode.json":{"chat":{"enabled":true}}}}`
	layoutWrite(t, registry, registryRaw)
	layoutWrite(t, home, homeRaw)
	layoutWrite(t, ledger, ledgerRaw)
	var output bytes.Buffer
	dir, err := ApplyLayout(context.Background(), env, nil, true, &output)
	if err != nil || dir == "" {
		t.Fatalf("strip journal=%q err=%v output=%s", dir, err, output.String())
	}
	for _, want := range []string{
		"change  layout account-mcp strip " + registry,
		"change  layout home-mcp strip " + home,
	} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("output lacks %q: %s", want, output.String())
		}
	}
	updated, err := os.ReadFile(registry)
	if err != nil || strings.Contains(string(updated), `"professor"`) ||
		!strings.Contains(string(updated), `"operator"`) || !strings.Contains(string(updated), "9007199254740993") ||
		!strings.Contains(string(updated), `"theme"`) {
		t.Fatalf("registry=%s err=%v", updated, err)
	}
	updated, err = os.ReadFile(home)
	if err != nil || strings.Contains(string(updated), `"harvester"`) ||
		!strings.Contains(string(updated), `"operator"`) {
		t.Fatalf("home .mcp.json=%s err=%v", updated, err)
	}
	updated, err = os.ReadFile(ledger)
	if err != nil || strings.Contains(string(updated), `"clients"`) ||
		!strings.Contains(string(updated), "/srv/opencode.json") {
		t.Fatalf("ledger=%s err=%v", updated, err)
	}
	for _, finding := range ClassifyLayout(env) {
		if finding.Verdict != VerdictOK || finding.Err != nil {
			t.Errorf("remaining finding %+v", finding)
		}
	}
	var second bytes.Buffer
	again, err := ApplyLayout(context.Background(), env, nil, true, &second)
	if err != nil || again != "" || !strings.Contains(second.String(), "layout: nothing to do") {
		t.Fatalf("second journal=%q err=%v output=%s", again, err, second.String())
	}
	if err := RollbackLayout(context.Background(), env, filepath.Base(dir), false, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{registry: registryRaw, home: homeRaw, ledger: ledgerRaw} {
		if got, err := os.ReadFile(path); err != nil || string(got) != want {
			t.Errorf("rollback %s=%s err=%v, want %s", path, got, err, want)
		}
	}
}

func TestLayoutApplyHomeMCPClientsOnlyAndMalformed(t *testing.T) {
	env := layoutFixture(t)
	home := filepath.Join(env.Home, ".mcp.json")
	ledger := filepath.Join(env.ManagedRoot, mcpOwnershipName)
	layoutWrite(t, ledger, `{"clients":["chat"]}`)
	var output bytes.Buffer
	if dir, err := ApplyLayout(context.Background(), env, nil, true, &output); err != nil || dir == "" {
		t.Fatalf("clients journal=%q err=%v output=%s", dir, err, output.String())
	}
	if _, err := os.Lstat(home); !os.IsNotExist(err) {
		t.Fatalf("absent home .mcp.json was created: %v", err)
	}
	if got, err := os.ReadFile(ledger); err != nil || strings.Contains(string(got), "clients") {
		t.Fatalf("ledger=%s err=%v", got, err)
	}
	layoutWrite(t, ledger, `{"clients":["chat"]}`)
	layoutWrite(t, home, `{bad`)
	output.Reset()
	if dir, err := ApplyLayout(context.Background(), env, nil, true, &output); err == nil ||
		!strings.Contains(
			err.Error(),
			"  refuse  layout home-mcp "+home+" — UNREADABLE ",
		) || dir != "" || output.Len() != 0 {
		t.Fatalf("malformed home .mcp.json gate: dir=%q err=%v output=%s", dir, err, output.String())
	}
	for path, want := range map[string]string{home: `{bad`, ledger: `{"clients":["chat"]}`} {
		if got, err := os.ReadFile(path); err != nil || string(got) != want {
			t.Errorf("malformed row changed %s=%s err=%v", path, got, err)
		}
	}
}

// Ruling 32: a strip of a shared settings.json writes through the links to the
// target, journals the target, and rolls the target's bytes back.
func TestLayoutApplyStripsSharedSettingsThroughTheLink(t *testing.T) {
	env, shared, links, raw := sharedSettingsFixture(t, filepath.Join("dotfiles", "claude-settings.json"))
	var output bytes.Buffer
	dir, err := ApplyLayout(context.Background(), env, nil, true, &output)
	if err != nil || dir == "" {
		t.Fatalf("strip journal=%q err=%v output=%s", dir, err, output.String())
	}
	updated, err := os.ReadFile(shared)
	if err != nil || strings.Contains(string(updated), "private-ledger-hook") ||
		!strings.Contains(string(updated), "operator-hook") {
		t.Fatalf("shared settings=%s err=%v", updated, err)
	}
	for _, link := range links {
		if target, err := os.Readlink(link); err != nil || target != shared {
			t.Fatalf("%s is no longer a link to %s: target=%q err=%v", link, shared, target, err)
		}
		requireLayoutVerdict(t, ClassifyLayout(env), "account-settings", link, VerdictOK)
	}
	if err := RollbackLayout(context.Background(), env, filepath.Base(dir), false, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(shared); err != nil || !bytes.Equal(got, raw) {
		t.Fatalf("shared settings rollback=%s err=%v", got, err)
	}
	if target, err := os.Readlink(links[0]); err != nil || target != shared {
		t.Fatalf("rollback replaced the link: target=%q err=%v", target, err)
	}
}

// A host that never loaded one pfm unit (MCP off, or an install older than
// the name-sync units) still moves its database: only the running units stop,
// and exactly those start again.
func TestLayoutDatabaseMoveStopsOnlyRunningServices(t *testing.T) {
	env := layoutFixture(t)
	if err := os.Remove(env.StateDB); err != nil {
		t.Fatal(err)
	}
	legacy := paths.LegacyStateDB(env.Home)
	db, err := sqlitedb.OpenStore(context.Background(), legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	runner := &layoutTestRunner{unloaded: []string{"pfm-mcp.service"}}
	env.runner = runner
	var output bytes.Buffer
	if _, err := ApplyLayout(context.Background(), env, nil, true, &output); err != nil {
		t.Fatalf("apply with an unloaded unit: %v output=%s calls=%v", err, output.String(), runner.calls)
	}
	if _, err := os.Stat(env.StateDB); err != nil {
		t.Fatalf("database not moved: %v output=%s", err, output.String())
	}
	var lifecycle []string
	for _, call := range runner.calls {
		if strings.Contains(call, " stop ") || strings.Contains(call, " start ") {
			lifecycle = append(lifecycle, call)
		}
	}
	// The unloaded MCP unit is neither stopped nor started; the scheduler
	// units start after installer.Run (Journal.RestartSchedulerUnits).
	want := []string{"systemctl --user stop pfm-name-sync.path pfm-name-sync.timer"}
	if !slices.Equal(lifecycle, want) {
		t.Fatalf("service lifecycle = %q, want %q", lifecycle, want)
	}
}
