package dream

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"hostops/pfm/internal/dream/artifact"
	"hostops/pfm/internal/dream/corpus"
	"hostops/pfm/internal/dream/gate"
	"hostops/pfm/internal/dream/seat"
)

func TestNightLunaLawIsTheFirstEffectBarrier(t *testing.T) {
	effects := 0
	dependencies := DefaultNightDependencies()
	dependencies.SeatLaw.Distill.Model = "not-luna"
	dependencies.NewSeatRunner = func(seat.EventSink) (NightSeatRunner, error) {
		effects++
		return nil, nil
	}
	dependencies.Git = func(string) NightGitReader {
		effects++
		return nil
	}
	dependencies.Clock = func() time.Time {
		effects++
		return time.Now()
	}
	dependencies.AcquireLock = func(string) (func() error, error) {
		effects++
		return func() error { return nil }, nil
	}

	_, err := Night(context.Background(), NightRequest{}, dependencies)
	if err == nil || !strings.Contains(err.Error(), "luna law") {
		t.Fatalf("Night() error = %v, want luna-law refusal", err)
	}
	if effects != 0 {
		t.Fatalf("luna-law refusal caused %d dependency effects, want zero", effects)
	}
}

func TestNightEmptyCorpusSucceedsBeforeLogsSeatsAndGates(t *testing.T) {
	fixture := newNightFixture(t, "")
	dreamerRoot := filepath.Join(fixture.organ, "dreamer")
	writeNightTest(t, filepath.Join(dreamerRoot, "2026-08-10.md"), "lane\texplorer\nEND-OF-SWEEP\n")
	ledgerBefore := snapshotNightDirectory(t, dreamerRoot)
	var stdout bytes.Buffer
	seatFactoryCalls := 0
	dependencies := fixture.dependencies(&stdout)
	dependencies.NewSeatRunner = func(seat.EventSink) (NightSeatRunner, error) {
		seatFactoryCalls++
		return nil, fmt.Errorf("seat factory must not run")
	}

	result, err := Night(context.Background(), fixture.request(), dependencies)
	if err != nil {
		t.Fatalf("Night() error = %v", err)
	}
	if !result.Empty || result.ApplyEligible || result.Applied || result.HoldState != "" {
		t.Fatalf("Night() result = %+v, want empty non-apply outcome", result)
	}
	if seatFactoryCalls != 0 {
		t.Fatalf("seat factory calls = %d, want zero", seatFactoryCalls)
	}
	if _, err := os.Stat(result.Stage.Root); !os.IsNotExist(err) {
		t.Fatalf("empty stage survives at %s: %v", result.Stage.Root, err)
	}
	logs, err := os.ReadDir(filepath.Join(fixture.organ, "tmp", "logs"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read logs directory: %v", err)
	}
	if len(logs) != 0 {
		t.Fatalf("empty corpus created logs: %v", logs)
	}
	if ledgerAfter := snapshotNightDirectory(t, dreamerRoot); !reflect.DeepEqual(ledgerAfter, ledgerBefore) {
		t.Fatalf("empty corpus changed dreamer ledger:\n before=%q\n  after=%q", ledgerBefore, ledgerAfter)
	}
	if got := strings.TrimSpace(stdout.String()); !strings.Contains(got, "EMPTY-WINDOW") {
		t.Fatalf("stdout = %q, want EMPTY-WINDOW", got)
	}
	if strings.Count(stdout.String(), "\n") != 1 {
		t.Fatalf("empty-window stdout has more than one line: %q", stdout.String())
	}
}

func TestNightRunsWithEmbeddedResourcesAndNoProfessorHome(t *testing.T) {
	isolatedHome := t.TempDir()
	t.Setenv("HOME", isolatedHome)
	t.Setenv("PFM_HOME", isolatedHome)

	fixture := newNightFixture(t, "")
	if err := os.RemoveAll(fixture.resourcesRoot); err != nil {
		t.Fatal(err)
	}
	request := fixture.request()
	request.ResourcesRoot = ""
	dependencies := fixture.dependencies(&bytes.Buffer{})
	dependencies.NewSeatRunner = func(seat.EventSink) (NightSeatRunner, error) {
		t.Fatal("empty embedded-resource night spent a seat")
		return nil, nil
	}

	result, err := Night(context.Background(), request, dependencies)
	if err != nil {
		t.Fatalf("Night() without on-disk resources error = %v", err)
	}
	if !result.Empty || result.Lane.Lane != "tracer" {
		t.Fatalf("Night() without on-disk resources = %+v", result)
	}
	if _, err := os.Lstat(filepath.Join(isolatedHome, ".professor")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Night() consulted or created HOME/.professor: %v", err)
	}
}

func snapshotNightDirectory(t *testing.T, root string) map[string]string {
	t.Helper()
	snapshot := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		snapshot[relative] = string(raw)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestNightRunsGatesBetweenSeatsAndHoldsWithoutApplying(t *testing.T) {
	fixture := newNightFixture(t, "transcript\n")
	var stdout bytes.Buffer
	prepared := &fakePreparedNight{fixture: fixture}
	runner := &fakeNightRunner{prepared: prepared}
	dependencies := fixture.dependencies(&stdout)
	dependencies.NewSeatRunner = func(seat.EventSink) (NightSeatRunner, error) {
		prepared.events = append(prepared.events, "runner")
		return runner, nil
	}

	result, err := Night(context.Background(), fixture.request(), dependencies)
	if err != nil {
		t.Fatalf("Night() error = %v", err)
	}
	if result.Empty || !result.ApplyEligible || result.Applied || result.HoldState != artifact.HoldReady ||
		result.Survivors != 1 ||
		result.Yield != 1 {
		t.Fatalf("Night() result = %+v, want one READY unapplied survivor", result)
	}
	wantOrder := []string{"runner", "prepare", "distill", "refiner"}
	if !reflect.DeepEqual(prepared.events, wantOrder) {
		t.Fatalf("seat order = %v, want %v", prepared.events, wantOrder)
	}
	for _, path := range []string{
		"gate-pin.log", "gate-pin-post-distill.log", "gate-coverage.log", "gate-anchors.log",
		"gate-pin-post-refine.log", "gate-verdicts.log", "gate-anchors-postrefine.log",
		"anchor-results.tsv", "anchor-survivors.txt", "anchor-postrefine.tsv",
		"anchor-postrefine-survivors.txt", "READY-FOR-APPLY",
	} {
		if _, err := os.Stat(filepath.Join(result.Stage.Root, path)); err != nil {
			t.Errorf("required staged artifact %s: %v", path, err)
		}
	}
	ready := readNightTest(t, filepath.Join(result.Stage.Root, "READY-FOR-APPLY"))
	if !strings.HasPrefix(ready, "READY\t") {
		t.Fatalf("READY-FOR-APPLY = %q", ready)
	}
	if _, err := os.Stat(filepath.Join(result.Stage.Root, "APPLIED")); !os.IsNotExist(err) {
		t.Fatalf("Night wrote APPLIED: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(fixture.organ, "maps"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("Night mutated organ maps: %v", entries)
	}
	if strings.Contains(stdout.String(), "signed apply command") || strings.Contains(stdout.String(), "APPLIED") {
		t.Fatalf("Night advertised or performed apply: %q", stdout.String())
	}
	structured := readNightTest(t, result.Stage.StructuredLog)
	for _, fact := range []string{`"phase":"start"`, `"recorded_tree":"` + fixture.tree + `"`, `"gate":"PIN"`, `"gate":"COVERAGE+CONDUCT"`, `"gate":"ANCHORS"`, `"gate":"VERDICTS"`, `"state":"READY"`, `"exit_reason":"READY"`} {
		if !strings.Contains(structured, fact) {
			t.Errorf("structured log lacks %s\n%s", fact, structured)
		}
	}
	for _, fact := range []string{"PREFLIGHT", "PIN PASS", "COVERAGE PASS", "ANCHORS PASS", "VERDICTS PASS", "HOLD-BEFORE-APPLY"} {
		if !strings.Contains(readNightTest(t, result.Stage.HumanLog), fact) {
			t.Errorf("human log lacks %s", fact)
		}
	}
}

func TestNightZeroYieldIsDistinctAndStillNeverAutoApplies(t *testing.T) {
	fixture := newNightFixture(t, "transcript\n")
	prepared := &fakePreparedNight{fixture: fixture, verdict: "REFUTE"}
	dependencies := fixture.dependencies(&bytes.Buffer{})
	dependencies.NewSeatRunner = func(seat.EventSink) (NightSeatRunner, error) {
		return &fakeNightRunner{prepared: prepared}, nil
	}

	result, err := Night(context.Background(), fixture.request(), dependencies)
	if err != nil {
		t.Fatalf("Night() error = %v", err)
	}
	if result.HoldState != artifact.HoldZeroYield || !result.ApplyEligible || result.Yield != 0 || result.Applied {
		t.Fatalf("Night() result = %+v, want explicit ZERO-YIELD hold", result)
	}
	if _, err := os.Stat(filepath.Join(result.Stage.Root, "APPLIED")); !os.IsNotExist(err) {
		t.Fatalf("ZERO-YIELD night wrote APPLIED: %v", err)
	}
	if line := readNightTest(
		t,
		filepath.Join(result.Stage.Root, "READY-FOR-APPLY"),
	); !strings.HasPrefix(
		line,
		"ZERO-YIELD\t",
	) {
		t.Fatalf("READY marker = %q", line)
	}
}

func TestNightZeroSurvivorsIsLoudAndOffersNoApply(t *testing.T) {
	fixture := newNightFixture(t, "transcript\n")
	var stdout bytes.Buffer
	prepared := &fakePreparedNight{fixture: fixture, noMaps: true}
	dependencies := fixture.dependencies(&stdout)
	dependencies.NewSeatRunner = func(seat.EventSink) (NightSeatRunner, error) {
		return &fakeNightRunner{prepared: prepared}, nil
	}

	result, err := Night(context.Background(), fixture.request(), dependencies)
	if err != nil {
		t.Fatalf("Night() error = %v", err)
	}
	if result.HoldState != artifact.HoldZeroSurvivors || result.ApplyEligible || result.Applied ||
		result.Survivors != 0 ||
		result.Yield != 0 {
		t.Fatalf("Night() result = %+v, want ZERO-SURVIVORS without apply", result)
	}
	if prepared.refinerCalls != 0 {
		t.Fatalf("refiner calls = %d, want zero", prepared.refinerCalls)
	}
	if got := stdout.String(); !strings.Contains(got, "HOLD-BEFORE-APPLY ZERO-SURVIVORS") ||
		!strings.Contains(
			got,
			"no signed apply command",
		) || containsLinePrefix(got, "dreamer-night: signed apply command:") {
		t.Fatalf("zero-survivor output = %q", got)
	}
	if line := readNightTest(
		t,
		filepath.Join(result.Stage.Root, "READY-FOR-APPLY"),
	); !strings.HasPrefix(
		line,
		"ZERO-SURVIVORS\t",
	) {
		t.Fatalf("READY marker = %q", line)
	}
	if body := readNightTest(
		t,
		filepath.Join(result.Stage.Root, "refiner-seat.log"),
	); body != "VERIFY SKIP zero anchor-valid staged maps\n" {
		t.Fatalf("refiner skip log = %q", body)
	}
}

func TestNightWiresOrganLockAndProfileAndFailsOnRefinerMapSetChange(t *testing.T) {
	fixture := newNightFixture(t, "transcript\n")
	prepared := &fakePreparedNight{fixture: fixture, addMapDuringRefine: true}
	dependencies := fixture.dependencies(&bytes.Buffer{})
	lockCalls, releases := 0, 0
	dependencies.AcquireLock = func(organ string) (func() error, error) {
		if organ != fixture.organ {
			t.Fatalf("lock organ = %s, want %s", organ, fixture.organ)
		}
		lockCalls++
		return func() error { releases++; return nil }, nil
	}
	dependencies.NewSeatRunner = func(seat.EventSink) (NightSeatRunner, error) {
		return &fakeNightRunner{prepared: prepared}, nil
	}

	result, err := Night(context.Background(), fixture.request(), dependencies)
	if err == nil || !strings.Contains(err.Error(), "staged map set changed during refinement") {
		t.Fatalf("Night() error = %v, want map-set change failure", err)
	}
	if lockCalls != 1 || releases != 1 {
		t.Fatalf("lock calls/releases = %d/%d, want 1/1", lockCalls, releases)
	}
	if !strings.Contains(
		readNightTest(t, filepath.Join(result.Stage.Root, "distill-brief.md")),
		"Lane `tracer` fixture.",
	) {
		t.Fatal("organ-resolved lane profile did not reach distill brief")
	}
	if _, err := os.Stat(filepath.Join(result.Stage.Root, "FAILED")); err != nil {
		t.Fatalf("failed night lacks FAILED marker: %v", err)
	}
}

func TestNightPinRetainsPreflightDigestWhenSeatRewritesPathsAndPinTogether(t *testing.T) {
	fixture := newNightFixture(t, "transcript\n")
	prepared := &fakePreparedNight{fixture: fixture, rewriteCorpusPin: true}
	dependencies := fixture.dependencies(&bytes.Buffer{})
	dependencies.NewSeatRunner = func(seat.EventSink) (NightSeatRunner, error) {
		return &fakeNightRunner{prepared: prepared}, nil
	}

	result, err := Night(context.Background(), fixture.request(), dependencies)
	if err == nil || !strings.Contains(err.Error(), "corpus changed from preflight digest") {
		t.Fatalf("Night() error = %v, want retained preflight PIN failure", err)
	}
	if prepared.refinerCalls != 0 {
		t.Fatalf("refiner calls = %d, want zero after post-distill PIN failure", prepared.refinerCalls)
	}
	if _, statErr := os.Stat(filepath.Join(result.Stage.Root, "FAILED")); statErr != nil {
		t.Fatalf("PIN failure lacks FAILED marker: %v", statErr)
	}
}

func TestNightPreflightFailureWritesDurableMarkerAndNextSuccessClearsIt(t *testing.T) {
	fixture := newNightFixture(t, "")
	badMap := filepath.Join(fixture.organ, "maps", "missing-question.md")
	writeNightTest(t, badMap, "# Missing Question\n\n## Answer\n\nanswer\n")
	dependencies := fixture.dependencies(&bytes.Buffer{})
	dependencies.NewSeatRunner = func(seat.EventSink) (NightSeatRunner, error) {
		t.Fatal("preflight failure spent a seat")
		return nil, nil
	}

	result, err := Night(context.Background(), fixture.request(), dependencies)
	if err == nil || !strings.Contains(err.Error(), "map lacks Question") {
		t.Fatalf("Night() error = %v, want missing Question", err)
	}
	marker := nightFailurePath(fixture.organ)
	body := readNightTest(t, marker)
	for _, want := range []string{
		"Phase: PREFLIGHT-FAILED\n",
		"Reason: build cached map questions: dedup map " + badMap + ": map lacks Question\n",
		"Path: " + badMap + "\n",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("night failure marker lacks %q:\n%s", want, body)
		}
	}

	writeNightTest(t, badMap, "# Recovered\n\n## Question\n\nWhat recovered?\n\n## Answer\n\nanswer\n")
	result, err = Night(context.Background(), fixture.request(), dependencies)
	if err != nil || !result.Empty {
		t.Fatalf("successful recovery Night() = %+v, %v", result, err)
	}
	if _, err := os.Lstat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("successful night left durable failure marker: %v", err)
	}
}

func TestNightBrokenOrganWritesDurableMarkerBeforeValidation(t *testing.T) {
	fixture := newNightFixture(t, "")
	missingMaps := filepath.Join(fixture.organ, "maps")
	if err := os.RemoveAll(missingMaps); err != nil {
		t.Fatal(err)
	}
	dependencies := fixture.dependencies(&bytes.Buffer{})
	dependencies.NewSeatRunner = func(seat.EventSink) (NightSeatRunner, error) {
		t.Fatal("broken-organ preflight spent a seat")
		return nil, nil
	}

	result, err := Night(context.Background(), fixture.request(), dependencies)
	if err == nil || !strings.Contains(err.Error(), "missing directory: "+missingMaps) {
		t.Fatalf("Night() = %+v, %v, want broken-organ failure", result, err)
	}
	if result.Stage.Root != "" {
		t.Fatalf("broken-organ failure created stage %s", result.Stage.Root)
	}
	body := readNightTest(t, nightFailurePath(fixture.organ))
	for _, want := range []string{
		"Phase: PREFLIGHT-FAILED\n",
		"Reason: validate dream organ: missing directory: " + missingMaps + "\n",
		"Path: " + missingMaps + "\n",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("night failure marker lacks %q:\n%s", want, body)
		}
	}
}

