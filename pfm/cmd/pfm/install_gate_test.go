package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestInstallYesRefusesLiveSessionMergeBeforeAnyChange(t *testing.T) {
	previous := runInstaller
	t.Cleanup(func() { runInstaller = previous })
	runInstaller = func(_ context.Context, options installer.Options) (installer.Report, error) {
		if options.Mode != installer.ModeDryRun {
			t.Fatal("applying installer ran after layout refusal")
		}
		return installer.Report{}, nil
	}
	home := t.TempDir()
	proc := filepath.Join(home, "proc")
	account := filepath.Join(home, ".cc", "2")
	for _, dir := range []string{proc, account, filepath.Join(home, ".claude"), filepath.Join(home, ".config", "pfm")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	config := pfmconfig.Defaults(home, nil, "")
	config.Path = filepath.Join(home, "pfm.config.json")
	config.Exists = true
	config.Accounts = append(config.Accounts, pfmconfig.Account{ID: 2, ConfigDir: account})
	legacy := filepath.Join(home, ".config", "pfm", "pfm.config.json")
	if err := os.WriteFile(legacy, []byte(`{"version":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	sessionStore := filepath.Join(account, "file-history")
	if err := os.MkdirAll(filepath.Join(sessionStore, "session"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessionStore, "session", "checkpoint"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(account, "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(account, "sessions", "123.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(proc, "123"), 0o700); err != nil {
		t.Fatal(err)
	}
	runtime := commandRuntime{Config: config, Paths: paths.Values{
		Home: home, ProcRoot: proc,
		ManagedSettingsDir: filepath.Join(home, "managed"), StateDB: filepath.Join(home, "state.db"),
		CacheDB: filepath.Join(home, "cache.db"),
	}}
	snapshot := func() map[string]string {
		t.Helper()
		state := map[string]string{}
		err := filepath.Walk(home, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			relative, err := filepath.Rel(home, path)
			if err != nil {
				return err
			}
			switch {
			case info.Mode().IsRegular():
				data, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				state[relative] = "file:" + string(data)
			case info.Mode()&os.ModeSymlink != 0:
				target, err := os.Readlink(path)
				if err != nil {
					return err
				}
				state[relative] = "link:" + target
			default:
				state[relative] = info.Mode().String()
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return state
	}
	before := snapshot()
	var stdout, stderr bytes.Buffer
	code := runInstall([]string{"--yes", "--skip-harvest"}, &stdout, &stderr, runtime)
	if code != 1 || !strings.HasPrefix(stderr.String(), "pfm install: refused before any change:\n") ||
		!strings.Contains(stderr.String(), "  refuse  layout session-store "+sessionStore+" — live chats: 123") ||
		strings.Contains(stdout.String(), "install journal:") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if got, err := os.ReadFile(legacy); err != nil || string(got) != `{"version":2}` {
		t.Fatalf("legacy config changed: %q %v", got, err)
	}
	if got, err := os.ReadFile(
		filepath.Join(sessionStore, "session", "checkpoint"),
	); err != nil ||
		string(got) != "keep" {
		t.Fatalf("account store changed: %q %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".local", "state", "pfm", "migrations")); !os.IsNotExist(err) {
		t.Fatalf("install created journal directory: %v", err)
	}
	if after := snapshot(); !reflect.DeepEqual(after, before) {
		t.Fatalf("refused install changed HOME: before=%v after=%v", before, after)
	}
}

// An identical config.json.pre-split beside the legacy config.json (a
// rollback restoring the pre-update config is one producer, issue #24 #7)
// must not abort the install with the "already exists" refusal.
func TestInstallApplyContinuesPastAnIdenticalPreSplitBackup(t *testing.T) {
	previous := runInstaller
	t.Cleanup(func() { runInstaller = previous })
	runInstaller = func(_ context.Context, _ installer.Options) (installer.Report, error) {
		return installer.Report{}, nil
	}
	home := t.TempDir()
	dir := filepath.Join(home, ".config", "pfm")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(dir, pfmconfig.LegacyFileName)
	content := `{"version":2,"theme":"tokyo-night","mcp":{"http":{"port":8377}}}`
	if err := os.WriteFile(legacy, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json.pre-split"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	// The jail's own config and clone would conflict with the legacy file:
	// the layout moves config.json to a free target in a clone of its own.
	t.Setenv(paths.EnvConfig, "")
	t.Setenv("PFM_SOURCE_REPO", t.TempDir())
	loaded, err := pfmconfig.Load("", home, nil)
	if err != nil {
		t.Fatal(err)
	}
	runtime := commandRuntime{Paths: paths.Values{Home: home}, Config: loaded}
	var stdout, stderr bytes.Buffer
	if code := runInstall([]string{"--yes", "--skip-harvest"}, &stdout, &stderr, runtime); code != 0 {
		t.Fatalf(
			"runInstall() code=%d stdout=%q stderr=%q, want 0 for an identical pre-split backup",
			code,
			stdout.String(),
			stderr.String(),
		)
	}
	if strings.Contains(stderr.String(), "apply config migration") {
		t.Fatalf("stderr=%q, want no apply config migration failure", stderr.String())
	}
}
