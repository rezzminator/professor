package config

import (
	"testing"
	"time"
)

func TestNameSyncIntervalDefaultsToFifteenMinutes(t *testing.T) {
	got, err := loadTmuxConfig(t, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.NameSync.Interval != 15*time.Minute {
		t.Fatalf("NameSync.Interval = %s, want 15m", got.NameSync.Interval)
	}
	if source := got.Source("nameSync.interval"); source != SourceDefault {
		t.Fatalf("unset nameSync.interval reports source %q, want default", source)
	}
}

func TestNameSyncIntervalFromFile(t *testing.T) {
	got, err := loadTmuxConfig(t, `, "nameSync": {"interval": "2m30s"}`)
	if err != nil {
		t.Fatal(err)
	}
	if got.NameSync.Interval != 150*time.Second {
		t.Fatalf("NameSync.Interval = %s, want 2m30s", got.NameSync.Interval)
	}
	if source := got.Source("nameSync.interval"); source != SourceFile {
		t.Fatalf("nameSync.interval reports source %q, want file", source)
	}
}
