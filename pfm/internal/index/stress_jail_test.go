//go:build jail

// Subject: a helper child of the test binary indexing a generated corpus under a real memory and time bound.

package index

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/testwait"
)

const (
	indexStressEnv       = "PFM_STRESS"
	indexStressHelperEnv = "PFM_INDEX_STRESS_HELPER"
	indexStressResultEnv = "PFM_INDEX_STRESS_RESULT"
	indexStressGiantEnv  = "PFM_INDEX_STRESS_GIANT"
	indexStressSmall     = 10000
	indexStressFileBytes = 200 << 20
	indexStressRSSBound  = 128 << 20
)

type indexStressResult struct {
	AppendBytes int
	// PeakRSS is the helper's own peak resident memory in bytes, read by the
	// helper itself (indexStressPeakRSS).
	PeakRSS int64
	Cold    Counters
	Warm    Counters
	Delta   Counters
}

func TestIndexStress(t *testing.T) {
	if os.Getenv(indexStressEnv) != "1" {
		t.Skip("set PFM_STRESS=1 to run the index stress battery")
	}

	root, giantPath := setupIndexStressCorpus(t)
	resultPath := filepath.Join(root, "stress-result.json")
	ctx := testwait.Context(t)

	var output bytes.Buffer
	command := exec.CommandContext(
		ctx,
		os.Args[0],
		"-test.run=^TestIndexStressHelper$",
	)
	command.Env = append(
		os.Environ(),
		indexStressHelperEnv+"=1",
		indexStressResultEnv+"="+resultPath,
		indexStressGiantEnv+"="+giantPath,
	)
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Run(); err != nil {
		t.Fatalf("index stress helper: %v\n%s", err, output.String())
	}

	content, err := os.ReadFile(resultPath)
	if err != nil {
		t.Fatalf("read index stress result: %v", err)
	}
	var result indexStressResult
	if err := json.Unmarshal(content, &result); err != nil {
		t.Fatalf("decode index stress result: %v", err)
	}

	peakRSS := result.PeakRSS
	if peakRSS >= indexStressRSSBound {
		t.Fatalf(
			"index stress peak RSS = %d MiB, want <%d MiB for a 200 MiB transcript",
			peakRSS>>20,
			indexStressRSSBound>>20,
		)
	}
	t.Logf("generated %d small transcripts + 200 MiB stream", indexStressSmall)
	t.Logf("cold peakRSS=%d MiB counters=%+v", peakRSS>>20, result.Cold)
	t.Logf("warm counters=%+v", result.Warm)
	t.Logf("delta append=%d bytes counters=%+v", result.AppendBytes, result.Delta)
}

func setupIndexStressCorpus(t testing.TB) (root, giantPath string) {
	t.Helper()

	root = t.TempDir()
	claudeRoot := filepath.Join(root, "claude")
	project := filepath.Join(claudeRoot, "project")
	codexHome := filepath.Join(root, "codex")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatalf("create stress Claude project: %v", err)
	}
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		t.Fatalf("create stress Codex root: %v", err)
	}

	smallLine := []byte(
		`{"type":"user","cwd":"/stress","message":{"content":"small prompt"}}` + "\n",
	)
	for index := 0; index < indexStressSmall; index++ {
		path := filepath.Join(project, fmt.Sprintf("small-%05d.jsonl", index))
		if err := os.WriteFile(path, smallLine, 0o600); err != nil {
			t.Fatalf("write stress transcript %d: %v", index, err)
		}
	}

	giantPath = filepath.Join(project, "giant.jsonl")
	writeGiantTranscript(t, giantPath, indexStressFileBytes)

	t.Setenv("TMUX_TMPDIR", filepath.Join(root, "t"))
	t.Setenv(paths.EnvCacheDB, filepath.Join(root, "state", "pfm-cache.db"))
	t.Setenv(paths.EnvSIDDir, filepath.Join(root, "sid"))
	t.Setenv(paths.EnvClaudeRoots, claudeRoot)
	t.Setenv(paths.EnvCodexHome, codexHome)
	t.Setenv(paths.EnvTmuxDir, filepath.Join(root, "tmux"))
	t.Setenv(paths.EnvHome, filepath.Join(root, "home"))
	return root, giantPath
}

