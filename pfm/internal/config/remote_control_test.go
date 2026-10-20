package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoteControlConfigInheritanceAndRoundTrip(t *testing.T) {
	for _, machineValue := range []string{"", `"remoteControl":true,`, `"remoteControl":false,`} {
		t.Run(machineValue, func(t *testing.T) {
			home := t.TempDir()
			path := filepath.Join(home, "config.json")
			content := fmt.Sprintf(`{"version":2,"claude":{%s"binary":"claude"},"accounts":[
				{"id":3,"configDir":"~/three","claude":{"remoteControl":false}},
				{"id":5,"configDir":"~/five","claude":{"binary":"claude-five"}},
				{"id":7,"configDir":"~/seven","claude":{"remoteControl":true}}
			]}`, machineValue)
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			loaded, err := Load(path, home, nil)
			if err != nil {
				t.Fatal(err)
			}
			wantMachine := machineValue != `"remoteControl":false,`
			for _, cfg := range []Config{loaded, roundTripRemoteConfig(t, loaded, path, home)} {
				if cfg.Claude.RemoteControl != wantMachine || cfg.EffectiveClaude(5).RemoteControl != wantMachine {
					t.Fatalf(
						"machine=%v inherited=%v, want %v",
						cfg.Claude.RemoteControl,
						cfg.EffectiveClaude(5).RemoteControl,
						wantMachine,
					)
				}
				if cfg.EffectiveClaude(3).RemoteControl || !cfg.EffectiveClaude(7).RemoteControl {
					t.Fatal("explicit account overrides lost")
				}
				if cfg.Source("accounts[0].claude.remoteControl") != SourceFile ||
					cfg.Source("accounts[2].claude.remoteControl") != SourceFile {
					t.Fatal("explicit account provenance lost")
				}
			}
			wantSource := SourceFile
			if machineValue == "" {
				wantSource = SourceDefault
			}
			if loaded.Source("claude.remoteControl") != wantSource {
				t.Fatal("machine provenance lost")
			}
		})
	}
}

func roundTripRemoteConfig(t *testing.T, cfg Config, path, home string) Config {
	t.Helper()
	encoded, err := Marshal(cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	reloaded, err := Load(path, home, nil)
	if err != nil {
		t.Fatal(err)
	}
	return reloaded
}

func TestRemoteControlConfigRejectsNonBoolean(t *testing.T) {
	home := t.TempDir()
	for _, raw := range []string{`{"version":2,"claude":{"remoteControl":"on"}}`, `{"version":2,"accounts":[{"id":1,"configDir":"~/one","claude":{"remoteControl":1}}]}`} {
		path := filepath.Join(home, "config.json")
		if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path, home, nil); err == nil || !strings.Contains(err.Error(), "remoteControl") {
			t.Fatalf("non-boolean remoteControl error=%v: %s", err, raw)
		}
	}
}
