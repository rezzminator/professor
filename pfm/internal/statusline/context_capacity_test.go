package statusline

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/transcript"
)

func parityRuntime(t *testing.T) Runtime {
	t.Helper()
	root := t.TempDir()
	now := time.Unix(1800000000, 0)
	path := filepath.Join(root, "models.json")
	if err := os.WriteFile(
		path,
		[]byte(
			`{"models":[{"slug":"gpt-6.1-sol","context_window":500000},{"slug":"gpt-6-luna","context_window":272000}]}`,
		),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, now, now); err != nil {
		t.Fatal(err)
	}
	return Runtime{
		Home:     root,
		CacheDir: root,
		SIDDir:   root,
		ProcRoot: root,
		TmuxDir:  root,
		Now:      func() time.Time { return now },
		Engine:   pfmengine.Claude,
		Env:      map[string]string{gatewayModelsPathEnv: path, paths.EnvCostLedgerDir: root},
		Command:  quietRunner{},
	}
}

func TestParityMainCurrentContext(t *testing.T) {
	for _, tc := range []struct{ name, transcript, want string }{
		{"payload", "", "33% 165.0K"},
		{"empty transcript", "{}\n", "33% 165.0K"},
		{"latest transcript", `{"type":"assistant","message":{"model":"gpt-6.1-sol","usage":{"input_tokens":50000,"cache_read_input_tokens":50000}}}` + "\n", "20% 100.0K"},
		{"zero transcript", `{"type":"assistant","message":{"model":"gpt-6.1-sol","usage":{"input_tokens":0,"cache_read_input_tokens":0}}}` + "\n", "0% 0"},
		{"model switch streaming", `{"type":"assistant","message":{"model":"gpt-6-luna","usage":{"input_tokens":100000}}}` + "\n" + `{"type":"assistant","message":{"model":"gpt-6.1-sol"}}` + "\n", "33% 165.0K"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := parityRuntime(t)
			var d input
			if err := json.Unmarshal(
				[]byte(
					`{"model":{"id":"gpt-6.1-sol-fast[1m]"},"context_window":{"context_window_size":1000000,"used_percentage":16.5,"current_usage":{"input_tokens":165000}}}`,
				),
				&d,
			); err != nil {
				t.Fatal(err)
			}
			if tc.transcript != "" {
				d.TranscriptPath = filepath.Join(r.Home, "main.jsonl")
				if err := os.WriteFile(d.TranscriptPath, []byte(tc.transcript), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			_, line, _ := renderContextLine(r, d, "", r.now())
			if got := stripANSICodes(line); !strings.Contains(got, tc.want) {
				t.Fatalf("got %q; want adjacent %q", got, tc.want)
			}
		})
	}
}

func TestParityClaudePayloadPercentWithoutCapacity(t *testing.T) {
	r := parityRuntime(t)
	var d input
	if err := json.Unmarshal(
		[]byte(
			`{"model":{"id":"claude-opus-5"},"context_window":{"used_percentage":33,"current_usage":{"input_tokens":165000}}}`,
		),
		&d,
	); err != nil {
		t.Fatal(err)
	}
	_, line, _ := renderContextLine(r, d, "", r.now())
	if got := stripANSICodes(line); !strings.Contains(got, "33% 165.0K") {
		t.Fatalf("same-source payload percentage/count lost: %q", got)
	}
}

func TestParitySubagentCatalogOverridesHarnessGuess(t *testing.T) {
	r := parityRuntime(t)
	raw := []byte(
		`{"tasks":[{"id":"a","model":"gpt-6.1-sol-fast[1m]","tokenCount":70300,"contextWindowSize":1000000}]}`,
	)
	out, err := RenderSubagents(raw, r.now(), r.SIDDir, nil, r.Home, &paths.MapEnv{Values: r.Env}, "", os.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	var row subagentRow
	if err := json.Unmarshal([]byte(out), &row); err != nil {
		t.Fatal(err)
	}
	if got := stripANSICodes(row.Content); !strings.Contains(got, "14% 70.3K/500.0K") {
		t.Fatalf("wrong provider capacity: %s", got)
	}
}

func TestParityOpenAIMainCacheHasNoClaudeTTL(t *testing.T) {
	r := parityRuntime(t)
	var d input
	d.Model.ID = "gpt-6-luna"
	d.TranscriptPath = filepath.Join(r.Home, "main.jsonl")
	if err := os.WriteFile(d.TranscriptPath, []byte(strings.Join(gptAgentLines, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	_, line, _ := renderContextLine(r, d, "", r.now())
	if got := stripANSICodes(line); !strings.Contains(got, "💾∅ 87%") {
		t.Fatalf("completed OpenAI cache share missing: %q", got)
	}
}

func TestContextCatalogStatesAndLazyRead(t *testing.T) {
	for _, tc := range []struct {
		name, body, model, state string
		want                     int64
		age                      time.Duration
	}{
		{name: "fresh alias", body: `{"models":[{"slug":"gpt-6.1-sol","context_window":500000}]}`, model: "gpt-6.1-sol-fast[1m]", want: 500000},
		{name: "stale last good", body: `{"models":[{"slug":"gpt-6.1-sol","context_window":500000}]}`, model: "gpt-6.1-sol", state: "stale", want: 500000, age: 301 * time.Second},
		{name: "unknown model", body: `{"models":[{"slug":"other","context_window":500000}]}`, model: "gpt-6.1-sol", state: "unknown"},
		{name: "unknown capacity", body: `{"models":[{"slug":"gpt-6.1-sol","context_window":null}]}`, model: "gpt-6.1-sol", state: "unknown"},
		{name: "malformed", body: `{`, state: catalogError},
		{name: "trailing JSON", body: `{"models":[{"slug":"gpt-6.1-sol","context_window":500000}]}{}`, state: catalogError},
		{name: "trailing garbage", body: `{"models":[{"slug":"gpt-6.1-sol","context_window":500000}]}oops`, state: catalogError},
		{name: "invalid value", body: `{"models":[{"slug":"gpt-6.1-sol","context_window":-1}]}`, state: catalogError},
		{name: "missing", state: "missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := parityRuntime(t)
			path := r.Env[gatewayModelsPathEnv]
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if tc.body != "" {
				if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
					t.Fatal(err)
				}
				stamp := r.now().Add(-tc.age)
				if err := os.Chtimes(path, stamp, stamp); err != nil {
					t.Fatal(err)
				}
			}
			catalog := r.catalog()
			window, state := catalog.capacity(tc.model)
			if window != tc.want || state != tc.state {
				t.Fatalf("got %d/%s want %d/%s", window, state, tc.want, tc.state)
			}
			if tc.body != "" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			if again, againState := catalog.capacity(tc.model); again != window || againState != state {
				t.Fatal("catalog re-read within one render")
			}
		})
	}
}

func TestContextCatalogRejectsSpecialFileWithoutBlocking(t *testing.T) {
	r := parityRuntime(t)
	path := r.Env[gatewayModelsPathEnv]
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	result := make(chan string, 1)
	go func() {
		_, state := r.catalog().capacity("gpt-6.1-sol")
		result <- state
	}()
	select {
	case state := <-result:
		if state != catalogError {
			t.Fatalf("special file state = %q", state)
		}
	case <-time.After(time.Second):
		t.Fatal("catalog blocked opening a FIFO")
	}
}

func TestContextSourcesNativeWindowAndExplicitOverride(t *testing.T) {
	r := parityRuntime(t)
	r.Engine = pfmengine.Codex
	var d input
	d.Model.ID = "gpt-6.1-sol"
	meta := transcript.Meta{ContextWindow: 475000}
	if got := contextWindow(r, d, meta); got != 475000 {
		t.Fatalf("native window changed: %d", got)
	}
	r.Env["PFM_CONTEXT_WINDOW"] = "123456"
	if got := contextWindow(r, d, meta); got != 123456 {
		t.Fatalf("explicit override changed: %d", got)
	}
	for _, tc := range []struct{ requested, observed, want string }{
		{"gpt-6.1-sol-fast[1m]", "gpt-6.1-sol", "gpt-6.1-sol"},
		{"gpt-6.1-sol", "gpt-6-luna", "gpt-6.1-sol"},
		{"", "gpt-6.1-sol", "gpt-6.1-sol"},
	} {
		if got := currentModel(tc.requested, tc.observed); got != tc.want {
			t.Fatalf("current model: %s", got)
		}
	}
}
