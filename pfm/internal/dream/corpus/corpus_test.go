package corpus

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"hostops/pfm/internal/dream/artifact"
)

func TestLatestAppliedSweepIsLaneScopedCompletedAndSequenceOrdered(t *testing.T) {
	organ := newOrgan(t)
	writeFile(t, filepath.Join(organ, "dreamer", "2026-08-09.md"), "END-OF-SWEEP\n", 0o600)
	writeFile(t, filepath.Join(organ, "dreamer", "2026-08-11.md"), "lane\texplorer\nnot complete\n", 0o600)
	writeFile(t, filepath.Join(organ, "dreamer", "2026-08-10-2.md"), "lane\texplorer\nEND-OF-SWEEP\n", 0o600)
	writeFile(t, filepath.Join(organ, "dreamer", "2026-08-10-12.md"), "lane\texplorer\nEND-OF-SWEEP\n", 0o600)
	writeFile(t, filepath.Join(organ, "dreamer", "2026-08-12.md"), "lane\tqa-orion-cortex\nEND-OF-SWEEP\n", 0o600)
	writeFile(t, filepath.Join(organ, "dreamer", "notes.md"), "END-OF-SWEEP\n", 0o600)

	got, err := LatestAppliedSweep(organ, "explorer")
	if err != nil {
		t.Fatalf("LatestAppliedSweep(explorer) error = %v", err)
	}
	if want := filepath.Join(organ, "dreamer", "2026-08-10-12.md"); got != want {
		t.Fatalf("LatestAppliedSweep(explorer) = %q, want %q", got, want)
	}
	got, err = LatestAppliedSweep(organ, "qa-orion-cortex")
	if err != nil {
		t.Fatalf("LatestAppliedSweep(qa) error = %v", err)
	}
	if want := filepath.Join(organ, "dreamer", "2026-08-12.md"); got != want {
		t.Fatalf("LatestAppliedSweep(qa) = %q, want %q", got, want)
	}

	empty, err := LatestAppliedSweep(organ, "general-purpose")
	if err != nil || empty != "" {
		t.Fatalf("LatestAppliedSweep(no matches) = %q, %v; want empty success", empty, err)
	}
}

func TestLatestAppliedSweepFailsClosedOnMalformedCandidates(t *testing.T) {
	tests := []struct {
		name string
		make func(t *testing.T, organ string)
		want string
	}{
		{
			name: "malformed lane",
			make: func(t *testing.T, organ string) {
				writeFile(
					t,
					filepath.Join(organ, "dreamer", "2026-08-10.md"),
					"lane\texplorer\textra\nEND-OF-SWEEP\n",
					0o600,
				)
			},
			want: "invalid lane row",
		},
		{
			name: "duplicate lane",
			make: func(t *testing.T, organ string) {
				writeFile(
					t,
					filepath.Join(organ, "dreamer", "2026-08-10.md"),
					"lane\texplorer\nlane\texplorer\nEND-OF-SWEEP\n",
					0o600,
				)
			},
			want: "duplicate lane row",
		},
		{
			name: "nonregular sweep",
			make: func(t *testing.T, organ string) {
				mustMkdir(t, filepath.Join(organ, "dreamer", "2026-08-10.md"))
			},
			want: "not a regular file",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			organ := newOrgan(t)
			test.make(t, organ)
			_, err := LatestAppliedSweep(organ, "explorer")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("LatestAppliedSweep() error = %v, want %q", err, test.want)
			}
		})
	}
	if _, err := LatestAppliedSweep("relative", "explorer"); err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("LatestAppliedSweep(relative) error = %v", err)
	}
}

