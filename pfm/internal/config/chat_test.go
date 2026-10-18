package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChatBackendConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, content, want string
		invalid             bool
	}{
		{name: "default", content: `{"version":2}`, want: "auto"},
		{name: "auto", content: `{"version":2,"chat":{"backend":"auto"}}`, want: "auto"},
		{name: "tmux", content: `{"version":2,"chat":{"backend":"tmux"}}`, want: "tmux"},
		{name: "herdr", content: `{"version":2,"chat":{"backend":"herdr"}}`, want: "herdr"},
		{name: "unknown", content: `{"version":2,"chat":{"backend":"other"}}`, invalid: true},
		{name: "empty", content: `{"version":2,"chat":{"backend":""}}`, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			path := filepath.Join(t.TempDir(), FileName)
			if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := Load(path, home, nil)
			if tc.invalid {
				if err == nil || !strings.Contains(err.Error(), "chat.backend") {
					t.Fatalf("Load() error = %v, want chat.backend rejection", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Chat.Backend != tc.want {
				t.Fatalf("chat.backend = %q, want %q", got.Chat.Backend, tc.want)
			}
			data, err := Marshal(got, false)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			roundtrip, err := Load(path, home, nil)
			if err != nil {
				t.Fatal(err)
			}
			if roundtrip.Chat.Backend != tc.want {
				t.Fatalf("roundtrip chat.backend = %q, want %q", roundtrip.Chat.Backend, tc.want)
			}
		})
	}
}

func TestHerdrTabStatusConfiguration(t *testing.T) {
	for _, mode := range []string{"emoji", "text", "off", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), FileName)
			if err := os.WriteFile(
				path,
				[]byte(`{"version":2,"chat":{"herdrTabStatus":"`+mode+`"}}`),
				0o600,
			); err != nil {
				t.Fatal(err)
			}
			got, err := Load(path, t.TempDir(), nil)
			if mode == "invalid" {
				if err == nil || !strings.Contains(err.Error(), "herdrTabStatus") {
					t.Fatalf("invalid mode error=%v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			data, err := Marshal(got, false)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), `"herdrTabStatus": "`+mode+`"`) {
				t.Fatalf("roundtrip lost mode: %s", data)
			}
		})
	}
}
