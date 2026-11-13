package headless

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/store"
)

func TestSummarizeDeliversLastExchangeToRunner(t *testing.T) {
	for _, engineName := range []string{"claude", "codex"} {
		t.Run(engineName, func(t *testing.T) {
			root, database := summaryTestStore(t)
			transcriptPath := filepath.Join(root, "exchange.jsonl")
			writeSummaryTranscript(t, transcriptPath,
				`{"type":"user","message":{"role":"user","content":"obsolete question"}}`,
				`{"type":"assistant","message":{"role":"assistant","content":"obsolete answer"}}`,
				`{"type":"user","message":{"role":"user","content":"which checks ran?"}}`,
				`{"type":"assistant","message":{"role":"assistant","content":"the capture regression passed"}}`,
			)
			bin := filepath.Join(root, "bin")
			output := `{"result":"capture regression passed"}` + "\n"
			if engineName == "codex" {
				output = `{"type":"item.completed","item":{"type":"agent_message","text":"capture regression passed"}}` + "\n" +
					`{"type":"turn.completed"}` + "\n"
			}
			writeEngine(t, bin, engineName)
			runner := fakeEngine(deps.StartOutput{Stdout: output})
			t.Setenv("PATH", bin)
			options := SummaryOptions{
				Config: summaryMachine(t, engineName), Database: database, TempDir: filepath.Join(root, "prepared"),
				Runner: runner,
			}
			result := Summarize(
				context.Background(),
				Chat{Name: "seat", Engine: pfmengine.Claude, Path: transcriptPath},
				options,
			)
			if result.Text != "capture regression passed" || result.Warning != nil {
				t.Fatalf("summary=%+v", result)
			}
			raw := startedStdin(t, runner, 0)
			for _, want := range []string{"which checks ran?", "the capture regression passed", "STATE: COMPLETE"} {
				if !strings.Contains(raw, want) {
					t.Errorf("runner input missing last exchange %q: %s", want, raw)
				}
			}
			if strings.Contains(raw, "obsolete") {
				t.Errorf("runner received an older exchange: %s", raw)
			}
			assertNoLeakedTempFiles(t, options.TempDir)
		})
	}
}

func TestSummarizeCachesCompleteExchangeAndBoundsAnswer(t *testing.T) {
	root, database := summaryTestStore(t)
	transcriptPath := filepath.Join(root, "complete.jsonl")
	writeSummaryTranscript(t, transcriptPath,
		`{"type":"user","message":{"role":"user","content":"what changed?"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":"the cache changed"}}`,
	)
	bin := filepath.Join(root, "bin")
	writeEngine(t, bin, "codex")
	words := "one two three four five six seven eight nine ten eleven twelve thirteen fourteen fifteen sixteen " +
		"seventeen eighteen nineteen twenty twenty-one twenty-two twenty-three twenty-four twenty-five twenty-six " +
		"twenty-seven twenty-eight twenty-nine thirty thirty-one thirty-two thirty-three thirty-four thirty-five " +
		"thirty-six thirty-seven thirty-eight thirty-nine forty forty-one forty-two"
	runner := fakeEngine(deps.StartOutput{
		Stdout: `{"type":"thread.started"}` + "\n" + `{"type":"turn.started"}` + "\n" +
			`{"type":"item.completed","item":{"type":"agent_message","text":"` + words + `"}}` + "\n" +
			`{"type":"turn.completed"}` + "\n",
	})
	t.Setenv("PATH", bin)
	options := SummaryOptions{
		Config: summaryMachine(t, "codex"), Database: database,
		TempDir: filepath.Join(root, "tmp", "chat-status"), Runner: runner,
	}
	chat := Chat{Name: "seat", Engine: "cc", Path: transcriptPath}
	first := Summarize(context.Background(), chat, options)
	if first.Cached || first.Warning != nil || len(strings.Fields(first.Text)) != 40 {
		t.Fatalf("first summary=%+v words=%d", first, len(strings.Fields(first.Text)))
	}
	second := Summarize(context.Background(), chat, options)
	if !second.Cached || second.Warning != nil || second.Text != first.Text {
		t.Fatalf("second summary=%+v, first=%+v", second, first)
	}
	if calls := len(runner.Starts()); calls != 1 {
		t.Fatalf("runner calls=%d, want 1", calls)
	}
	if entries, err := os.ReadDir(options.TempDir); err != nil || len(entries) != 0 {
		t.Fatalf("prepared temp entries=%v err=%v", entries, err)
	}
}

