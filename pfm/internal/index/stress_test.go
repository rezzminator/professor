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
	"syscall"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/paths"
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
	ColdDuration  time.Duration
	WarmDuration  time.Duration
	DeltaDuration time.Duration
	AppendBytes   int
	Cold          Counters
	Warm          Counters
	Delta         Counters
}

func TestIndexStress(t *testing.T) {
	if os.Getenv(indexStressEnv) != "1" {
		t.Skip("set PFM_STRESS=1 to run the index stress battery")
	}
	strict := os.Getenv("PFM_STRESS_STRICT") == "1"

	root, giantPath, generationDuration := setupIndexStressCorpus(t)
	resultPath := filepath.Join(root, "stress-result.json")
	helperTimeout := 10 * time.Minute
	if strict {
		helperTimeout = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), helperTimeout)
	defer cancel()

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

	usage, ok := command.ProcessState.SysUsage().(*syscall.Rusage)
	if !ok {
		t.Fatalf("index stress process usage has type %T", command.ProcessState.SysUsage())
	}
	peakRSS := usage.Maxrss * 1024
	if peakRSS >= indexStressRSSBound {
		t.Fatalf(
			"index stress peak RSS = %d MiB, want <%d MiB for a 200 MiB transcript",
			peakRSS>>20,
			indexStressRSSBound>>20,
		)
	}
	t.Logf(
		"generated %d small transcripts + 200 MiB stream in %s (strict=%t)",
		indexStressSmall,
		generationDuration.Round(time.Millisecond),
		strict,
	)
	t.Logf(
		"cold=%s peakRSS=%d MiB counters=%+v",
		result.ColdDuration.Round(time.Millisecond),
		peakRSS>>20,
		result.Cold,
	)
	t.Logf(
		"warm=%s counters=%+v",
		result.WarmDuration.Round(time.Millisecond),
		result.Warm,
	)
	t.Logf(
		"delta=%s append=%d bytes counters=%+v",
		result.DeltaDuration.Round(time.Millisecond),
		result.AppendBytes,
		result.Delta,
	)
}

func setupIndexStressCorpus(t *testing.T) (root, giantPath string, elapsed time.Duration) {
	t.Helper()

	started := time.Now()
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
	t.Setenv(paths.EnvDB, filepath.Join(root, "state", "fleet.db"))
	t.Setenv(paths.EnvSIDDir, filepath.Join(root, "sid"))
	t.Setenv(paths.EnvClaudeRoots, claudeRoot)
	t.Setenv(paths.EnvCodexHome, codexHome)
	t.Setenv(paths.EnvTmuxDir, filepath.Join(root, "tmux"))
	t.Setenv(paths.EnvHome, filepath.Join(root, "home"))
	return root, giantPath, time.Since(started)
}

func writeGiantTranscript(t *testing.T, path string, targetBytes int64) {
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
	if os.Getenv(paths.EnvDB) == "" || os.Getenv(paths.EnvClaudeRoots) == "" {
		t.Fatal("index stress helper is not jailed")
	}
	strict := os.Getenv("PFM_STRESS_STRICT") == "1"

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

	started := time.Now()
	cold, err := indexer.Run(ctx, Options{})
	if err != nil {
		t.Fatalf("cold stress Run() error = %v", err)
	}
	coldDuration := time.Since(started)
	wantFiles := indexStressSmall + 1
	if cold.FilesSeen != wantFiles ||
		cold.FullParsed != wantFiles ||
		cold.BytesRead < indexStressFileBytes {
		t.Fatalf("cold stress counters = %+v", cold)
	}
	coldLimit := 5 * time.Minute
	if strict {
		coldLimit = time.Minute
	}
	if coldDuration > coldLimit {
		t.Fatalf(
			"cold stress duration = %s, want <%s (strict=%t)",
			coldDuration,
			coldLimit,
			strict,
		)
	}

	started = time.Now()
	warm, err := indexer.Run(ctx, Options{})
	if err != nil {
		t.Fatalf("warm stress Run() error = %v", err)
	}
	warmDuration := time.Since(started)
	assertNoChangeCounters(t, warm, wantFiles)
	warmLimit := time.Minute
	if strict {
		warmLimit = 5 * time.Second
	}
	if warmDuration > warmLimit {
		t.Fatalf(
			"warm stress duration = %s, want <%s (strict=%t)",
			warmDuration,
			warmLimit,
			strict,
		)
	}

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

	started = time.Now()
	delta, err := indexer.Run(ctx, Options{})
	if err != nil {
		t.Fatalf("delta stress Run() error = %v", err)
	}
	deltaDuration := time.Since(started)
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
	deltaLimit := time.Minute
	if strict {
		deltaLimit = 5 * time.Second
	}
	if deltaDuration > deltaLimit {
		t.Fatalf(
			"delta stress duration = %s, want <%s (strict=%t)",
			deltaDuration,
			deltaLimit,
			strict,
		)
	}

	result := indexStressResult{
		ColdDuration:  coldDuration,
		WarmDuration:  warmDuration,
		DeltaDuration: deltaDuration,
		AppendBytes:   len(appendLine),
		Cold:          cold,
		Warm:          warm,
		Delta:         delta,
	}
	content, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal index stress result: %v", err)
	}
	if err := os.WriteFile(os.Getenv(indexStressResultEnv), content, 0o600); err != nil {
		t.Fatalf("write index stress result: %v", err)
	}
}
