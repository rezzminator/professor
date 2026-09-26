// Executor: codex-subagent/qa
package installer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestApplyMigratesOwnedMemoryHelpersAndTheirExactHooks(t *testing.T) {
	home := t.TempDir()
	canonical := filepath.Join(home, ".claude")
	account := filepath.Join(home, ".cc", "2")
	configDirs := []string{canonical, account}

	type installedHelper struct {
		configDir string
		oldName   string
		newName   string
		mode      os.FileMode
		content   []byte
	}
	helpers := []installedHelper{
		{
			canonical,
			"cc-memory-wire.sh",
			"memory-wire.sh",
			0o751,
			oldMemoryHelperFixture(t, "memory-wire.sh", "invented-vault"),
		},
		{
			canonical,
			"cc-memory-consolidate.sh",
			"memory-consolidate.sh",
			0o705,
			oldMemoryHelperFixture(t, "memory-consolidate.sh", "invented-vault"),
		},
		{
			account,
			"cc-memory-wire.sh",
			"memory-wire.sh",
			0o710,
			oldMemoryHelperFixture(t, "memory-wire.sh", "other-invented-vault"),
		},
		{
			account,
			"cc-memory-consolidate.sh",
			"memory-consolidate.sh",
			0o740,
			oldMemoryHelperFixture(t, "memory-consolidate.sh", "other-invented-vault"),
		},
	}
	for _, helper := range helpers {
		oldPath := filepath.Join(helper.configDir, "scripts", helper.oldName)
		writeMemoryHelperFixture(t, oldPath, helper.content, helper.mode)
	}
	// An already-copied byte-and-mode-identical destination is accepted and the
	// old source is still retired; this is the interrupted prior-apply boundary.
	existing := helpers[len(helpers)-1]
	writeMemoryHelperFixture(
		t,
		filepath.Join(existing.configDir, "scripts", existing.newName),
		existing.content,
		existing.mode,
	)

	canonicalOld := filepath.Join(canonical, "scripts", "cc-memory-wire.sh")
	canonicalNew := filepath.Join(canonical, "scripts", "memory-wire.sh")
	accountOld := filepath.Join(account, "scripts", "cc-memory-wire.sh")
	accountNew := filepath.Join(account, "scripts", "memory-wire.sh")
	settingsPath := filepath.Join(canonical, "settings.json")
	localSettingsPath := filepath.Join(account, "settings.local.json")
	settingsOriginal := memorySettingsFixture(
		`bash "`+canonicalOld+`"`,
		`sh '$HOME/.claude/scripts/cc-memory-wire.sh'`,
		`bash /opt/operator/cc-memory-wire.sh`,
	)
	localOriginal := memorySettingsFixture(accountOld)
	writeFixture(t, settingsPath, settingsOriginal)
	writeFixture(t, localSettingsPath, localOriginal)

	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if _, err := Run(context.Background(), Options{
		Mode:       ModeApply,
		Home:       home,
		ConfigDirs: configDirs,
		Runner:     &fakeRunner{},
		Now:        func() time.Time { return now },
	}); err != nil {
		t.Fatalf("apply migration: %v", err)
	}

	for _, helper := range helpers {
		oldPath := filepath.Join(helper.configDir, "scripts", helper.oldName)
		newPath := filepath.Join(helper.configDir, "scripts", helper.newName)
		if _, err := os.Lstat(oldPath); !os.IsNotExist(err) {
			t.Errorf("retired helper remains at %s: %v", oldPath, err)
		}
		content, err := os.ReadFile(newPath)
		if err != nil {
			t.Errorf("read migrated helper %s: %v", newPath, err)
			continue
		}
		if !bytes.Equal(content, helper.content) {
			t.Errorf("migrated helper %s changed bytes", newPath)
		}
		info, err := os.Stat(newPath)
		if err != nil {
			t.Errorf("stat migrated helper %s: %v", newPath, err)
		} else if info.Mode().Perm() != helper.mode.Perm() {
			t.Errorf("migrated helper %s mode = %o, want %o", newPath, info.Mode().Perm(), helper.mode.Perm())
		}
	}

	settings := readFixture(t, settingsPath)
	settingsCommands := memoryHookCommands(t, []byte(settings))
	for _, want := range []string{
		`bash "` + canonicalNew + `"`,
		`sh '$HOME/.claude/scripts/memory-wire.sh'`,
		`bash /opt/operator/cc-memory-wire.sh`,
	} {
		if !containsString(settingsCommands, want) {
			t.Errorf("settings.json omitted command %q after migration:\n%s", want, settings)
		}
	}
	for _, command := range settingsCommands {
		if strings.Contains(command, canonicalOld) ||
			strings.Contains(command, "$HOME/.claude/scripts/cc-memory-wire.sh") {
			t.Errorf("settings.json retained owned old path in %q:\n%s", command, settings)
		}
	}
	localSettings := readFixture(t, localSettingsPath)
	localCommands := memoryHookCommands(t, []byte(localSettings))
	if !containsString(localCommands, accountNew) || containsString(localCommands, accountOld) {
		t.Errorf("settings.local.json hook was not migrated exactly:\n%s", localSettings)
	}
	for path, original := range map[string]string{settingsPath: settingsOriginal, localSettingsPath: localOriginal} {
		backup := path + ".pre-professor-20260102-030405"
		if got := readFixture(t, backup); got != original {
			t.Errorf("backup for %s changed bytes", path)
		}
	}
}