func writeGiantTranscript(t testing.TB, path string, targetBytes int64) {
	t.Helper()

	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("create giant transcript: %v", err)
	}
	writer := bufio.NewWriterSize(file, 1<<20)
	line := []byte(
		`{"type":"user","cwd":"/stress/giant","message":{"content":"` +
			strings.Repeat("G", 32<<10) +
			`"}}` + "\n",
	)
	var written int64
	for written < targetBytes {
		count, err := writer.Write(line)
		if err != nil {
			_ = file.Close()
			t.Fatalf("write giant transcript: %v", err)
		}
		written += int64(count)
	}
	if err := writer.Flush(); err != nil {
		_ = file.Close()
		t.Fatalf("flush giant transcript: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close giant transcript: %v", err)
	}
}

func TestIndexStressHelper(t *testing.T) {
	if os.Getenv(indexStressHelperEnv) != "1" {
		t.Skip("index stress helper process only")
	}
	if os.Getenv(paths.EnvCacheDB) == "" || os.Getenv(paths.EnvClaudeRoots) == "" {
		t.Fatal("index stress helper is not jailed")
	}

	database := openIndexStore(t)
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	}()
	indexer, err := New(database)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx := context.Background()

	cold, err := indexer.Run(ctx, Options{})
	if err != nil {
		t.Fatalf("cold stress Run() error = %v", err)
	}
	wantFiles := indexStressSmall + 1
	if cold.FilesSeen != wantFiles ||
		cold.FullParsed != wantFiles ||
		cold.BytesRead < indexStressFileBytes {
		t.Fatalf("cold stress counters = %+v", cold)
	}

	warm, err := indexer.Run(ctx, Options{})
	if err != nil {
		t.Fatalf("warm stress Run() error = %v", err)
	}
	assertNoChangeCounters(t, warm, wantFiles)

	appendLine, err := json.Marshal(map[string]any{
		"type": "user",
		"cwd":  "/stress/giant",
		"message": map[string]any{
			"content": strings.Repeat("D", 900),
		},
	})
	if err != nil {
		t.Fatalf("marshal stress delta: %v", err)
	}
	appendLine = append(appendLine, '\n')
	giantPath := os.Getenv(indexStressGiantEnv)
	appendBytes(t, giantPath, appendLine)

	delta, err := indexer.Run(ctx, Options{})
	if err != nil {
		t.Fatalf("delta stress Run() error = %v", err)
	}
	if delta.FilesSeen != wantFiles ||
		delta.FilesSkipped != wantFiles-1 ||
		delta.DeltaParsed != 1 ||
		delta.FullParsed != 0 ||
		delta.RowsTouched != 1 ||
		delta.BytesRead != int64(len(appendLine)) {
		t.Fatalf(
			"delta stress counters = %+v; append=%d",
			delta,
			len(appendLine),
		)
	}

	result := indexStressResult{
		AppendBytes: len(appendLine),
		Cold:        cold,
		Warm:        warm,
		Delta:       delta,
	}
	result.PeakRSS, err = indexStressPeakRSS()
	if err != nil {
		t.Fatalf("read index stress helper peak RSS: %v", err)
	}
	content, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal index stress result: %v", err)
	}
	if err := os.WriteFile(os.Getenv(indexStressResultEnv), content, 0o600); err != nil {
		t.Fatalf("write index stress result: %v", err)
	}
}

// BenchmarkIndexStressRun is the cold and the no-change index run over the
// stress corpus that TestIndexStress asserts the counters of.
func BenchmarkIndexStressRun(b *testing.B) {
	root, _ := setupIndexStressCorpus(b)
	open := func(name string) *Indexer {
		b.Setenv(paths.EnvCacheDB, filepath.Join(root, name, "pfm-cache.db"))
		database, err := store.Open()
		if err != nil {
			b.Fatalf("store.Open() error = %v", err)
		}
		b.Cleanup(func() { _ = database.Close() })
		indexer, err := New(database)
		if err != nil {
			b.Fatalf("New() error = %v", err)
		}
		return indexer
	}
	b.Run("cold", func(b *testing.B) {
		for iteration := 0; b.Loop(); iteration++ {
			indexer := open(fmt.Sprintf("cold-%d", iteration))
			if _, err := indexer.Run(context.Background(), Options{}); err != nil {
				b.Fatalf("cold Run() error = %v", err)
			}
		}
	})
	b.Run("warm", func(b *testing.B) {
		indexer := open("warm")
		if _, err := indexer.Run(context.Background(), Options{}); err != nil {
			b.Fatalf("priming Run() error = %v", err)
		}
		for b.Loop() {
			if _, err := indexer.Run(context.Background(), Options{}); err != nil {
				b.Fatalf("warm Run() error = %v", err)
			}
		}
	})
}