func TestNightCanceledBeforePreflightWritesDurableMarker(t *testing.T) {
	fixture := newNightFixture(t, "")
	dependencies := fixture.dependencies(&bytes.Buffer{})
	dependencies.NewSeatRunner = func(seat.EventSink) (NightSeatRunner, error) {
		t.Fatal("canceled preflight spent a seat")
		return nil, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := Night(ctx, fixture.request(), dependencies)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Night() = %+v, %v, want context.Canceled", result, err)
	}
	if result.Stage.Root != "" {
		t.Fatalf("canceled preflight created stage %s", result.Stage.Root)
	}
	body := readNightTest(t, nightFailurePath(fixture.organ))
	for _, want := range []string{
		"Phase: PREFLIGHT-FAILED\n",
		"Reason: dream night canceled before preflight: context canceled\n",
		"Path: " + fixture.organ + "\n",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("night failure marker lacks %q:\n%s", want, body)
		}
	}
}

func TestNightSymlinkPromptNamesExactResourceInDurableMarker(t *testing.T) {
	fixture := newNightFixture(t, "")
	unsafePrompt := filepath.Join(fixture.resourcesRoot, distillPromptFile)
	if err := os.Remove(unsafePrompt); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(
		filepath.Join(fixture.resourcesRoot, refinerPromptFile),
		unsafePrompt,
	); err != nil {
		t.Fatal(err)
	}
	dependencies := fixture.dependencies(&bytes.Buffer{})
	dependencies.NewSeatRunner = func(seat.EventSink) (NightSeatRunner, error) {
		t.Fatal("unsafe-prompt preflight spent a seat")
		return nil, nil
	}

	result, err := Night(context.Background(), fixture.request(), dependencies)
	if err == nil || !strings.Contains(err.Error(), "resource path is a symlink: "+unsafePrompt) {
		t.Fatalf("Night() = %+v, %v, want symlink prompt refusal", result, err)
	}
	body := readNightTest(t, nightFailurePath(fixture.organ))
	if !strings.Contains(body, "Path: "+unsafePrompt+"\n") {
		t.Fatalf("night failure marker does not name unsafe prompt:\n%s", body)
	}
}