func TestMemoryHelperMigrationAbsentDoesNotOptIn(t *testing.T) {
	home := t.TempDir()
	if _, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: home, ConfigDirs: []string{filepath.Join(home, ".claude")}, Runner: &fakeRunner{},
	}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"cc-memory-wire.sh", "memory-wire.sh", "cc-memory-consolidate.sh", "memory-consolidate.sh"} {
		path := filepath.Join(home, ".claude", "scripts", name)
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Errorf("ordinary install opted into optional memory helper %s: %v", path, err)
		}
	}
}

func TestMemoryHelperMigrationDryRunIsReadOnly(t *testing.T) {
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	oldPath := filepath.Join(config, "scripts", "cc-memory-wire.sh")
	writeMemoryHelperFixture(t, oldPath, oldMemoryHelperFixture(t, "memory-wire.sh", "dry-run-vault"), 0o751)
	writeFixture(t, filepath.Join(config, "settings.json"), memorySettingsFixture(oldPath))
	before := snapshotMemoryMigrationTree(t, home)

	report, err := Run(context.Background(), Options{
		Mode: ModeDryRun, Home: home, ConfigDirs: []string{config}, Runner: &fakeRunner{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Changed == 0 {
		t.Fatal("dry run did not report the planned migration")
	}
	if after := snapshotMemoryMigrationTree(t, home); !reflect.DeepEqual(after, before) {
		t.Fatalf("dry run mutated the fixture tree:\nbefore=%#v\nafter=%#v", before, after)
	}
}

func TestMemoryHelperMigrationConflictsRefuseBeforeAnyMutation(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*testing.T, string, string, string)
		wantErr string
	}{
		{
			name: "custom old script",
			prepare: func(t *testing.T, _, oldConsolidate, _ string) {
				content, err := os.ReadFile(oldConsolidate)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(
					oldConsolidate,
					append(content, []byte("# operator customization\n")...),
					0o700,
				); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: "content differs from the canonical Professor helper",
		},
		{
			name: "old script symlink",
			prepare: func(t *testing.T, home, oldConsolidate, _ string) {
				target := filepath.Join(home, "operator-consolidate.sh")
				writeMemoryHelperFixture(
					t,
					target,
					oldMemoryHelperFixture(t, "memory-consolidate.sh", "conflict-vault"),
					0o700,
				)
				if err := os.Remove(oldConsolidate); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, oldConsolidate); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: "expected a regular file",
		},
		{
			name: "destination content conflict",
			prepare: func(t *testing.T, _, _, newConsolidate string) {
				writeMemoryHelperFixture(t, newConsolidate, []byte("operator destination\n"), 0o700)
			},
			wantErr: "content conflicts with owned source",
		},
		{
			name: "destination symlink",
			prepare: func(t *testing.T, home, _, newConsolidate string) {
				target := filepath.Join(home, "operator-destination.sh")
				writeMemoryHelperFixture(t, target, []byte("operator destination\n"), 0o700)
				if err := os.Symlink(target, newConsolidate); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: "expected a regular file",
		},
		{
			name: "destination mode conflict",
			prepare: func(t *testing.T, _, _, newConsolidate string) {
				writeMemoryHelperFixture(
					t,
					newConsolidate,
					oldMemoryHelperFixture(t, "memory-consolidate.sh", "conflict-vault"),
					0o600,
				)
			},
			wantErr: "mode 600 conflicts with owned source",
		},
		{
			name: "compound old path hook",
			prepare: func(t *testing.T, home, _, _ string) {
				oldWire := filepath.Join(home, ".claude", "scripts", "cc-memory-wire.sh")
				writeFixture(
					t,
					filepath.Join(home, ".claude", "settings.local.json"),
					memorySettingsFixture(oldWire+" && echo done"),
				)
			},
			wantErr: "requires manual migration",
		},
		{
			name: "argument-bearing old path hook",
			prepare: func(t *testing.T, home, _, _ string) {
				oldWire := filepath.Join(home, ".claude", "scripts", "cc-memory-wire.sh")
				writeFixture(
					t,
					filepath.Join(home, ".claude", "settings.local.json"),
					memorySettingsFixture("bash "+oldWire+" --operator-flag"),
				)
			},
			wantErr: "requires manual migration",
		},
		{
			name: "braced HOME old path hook",
			prepare: func(t *testing.T, home, _, _ string) {
				writeFixture(
					t,
					filepath.Join(home, ".claude", "settings.local.json"),
					memorySettingsFixture("bash ${HOME}/.claude/scripts/cc-memory-wire.sh --operator-flag"),
				)
			},
			wantErr: "requires manual migration",
		},
		{
			name: "tilde old path hook",
			prepare: func(t *testing.T, home, _, _ string) {
				writeFixture(
					t,
					filepath.Join(home, ".claude", "settings.local.json"),
					memorySettingsFixture("~/.claude/scripts/cc-memory-wire.sh | cat"),
				)
			},
			wantErr: "requires manual migration",
		},
		{
			name: "split quoted old path hook",
			prepare: func(t *testing.T, home, _, _ string) {
				writeFixture(
					t,
					filepath.Join(home, ".claude", "settings.local.json"),
					memorySettingsFixture(`bash $HOME/.claude/scripts/'cc-memory'-wire.sh --operator-flag`),
				)
			},
			wantErr: "requires manual migration",
		},
		{
			name: "non-command old path hook",
			prepare: func(t *testing.T, home, _, _ string) {
				oldWire := filepath.Join(home, ".claude", "scripts", "cc-memory-wire.sh")
				document := memorySettingsDocument(oldWire)
				events := document["hooks"].(map[string]any)
				entries := events["SessionStart"].([]any)
				hooks := entries[0].(map[string]any)["hooks"].([]any)
				hooks[0].(map[string]any)["type"] = "prompt"
				content, err := json.Marshal(document)
				if err != nil {
					t.Fatal(err)
				}
				writeFixture(t, filepath.Join(home, ".claude", "settings.local.json"), string(content))
			},
			wantErr: "requires manual migration",
		},
		{
			name: "settings symlink",
			prepare: func(t *testing.T, home, _, _ string) {
				target := filepath.Join(home, "operator-settings.json")
				writeFixture(t, target, memorySettingsFixture("echo operator"))
				if err := os.Symlink(target, filepath.Join(home, ".claude", "settings.local.json")); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: "non-regular settings file",
		},
		{
			name: "malformed settings shape",
			prepare: func(t *testing.T, home, _, _ string) {
				writeFixture(t, filepath.Join(home, ".claude", "settings.local.json"), `{"hooks":[]}`)
			},
			wantErr: "settings hooks must be an object",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			config := filepath.Join(home, ".claude")
			oldWire := filepath.Join(config, "scripts", "cc-memory-wire.sh")
			oldConsolidate := filepath.Join(config, "scripts", "cc-memory-consolidate.sh")
			newConsolidate := filepath.Join(config, "scripts", "memory-consolidate.sh")
			writeMemoryHelperFixture(t, oldWire, oldMemoryHelperFixture(t, "memory-wire.sh", "conflict-vault"), 0o751)
			writeMemoryHelperFixture(
				t,
				oldConsolidate,
				oldMemoryHelperFixture(t, "memory-consolidate.sh", "conflict-vault"),
				0o705,
			)
			writeFixture(t, filepath.Join(config, "settings.json"), memorySettingsFixture(oldWire))
			test.prepare(t, home, oldConsolidate, newConsolidate)
			before := snapshotMemoryMigrationTree(t, home)

			_, err := Run(context.Background(), Options{
				Mode: ModeApply, Home: home, ConfigDirs: []string{config}, Runner: &fakeRunner{},
			})
			if err == nil || !strings.Contains(err.Error(), "preflight apply plan") ||
				!strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("apply error = %v, want preflight refusal containing %q", err, test.wantErr)
			}
			if after := snapshotMemoryMigrationTree(t, home); !reflect.DeepEqual(after, before) {
				t.Fatalf("preflight refusal mutated files:\nbefore=%#v\nafter=%#v", before, after)
			}
		})
	}
}

func TestRewriteMemoryHelperHookPathsAcceptsOnlyExactStandaloneForms(t *testing.T) {
	home := filepath.Join(t.TempDir(), "operator's-home")
	oldPath := filepath.Join(home, ".claude", "scripts", "cc-memory-wire.sh")
	newPath := filepath.Join(home, ".claude", "scripts", "memory-wire.sh")
	var commands, wanted []string
	for _, shell := range []string{"", "sh ", "bash "} {
		for _, quote := range []string{"", `"`, `'`} {
			commands = append(commands, shell+quote+oldPath+quote)
			wanted = append(wanted, shell+quote+newPath+quote)
			commands = append(commands, shell+quote+`$HOME/.claude/scripts/cc-memory-wire.sh`+quote)
			wanted = append(wanted, shell+quote+`$HOME/.claude/scripts/memory-wire.sh`+quote)
		}
	}
	foreign := "bash /opt/operator/cc-memory-wire.sh"
	commands = append(commands, foreign)
	wanted = append(wanted, foreign)
	document := memorySettingsDocument(commands...)
	document["metadata"] = map[string]any{"command": oldPath}
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}

	updated, changed, err := rewriteMemoryHelperHookPaths(raw, map[string]string{oldPath: newPath}, home)
	if err != nil || !changed {
		t.Fatalf("rewrite changed=%v err=%v", changed, err)
	}
	got := memoryHookCommands(t, updated)
	sort.Strings(got)
	sort.Strings(wanted)
	if !reflect.DeepEqual(got, wanted) {
		t.Fatalf("rewritten hook commands = %#v, want %#v", got, wanted)
	}
	var decoded map[string]any
	if err := json.Unmarshal(updated, &decoded); err != nil {
		t.Fatal(err)
	}
	metadata := decoded["metadata"].(map[string]any)
	if metadata["command"] != oldPath {
		t.Fatalf("non-hook command field changed to %q", metadata["command"])
	}
	for _, literal := range []string{
		`bash "` + newPath + `"`,
		`bash '$HOME/.claude/scripts/memory-wire.sh'`,
	} {
		if !containsString(got, literal) {
			t.Errorf("quoted command %q was expanded or lost: %#v", literal, got)
		}
	}
}

func TestRewriteMemoryHelperHookPathsRejectsMalformedSettingsShapes(t *testing.T) {
	home := t.TempDir()
	oldPath := filepath.Join(home, ".claude", "scripts", "cc-memory-wire.sh")
	paths := map[string]string{oldPath: filepath.Join(home, ".claude", "scripts", "memory-wire.sh")}
	tests := []struct {
		name    string
		raw     string
		wantErr string
	}{
		{"null document", `null`, "settings must be an object"},
		{"hooks array", `{"hooks":[]}`, "settings hooks must be an object"},
		{"hooks null", `{"hooks":null}`, "settings hooks must be an object"},
		{"event object", `{"hooks":{"SessionStart":{}}}`, "settings hook event must be an array"},
		{"entry scalar", `{"hooks":{"SessionStart":["bad"]}}`, "settings hook entry must be an object"},
		{
			"entry hooks object",
			`{"hooks":{"SessionStart":[{"hooks":{}}]}}`,
			"settings hook entry hooks must be an array",
		},
		{"hook scalar", `{"hooks":{"SessionStart":[{"hooks":["bad"]}]}}`, "settings hook must be an object"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := rewriteMemoryHelperHookPaths([]byte(test.raw), paths, home)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("error = %v, want %q", err, test.wantErr)
			}
		})
	}
}