func TestCutoffPrecedenceAndBootstrapFallback(t *testing.T) {
	location := time.FixedZone("fixture", 2*60*60)
	now := time.Date(2026, 8, 13, 8, 30, 0, 0, location)
	tests := []struct {
		name        string
		body        string
		wantSource  CutoffSource
		wantDisplay string
		wantTime    time.Time
	}{
		{
			name:       "enumerated at",
			body:       "enumerated-at\t2026-08-10T12:34:56+02:00\nApplied: 2026-08-10T13:00:00+02:00\nEND-OF-SWEEP\n",
			wantSource: CutoffEnumeratedAt, wantDisplay: "2026-08-10T12:34:56+02:00",
			wantTime: time.Date(2026, 8, 10, 12, 34, 56, 0, location),
		},
		{
			name:        "invalid last enumerated falls back to applied",
			body:        "enumerated-at\t2026-08-10T11:00:00+02:00\nenumerated-at\tnot-a-date\nApplied: 2026-08-10T13:00:00+02:00\nEND-OF-SWEEP\n",
			wantSource:  CutoffApplied,
			wantDisplay: "2026-08-10T13:00:00+02:00",
			wantTime:    time.Date(2026, 8, 10, 13, 0, 0, 0, location),
		},
		{
			name:       "filename midnight",
			body:       "enumerated-at\tbad\nApplied: also-bad\nEND-OF-SWEEP\n",
			wantSource: CutoffFilenameDate, wantDisplay: "2026-08-10 00:00:00",
			wantTime: time.Date(2026, 8, 10, 0, 0, 0, 0, location),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			organ := newOrgan(t)
			writeFile(t, filepath.Join(organ, "dreamer", "2026-08-10.md"), test.body, 0o600)
			window, err := Cutoff(organ, "explorer", now)
			if err != nil {
				t.Fatalf("Cutoff() error = %v", err)
			}
			if window.CutoffSource != test.wantSource || window.CutoffExclusive != test.wantDisplay ||
				!window.CutoffTime.Equal(test.wantTime) {
				t.Fatalf(
					"Cutoff() = %#v, want source=%s display=%s time=%s",
					window,
					test.wantSource,
					test.wantDisplay,
					test.wantTime,
				)
			}
		})
	}

	organ := newOrgan(t)
	window, err := Cutoff(organ, "qa-orion-cortex", now)
	if err != nil {
		t.Fatalf("Cutoff(no sweep) error = %v", err)
	}
	if window.NewestAppliedSweep != "NONE" || window.CutoffSource != CutoffBootstrap ||
		window.CutoffExclusive != "7 days ago" || !window.CutoffTime.Equal(now.Add(-7*24*time.Hour)) {
		t.Fatalf("Cutoff(no sweep) = %#v", window)
	}
}

