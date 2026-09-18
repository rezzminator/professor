package config

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hostops/pfm/internal/obs"
)

func TestLogDefaultsToTheBuildLevelAndTheShippedRotation(t *testing.T) {
	got, err := loadTmuxConfig(t, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Log.Level != "" {
		t.Fatalf("Log.Level = %q, want empty — an unset level means the build decides", got.Log.Level)
	}
	if got.Log.KeepFiles != obs.DefaultKeepFiles || got.Log.MaxMB != obs.DefaultMaxMB ||
		got.Log.KeepDays != obs.DefaultKeepDays {
		t.Fatalf("Log rotation = %+v, want the internal/obs defaults", got.Log)
	}
	if len(got.Log.Components) != 0 {
		t.Fatalf("Log.Components = %v, want none — every component takes the global", got.Log.Components)
	}
	if source := got.Source("log.level"); source != SourceDefault {
		t.Fatalf("unset log.level reports source %q, want default", source)
	}
}

func TestLogKeysFromFile(t *testing.T) {
	got, err := loadTmuxConfig(t, `, "log": {"level": "WARNING", "keepFiles": 2, "maxMB": 16, "keepDays": 0,
		"components": {"mcp": "debug", "db": "off", "tmux": "Warning"}}`)
	if err != nil {
		t.Fatal(err)
	}
	if got.Log.Level != "warn" || got.Log.KeepFiles != 2 || got.Log.MaxMB != 16 || got.Log.KeepDays != 0 {
		t.Fatalf("Log = %+v, want warn/2/16/0 — warning is stored as its canonical warn", got.Log)
	}
	components := got.Log.Components
	if components["mcp"] != "debug" || components["db"] != "off" || components["tmux"] != "warn" {
		t.Fatalf("Log.Components = %v, want mcp=debug db=off tmux=warn", got.Log.Components)
	}
	for _, key := range []string{"log.level", "log.keepFiles", "log.maxMB", "log.keepDays", "log.components"} {
		if source := got.Source(key); source != SourceFile {
			t.Fatalf("%s reports source %q, want file", key, source)
		}
	}
}

// A log nobody can size is a log nobody reads: every bad value is refused
// loudly, never clamped to a default the operator never sees.
func TestLogKeysRejectUnusableValues(t *testing.T) {
	for _, test := range []struct{ body, want string }{
		{`, "log": {"level": "chatty"}`, "log.level must be one of debug, info, warn, warning, error, off"},
		{
			`, "log": {"components": {"mcp": "loud"}}`,
			"log.components.mcp must be one of debug, info, warn, warning, error, off",
		},
		{`, "log": {"components": {"database": "off"}}`, `log.components: "database" is not one of cli, mcp, http.in`},
		{`, "log": {"keepDays": -1}`, "log.keepDays must be 0..3650"},
		{`, "log": {"keepDays": 4000}`, "log.keepDays must be 0..3650"},
		{`, "log": {"keepFiles": 0}`, "log.keepFiles must be 1..64"},
		{`, "log": {"keepFiles": 65}`, "log.keepFiles must be 1..64"},
		{`, "log": {"maxMB": 0}`, "log.maxMB must be 1..1024"},
		{`, "log": {"maxMB": 4096}`, "log.maxMB must be 1..1024"},
		{`, "log": {"keep_files": 2}`, "unknown field"},
	} {
		if _, err := loadTmuxConfig(t, test.body); err == nil {
			t.Fatalf("%s was accepted", test.body)
		} else if !strings.Contains(err.Error(), test.want) {
			t.Fatalf("%s reported %v, want a message containing %q", test.body, err, test.want)
		}
	}
}

// TestLogLevelsIsTheOneList: config accepts exactly what internal/obs parses.
func TestLogLevelsIsTheOneList(t *testing.T) {
	if strings.Join(LogLevels, ",") != strings.Join(obs.LevelNames, ",") {
		t.Fatalf("LogLevels = %v, want obs.LevelNames %v", LogLevels, obs.LevelNames)
	}
	got, err := loadTmuxConfig(t, `, "log": {"level": "off"}`)
	if err != nil {
		t.Fatal(err)
	}
	if got.Log.Level != "off" {
		t.Fatalf("Log.Level = %q, want off", got.Log.Level)
	}
}

// TestInstallLogBlockIsTheSpecDefault pins what `pfm install` writes and what a
// fresh `pfm config init` pins: {"level": "info", "keepDays": 30}, one source.
func TestInstallLogBlockIsTheSpecDefault(t *testing.T) {
	encoded, err := json.Marshal(InstallLogBlock())
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"keepDays":30,"level":"info"}` {
		t.Fatalf("InstallLogBlock = %s", encoded)
	}
	if InstallLogLevel != "info" {
		t.Fatalf("InstallLogLevel = %q", InstallLogLevel)
	}
	block := MarshalLog(Log{
		Level: InstallLogLevel, KeepFiles: 5, MaxMB: 8, KeepDays: 30, Components: map[string]string{"db": "off"},
	})
	encoded, err = json.Marshal(block)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"components":{"db":"off"},"keepDays":30,"keepFiles":5,"level":"info","maxMB":8}` {
		t.Fatalf("MarshalLog = %s", encoded)
	}
	// An unset level is "the build decides": Marshal leaves the key out
	// rather than writing an empty string the loader would read as the same.
	encoded, err = json.Marshal(MarshalLog(Log{KeepFiles: 5, MaxMB: 8, KeepDays: 30}))
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"keepDays":30,"keepFiles":5,"maxMB":8}` {
		t.Fatalf("MarshalLog(unset level) = %s", encoded)
	}
}

// TestLogDefaultInsertion is the installer's planner: a file without a log key
// gets the install block; a file with one — whatever it says — is left alone;
// an absent file is fs.ErrNotExist (config init creates it); an unreadable or
// damaged one is an error.
func TestLogDefaultInsertion(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "pfm.config.json")
	if err := os.WriteFile(path, []byte(`{"version": 2, "theme": "dark"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	content, changed, err := LogDefaultInsertion(path)
	if err != nil || !changed {
		t.Fatalf("insertion = changed %t err %v, want a change", changed, err)
	}
	if !strings.Contains(string(content), `"level": "info"`) || !strings.Contains(string(content), `"keepDays": 30`) ||
		!strings.Contains(string(content), `"theme": "dark"`) {
		t.Fatalf("content = %s", content)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, filepath.Join(directory, "home"), nil); err != nil {
		t.Fatalf("the strict loader refused the inserted block: %v", err)
	}
	if _, changed, err := LogDefaultInsertion(path); err != nil || changed {
		t.Fatalf("a file with a log key was changed=%t err=%v", changed, err)
	}

	off := filepath.Join(directory, "off.json")
	if err := os.WriteFile(off, []byte(`{"version": 2, "log": {"level": "off"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, changed, err := LogDefaultInsertion(off); err != nil || changed {
		t.Fatalf("an existing log key was overwritten: changed=%t err=%v", changed, err)
	}
	_, changed, err = LogDefaultInsertion(filepath.Join(directory, "absent.json"))
	if !errors.Is(err, fs.ErrNotExist) || changed {
		t.Fatalf("an absent file: changed=%t err=%v, want fs.ErrNotExist (config init creates it)", changed, err)
	}
	if _, _, err := LogDefaultInsertion(directory); err == nil {
		t.Fatal("an unreadable path was not an error")
	}
	broken := filepath.Join(directory, "broken.json")
	if err := os.WriteFile(broken, []byte(`{not json`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LogDefaultInsertion(broken); err == nil {
		t.Fatal("a damaged file was not an error")
	}
}