func TestNightGhostCorpusPathNamesExactTranscriptInDurableMarker(t *testing.T) {
	fixture := newNightFixture(t, "")
	ghost := filepath.Join(filepath.Dir(fixture.transcript), "ghost.jsonl")
	writeNightTest(t, fixture.corpusFile, ghost+"\n")
	dependencies := fixture.dependencies(&bytes.Buffer{})
	dependencies.NewSeatRunner = func(seat.EventSink) (NightSeatRunner, error) {
		t.Fatal("ghost-corpus preflight spent a seat")
		return nil, nil
	}

	result, err := Night(context.Background(), fixture.request(), dependencies)
	if err == nil || !strings.Contains(err.Error(), "corpus path is not a readable file: "+ghost) {
		t.Fatalf("Night() = %+v, %v, want ghost corpus failure", result, err)
	}
	body := readNightTest(t, nightFailurePath(fixture.organ))
	if !strings.Contains(body, "Path: "+ghost+"\n") {
		t.Fatalf("night failure marker does not name ghost transcript:\n%s", body)
	}
}

type nightFixture struct {
	t             *testing.T
	repo          string
	organ         string
	registryBase  string
	resourcesRoot string
	corpusFile    string
	transcript    string
	started       time.Time
	now           time.Time
	tree          string
}

