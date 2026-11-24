package compose

import (
	"fmt"
	"os"
	"reflect"
	"runtime"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestComposeStress(t *testing.T) {
	if os.Getenv("PFM_STRESS") != "1" {
		t.Skip("set PFM_STRESS=1 to run the WP5 stress phase")
	}
	testjail.PauseFlightRecorder(t)

	input := composeStressInput()
	_ = Compose(input)

	allOutput := Compose(input)
	const expectedRows = 60 + ClaudeResumeCap + 1
	if len(allOutput.Rows) != expectedRows {
		t.Fatalf("50k compose rows = %d, want %d", len(allOutput.Rows), expectedRows)
	}

	stabilityInput := input
	stabilityInput.Options.View = DefaultView
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	minHeap := ^uint64(0)
	maxHeap := uint64(0)
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
		"stress compose: 50,000 transcripts + 200 panes / 60 sockets -> %d rows",
		len(allOutput.Rows),
	)
	t.Logf(
		"stress allocation stability: 1,000 passes, retained=%d bytes, post-GC span=%d bytes, checksum=%d",
		retained,
		heapSpan,
		rowChecksum,
	)
}

func TestCodexLineageStress(t *testing.T) {
	if os.Getenv("PFM_STRESS") != "1" {
		t.Skip("set PFM_STRESS=1 to run the lineage stress phase")
	}
	input := codexLineageStressInput()
	rollouts := input.Rollouts
	output := Compose(input)
	if len(output.Rows) != CodexResumeCap {
		t.Fatalf("lineage stress rows = %d, want %d", len(output.Rows), CodexResumeCap)
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
		if row.PromptCount != lineageFilesPerRoot {
			t.Fatalf("lineage %q prompts = %d", row.ID, row.PromptCount)
		}
	}
	if len(seen) != CodexResumeCap {
		t.Fatalf("lineage stress Codex rows = %d, want %d", len(seen), CodexResumeCap)
	}
	t.Logf(
		"STRESS lineage files=%d roots=%d rows=%d",
		len(rollouts),
		lineageStressCount,
		len(output.Rows),
	)
}

// BenchmarkComposeStress measures the 50k-transcript compose the stress test
// asserts the rows of; the number lives here, never in a test's verdict.
func BenchmarkComposeStress(b *testing.B) {
	input := composeStressInput()
	b.ResetTimer()
	for b.Loop() {
		_ = Compose(input)
	}
}

// BenchmarkCodexLineageStress measures the 10k-rollout lineage compose.
func BenchmarkCodexLineageStress(b *testing.B) {
	input := codexLineageStressInput()
	b.ResetTimer()
	for b.Loop() {
		_ = Compose(input)
	}
}

const (
	lineageStressCount  = 1000
	lineageFilesPerRoot = 10
)

func codexLineageStressInput() Input {
	const (
		lineageCount = lineageStressCount
		filesPerRoot = lineageFilesPerRoot
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
				Path:         testjail.Place("/codex/") + id + ".jsonl",
				Size:         int64(1000 + member),
				MTimeNS:      int64(lineageIndex*filesPerRoot + member + 1),
				CWD:          fmt.Sprintf(testjail.Place("/work/project-%02d"), lineageIndex%50),
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
			CurrentDir:     testjail.Place("/work/project-00"),
			PrimaryAccount: 1,
			NowNS:          int64(len(rollouts) + 100),
		},
	}
	return input
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
			Path:        testjail.Place("/accounts/1/projects/") + project + "/" + id + ".jsonl",
			Size:        int64(1000 + index),
			MTimeNS:     int64(index + 1),
			CWD:         testjail.Place("/work/") + project,
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
		ClaudeSeats: []ClaudeSeat{{
			Account:   1,
			ConfigDir: testjail.Place("/accounts/1"),
		}},
		Options: Options{
			View:           DefaultView,
			CurrentDir:     testjail.Place("/work/project-00"),
			PrimaryAccount: 1,
			NowNS:          transcriptCount + 1000,
		},
	}
}