func TestEnumerateBootstrapMatchesBatteryCensusAndTieBreak(t *testing.T) {
	ctx := newContext(t)
	location := time.FixedZone("fixture", 2*60*60)
	now := time.Date(2026, 8, 13, 8, 0, 0, 0, location)
	tie := time.Date(2026, 8, 12, 7, 0, 0, 500_000_000, location)
	newest := makeMeta(
		t,
		ctx.Registry,
		"newest",
		`{"agentType":"Explore"}`,
		true,
		time.Date(2026, 8, 12, 8, 0, 0, 900_000_000, location),
	)
	tieB := makeMeta(t, ctx.Registry, "tie-b", `{"agentType":"Explore"}`, true, tie)
	tieA := makeMeta(t, ctx.Registry, "tie-a", `{"agentType":"Explore"}`, true, tie)
	makeMeta(t, ctx.Registry, "older", `{"agentType":"Explore"}`, true, time.Date(2026, 8, 11, 23, 0, 0, 0, location))
	missing := makeMeta(
		t,
		ctx.Registry,
		"missing",
		`{"agentType":"Explore"}`,
		false,
		time.Date(2026, 8, 12, 9, 0, 0, 0, location),
	)
	makeMeta(
		t,
		ctx.Registry,
		"worker",
		`{"agentType":"general-purpose"}`,
		true,
		time.Date(2026, 8, 12, 10, 0, 0, 0, location),
	)
	makeMeta(t, ctx.Registry, "invalid", `{}`, true, time.Date(2026, 8, 12, 11, 0, 0, 0, location))

	result, err := Enumerate(
		ctx,
		artifact.LaneContext{AgentType: "Explore", Lane: "explorer"},
		Selection{BootstrapCount: 3},
		now,
	)
	if err != nil {
		t.Fatalf("Enumerate() error = %v", err)
	}
	wantPaths := sortedUnique([]string{newest.transcript, tieA.transcript, tieB.transcript})
	if !reflect.DeepEqual(result.Paths, wantPaths) {
		t.Fatalf("paths = %#v, want %#v", result.Paths, wantPaths)
	}
	wantCensus := artifact.Census{
		WindowMetaCount: 7, AgentMetaCount: 5, PairedTranscriptCount: 4,
		SelectedPairedTranscriptCount: 3, OmittedPairedTranscriptCount: 1,
		CoverageGapCount: 1, ExcludedOtherAgentOrInvalidCount: 2, InvalidMetaCount: 1,
	}
	if result.Census != wantCensus {
		t.Fatalf("census = %#v, want %#v", result.Census, wantCensus)
	}
	if len(result.Selected) != 3 || result.Selected[0].Meta != newest.meta || result.Selected[1].Meta != tieA.meta ||
		result.Selected[2].Meta != tieB.meta {
		t.Fatalf("bootstrap ranking = %#v", result.Selected)
	}
	if len(result.Gaps) != 1 || result.Gaps[0].Meta != missing.meta || result.Gaps[0].Kind != GapMissingTranscript {
		t.Fatalf("gaps = %#v", result.Gaps)
	}
	if result.Window.Mode != WindowBootstrap || result.Window.BootstrapCount != 3 ||
		result.CutoffDescription != "bootstrap-count 3" {
		t.Fatalf("window/result = %#v / %#v", result.Window, result)
	}

	stage := newStage(t)
	if err := Write(stage, result); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	assertStageArtifacts(t, stage, result)
	selection := readFile(t, filepath.Join(stage.Meta, "bootstrap-selection.tsv"))
	if rows := strings.Split(
		strings.TrimSuffix(selection, "\n"),
		"\n",
	); len(rows) != 3 || !strings.Contains(rows[0], newest.meta) || !strings.Contains(rows[1], tieA.meta) ||
		!strings.Contains(rows[2], tieB.meta) {
		t.Fatalf("bootstrap-selection.tsv = %q", selection)
	}
}

func TestEnumerateRollingWindowIsStrictlyExclusiveAndLaneScoped(t *testing.T) {
	ctx := newContext(t)
	location := time.FixedZone("fixture", 2*60*60)
	cutoff := time.Date(2026, 8, 10, 12, 34, 56, 0, location)
	now := cutoff.Add(72 * time.Hour)
	writeFile(
		t,
		filepath.Join(ctx.Organ, "dreamer", "2026-08-10.md"),
		"lane\texplorer\nenumerated-at\t2026-08-10T12:34:56+02:00\nEND-OF-SWEEP\n",
		0o600,
	)
	makeMeta(t, ctx.Registry, "before", `{"agentType":"Explore"}`, true, cutoff.Add(-time.Nanosecond))
	makeMeta(t, ctx.Registry, "equal", `{"agentType":"Explore"}`, true, cutoff)
	after := makeMeta(t, ctx.Registry, "after", `{"agentType":"Explore"}`, true, cutoff.Add(time.Nanosecond))
	missing := makeMeta(t, ctx.Registry, "missing", `{"agentType":"Explore"}`, false, cutoff.Add(time.Second))
	makeMeta(t, ctx.Registry, "other", `{"agentType":"general-purpose"}`, true, cutoff.Add(2*time.Second))
	makeMeta(t, ctx.Registry, "invalid", `[]`, true, cutoff.Add(3*time.Second))

	result, err := Enumerate(ctx, artifact.LaneContext{AgentType: "Explore", Lane: "explorer"}, Selection{}, now)
	if err != nil {
		t.Fatalf("Enumerate() error = %v", err)
	}
	if !reflect.DeepEqual(result.Paths, []string{after.transcript}) {
		t.Fatalf("strict window paths = %#v", result.Paths)
	}
	want := artifact.Census{
		WindowMetaCount: 4, AgentMetaCount: 2, PairedTranscriptCount: 1,
		SelectedPairedTranscriptCount: 1, CoverageGapCount: 1,
		ExcludedOtherAgentOrInvalidCount: 2, InvalidMetaCount: 1,
	}
	if result.Census != want {
		t.Fatalf("rolling census = %#v, want %#v", result.Census, want)
	}
	if len(result.Gaps) != 1 || result.Gaps[0].Meta != missing.meta {
		t.Fatalf("rolling gaps = %#v", result.Gaps)
	}
	if result.Window.CutoffSource != CutoffEnumeratedAt ||
		result.Window.CutoffExclusive != "2026-08-10T12:34:56+02:00" {
		t.Fatalf("rolling window = %#v", result.Window)
	}

	qa, err := Enumerate(
		ctx,
		artifact.LaneContext{AgentType: "qa-orion-cortex", Lane: "qa-orion-cortex"},
		Selection{},
		now,
	)
	if err != nil {
		t.Fatalf("Enumerate(QA) error = %v", err)
	}
	if qa.Window.NewestAppliedSweep != "NONE" || qa.Window.CutoffExclusive != "7 days ago" {
		t.Fatalf("QA inherited explorer window: %#v", qa.Window)
	}
}