func newNightFixture(t *testing.T, transcriptBody string) *nightFixture {
	t.Helper()
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	organRoot := filepath.Join(repo, ".professor", "stm")
	registryBase := filepath.Join(root, "registry")
	registry := filepath.Join(registryBase, strings.ReplaceAll(repo, string(filepath.Separator), "-"))
	resourcesRoot := filepath.Join(root, "resources")
	transcript := filepath.Join(root, "agent.jsonl")
	corpusFile := filepath.Join(root, "corpus.txt")
	for _, directory := range []string{
		repo, filepath.Join(organRoot, "maps"), filepath.Join(organRoot, "dreamer"),
		filepath.Join(organRoot, "archive"), registry, filepath.Join(resourcesRoot, "lanes"),
	} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeNightTest(t, filepath.Join(repo, "a.txt"), "a\n")
	writeNightTest(t, filepath.Join(repo, "b.txt"), "b\n")
	writeNightTest(t, filepath.Join(organRoot, "stm.md"), "# Index\n")
	writeNightTest(t, filepath.Join(resourcesRoot, "lanes", "tracer.md"), "Serves: Explore\n\nLane `tracer` fixture.\n")
	writeNightTest(t, filepath.Join(resourcesRoot, distillPromptFile), "DISTILL TEMPLATE\n")
	writeNightTest(t, filepath.Join(resourcesRoot, refinerPromptFile), "REFINER TEMPLATE\n")
	writeNightTest(t, transcript, transcriptBody)
	corpusBody := ""
	if transcriptBody != "" {
		corpusBody = transcript + "\n"
	}
	writeNightTest(t, corpusFile, corpusBody)
	runNightGit(t, repo, "init", "-q")
	runNightGit(t, repo, "config", "user.name", "Dream Test")
	runNightGit(t, repo, "config", "user.email", "dream@example.invalid")
	runNightGit(t, repo, "add", ".")
	runNightGit(t, repo, "commit", "-qm", "fixture")
	tree := strings.TrimSpace(runNightGit(t, repo, "rev-parse", "HEAD"))
	return &nightFixture{
		t: t, repo: repo, organ: organRoot, registryBase: registryBase,
		resourcesRoot: resourcesRoot, corpusFile: corpusFile, transcript: transcript,
		started: time.Date(2026, 8, 13, 1, 2, 3, 0, time.FixedZone("CEST", 2*60*60)),
		now:     time.Date(2026, 8, 13, 2, 3, 4, 0, time.FixedZone("CEST", 2*60*60)),
		tree:    tree,
	}
}