// A cache that cannot be read or written costs only the cache: the summary
// is run and handed back, and both cache failures come back as its Warning.
func TestSummarizeKeepsTheSummaryPastAnUnusableCache(t *testing.T) {
	root, database := summaryTestStore(t)
	transcriptPath := filepath.Join(root, "complete.jsonl")
	writeSummaryTranscript(t, transcriptPath,
		`{"type":"user","message":{"role":"user","content":"what changed?"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":"the cache changed"}}`,
	)
	bin := filepath.Join(root, "bin")
	writeEngine(t, bin, "claude")
	runner := fakeEngine(deps.StartOutput{Stdout: `{"result":"the cache changed"}` + "\n"})
	t.Setenv("PATH", bin)
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	result := Summarize(context.Background(), Chat{Name: "seat", Engine: "cc", Path: transcriptPath}, SummaryOptions{
		Config: summaryMachine(t, "claude"), Database: database, TempDir: filepath.Join(root, "prepared"),
		Runner: runner,
	})
	if result.Text != "the cache changed" || result.Cached {
		t.Fatalf("summary=%+v, want the fresh summary text", result)
	}
	if calls := len(runner.Starts()); calls != 1 {
		t.Fatalf("runner calls=%d, want one run", calls)
	}
	if result.Warning == nil {
		t.Fatal("Warning = nil, want both cache failures")
	}
	lines := strings.Split(result.Warning.Error(), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "read summary cache: ") ||
		!strings.HasPrefix(lines[1], "write summary cache: ") {
		t.Fatalf("Warning = %q, want a read then a write cache failure", result.Warning)
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
	bin := filepath.Join(root, "bin")
	writeEngine(t, bin, "claude")
	runner := fakeEngine(deps.StartOutput{Stdout: `{"result":"checks are running"}` + "\n"})
	t.Setenv("PATH", bin)
	options := SummaryOptions{
		Config:   summaryMachine(t, "claude"),
		Database: database,
		TempDir:  filepath.Join(root, "tmp"),
		Runner:   runner,
	}
	chat := Chat{Name: "seat", Engine: "cc", Path: transcriptPath}
	for iteration := 0; iteration < 2; iteration++ {
		result := Summarize(context.Background(), chat, options)
		if result.Cached || !strings.HasPrefix(result.Text, "PARTIAL: ") {
			t.Fatalf("iteration %d summary=%+v", iteration, result)
		}
	}
	if calls := len(runner.Starts()); calls != 2 {
		t.Fatalf("runner calls=%d, want 2", calls)
	}
}

// A Codex rollout whose newest entry is commentary between tool calls is a
// running turn: its summary is PARTIAL and never cached as final, so the next
// read pays the runner again.
func TestSummarizeNeverCachesAMidTurnCodexExchange(t *testing.T) {
	root, database := summaryTestStore(t)
	transcriptPath := filepath.Join(root, "rollout.jsonl")
	writeSummaryTranscript(
		t,
		transcriptPath,
		codexTurnStartLine,
		codexUserLine,
		codexToolLine,
		codexSaid("checking the next file"),
	)
	bin := filepath.Join(root, "bin")
	writeEngine(t, bin, "claude")
	runner := fakeEngine(deps.StartOutput{Stdout: `{"result":"still checking files"}` + "\n"})
	t.Setenv("PATH", bin)
	options := SummaryOptions{
		Config:   summaryMachine(t, "claude"),
		Database: database,
		TempDir:  filepath.Join(root, "tmp"),
		Runner:   runner,
	}
	chat := Chat{Name: "seat", Engine: pfmengine.Codex, Path: transcriptPath}
	for iteration := 0; iteration < 2; iteration++ {
		result := Summarize(context.Background(), chat, options)
		if result.Cached || !strings.HasPrefix(result.Text, "PARTIAL: ") {
			t.Fatalf("iteration %d summary=%+v, want an uncached PARTIAL", iteration, result)
		}
	}
	if calls := len(runner.Starts()); calls != 2 {
		t.Fatalf("runner calls=%d, want 2", calls)
	}
	info, err := os.Stat(transcriptPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := database.ChatSummary(context.Background(), transcriptPath, info.Size()); err != nil || found {
		t.Fatalf("mid-turn summary cached at offset %d: found=%v err=%v", info.Size(), found, err)
	}
}

func TestSummarizePreservesAnswerWhenPreparedExchangeCleanupFails(t *testing.T) {
	root, database := summaryTestStore(t)
	transcriptPath := filepath.Join(root, "exchange.jsonl")
	writeSummaryTranscript(t, transcriptPath,
		`{"type":"user","message":{"role":"user","content":"what changed?"}}`,
		`{"type":"assistant","message":{"role":"assistant","content":"the cleanup path"}}`,
	)
	tempDir := filepath.Join(root, "tmp", "chat-status")
	bin := filepath.Join(root, "bin")
	writeEngine(t, bin, "codex")
	runner := fakeEngine(deps.StartOutput{
		AtStart: removePreparedExchange(t, tempDir),
		Stdout: `{"type":"thread.started"}` + "\n" + `{"type":"turn.started"}` + "\n" +
			`{"type":"item.completed","item":{"type":"agent_message","text":"cleanup-resistant summary"}}` + "\n" +
			`{"type":"turn.completed"}` + "\n",
	})
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	result := Summarize(context.Background(), Chat{Name: "seat", Engine: "cc", Path: transcriptPath}, SummaryOptions{
		Config: summaryMachine(t, "codex"), Database: database, TempDir: tempDir, Runner: runner,
	})
	if result.Text != "cleanup-resistant summary" || strings.HasPrefix(result.Text, "failed (") {
		t.Fatalf("summary lost its computed answer after cleanup failure: %+v", result)
	}
	if !errors.Is(result.Warning, fs.ErrNotExist) {
		t.Fatalf("summary cleanup warning=%v, want fs.ErrNotExist", result.Warning)
	}

	cached := Summarize(context.Background(), Chat{Name: "seat", Engine: "cc", Path: transcriptPath}, SummaryOptions{
		Config: summaryMachine(t, "codex"), Database: database, TempDir: tempDir, Runner: runner,
	})
	if cached.Text != result.Text || !cached.Cached || cached.Warning != nil {
		t.Fatalf("summary was not cached after cleanup warning: first=%+v cached=%+v", result, cached)
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
	machine := summaryMachine(t, "codex")
	machine.Codex.Binary = "absent-codex"
	missing := Summarize(context.Background(), Chat{Engine: "cc", Path: transcriptPath}, SummaryOptions{
		Config: machine, Database: database, TempDir: filepath.Join(root, "tmp"),
	})
	if missing.Text != "unavailable (codex binary MISSING)" {
		t.Fatalf("missing summary=%+v", missing)
	}

	writeEngine(t, bin, "codex")
	failed := Summarize(context.Background(), Chat{Engine: "cc", Path: transcriptPath}, SummaryOptions{
		Config: summaryMachine(t, "codex"), Database: database, TempDir: filepath.Join(root, "tmp"),
		Runner: fakeEngine(deps.StartOutput{Stderr: "provider crashed\n", ExitCode: 9}),
	})
	if !strings.HasPrefix(failed.Text, "failed (") || !strings.Contains(failed.Text, "provider crashed") {
		t.Fatalf("failed summary=%+v", failed)
	}
}

func summaryTestStore(t *testing.T) (string, *store.Store) {
	t.Helper()
	root := t.TempDir()
	t.Setenv(paths.EnvHome, filepath.Join(root, "home"))
	t.Setenv(paths.EnvCacheDB, filepath.Join(root, "state", "pfm-cache.db"))
	t.Setenv(paths.EnvStateDB, filepath.Join(root, "state", "shared.db"))
	t.Setenv(paths.EnvSIDDir, filepath.Join(root, "sid"))
	t.Setenv(paths.EnvTmuxDir, filepath.Join(root, "tmux"))
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return root, database
}

func summaryMachine(t *testing.T, engineName string) pfmconfig.Config {
	t.Helper()
	codexHome := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(codexHome, "auth.json"),
		[]byte(`{"tokens":{"access_token":"fixture","account_id":"fixture"}}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	id, err := pfmengine.Parse(engineName)
	if err != nil {
		panic(err)
	}
	binaries := t.TempDir()
	writeEngine(t, binaries, "claude")
	writeEngine(t, binaries, "codex")
	return pfmconfig.Config{
		Accounts:      []pfmconfig.Account{{ID: 1, ConfigDir: t.TempDir()}},
		CodexAccounts: []pfmconfig.CodexAccount{{ID: 1, Home: codexHome}},
		Claude:        pfmconfig.Claude{Binary: filepath.Join(binaries, "claude")},
		Codex:         pfmconfig.Codex{Binary: filepath.Join(binaries, "codex")},
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