func TestExplicitCorpusReadsOnceCopiesExactBytesAndDeduplicates(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "a.jsonl")
	b := filepath.Join(root, "b.jsonl")
	writeFile(t, a, "a\n", 0o600)
	writeFile(t, b, "b\n", 0o600)
	corpusFile := filepath.Join(root, "corpus.txt")
	original := "# exact provenance\n" + b + "\n\n#literal comment\n" + a + "\n" + b
	writeFile(t, corpusFile, original, 0o600)
	now := time.Date(2026, 8, 13, 8, 0, 0, 0, time.UTC)
	result, err := Enumerate(
		artifact.RepoContext{},
		artifact.LaneContext{AgentType: "qa-orion-cortex", Lane: "qa-orion-cortex"},
		Selection{CorpusFile: corpusFile},
		now,
	)
	if err != nil {
		t.Fatalf("Enumerate(corpus file) error = %v", err)
	}
	if !reflect.DeepEqual(result.Paths, []string{a, b}) {
		t.Fatalf("explicit paths = %#v", result.Paths)
	}
	wantCensus := artifact.Census{
		WindowMetaCount: 3, AgentMetaCount: 3, PairedTranscriptCount: 3,
		SelectedPairedTranscriptCount: 2, OmittedPairedTranscriptCount: 1,
	}
	if result.Census != wantCensus {
		t.Fatalf("explicit census = %#v, want %#v", result.Census, wantCensus)
	}
	sum := sha256.Sum256([]byte(original))
	if result.Window.CorpusFileSHA256 != hex.EncodeToString(sum[:]) || string(result.CorpusFileBytes) != original {
		t.Fatalf("explicit source audit mismatch: %#v", result.Window)
	}

	writeFile(t, corpusFile, "changed after enumeration\n", 0o600)
	stage := newStage(t)
	if err := Write(stage, result); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if got := readFile(t, filepath.Join(stage.Meta, "corpus-file.txt")); got != original {
		t.Fatalf("staged corpus audit = %q, want exact original %q", got, original)
	}
	assertStageArtifacts(t, stage, result)
}

