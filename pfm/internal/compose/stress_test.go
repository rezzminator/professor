package compose

import (
	"fmt"
	"os"
	"reflect"
	"runtime"
	"testing"
	"time"

	"hostops/pfm/internal/gather"
	"hostops/pfm/internal/store"
)

func TestComposeStress(t *testing.T) {
	if os.Getenv("PFM_STRESS") != "1" {
		t.Skip("set PFM_STRESS=1 to run the WP5 stress phase")
	}
	strict := os.Getenv("PFM_STRESS_STRICT") == "1"

	input := composeStressInput()
	_ = Compose(input)

	started := time.Now()
	allOutput := Compose(input)
	composeElapsed := time.Since(started)
	const expectedRows = 60 + claudeResumeCap + 1
	if len(allOutput.Rows) != expectedRows {
		t.Fatalf("50k compose rows = %d, want %d", len(allOutput.Rows), expectedRows)
	}
	composeLimit := 5 * time.Second
	if strict {
		// go test runs packages concurrently, so this must remain a latency
		// ceiling rather than a scheduler benchmark. The old 100ms bound
		// failed only while the rest of the stress suite saturated the same
		// container; 500ms still catches the former per-row EvalSymlinks path,
		// which took more than ten minutes.
		composeLimit = 500 * time.Millisecond
	}
	if composeElapsed >= composeLimit {
		logComposePhases(t, input)
		t.Fatalf(
			"50k/200-pane compose took %s, want under %s (strict=%t)",
			composeElapsed,
			composeLimit,
			strict,
		)
	}

	stabilityInput := input
	stabilityInput.Options.View = DefaultView
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	minHeap := ^uint64(0)
	maxHeap := uint64(0)
	iterationsStarted := time.Now()
	rowChecksum := 0
	for iteration := 1; iteration <= 1000; iteration++ {
		output := Compose(stabilityInput)
		rowChecksum += len(output.Rows)
		if iteration%100 == 0 {
			runtime.GC()
			var sample runtime.MemStats
			runtime.ReadMemStats(&sample)
			minHeap = min(minHeap, sample.HeapAlloc)
			maxHeap = max(maxHeap, sample.HeapAlloc)
		}
	}
	iterationsElapsed := time.Since(iterationsStarted)
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	retained := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	heapSpan := maxHeap - minHeap
	if retained > 8<<20 {
		t.Fatalf("1,000 composes retained %d bytes, want <= 8 MiB", retained)
	}
	if heapSpan > 16<<20 {
		t.Fatalf("post-GC heap varied by %d bytes, want <= 16 MiB", heapSpan)
	}

	repeatedOne := Compose(stabilityInput)
	repeatedTwo := Compose(stabilityInput)
	if !reflect.DeepEqual(rowIDs(repeatedOne.Rows), rowIDs(repeatedTwo.Rows)) ||
		repeatedOne.KilledCount != repeatedTwo.KilledCount ||
		repeatedOne.SuppressedCount != repeatedTwo.SuppressedCount {
		t.Fatal("kill decisions changed across identical stress composes")
	}

	t.Logf(
		"stress compose: 50,000 transcripts + 200 panes / 60 sockets -> %d rows in %s (strict=%t)",
		len(allOutput.Rows),
		composeElapsed.Round(time.Microsecond),
		strict,
	)
	t.Logf(
		"stress allocation stability: 1,000 passes in %s, retained=%d bytes, post-GC span=%d bytes, checksum=%d",
		iterationsElapsed.Round(time.Millisecond),
		retained,
		heapSpan,
		rowChecksum,
	)
}

func TestCodexLineageStress(t *testing.T) {
	if os.Getenv("PFM_STRESS") != "1" {
		t.Skip("set PFM_STRESS=1 to run the lineage stress phase")
	}
	const (
		lineageCount = 1000
		filesPerRoot = 10
	)
	rollouts := make([]store.Rollout, 0, lineageCount*filesPerRoot)
	for lineageIndex := 0; lineageIndex < lineageCount; lineageIndex++ {
		root := fmt.Sprintf("root-%04d", lineageIndex)
		for member := 0; member < filesPerRoot; member++ {
			id := root
			if member != 0 {
				id = fmt.Sprintf("%s-child-%02d", root, member)
			}
			rollouts = append(rollouts, store.Rollout{
				ID:           id,
				Path:         "/codex/" + id + ".jsonl",
				Size:         int64(1000 + member),
				MTimeNS:      int64(lineageIndex*filesPerRoot + member + 1),
				CWD:          fmt.Sprintf("/work/project-%02d", lineageIndex%50),
				UserThread:   true,
				SessionID:    root,
				ParentThread: root,
				LineageRoot:  root,
				FirstPrompt:  "WEB",
				PromptCount:  int64(member + 1),
			})
		}
	}
	input := Input{
		Rollouts: rollouts,
		Options: Options{
			View:           DefaultView,
			CurrentDir:     "/work/project-00",
			PrimaryAccount: 1,
			NowNS:          int64(len(rollouts) + 100),
		},
	}
	started := time.Now()
	output := Compose(input)
	elapsed := time.Since(started)
	if len(output.Rows) != codexResumeCap {
		t.Fatalf("lineage stress rows = %d, want %d", len(output.Rows), codexResumeCap)
	}
	seen := make(map[string]struct{})
	for _, row := range output.Rows {
		if row.Kind != ResumeCodex {
			continue
		}
		if _, duplicate := seen[row.ID]; duplicate {
			t.Fatalf("duplicate lineage row %q", row.ID)
		}
		seen[row.ID] = struct{}{}
		if row.PromptCount != filesPerRoot {
			t.Fatalf("lineage %q prompts = %d", row.ID, row.PromptCount)
		}
	}
	if len(seen) != codexResumeCap {
		t.Fatalf("lineage stress Codex rows = %d, want %d", len(seen), codexResumeCap)
	}
	limit := 5 * time.Second
	if os.Getenv("PFM_STRESS_STRICT") == "1" {
		limit = time.Second
	}
	t.Logf(
		"STRESS lineage files=%d roots=%d rows=%d elapsed=%s limit=%s",
		len(rollouts),
		lineageCount,
		len(output.Rows),
		elapsed,
		limit,
	)
	if elapsed >= limit {
		t.Fatalf("lineage compose took %s, want <%s", elapsed, limit)
	}
}