func TestMemoryHelperConfigAliasMigratesOnceAndRewritesEveryLexicalPath(t *testing.T) {
	home := t.TempDir()
	canonical := filepath.Join(home, ".claude")
	alias := filepath.Join(home, ".cc", "current")
	if err := os.MkdirAll(filepath.Dir(alias), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(canonical, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(canonical, alias); err != nil {
		t.Fatal(err)
	}
	oldCanonical := filepath.Join(canonical, "scripts", "cc-memory-wire.sh")
	oldAlias := filepath.Join(alias, "scripts", "cc-memory-wire.sh")
	newCanonical := filepath.Join(canonical, "scripts", "memory-wire.sh")
	newAlias := filepath.Join(alias, "scripts", "memory-wire.sh")
	content := oldMemoryHelperFixture(t, "memory-wire.sh", "alias-vault")
	writeMemoryHelperFixture(t, oldCanonical, content, 0o751)
	settingsPath := filepath.Join(canonical, "settings.json")
	writeFixture(t, settingsPath, memorySettingsFixture(oldAlias))
	installer := &engine{
		options: Options{Home: home, ConfigDirs: []string{alias}, Stdout: io.Discard},
		apply:   true, stamp: "fixture",
	}

	if err := installer.migrateMemoryHelpers(); err != nil {
		t.Fatalf("migrate aliases: %v", err)
	}
	if _, err := os.Lstat(oldCanonical); !os.IsNotExist(err) {
		t.Fatalf("canonical old helper remains: %v", err)
	}
	if got, err := os.ReadFile(newCanonical); err != nil || !bytes.Equal(got, content) {
		t.Fatalf("canonical destination content mismatch: err=%v", err)
	}
	if got, err := os.ReadFile(newAlias); err != nil || !bytes.Equal(got, content) {
		t.Fatalf("alias destination content mismatch: err=%v", err)
	}
	settings := readFixture(t, settingsPath)
	if !strings.Contains(settings, newAlias) || strings.Contains(settings, oldAlias) {
		t.Fatalf("lexical alias hook was not preserved through rename:\n%s", settings)
	}
}

func TestNormalizedMemoryHelperFingerprintPinsHistoricalTemplatesAndRejectsShellSyntax(t *testing.T) {
	for _, test := range []struct {
		name     string
		newName  string
		wantHash string
	}{
		{"wire", "memory-wire.sh", "ed3d1028ec8299e84d42c2fcd2e9797e64b6213116631cf1d9534df27c36c0a6"},
		{"consolidate", "memory-consolidate.sh", "d7d1672964f35cb2cd5a41a1ed38d5d3f5a2f1a37a4ba530d9e75bc48e6538a7"},
	} {
		t.Run(test.name, func(t *testing.T) {
			content := oldMemoryHelperFixture(t, test.newName, "historical-invented-vault")
			got, err := normalizedMemoryHelperFingerprint(content)
			if err != nil || got != test.wantHash {
				t.Fatalf("fingerprint = %q, %v, want %q", got, err, test.wantHash)
			}
		})
	}

	template := oldMemoryHelperFixture(t, "memory-wire.sh", "{MEMORY_VAULT_DIR}")
	for _, value := range []string{"", "vault`command`", `vault\\escape`, `$HOME`, `"quoted"`, `{nested}`} {
		t.Run(fmt.Sprintf("reject_%q", value), func(t *testing.T) {
			content := bytes.Replace(template, []byte("{MEMORY_VAULT_DIR}"), []byte(value), 1)
			if _, err := normalizedMemoryHelperFingerprint(content); err == nil {
				t.Fatalf("accepted unsafe REPO substitution %q", value)
			}
		})
	}
}

func oldMemoryHelperFixture(t *testing.T, newName, vault string) []byte {
	t.Helper()
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test file")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(testFile), "..", "..", ".."))
	content, err := os.ReadFile(filepath.Join(repoRoot, "templates", "project", "scripts", newName))
	if err != nil {
		t.Fatal(err)
	}
	switch newName {
	case "memory-wire.sh":
		content = bytes.Replace(content, []byte("# memory-wire.sh —"), []byte("# cc-memory-wire.sh —"), 1)
	case "memory-consolidate.sh":
		content = bytes.Replace(content, []byte("# memory-consolidate.sh —"), []byte("# cc-memory-consolidate.sh —"), 1)
		content = bytes.Replace(
			content,
			[]byte("SessionStart hook (memory-wire.sh)"),
			[]byte("SessionStart hook (cc-memory-wire.sh)"),
			1,
		)
	default:
		t.Fatalf("unknown memory helper template %q", newName)
	}
	if count := bytes.Count(content, []byte("{MEMORY_VAULT_DIR}")); count != 1 {
		t.Fatalf("template %s placeholder count = %d, want 1", newName, count)
	}
	return bytes.Replace(content, []byte("{MEMORY_VAULT_DIR}"), []byte(vault), 1)
}

func writeMemoryHelperFixture(t *testing.T, path string, content []byte, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, mode); err != nil {
		t.Fatal(err)
	}
}

func memorySettingsFixture(commands ...string) string {
	document := memorySettingsDocument(commands...)
	document["metadata"] = map[string]any{"command": "leave non-hook command fields alone"}
	content, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		panic(err)
	}
	return string(content) + "\n"
}