func TestExplicitCorpusRejectsGhostRelativeControlAndNonregularPaths(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "directory")
	mustMkdir(t, directory)
	ghost := filepath.Join(root, "ghost.jsonl")
	tests := []struct {
		name string
		line string
		want string
	}{
		{"relative", "relative.jsonl", "not absolute"},
		{"leading whitespace is not a comment", " #not-a-comment", "not absolute"},
		{"ghost", ghost, "not a readable file"},
		{"directory", directory, "not a readable file"},
		{"control", filepath.Join(root, "bad") + "\tpath", "control character"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			corpusFile := filepath.Join(root, strings.ReplaceAll(test.name, " ", "-")+".txt")
			writeFile(t, corpusFile, test.line+"\n", 0o600)
			_, err := Enumerate(
				artifact.RepoContext{},
				artifact.LaneContext{AgentType: "Explore", Lane: "explorer"},
				Selection{CorpusFile: corpusFile},
				time.Now(),
			)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Enumerate() error = %v, want %q", err, test.want)
			}
		})
	}

	missingCorpus := filepath.Join(root, "missing-corpus.txt")
	_, err := Enumerate(
		artifact.RepoContext{},
		artifact.LaneContext{AgentType: "Explore", Lane: "explorer"},
		Selection{CorpusFile: missingCorpus},
		time.Now(),
	)
	if err == nil || !strings.Contains(err.Error(), "read corpus file") {
		t.Fatalf("missing corpus file error = %v", err)
	}
}

func TestEmptyExplicitCorpusIsAValidSelectionArtifact(t *testing.T) {
	root := t.TempDir()
	corpusFile := filepath.Join(root, "empty.txt")
	original := "# only provenance\n\n# another comment\n"
	writeFile(t, corpusFile, original, 0o600)
	result, err := Enumerate(
		artifact.RepoContext{},
		artifact.LaneContext{AgentType: "Explore", Lane: "explorer"},
		Selection{CorpusFile: corpusFile},
		time.Now(),
	)
	if err != nil {
		t.Fatalf("Enumerate(empty corpus) error = %v", err)
	}
	if len(result.Paths) != 0 || result.Census != (artifact.Census{}) || result.PathsSHA256 != digest(nil) {
		t.Fatalf("empty corpus result = %#v", result)
	}
	stage := newStage(t)
	if err := Write(stage, result); err != nil {
		t.Fatalf("Write(empty corpus) error = %v", err)
	}
	if got := readFile(t, stage.Paths); got != "" {
		t.Fatalf("empty paths artifact = %q", got)
	}
	if got := readFile(t, stage.Pin); got != digest(nil)+"\n" {
		t.Fatalf("empty pin artifact = %q", got)
	}
	if got := readFile(t, filepath.Join(stage.Meta, "corpus-file.txt")); got != original {
		t.Fatalf("empty corpus audit = %q", got)
	}
}

func TestMetadataShapeAccountingDistinguishesInvalidFromOtherAgent(t *testing.T) {
	ctx := newContext(t)
	mtime := time.Date(2026, 8, 12, 8, 0, 0, 0, time.UTC)
	makeMeta(t, ctx.Registry, "array", `[]`, true, mtime)
	makeMeta(t, ctx.Registry, "malformed", `{`, true, mtime)
	makeMeta(t, ctx.Registry, "number", `{"agentType":7}`, true, mtime)
	makeMeta(t, ctx.Registry, "missing", `{}`, true, mtime)
	makeMeta(t, ctx.Registry, "empty", `{"agentType":""}`, true, mtime)
	makeMeta(t, ctx.Registry, "other", `{"agentType":"general-purpose"}`, true, mtime)
	match := makeMeta(t, ctx.Registry, "match", `{"agentType":"Explore"}`, true, mtime)

	result, err := Enumerate(
		ctx,
		artifact.LaneContext{AgentType: "Explore", Lane: "explorer"},
		Selection{BootstrapCount: 20},
		time.Now(),
	)
	if err != nil {
		t.Fatalf("Enumerate() error = %v", err)
	}
	if !reflect.DeepEqual(result.Paths, []string{match.transcript}) {
		t.Fatalf("paths = %#v", result.Paths)
	}
	if result.Census.WindowMetaCount != 7 || result.Census.AgentMetaCount != 1 ||
		result.Census.ExcludedOtherAgentOrInvalidCount != 6 || result.Census.InvalidMetaCount != 4 {
		t.Fatalf("metadata accounting = %#v", result.Census)
	}
}