func (fixture *nightFixture) request() NightRequest {
	return NightRequest{
		RepoRoot: fixture.repo, RegistryBase: fixture.registryBase,
		ResourcesRoot: fixture.resourcesRoot, AgentType: "Explore",
		Selection: corpus.Selection{CorpusFile: fixture.corpusFile}, StartedAt: fixture.started,
	}
}

func (fixture *nightFixture) dependencies(stdout *bytes.Buffer) NightDependencies {
	dependencies := DefaultNightDependencies()
	dependencies.Git = func(string) NightGitReader {
		return fakeNightGit{fixture: fixture}
	}
	dependencies.Clock = func() time.Time { return fixture.now }
	dependencies.AcquireLock = func(organ string) (func() error, error) {
		if organ != fixture.organ {
			return nil, fmt.Errorf("lock organ = %s", organ)
		}
		return func() error { return nil }, nil
	}
	dependencies.Stdout = stdout
	dependencies.Stderr = &bytes.Buffer{}
	return dependencies
}

type fakeNightGit struct {
	fixture *nightFixture
}

func (git fakeNightGit) Head(context.Context) (string, error) {
	return git.fixture.tree, nil
}

func (git fakeNightGit) Resolve(tree, path string) (gate.GitObject, bool, error) {
	if tree != git.fixture.tree {
		return gate.GitObject{}, false, fmt.Errorf("tree = %s", tree)
	}
	if path == "" {
		return gate.GitObject{Hash: tree, Type: artifact.GitTree}, true, nil
	}
	if path != "a.txt" && path != "b.txt" {
		return gate.GitObject{}, false, nil
	}
	hash := strings.TrimSpace(runNightGit(git.fixture.t, git.fixture.repo, "rev-parse", tree+":"+path))
	return gate.GitObject{Hash: hash, Type: artifact.GitBlob}, true, nil
}

