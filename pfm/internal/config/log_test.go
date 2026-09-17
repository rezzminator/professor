package config

import (
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
	if got.Log.KeepFiles != obs.DefaultKeepFiles || got.Log.MaxMB != obs.DefaultMaxMB {
		t.Fatalf("Log rotation = %+v, want the internal/obs defaults", got.Log)
	}
	if source := got.Source("log.level"); source != SourceDefault {
		t.Fatalf("unset log.level reports source %q, want default", source)
	}
}

func TestLogKeysFromFile(t *testing.T) {
	got, err := loadTmuxConfig(t, `, "log": {"level": "WARN", "keepFiles": 2, "maxMB": 16}`)
	if err != nil {
		t.Fatal(err)
	}
	if got.Log.Level != "warn" || got.Log.KeepFiles != 2 || got.Log.MaxMB != 16 {
		t.Fatalf("Log = %+v, want warn/2/16", got.Log)
	}
	for _, key := range []string{"log.level", "log.keepFiles", "log.maxMB"} {
		if source := got.Source(key); source != SourceFile {
			t.Fatalf("%s reports source %q, want file", key, source)
		}
	}
}

// A log nobody can size is a log nobody reads: every bad value is refused
// loudly, never clamped to a default the operator never sees.
func TestLogKeysRejectUnusableValues(t *testing.T) {
	for _, test := range []struct{ body, want string }{
		{`, "log": {"level": "chatty"}`, "log.level must be one of"},
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