func logComposePhases(t *testing.T, input Input) {
	t.Helper()
	started := time.Now()
	current := &composer{input: input}
	current.buildIndexes()
	indexed := time.Now()
	liveClaude, splits := current.liveClaudeRows()
	liveCodex := current.liveCodexRows()
	liveRows := collapseLiveServers(append(liveClaude, liveCodex...))
	liveRows = append(liveRows, splits...)
	agents := current.agentRows()
	liveDone := time.Now()
	liveRows = append(liveRows, agents...)
	rows := liveRows
	top := make([]Row, 0, claudeResumeCap)
	for index := range input.Transcripts {
		transcript := input.Transcripts[index]
		if _, live := current.liveTranscripts[transcript.UUID]; live {
			continue
		}
		row := current.transcriptRow(transcript, ResumeClaude)
		row = current.applyKill(row, "cc")
		if defaultEligible(row) {
			top = insertTopRow(top, row, claudeResumeCap)
		}
	}
	rows = append(rows, top...)
	rowsDone := time.Now()
	_ = projectDirs(input)
	dirsDone := time.Now()
	_, _ = sortProjectRows(rows)
	sorted := time.Now()
	t.Logf(
		"profile phases: indexes=%s live=%s rows=%s dirs=%s sort=%s",
		indexed.Sub(started).Round(time.Microsecond),
		liveDone.Sub(indexed).Round(time.Microsecond),
		rowsDone.Sub(liveDone).Round(time.Microsecond),
		dirsDone.Sub(rowsDone).Round(time.Microsecond),
		sorted.Sub(dirsDone).Round(time.Microsecond),
	)
}

func composeStressInput() Input {
	const (
		transcriptCount = 50000
		paneCount       = 200
		socketCount     = 60
		projectCount    = 60
	)
	transcripts := make([]store.Transcript, 0, transcriptCount)
	for index := 0; index < transcriptCount; index++ {
		id := fmt.Sprintf("stress-%05d", index)
		project := fmt.Sprintf("project-%02d", index%projectCount)
		transcripts = append(transcripts, store.Transcript{
			UUID:        id,
			Path:        "/accounts/1/projects/" + project + "/" + id + ".jsonl",
			Size:        int64(1000 + index),
			MTimeNS:     int64(index + 1),
			CWD:         "/work/" + project,
			CustomTitle: "Stress " + id,
			FirstPrompt: "prompt " + id,
			LastPrompt:  "last " + id,
			PromptCount: int64(index%20 + 1),
			IsBG:        index%997 == 0,
		})
	}

	panes := make([]gather.ProbePane, 0, paneCount)
	crumbs := make([]gather.Crumb, 0, paneCount)
	for index := 0; index < paneCount; index++ {
		socketIndex := index % socketCount
		socket := fmt.Sprintf("cc-%d-100-%d", 1000+socketIndex, socketIndex)
		paneID := fmt.Sprintf("%%%d", index)
		panes = append(panes, gather.ProbePane{
			Socket:      socket,
			SessionName: socket,
			PaneTitle:   "stress",
			PaneID:      paneID,
			Attached:    index%7 == 0,
		})
		crumbs = append(crumbs, gather.Crumb{
			Filename:       socket + "." + paneID,
			Socket:         socket,
			PaneID:         paneID,
			TranscriptPath: transcripts[index].Path,
		})
	}

	baseline := int64(100)
	killed := make([]store.Killed, 0, transcriptCount/1000)
	for index := 500; index < transcriptCount; index += 1000 {
		killed = append(killed, store.Killed{
			ID:              transcripts[index].UUID,
			Engine:          "cc",
			BaselinePrompts: &baseline,
		})
	}
	return Input{
		Snapshot: gather.Snapshot{
			Panes:  panes,
			Crumbs: crumbs,
		},
		Transcripts: transcripts,
		Killed:      killed,
		AccountRoots: []AccountRoot{{
			Account: 1,
			Path:    "/accounts/1",
		}},
		Options: Options{
			View:           DefaultView,
			CurrentDir:     "/work/project-00",
			PrimaryAccount: 1,
			NowNS:          transcriptCount + 1000,
		},
	}
}