func TestEnumerationProbeFailuresAreErrorsNotEmptyResults(t *testing.T) {
	root := t.TempDir()
	organ := filepath.Join(root, "organ")
	mustMkdir(t, organ)
	lane := artifact.LaneContext{AgentType: "Explore", Lane: "explorer"}

	_, err := Enumerate(
		artifact.RepoContext{Organ: organ, Registry: filepath.Join(root, "missing-registry")},
		lane,
		Selection{BootstrapCount: 1},
		time.Now(),
	)
	if err == nil || !strings.Contains(err.Error(), "stat registry") {
		t.Fatalf("missing registry error = %v", err)
	}
	registryFile := filepath.Join(root, "registry-file")
	writeFile(t, registryFile, "not a directory", 0o600)
	_, err = Enumerate(
		artifact.RepoContext{Organ: organ, Registry: registryFile},
		lane,
		Selection{BootstrapCount: 1},
		time.Now(),
	)
	if err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("file registry error = %v", err)
	}
	registry := filepath.Join(root, "registry")
	mustMkdir(t, registry)
	_, err = Enumerate(artifact.RepoContext{Organ: organ, Registry: registry}, lane, Selection{}, time.Now())
	if err == nil || !strings.Contains(err.Error(), "read dreamer sweep directory") {
		t.Fatalf("missing sweep directory error = %v", err)
	}
	_, err = Enumerate(artifact.RepoContext{Organ: organ, Registry: registry}, lane, Selection{}, time.Time{})
	if err == nil || !strings.Contains(err.Error(), "time is missing") {
		t.Fatalf("missing now error = %v", err)
	}
}

func TestOnlyClaudeAgentMetadataEntersEnumeration(t *testing.T) {
	ctx := newContext(t)
	writeFile(t, filepath.Join(ctx.Registry, "rollout-2026.jsonl"), "codex seat rollout\n", 0o600)
	writeFile(t, filepath.Join(ctx.Registry, "rollout.meta.json"), `{"agentType":"Explore"}`, 0o600)
	writeFile(t, filepath.Join(ctx.Registry, "worker.meta.json"), `{"agentType":"Explore"}`, 0o600)
	result, err := Enumerate(
		ctx,
		artifact.LaneContext{AgentType: "Explore", Lane: "explorer"},
		Selection{BootstrapCount: 10},
		time.Now(),
	)
	if err != nil {
		t.Fatalf("Enumerate() error = %v", err)
	}
	if result.Census.WindowMetaCount != 0 || len(result.Paths) != 0 {
		t.Fatalf("non-agent files entered corpus: %#v", result)
	}
}

func TestRenderersFailClosedAndRemainByteStable(t *testing.T) {
	if got, err := RenderPaths([]string{"/a", "/b"}); err != nil || got != "/a\n/b\n" {
		t.Fatalf("RenderPaths() = %q, %v", got, err)
	}
	for _, paths := range [][]string{{"/b", "/a"}, {"/a", "/a"}, {"relative"}, {"/bad\tpath"}} {
		if _, err := RenderPaths(paths); err == nil {
			t.Fatalf("RenderPaths(%q) unexpectedly passed", paths)
		}
	}
	gapA := Gap{Kind: GapMissingTranscript, Meta: "/a.meta.json", Transcript: "/a.jsonl"}
	gapB := Gap{Kind: GapMissingTranscript, Meta: "/b.meta.json", Transcript: "/b.jsonl"}
	if got, err := RenderGaps(
		[]Gap{gapA, gapB},
	); err != nil ||
		got != "META-PRESENT-TRANSCRIPT-MISSING\t/a.meta.json\t/a.jsonl\nMETA-PRESENT-TRANSCRIPT-MISSING\t/b.meta.json\t/b.jsonl\n" {
		t.Fatalf("RenderGaps() = %q, %v", got, err)
	}
	if _, err := RenderGaps([]Gap{gapB, gapA}); err == nil {
		t.Fatal("RenderGaps accepted unsorted input")
	}

	now := time.Date(2026, 8, 13, 8, 0, 0, 0, time.UTC)
	window := Window{
		Mode:            WindowBootstrap,
		BootstrapCount:  3,
		AgentType:       "Explore",
		Lane:            "explorer",
		CutoffExclusive: "NONE",
		EnumeratedAt:    now,
	}
	want := "window-mode\tbootstrap-count\nbootstrap-count\t3\nagent-type\tExplore\nlane\texplorer\ncutoff-exclusive\tNONE\nenumerated-at\t2026-08-13T08:00:00Z\n"
	if got, err := RenderWindow(window); err != nil || got != want {
		t.Fatalf("RenderWindow() = %q, %v; want %q", got, err, want)
	}
	window.BootstrapCount = 0
	if _, err := RenderWindow(window); err == nil {
		t.Fatal("RenderWindow accepted zero bootstrap count")
	}
	window = Window{
		Mode:             WindowExplicitCorpus,
		CorpusFile:       "/corpus",
		CorpusFileSHA256: strings.Repeat("A", 64),
		AgentType:        "Explore",
		Lane:             "explorer",
		CutoffExclusive:  "NONE",
		EnumeratedAt:     now,
	}
	if _, err := RenderWindow(window); err == nil {
		t.Fatal("RenderWindow accepted non-lowercase digest")
	}
}