type fakeNightRunner struct {
	prepared *fakePreparedNight
}

func (runner *fakeNightRunner) PrepareNight(
	_ context.Context,
	law seat.SeatLaw,
	stage string,
) (NightPreparedSeats, error) {
	if law != seat.RequiredSeatLaw() {
		return nil, fmt.Errorf("wrong seat law: %+v", law)
	}
	runner.prepared.stage = stage
	runner.prepared.events = append(runner.prepared.events, "prepare")
	return runner.prepared, nil
}

type fakePreparedNight struct {
	fixture            *nightFixture
	stage              string
	verdict            string
	noMaps             bool
	addMapDuringRefine bool
	rewriteCorpusPin   bool
	refinerCalls       int
	events             []string
}

func (night *fakePreparedNight) Config() seat.PinnedConfig {
	return seat.PinnedConfig{Model: seat.SeatModel, Effort: seat.SeatEffort}
}

func (night *fakePreparedNight) Verification() seat.Verification {
	return seat.Verification{ConfigLoaded: true, Limitation: seat.MCPVerificationLimitation}
}

func (night *fakePreparedNight) RunDistill(_ context.Context, input seat.SeatInput) (seat.SeatResult, error) {
	night.events = append(night.events, "distill")
	if input.Prompt == "" || !strings.Contains(input.Prompt, night.fixture.transcript) {
		return seat.SeatResult{}, fmt.Errorf("distill prompt omitted transcript")
	}
	coverage := "1\tREAD\tfixture read\n" +
		"CONDUCT\ttechnique\tNONE\tfixture had no durable technique\n" +
		"CONDUCT\tprior\tNONE\tfixture had no corrected prior\n" +
		"CONDUCT\tbaseline\tNONE\tfixture had no durable baseline\n" +
		"END-OF-RUN\n"
	writeNightTest(night.fixture.t, filepath.Join(night.stage, "coverage.md"), coverage)
	if night.rewriteCorpusPin {
		pathsRaw := []byte("/different/transcript.jsonl\n")
		digest := sha256.Sum256(pathsRaw)
		writeNightTest(night.fixture.t, filepath.Join(night.stage, "paths.txt"), string(pathsRaw))
		writeNightTest(night.fixture.t, filepath.Join(night.stage, "paths.sha256"), hex.EncodeToString(digest[:])+"\n")
	}
	if night.noMaps {
		return seat.SeatResult{
			Name:          input.Name,
			ExitReason:    "idle",
			LastAssistant: "no maps",
			SessionID:     "d",
			RolloutPath:   "/rollout/d",
		}, nil
	}
	hashA := strings.TrimSpace(runNightGit(night.fixture.t, night.fixture.repo, "rev-parse", night.fixture.tree+":a.txt"))[:12]
	hashB := strings.TrimSpace(runNightGit(night.fixture.t, night.fixture.repo, "rev-parse", night.fixture.tree+":b.txt"))[:12]
	mapBody := fmt.Sprintf(
		"# Fixture map\n\n## Question\n\nWhat is the fixture?\n\n## Answer\n\nIt is pinned.\n\n## Derivation trail\n\nRead both files.\n\nProvenance: 2026-08-13 · sid abcdef12\n\n## Anchors\n\n- `a.txt` — blob `%s`\n- `b.txt` — blob `%s`\n",
		hashA,
		hashB,
	)
	writeNightTest(night.fixture.t, filepath.Join(night.stage, "maps", "fixture-map.md"), mapBody)
	return seat.SeatResult{
		Name:          input.Name,
		ExitReason:    "idle",
		LastAssistant: "distilled",
		SessionID:     "d",
		RolloutPath:   "/rollout/d",
	}, nil
}

