package installer

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadPluginLogEnv: every config dir gets a row, a dir named twice one; a
// settings.json linked from several dirs is one physical file, its value given
// to each; an absent file, env or key and an empty value are absent, while an
// unparsable file, an env that is not an object, a value that is not a
// string and a settings.json that is not a regular file (a FIFO would block)
// are errors, never absent.
func TestReadPluginLogEnv(t *testing.T) {
	home := t.TempDir()
	store := filepath.Join(home, ".claude")
	writeFixture(t, filepath.Join(store, "settings.json"), `{"env":{"PLUGIN_LOG_DIR":"/logs"}}`)
	linked := filepath.Join(home, "linked")
	if err := os.MkdirAll(linked, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(store, "settings.json"), filepath.Join(linked, "settings.json")); err != nil {
		t.Fatal(err)
	}
	dangling := filepath.Join(home, "dangling")
	if err := os.MkdirAll(dangling, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, "gone.json"), filepath.Join(dangling, "settings.json")); err != nil {
		t.Fatal(err)
	}
	device := filepath.Join(home, "device")
	if err := os.MkdirAll(device, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(os.DevNull, filepath.Join(device, "settings.json")); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"no-env":     `{"model":"x"}`,
		"null-env":   `{"env":null}`,
		"no-key":     `{"env":{"OTHER":"1"}}`,
		"empty":      `{"env":{"PLUGIN_LOG_DIR":""}}`,
		"invalid":    `{`,
		"env-list":   `{"env":["PLUGIN_LOG_DIR"]}`,
		"not-string": `{"env":{"PLUGIN_LOG_DIR":7}}`,
	}
	for name, content := range files {
		writeFixture(t, filepath.Join(home, name, "settings.json"), content)
	}
	dir := func(name string) string { return filepath.Join(home, name) }
	got := ReadPluginLogEnv([]string{
		store, linked, store + "/", dir("missing"), dangling, dir("no-env"), dir("null-env"), dir("no-key"),
		dir("empty"), dir("invalid"), dir("env-list"), dir("not-string"), device,
	})
	type want struct {
		dir, physical, value string
		absent               bool
		err                  string
	}
	settings := func(name string) string { return filepath.Join(dir(name), "settings.json") }
	realStore, err := filepath.EvalSymlinks(filepath.Join(store, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	physical := func(name string) string {
		path, err := filepath.EvalSymlinks(settings(name))
		if err != nil {
			t.Fatal(err)
		}
		return path
	}
	wants := []want{
		{dir: store, physical: realStore, value: "/logs"},
		{dir: linked, physical: realStore, value: "/logs"},
		{dir: dir("missing"), physical: settings("missing"), absent: true},
		{dir: dangling, physical: settings("dangling"), err: "resolve " + settings("dangling")},
		{dir: dir("no-env"), physical: physical("no-env"), absent: true},
		{dir: dir("null-env"), physical: physical("null-env"), absent: true},
		{dir: dir("no-key"), physical: physical("no-key"), absent: true},
		{dir: dir("empty"), physical: physical("empty"), absent: true},
		{dir: dir("invalid"), physical: physical("invalid"), err: "parse " + physical("invalid")},
		{dir: dir("env-list"), physical: physical("env-list"), err: physical("env-list") + ": env is not an object"},
		{
			dir: dir("not-string"), physical: physical("not-string"),
			err: physical("not-string") + ": env.PLUGIN_LOG_DIR is not a string",
		},
		{dir: device, physical: physical("device"), err: physical("device") + " is not a regular file"},
	}
	if len(got) != len(wants) {
		t.Fatalf("got %d rows, want %d: %+v", len(got), len(wants), got)
	}
	for index, expect := range wants {
		row := got[index]
		if row.Dir != expect.dir || row.Settings != filepath.Join(expect.dir, "settings.json") ||
			row.Physical != expect.physical || row.Value != expect.value || row.Absent != expect.absent {
			t.Errorf("row %d = %+v, want %+v", index, row, expect)
		}
		switch {
		case expect.err == "" && row.Err != nil:
			t.Errorf("row %d (%s) err = %v, want none", index, expect.dir, row.Err)
		case expect.err != "" && (row.Err == nil || !strings.HasPrefix(row.Err.Error(), expect.err)):
			t.Errorf("row %d (%s) err = %v, want one starting %q", index, expect.dir, row.Err, expect.err)
		}
	}
}

func TestAlphaInstallOwnsSharedPluginLogEnv(t *testing.T) {
	t.Run("preexisting shared log policy", func(t *testing.T) {
		home := t.TempDir()
		store := filepath.Join(home, ".claude")
		logDir := filepath.Join(home, ".local", "state", "pfm", "plugin-logs")
		writeFixture(
			t,
			filepath.Join(store, "settings.json"),
			`{"env":{"PLUGIN_LOG_DIR":"`+filepath.ToSlash(logDir)+`"}}`,
		)
		inst := &engine{
			options:     Options{Home: home, ConfigDir: store, Version: alphaVersion, Stdout: &bytes.Buffer{}},
			managedRoot: managedRootForHome(home), apply: true,
		}
		if err := inst.ensureClaudeEnv(false); err != nil {
			t.Fatal(err)
		}
		ledger, err := readClaudeEnvOwnership(inst.managedRoot)
		if err != nil || ledger.PluginLogDir != logDir || len(ledger.Files) != 0 {
			t.Fatalf("policy lost or user value claimed: %+v, %v", ledger, err)
		}
		if gaps, err := ClaudeEnvGaps(inst.managedRoot, inst.claudeEnvTarget(), nil); err != nil || len(gaps) != 0 {
			t.Fatalf("matching user-owned policy is stale: %v, %v", gaps, err)
		}
		writeFixture(t, filepath.Join(store, "settings.json"), `{"env":{"PLUGIN_LOG_DIR":"/changed-by-user"}}`)
		if gaps, err := ClaudeEnvGaps(inst.managedRoot, inst.claudeEnvTarget(), nil); err != nil || len(gaps) != 1 {
			t.Fatalf("user-owned policy drift was hidden: %v, %v", gaps, err)
		}
		if err := inst.ensureClaudeEnv(true); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(claudeEnvOwnershipPath(inst.managedRoot)); !os.IsNotExist(err) {
			t.Fatalf("uninstall retained policy-only ledger: %v", err)
		}
		if got := ReadPluginLogEnv([]string{store}); len(got) != 1 || got[0].Value != "/changed-by-user" {
			t.Fatalf("uninstall removed user-owned value: %+v", got)
		}
	})
	home := t.TempDir()
	store, first, second := filepath.Join(
		home,
		".claude",
	), filepath.Join(
		home,
		"account-one",
	), filepath.Join(
		home,
		"account-two",
	)
	writeFixture(t, filepath.Join(store, "settings.json"), `{"env":{"OTHER":"keep"}}`)
	if err := os.MkdirAll(first, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(store, "settings.json"), filepath.Join(first, "settings.json")); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	inst := &engine{
		options: Options{
			Home:             home,
			ConfigDir:        store,
			PrimaryConfigDir: first,
			RosterConfigDirs: []string{first, second},
			Version:          alphaVersion,
			Stdout:           &output,
		},
		managedRoot: managedRootForHome(home),
		apply:       true,
	}
	if err := inst.ensureClaudeEnv(false); err != nil {
		t.Fatal(err)
	}
	var shared string
	for _, setting := range ReadPluginLogEnv([]string{store, first, second}) {
		if setting.Err != nil || setting.Absent || !filepath.IsAbs(setting.Value) {
			t.Fatalf("alpha install left log env unset: %+v", setting)
		}
		if shared != "" && shared != setting.Value {
			t.Fatalf("unshared log roots %s, %s", shared, setting.Value)
		}
		shared = setting.Value
	}
	if info, err := os.Stat(shared); err != nil || !info.IsDir() {
		t.Fatalf("log folder unusable: %v, %v", info, err)
	}
	ledger, err := readClaudeEnvOwnership(inst.managedRoot)
	if err != nil || len(ledger.Files) != 2 {
		t.Fatalf("log ownership missing: %+v, %v", ledger, err)
	}
	if gaps, err := ClaudeEnvGaps(inst.managedRoot, inst.claudeEnvTarget(), nil); err != nil || len(gaps) != 0 {
		t.Fatalf("generated log env is spuriously stale: %v, %v", gaps, err)
	}
	output.Reset()
	if err := inst.ensureClaudeEnv(false); err != nil || strings.Contains(output.String(), "  change  ") {
		t.Fatalf("unchanged log env re-applied: %v, %s", err, output.String())
	}
	inst.options.Version = "0.82.0"
	if err := inst.ensureClaudeEnv(false); err != nil {
		t.Fatal(err)
	}
	for _, setting := range ReadPluginLogEnv([]string{store, first, second}) {
		if setting.Err != nil || !setting.Absent {
			t.Fatalf("release kept alpha log key: %+v", setting)
		}
	}
	inst.options.Version = alphaVersion
	if err := inst.ensureClaudeEnv(false); err != nil {
		t.Fatal(err)
	}
	inst.options.ClaudeEnv = map[string]string{PluginLogDirEnv: filepath.Join(home, "chosen-logs")}
	if gaps, err := ClaudeEnvGaps(
		inst.managedRoot,
		inst.claudeEnvTarget(),
		inst.options.ClaudeEnv,
	); err != nil ||
		len(gaps) != 2 {
		t.Fatalf("changed explicit log policy was hidden by the installed ledger: %v, %v", gaps, err)
	} else {
		for _, gap := range gaps {
			if !strings.Contains(gap, inst.options.ClaudeEnv[PluginLogDirEnv]) {
				t.Fatalf("gap does not name the current source policy: %s", gap)
			}
		}
	}
	if err := inst.ensureClaudeEnv(false); err != nil {
		t.Fatal(err)
	}
	for _, setting := range ReadPluginLogEnv([]string{store, first, second}) {
		if setting.Value != inst.options.ClaudeEnv[PluginLogDirEnv] {
			t.Fatalf("explicit log root ignored: %+v", setting)
		}
	}
	inst.options.ClaudeEnv[PluginLogDirEnv] = "relative-logs"
	if err := inst.ensureClaudeEnv(false); err == nil || !strings.Contains(err.Error(), "must be absolute") {
		t.Fatalf("invalid log root accepted: %v", err)
	}
	if err := inst.ensureClaudeEnv(true); err != nil {
		t.Fatal(err)
	}
	for _, setting := range ReadPluginLogEnv([]string{store, first, second}) {
		if setting.Err != nil || !setting.Absent {
			t.Fatalf("uninstall kept owned log key: %+v", setting)
		}
	}
	if got := readFixture(t, filepath.Join(store, "settings.json")); !strings.Contains(got, "keep") {
		t.Fatalf("unowned env lost: %s", got)
	}
}