func memorySettingsDocument(commands ...string) map[string]any {
	hooks := make([]any, 0, len(commands))
	for _, command := range commands {
		hooks = append(hooks, map[string]any{"type": "command", "command": command})
	}
	return map[string]any{
		"hooks": map[string]any{
			"SessionStart": []any{map[string]any{"matcher": "", "hooks": hooks}},
		},
	}
}

func memoryHookCommands(t *testing.T, raw []byte) []string {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	var commands []string
	events := document["hooks"].(map[string]any)
	for _, event := range events {
		for _, entryValue := range event.([]any) {
			entry := entryValue.(map[string]any)
			for _, hookValue := range entry["hooks"].([]any) {
				hook := hookValue.(map[string]any)
				if command, ok := hook["command"].(string); ok {
					commands = append(commands, command)
				}
			}
		}
	}
	return commands
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func snapshotMemoryMigrationTree(t *testing.T, root string) map[string]string {
	t.Helper()
	snapshot := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, _ os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			snapshot[relative] = fmt.Sprintf("symlink:%o:%s", info.Mode().Perm(), target)
		case info.IsDir():
			snapshot[relative] = fmt.Sprintf("dir:%o", info.Mode().Perm())
		default:
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			snapshot[relative] = fmt.Sprintf("file:%o:%x", info.Mode().Perm(), content)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}
