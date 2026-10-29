package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The v0.82 top-level reader is frozen here; nested Claude compatibility has
// its separate historical-key assertion in claude_prefs_test.go.
func TestDefaultConfigDecodesWithPreviousTopLevelSchema(t *testing.T) {
	body, err := MarshalDefault(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var previous struct {
		Version  *int          `json:"version"`
		Theme    *string       `json:"theme"`
		State    *rawState     `json:"state"`
		Accounts *[]rawAccount `json:"accounts"`
		Claude   *rawClaude    `json:"claude"`
		Codex    *rawCodex     `json:"codex"`
		OpenCode *rawOpenCode  `json:"opencode"`
		Tmux     *rawTmux      `json:"tmux"`
		NameSync *rawNameSync  `json:"nameSync"`
		Doctor   *rawDoctor    `json:"doctor"`
		MCP      *rawMCP       `json:"mcp"`
		Log      *rawLog       `json:"log"`
		Ask      *rawAsk       `json:"ask"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&previous); err != nil {
		t.Fatalf("default file rejected by previous strict schema: %v", err)
	}
}

func TestChatSummaryIncludesResolvedTabStatus(t *testing.T) {
	for _, mode := range []string{"emoji", "text", "off"} {
		path := filepath.Join(t.TempDir(), FileName)
		if err := os.WriteFile(path, []byte(`{"version":2,"chat":{"herdrTabStatus":"`+mode+`"}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := Load(path, t.TempDir(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if want := "config chat.herdrTabStatus=" + mode + " (file)\n"; !strings.Contains(got.ChatSummary(), want) {
			t.Fatalf("summary=%q, want %q", got.ChatSummary(), want)
		}
	}
	got := Defaults(t.TempDir(), nil)
	if !strings.Contains(got.ChatSummary(), "config chat.herdrTabStatus=emoji (default)\n") {
		t.Fatalf("default tab status/source missing: %q", got.ChatSummary())
	}
}

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
			if roundtrip.Chat.Backend != tc.want || roundtrip.Source("chat.backend") != got.Source("chat.backend") {
				t.Fatalf("roundtrip chat.backend = %q, want %q", roundtrip.Chat.Backend, tc.want)
			}
		})
	}
}

func TestRetiredHerdrTabPriorityLoadsAndDrops(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(path, []byte(`{"version":2,"chat":{"herdrTabPriority":true}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path, t.TempDir(), nil)
	if err != nil {
		t.Fatalf("a config carrying the retired chat.herdrTabPriority must still load: %v", err)
	}
	body, err := Marshal(got, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "herdrTabPriority") {
		t.Fatalf("retired key written back:\n%s", body)
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
