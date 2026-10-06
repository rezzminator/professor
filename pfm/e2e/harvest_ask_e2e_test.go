//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestHarvestAskE2E(t *testing.T) {
	t.Parallel()
	requireE2EFence(t)
	repo := t.TempDir()
	headBinary := os.Getenv(e2eScriptBinaryEnv)
	if headBinary == "" {
		t.Fatalf("%s: e2e binary was not built", e2eScriptBinaryEnv)
	}
	harness := &e2eHarness{
		t:          t,
		repo:       repo,
		headBinary: headBinary,
		goCache:    requiredGoEnv(t, "GOCACHE"),
		goModCache: requiredGoEnv(t, "GOMODCACHE"),
	}
	home := harness.newHome(harness.headBinary)
	source := filepath.Join(home, "evidence.txt")
	if err := os.WriteFile(source, []byte("full cached evidence reaches the adapter\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	engines := map[string]struct {
		homeVariable string
		answer       string
		reply        []string
		wantUsage    string
		wantArgs     []string
	}{
		"claude": {
			homeVariable: "CLAUDE_CONFIG_DIR",
			answer:       "claude-e2e-answer",
			reply: []string{
				`{"result":"claude-e2e-answer","is_error":false,"usage":{"input_tokens":100,"cache_read_input_tokens":7,"cache_creation_input_tokens":3,"output_tokens":20}}`,
			},
			wantUsage: "pfm harvest ask: usage input=100 cached_input=7 cache_creation=3 output=20",
			wantArgs: []string{
				"-p\n",
				"--model\nclaude-e2e-model\n",
				"--effort\nhigh\n",
				"--output-format\njson\n",
			},
		},
		"codex": {
			homeVariable: "CODEX_HOME",
			answer:       "codex-e2e-answer",
			reply: []string{
				`{"type":"thread.started"}`,
				`{"type":"turn.started"}`,
				`{"type":"item.completed","item":{"type":"agent_message","text":"codex-e2e-answer"}}`,
				`{"type":"turn.completed","usage":{"input_tokens":40,"cached_input_tokens":10,"output_tokens":5}}`,
			},
			wantUsage: "pfm harvest ask: usage input=40 cached_input=10 cache_creation=0 output=5",
			wantArgs: []string{
				"exec\n",
				"--model\ncodex-e2e-model\n",
				"-c\nmodel_reasoning_effort=\"medium\"\n",
				"--json\n",
				"--ephemeral\n",
				"--skip-git-repo-check\n",
				"--color\nnever\n",
			},
		},
	}
	binaries := map[string]string{}
	captures := map[string]string{}
	for name, engine := range engines {
		binary := filepath.Join(home, ".local", "bin", name+"-ask-fixture")
		capture := filepath.Join(home, name+"-ask-capture")
		replyArgs := make([]string, 0, len(engine.reply))
		for _, line := range engine.reply {
			replyArgs = append(replyArgs, shellQuoteFixture(line))
		}
		body := "#!/bin/sh\n" +
			"printf 'home=%s\\n' \"${" + engine.homeVariable + "-}\" > " + shellQuoteFixture(capture+".meta") + "\n" +
			"printf '%s\\n' \"$@\" >> " + shellQuoteFixture(capture+".meta") + "\n" +
			"cat > " + shellQuoteFixture(capture+".prompt") + "\n" +
			"sed -n 's/^[0-9][0-9]*\\. \\(.*\\) — source:.*$/\\1/p' " + shellQuoteFixture(capture+".prompt") + " | while IFS= read -r prepared; do cat \"$prepared\"; done > " + shellQuoteFixture(capture+".files") + "\n" +
			"printf '%s\\n' " + strings.Join(replyArgs, " ") + "\n"
		if err := testjail.WriteExecutable(binary, []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
		binaries[name] = binary
		captures[name] = capture
	}

	configPath := filepath.Join(home, "pfm.config.json")
	config := map[string]any{
		"version": 2,
		"accounts": []map[string]any{{
			"id": 1, "configDir": filepath.Join(home, ".cc", "1"),
		}},
		"claude": map[string]any{"binary": binaries["claude"]},
		"codex": map[string]any{
			"binary": binaries["codex"],
			"homes":  []map[string]any{{"id": 1, "home": filepath.Join(home, ".codex")}},
		},
		"ask": map[string]any{
			"engine": "codex",
			"claude": map[string]any{"model": "claude-e2e-model", "effort": "high"},
			"codex":  map[string]any{"model": "codex-e2e-model", "effort": "medium"},
		},
	}
	rawConfig, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, append(rawConfig, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	codexHome := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(codexHome, "auth.json"),
		[]byte(`{"tokens":{"access_token":"fixture","account_id":"fixture"}}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	for name, engine := range engines {
		t.Run(name, func(t *testing.T) {
			subHarness := *harness
			subHarness.t = t
			args := []string{
				"--config",
				configPath,
				"harvest",
				"ask",
				"-p",
				"State the evidence",
				"--engine",
				name,
				source,
			}
			result := subHarness.pfm(home, args...)
			subHarness.requireSuccess(name+" harvest ask", result)
			if strings.TrimSpace(result.stdout) != engine.answer {
				t.Fatalf("%s stdout=%q stderr=%q", name, result.stdout, result.stderr)
			}
			if !strings.Contains(result.stderr, engine.wantUsage) {
				t.Fatalf(
					"%s usage omitted %q: stdout=%q stderr=%q",
					name,
					engine.wantUsage,
					result.stdout,
					result.stderr,
				)
			}
			meta, err := os.ReadFile(captures[name] + ".meta")
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range append([]string{"home="}, engine.wantArgs...) {
				if !strings.Contains(string(meta), want) {
					t.Errorf("%s adapter metadata omitted %q:\n%s", name, want, meta)
				}
			}
			prompt, err := os.ReadFile(captures[name] + ".prompt")
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"source: " + source, "TASK: State the evidence", "EVIDENCE"} {
				if !strings.Contains(string(prompt), want) {
					t.Errorf("%s prompt omitted %q:\n%s", name, want, prompt)
				}
			}
			files, err := os.ReadFile(captures[name] + ".files")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(files), "full cached evidence reaches the adapter") {
				t.Fatalf("%s adapter could not read full prepared file:\n%s", name, files)
			}
		})
	}
}
