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

func TestHerdrTabPriorityConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, content string
		source        Source
		want, invalid bool
	}{
		{name: "default", content: `{"version":2}`, source: SourceDefault},
		{name: "on", content: `{"version":2,"chat":{"herdrTabPriority":true}}`, source: SourceFile, want: true},
		{name: "off", content: `{"version":2,"chat":{"herdrTabPriority":false}}`, source: SourceFile},
		{name: "string", content: `{"version":2,"chat":{"herdrTabPriority":"true"}}`, invalid: true},
		{name: "number", content: `{"version":2,"chat":{"herdrTabPriority":1}}`, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), FileName)
			if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := Load(path, t.TempDir(), nil)
			if tc.invalid {
				if err == nil || !strings.Contains(err.Error(), "herdrTabPriority") {
					t.Fatalf("invalid priority error=%v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Chat.HerdrTabPriority != tc.want || got.Source("chat.herdrTabPriority") != tc.source {
				t.Fatalf(
					"priority=%t source=%s, want %t %s",
					got.Chat.HerdrTabPriority,
					got.Source("chat.herdrTabPriority"),
					tc.want,
					tc.source,
				)
			}
			body, err := Marshal(got, false)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, body, 0o600); err != nil {
				t.Fatal(err)
			}
			again, err := Load(path, t.TempDir(), nil)
			if err != nil || again.Chat.HerdrTabPriority != tc.want {
				t.Fatalf("roundtrip priority=%t error=%v", again.Chat.HerdrTabPriority, err)
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
