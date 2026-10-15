package installer

import (
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