func TestWriteRejectsMutatedEvidenceAndEscapedLayout(t *testing.T) {
	root := t.TempDir()
	transcript := filepath.Join(root, "one.jsonl")
	writeFile(t, transcript, "{}\n", 0o600)
	corpusFile := filepath.Join(root, "corpus.txt")
	writeFile(t, corpusFile, transcript+"\n", 0o600)
	result, err := Enumerate(
		artifact.RepoContext{},
		artifact.LaneContext{AgentType: "Explore", Lane: "explorer"},
		Selection{CorpusFile: corpusFile},
		time.Now(),
	)
	if err != nil {
		t.Fatalf("Enumerate() error = %v", err)
	}

	t.Run("paths digest", func(t *testing.T) {
		mutated := result
		mutated.PathsSHA256 = strings.Repeat("0", 64)
		if err := Write(newStage(t), mutated); err == nil || !strings.Contains(err.Error(), "paths digest mismatch") {
			t.Fatalf("Write() error = %v", err)
		}
	})
	t.Run("source digest", func(t *testing.T) {
		mutated := result
		mutated.CorpusFileBytes = []byte("mutated")
		if err := Write(
			newStage(t),
			mutated,
		); err == nil ||
			!strings.Contains(err.Error(), "corpus file digest mismatch") {
			t.Fatalf("Write() error = %v", err)
		}
	})
	t.Run("census", func(t *testing.T) {
		mutated := result
		mutated.Census.SelectedPairedTranscriptCount = 0
		if err := Write(newStage(t), mutated); err == nil || !strings.Contains(err.Error(), "selected count") {
			t.Fatalf("Write() error = %v", err)
		}
	})
	t.Run("layout escape", func(t *testing.T) {
		stage := newStage(t)
		stage.Paths = filepath.Join(t.TempDir(), "escaped-paths.txt")
		if err := Write(stage, result); err == nil || !strings.Contains(err.Error(), "outside stage root") {
			t.Fatalf("Write() error = %v", err)
		}
	})
}

