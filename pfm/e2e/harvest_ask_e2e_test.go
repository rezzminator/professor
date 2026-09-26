//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHarvestAskE2E(t *testing.T) {
	requireE2EFence(t)
	repo := sourceRepo(t)
	harness := &e2eHarness{
		t:          t,
		repo:       repo,
		goCache:    requiredGoEnv(t, "GOCACHE"),
		goModCache: requiredGoEnv(t, "GOMODCACHE"),
	}
	harness.headBinary = harness.build(repo, filepath.Join(t.TempDir(), "pfm-head"))
	home := harness.newHome(harness.headBinary)
	source := filepath.Join(home, "evidence.txt")
	if err := os.WriteFile(source, []byte("full cached evidence reaches the adapter\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	engines := map[string]struct {
		homeVariable string
		answer       string
		wantArgs     []string
	}{
		"claude": {
			homeVariable: "CLAUDE_CONFIG_DIR",
			answer:       "claude-e2e-answer",
			wantArgs:     []string{"-p", "--model", "claude-e2e-model", "--effort", "high", "--output-format", "text"},
		},
		"codex": {
			homeVariable: "CODEX_HOME",
			answer:       "codex-e2e-answer",
			wantArgs: []string{
				"exec",
				"--model",
				"codex-e2e-model",
				"model_reasoning_effort=\"medium\"",
				"--ephemeral",
				"--skip-git-repo-check",
				"--color",
				"never",
				"-",
			},
		},
	}
	binaries := map[string]string{}
	captures := map[string]string{}
	for name, engine := range engines {
		binary := filepath.Join(home, ".local", "bin", name+"-ask-fixture")
		capture := filepath.Join(home, name+"-ask-capture")
		body := "#!/bin/sh\n" +
			"printf 'home=%s\\n' \"${" + engine.homeVariable + "-}\" > " + shellQuoteFixture(capture+".meta") + "\n" +
			"printf '%s\\n' \"$@\" >> " + shellQuoteFixture(capture+".meta") + "\n" +
			"cat > " + shellQuoteFixture(capture+".prompt") + "\n" +
			"sed -n 's/^[0-9][0-9]*\\. \\(.*\\) — source:.*$/\\1/p' " + shellQuoteFixture(capture+".prompt") + " | while IFS= read -r prepared; do cat \"$prepared\"; done > " + shellQuoteFixture(capture+".files") + "\n" +
			"printf '" + engine.answer + "\\n'\n"
		if err := os.WriteFile(binary, []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
		binaries[name] = binary
		captures[name] = capture
	}

	configPath := filepath.Join(home, ".config", "pfm", "config.json")
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

	for name, engine := range engines {
		t.Run(name, func(t *testing.T) {
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
			result := harness.pfm(home, args...)
			harness.requireSuccess(name+" harvest ask", result)
			if strings.TrimSpace(result.stdout) != engine.answer {
				t.Fatalf("%s stdout=%q stderr=%q", name, result.stdout, result.stderr)
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