func (night *fakePreparedNight) RunRefiner(_ context.Context, input seat.SeatInput) (seat.SeatResult, error) {
	night.refinerCalls++
	night.events = append(night.events, "refiner")
	for _, path := range []string{"gate-pin-post-distill.log", "coverage.md.expanded", "anchor-results.tsv", "anchor-survivors.txt"} {
		if _, err := os.Stat(filepath.Join(night.stage, path)); err != nil {
			return seat.SeatResult{}, fmt.Errorf("refiner ran before %s: %w", path, err)
		}
	}
	verdict := night.verdict
	if verdict == "" {
		verdict = "CONFIRM"
	}
	writeNightTest(
		night.fixture.t,
		filepath.Join(night.stage, "verdicts.md"),
		verdict+"\tmaps/fixture-map.md\tfixture evidence\n",
	)
	if night.addMapDuringRefine {
		writeNightTest(
			night.fixture.t,
			filepath.Join(night.stage, "maps", "injected-map.md"),
			readNightTest(night.fixture.t, filepath.Join(night.stage, "maps", "fixture-map.md")),
		)
	}
	return seat.SeatResult{
		Name:          input.Name,
		ExitReason:    "idle",
		LastAssistant: "verified",
		SessionID:     "r",
		RolloutPath:   "/rollout/r",
	}, nil
}

func writeNightTest(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readNightTest(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func runNightGit(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, arguments...)...)
	command.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", arguments, err, output)
	}
	return string(output)
}

func containsLinePrefix(body, prefix string) bool {
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}
