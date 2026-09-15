package headless

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "hostops/pfm/internal/config"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/paths"
	"hostops/pfm/internal/store"
)

func TestSummarizeCachesCompleteExchangeAndBoundsAnswer(t *testing.T) {
	root, database := summaryTestStore(t)
	transcriptPath := filepath.Join(root, "complete.jsonl")
	writeSummaryTranscript(t, transcriptPath,
		`{"type":"user","message":{"role":"user","content":"what changed?"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":"the cache changed"}}`,
	)
	counter := filepath.Join(root, "calls")
	bin := filepath.Join(root, "bin")
	writeSummaryStub(t, bin, "codex", `
printf x >> "$ASK_COUNTER"
printf 'one two three four five six seven eight nine ten eleven twelve thirteen fourteen fifteen sixteen seventeen eighteen nineteen twenty twenty-one twenty-two twenty-three twenty-four twenty-five twenty-six twenty-seven twenty-eight twenty-nine thirty thirty-one thirty-two thirty-three thirty-four thirty-five thirty-six thirty-seven thirty-eight thirty-nine forty forty-one forty-two\n'`)
	t.Setenv("PATH", bin)
	t.Setenv("ASK_COUNTER", counter)
	options := SummaryOptions{
		Config: summaryMachine("codex"), Database: database,
		TempDir: filepath.Join(root, "tmp", "chat-status"),
	}
	chat := Chat{Name: "seat", Engine: "cc", Path: transcriptPath}
	first := Summarize(context.Background(), chat, options)
	if first.Cached || len(strings.Fields(first.Text)) != 40 {
		t.Fatalf("first summary=%+v words=%d", first, len(strings.Fields(first.Text)))
	}
	second := Summarize(context.Background(), chat, options)
	if !second.Cached || second.Text != first.Text {
		t.Fatalf("second summary=%+v, first=%+v", second, first)
	}
	if calls, err := os.ReadFile(counter); err != nil || string(calls) != "x" {
		t.Fatalf("runner calls=%q err=%v", calls, err)
	}
	if entries, err := os.ReadDir(options.TempDir); err != nil || len(entries) != 0 {
		t.Fatalf("prepared temp entries=%v err=%v", entries, err)
	}
}

func TestSummarizeMarksPartialAndNeverCachesIt(t *testing.T) {
	root, database := summaryTestStore(t)
	transcriptPath := filepath.Join(root, "working.jsonl")
	writeSummaryTranscript(
		t,
		transcriptPath,
		`{"type":"user","message":{"role":"user","content":"run the checks"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"go test ./..."}}]}}`,
	)
	counter := filepath.Join(root, "calls")
	bin := filepath.Join(root, "bin")
	writeSummaryStub(t, bin, "claude", "printf x >> \"$ASK_COUNTER\"\nprintf 'checks are running\\n'")
	t.Setenv("PATH", bin)
	t.Setenv("ASK_COUNTER", counter)
	options := SummaryOptions{Config: summaryMachine("claude"), Database: database, TempDir: filepath.Join(root, "tmp")}
	chat := Chat{Name: "seat", Engine: "cc", Path: transcriptPath}
	for iteration := 0; iteration < 2; iteration++ {
		result := Summarize(context.Background(), chat, options)
		if result.Cached || !strings.HasPrefix(result.Text, "PARTIAL: ") {
			t.Fatalf("iteration %d summary=%+v", iteration, result)
		}
	}
	if calls, err := os.ReadFile(counter); err != nil || string(calls) != "xx" {
		t.Fatalf("runner calls=%q err=%v", calls, err)
	}
}

func TestSummarizeDistinguishesMissingEngineFromRunnerFailure(t *testing.T) {
	root, database := summaryTestStore(t)
	transcriptPath := filepath.Join(root, "exchange.jsonl")
	writeSummaryTranscript(t, transcriptPath,
		`{"type":"user","message":{"role":"user","content":"question"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":"answer"}}`,
	)
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	machine := summaryMachine("codex")
	machine.Codex.Binary = "absent-codex"
	missing := Summarize(context.Background(), Chat{Engine: "cc", Path: transcriptPath}, SummaryOptions{
		Config: machine, Database: database, TempDir: filepath.Join(root, "tmp"),
	})
	if missing.Text != "unavailable (codex binary MISSING)" {
		t.Fatalf("missing summary=%+v", missing)
	}

	writeSummaryStub(t, bin, "codex", "printf 'provider crashed\\n' >&2\nexit 9")
	failed := Summarize(context.Background(), Chat{Engine: "cc", Path: transcriptPath}, SummaryOptions{
		Config: summaryMachine("codex"), Database: database, TempDir: filepath.Join(root, "tmp"),
	})
	if !strings.HasPrefix(failed.Text, "failed (") || !strings.Contains(failed.Text, "provider crashed") {
		t.Fatalf("failed summary=%+v", failed)
	}
}

func summaryTestStore(t *testing.T) (string, *store.Store) {
	t.Helper()
	root := t.TempDir()
	t.Setenv(paths.EnvHome, filepath.Join(root, "home"))
	t.Setenv(paths.EnvDB, filepath.Join(root, "state", "fleet.db"))
	t.Setenv(paths.EnvSharedDB, filepath.Join(root, "state", "shared.db"))
	t.Setenv(paths.EnvSIDDir, filepath.Join(root, "sid"))
	t.Setenv(paths.EnvTmuxDir, filepath.Join(root, "tmux"))
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return root, database
}

func summaryMachine(engineName string) pfmconfig.Config {
	id, err := pfmengine.Parse(engineName)
	if err != nil {
		panic(err)
	}
	return pfmconfig.Config{
		Accounts:      []pfmconfig.Account{{ID: 1, ConfigDir: "/fixture/claude"}},
		CodexAccounts: []pfmconfig.CodexAccount{{ID: 1, Home: "/fixture/codex"}},
		Claude:        pfmconfig.Claude{Binary: "claude"},
		Codex:         pfmconfig.Codex{Binary: "codex"},
		Ask: pfmconfig.AskConfig{Engine: id, Prefs: map[pfmengine.ID]pfmconfig.EnginePrefs{
			pfmengine.Codex: {Model: "cx-test", Effort: "low"}, pfmengine.Claude: {Model: "cc-test", Effort: "low"},
		}},
	}
}

func writeSummaryTranscript(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeSummaryStub(t *testing.T, directory, name, body string) {
	t.Helper()
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, name), []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
}