func TestEnumerateIsDeterministicForFrozenInputs(t *testing.T) {
	ctx := newContext(t)
	mtime := time.Date(2026, 8, 12, 8, 0, 0, 123, time.UTC)
	makeMeta(t, ctx.Registry, "b", `{"agentType":"Explore"}`, true, mtime)
	makeMeta(t, ctx.Registry, "a", `{"agentType":"Explore"}`, true, mtime)
	now := time.Date(2026, 8, 13, 8, 0, 0, 0, time.UTC)
	first, err := Enumerate(
		ctx,
		artifact.LaneContext{AgentType: "Explore", Lane: "explorer"},
		Selection{BootstrapCount: 2},
		now,
	)
	if err != nil {
		t.Fatalf("first Enumerate() error = %v", err)
	}
	second, err := Enumerate(
		ctx,
		artifact.LaneContext{AgentType: "Explore", Lane: "explorer"},
		Selection{BootstrapCount: 2},
		now,
	)
	if err != nil {
		t.Fatalf("second Enumerate() error = %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("frozen enumeration changed:\nfirst=%#v\nsecond=%#v", first, second)
	}
}

type metaFixture struct {
	meta       string
	transcript string
}

func makeMeta(t *testing.T, registry, name, body string, paired bool, mtime time.Time) metaFixture {
	t.Helper()
	directory := filepath.Join(registry, name)
	mustMkdir(t, directory)
	meta := filepath.Join(directory, "agent-"+name+".meta.json")
	transcript := filepath.Join(directory, "agent-"+name+".jsonl")
	writeFile(t, meta, body+"\n", 0o600)
	if paired {
		writeFile(t, transcript, "{}\n", 0o600)
	}
	if err := os.Chtimes(meta, mtime, mtime); err != nil {
		t.Fatalf("Chtimes(%s): %v", meta, err)
	}
	return metaFixture{meta: meta, transcript: transcript}
}

func newContext(t *testing.T) artifact.RepoContext {
	t.Helper()
	root := t.TempDir()
	organ := filepath.Join(root, "organ")
	registry := filepath.Join(root, "registry")
	mustMkdir(t, filepath.Join(organ, "dreamer"))
	mustMkdir(t, registry)
	return artifact.RepoContext{RepoRoot: root, Organ: organ, Registry: registry}
}

func newOrgan(t *testing.T) string {
	t.Helper()
	organ := filepath.Join(t.TempDir(), "organ")
	mustMkdir(t, filepath.Join(organ, "dreamer"))
	return organ
}

func newStage(t *testing.T) artifact.StageLayout {
	t.Helper()
	root := filepath.Join(t.TempDir(), "stage")
	meta := filepath.Join(root, "meta")
	maps := filepath.Join(root, "maps")
	mustMkdir(t, root)
	mustMkdir(t, meta)
	mustMkdir(t, maps)
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatalf("Chmod(%s): %v", root, err)
	}
	return artifact.StageLayout{
		Root: root, Meta: meta, Maps: maps,
		Paths: filepath.Join(root, "paths.txt"),
		Pin:   filepath.Join(root, "paths.sha256"),
	}
}

func assertStageArtifacts(t *testing.T, stage artifact.StageLayout, result Result) {
	t.Helper()
	pathsRaw, err := RenderPaths(result.Paths)
	if err != nil {
		t.Fatalf("RenderPaths() error = %v", err)
	}
	if got := readFile(t, stage.Paths); got != pathsRaw {
		t.Fatalf("paths.txt = %q, want %q", got, pathsRaw)
	}
	if got := readFile(t, stage.Pin); got != result.PathsSHA256+"\n" {
		t.Fatalf("paths.sha256 = %q", got)
	}
	windowRaw, err := RenderWindow(result.Window)
	if err != nil {
		t.Fatalf("RenderWindow() error = %v", err)
	}
	if got := readFile(t, filepath.Join(stage.Meta, "window.tsv")); got != windowRaw {
		t.Fatalf("window.tsv = %q, want %q", got, windowRaw)
	}
	for _, path := range []string{
		stage.Paths,
		stage.Pin,
		filepath.Join(stage.Root, "gaps.tsv"),
		filepath.Join(stage.Root, "census.tsv"),
		filepath.Join(stage.Meta, "window.tsv"),
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("Stat(%s): %v", path, err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("mode(%s) = %#o, want 0600", path, info.Mode().Perm())
		}
	}
	if len(result.Paths) > 0 {
		sum := sha256.Sum256([]byte(pathsRaw))
		if result.PathsSHA256 != hex.EncodeToString(sum[:]) {
			t.Fatalf("paths digest = %s", result.PathsSHA256)
		}
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatalf("MkdirAll(%s): %v", path, err)
	}
}

func writeFile(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	return string(raw)
}
