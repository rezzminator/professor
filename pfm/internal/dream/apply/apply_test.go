package apply

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
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"hostops/pfm/internal/dream/artifact"
	"hostops/pfm/internal/dream/gate"
	"hostops/pfm/internal/dream/organ"
)

var applyNow = time.Date(2026, 8, 13, 9, 10, 11, 0, time.FixedZone("fixture", 2*60*60))

type fixture struct {
	repo         artifact.RepoContext
	lane         artifact.LaneContext
	stage        artifact.StageLayout
	recordedTree string
	aHash        string
	docsHash     string
	mapRaw       map[string]string
}

func TestRunAppliesEveryVerdictRouteAtRecordedTreeAndRendersDeterministically(t *testing.T) {
	f := newFixture(t, map[string]string{
		"confirm.md": "Confirm",
		"amend.md":   "Amend",
		"refute.md":  "Refute",
		"unruled.md": "Unruled",
	}, []string{
		"CONFIRM\tmaps/confirm.md\tconfirmed evidence",
		"AMEND\tmaps/amend.md\tamended evidence",
		"REFUTE\tmaps/refute.md\trefuted evidence",
	}, artifact.HoldReady)
	writePrivateTest(t, filepath.Join(f.repo.Organ, "explorer-index.md"), []byte("LEGACY SURFACE\n"))
	writePrivateTest(t, f.stage.NormalizedVerdicts, []byte("CONFIRM\tmaps/unruled.md\thand edit back door\n"))

	// HEAD motion after preflight is deliberately irrelevant. The apply still
	// verifies and restamps at recordedTree, not this newer live HEAD.
	writeTest(t, filepath.Join(f.repo.RepoRoot, "a.txt"), "changed live HEAD\n", 0o600)
	gitTest(t, f.repo.RepoRoot, "add", "a.txt")
	gitTest(t, f.repo.RepoRoot, "commit", "-m", "move live head")

	result, err := Run(context.Background(), Request{Repo: f.repo, Lane: f.lane, Stage: f.stage}, Dependencies{
		Git: CommandGitReader{Repo: f.repo.RepoRoot}, Clock: func() time.Time { return applyNow },
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.State != artifact.HoldReady || result.Sweep != "2026-08-12.md" ||
		!reflect.DeepEqual(result.AppliedMaps, []string{"maps/amend.md", "maps/confirm.md"}) ||
		!reflect.DeepEqual(result.ArchivedMaps, []string{"archive/2026-08-12-refute.md"}) {
		t.Fatalf("Run() result = %#v", result)
	}

	for _, name := range []string{"amend.md", "confirm.md"} {
		raw := readTest(t, filepath.Join(f.repo.Organ, "maps", name))
		if !strings.Contains(raw, "Provenance: 2026-08-12 · sid deadbeef") ||
			!strings.Contains(raw, "- `a.txt:1-2` — blob `"+f.aHash[:12]+"`") ||
			!strings.Contains(raw, "- `docs` — tree `"+f.docsHash[:12]+"`") {
			t.Fatalf("restamped %s = %q", name, raw)
		}
	}
	if existsTest(filepath.Join(f.repo.Organ, "maps", "refute.md")) ||
		existsTest(filepath.Join(f.repo.Organ, "maps", "unruled.md")) {
		t.Fatal("REFUTE or UNRULED map entered the live map pool")
	}
	wantRefute := f.mapRaw["refute.md"] + "\nVerdict: REFUTE — refuted evidence\n"
	if got := readTest(t, filepath.Join(f.repo.Organ, "archive", "2026-08-12-refute.md")); got != wantRefute {
		t.Fatalf("refuted archive = %q, want exact source plus note %q", got, wantRefute)
	}
	if got := readTest(
		t,
		filepath.Join(f.repo.Organ, "archive", "2026-08-12-explorer-index.md"),
	); got != "LEGACY SURFACE\n" {
		t.Fatalf("legacy explorer archive = %q", got)
	}
	if existsTest(filepath.Join(f.repo.Organ, "explorer-index.md")) {
		t.Fatal("legacy explorer surface survived outside archive")
	}

	wantNormalized := "AMEND\tmaps/amend.md\tamended evidence\n" +
		"CONFIRM\tmaps/confirm.md\tconfirmed evidence\n" +
		"REFUTE\tmaps/refute.md\trefuted evidence\n" +
		"UNRULED\tmaps/unruled.md\tno verifier verdict; not applied\n"
	if got := readTest(t, f.stage.NormalizedVerdicts); got != wantNormalized {
		t.Fatalf("normalized verdicts retained an edit or are unstable:\n%s", got)
	}
	wantLanes := "amend.md\texplorer\nconfirm.md\texplorer\nold-map.md\texplorer\n"
	if got := readTest(t, filepath.Join(f.repo.Organ, "lanes.tsv")); got != wantLanes {
		t.Fatalf("lanes.tsv = %q, want %q", got, wantLanes)
	}
	wantRows := "- Amend -> maps/amend.md\n- Confirm -> maps/confirm.md\n- Old -> maps/old-map.md\n"
	wantSurface := "Cached by the dreamer from earlier runs — read these first, before you act; if one answers your question, trust it and skip the re-derivation.\n" + wantRows
	if got := readTest(t, filepath.Join(f.repo.Organ, "agents", "explorer.md")); got != wantSurface {
		t.Fatalf("explorer surface = %q, want %q", got, wantSurface)
	}
	wantSTM := "# Index of maps/ — stale content: edit the map file directly.\n" + wantRows + "- retained keeper\n"
	if got := readTest(t, filepath.Join(f.repo.Organ, "stm.md")); got != wantSTM {
		t.Fatalf("stm.md = %q, want %q", got, wantSTM)
	}
	assertSweepGolden(t, f)
	if got := readTest(t, filepath.Join(f.stage.Root, "APPLIED")); got != "APPLIED\t2026-08-13T09:10:11+02:00\n" {
		t.Fatalf("APPLIED = %q", got)
	}
}

func TestRunSkipsEligibleVerdictAfterPostRefineAnchorRejection(t *testing.T) {
	f := newFixture(t, map[string]string{"rejected.md": "Rejected"}, []string{
		"CONFIRM\tmaps/rejected.md\tlooked valid before amend",
	}, artifact.HoldReady)
	raw := strings.Replace(f.mapRaw["rejected.md"], f.aHash[:12], flipped(f.aHash[:12]), 1)
	writePrivateTest(t, filepath.Join(f.stage.Maps, "rejected.md"), []byte(raw))
	rewriteReadyFromCurrent(t, &f)

	result, err := runFixture(f)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.State != artifact.HoldZeroSurvivors || len(result.AppliedMaps) != 0 {
		t.Fatalf("post-refine rejection result = %#v", result)
	}
	if existsTest(filepath.Join(f.repo.Organ, "maps", "rejected.md")) {
		t.Fatal("post-refine rejected CONFIRM map was applied")
	}
	if got := readTest(
		t,
		filepath.Join(f.stage.Root, "apply", "ops.tsv"),
	); !strings.Contains(
		got,
		"NOT-APPLIED\tmaps/rejected.md\tpost-refine anchor rejection\n",
	) {
		t.Fatalf("ops.tsv = %q", got)
	}
}

func TestDeriveHoldDistinguishesThreeStatesIncludingAllUnruled(t *testing.T) {
	paths := []string{"maps/a.md"}
	tests := []struct {
		name      string
		post      []string
		verdicts  []artifact.NormalizedVerdict
		wantState artifact.HoldState
		wantYield int
	}{
		{
			"zero survivors",
			nil,
			[]artifact.NormalizedVerdict{{Kind: artifact.NormalizedConfirm, MapPath: paths[0]}},
			artifact.HoldZeroSurvivors,
			0,
		},
		{
			"all refuted",
			paths,
			[]artifact.NormalizedVerdict{{Kind: artifact.NormalizedRefute, MapPath: paths[0]}},
			artifact.HoldZeroYield,
			0,
		},
		{
			"all unruled",
			paths,
			[]artifact.NormalizedVerdict{{Kind: artifact.NormalizedUnruled, MapPath: paths[0]}},
			artifact.HoldZeroYield,
			0,
		},
		{
			"ready",
			paths,
			[]artifact.NormalizedVerdict{{Kind: artifact.NormalizedAmend, MapPath: paths[0]}},
			artifact.HoldReady,
			1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state, yield, err := DeriveHold(test.post, test.verdicts)
			if err != nil || state != test.wantState || yield != test.wantYield {
				t.Fatalf("DeriveHold() = %s, %d, %v; want %s, %d", state, yield, err, test.wantState, test.wantYield)
			}
		})
	}
}

func TestRunFailsClosedBeforeOrganMutationForEveryMechanicalGate(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, f *fixture)
		want   string
	}{
		{
			"pin",
			func(t *testing.T, f *fixture) { writePrivateTest(t, f.stage.Pin, []byte(strings.Repeat("0", 64)+"\n")) },
			"PIN gate",
		},
		{"coverage conduct", func(t *testing.T, f *fixture) {
			writePrivateTest(
				t,
				f.stage.Coverage,
				[]byte("1\tREAD\tread\nCONDUCT\ttechnique\tNONE\tx\nCONDUCT\tprior\tNONE\tx\nEND-OF-RUN\n"),
			)
		}, "missing CONDUCT accounting for: baseline"},
		{"raw verdict", func(t *testing.T, f *fixture) {
			writePrivateTest(t, f.stage.Verdicts, []byte("CONFIRM\tmaps/map.md\n"))
		}, "VERDICTS gate"},
		{"ready mismatch", func(t *testing.T, f *fixture) {
			writePrivateTest(
				t,
				filepath.Join(f.stage.Root, "READY-FOR-APPLY"),
				[]byte("ZERO-YIELD\t2026-08-13T08:00:00+02:00\n"),
			)
		}, "state mismatch"},
		{"anchor mechanism unavailable", func(_ *testing.T, _ *fixture) {}, "verify staged recorded tree"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := newFixture(
				t,
				map[string]string{"map.md": "Map"},
				[]string{"CONFIRM\tmaps/map.md\tevidence"},
				artifact.HoldReady,
			)
			test.mutate(t, &f)
			before := snapshotOrgan(t, f.repo.Organ)
			deps := Dependencies{
				Git:   CommandGitReader{Repo: f.repo.RepoRoot},
				Clock: func() time.Time { return applyNow },
			}
			if test.name == "anchor mechanism unavailable" {
				deps.Git = failingGitReader{}
			}
			_, err := Run(context.Background(), Request{Repo: f.repo, Lane: f.lane, Stage: f.stage}, deps)
			assertErrorContains(t, err, test.want)
			if after := snapshotOrgan(t, f.repo.Organ); !reflect.DeepEqual(after, before) {
				t.Fatalf("organ changed across failed %s gate\nbefore=%#v\nafter=%#v", test.name, before, after)
			}
		})
	}
}

func TestRunRejectsIdentitySafetyFingerprintCollisionPreparationAndReplay(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, f *fixture)
		want   string
	}{
		{"identity mismatch", func(t *testing.T, f *fixture) {
			writePrivateTest(t, filepath.Join(f.stage.Meta, "lane.txt"), []byte("qa\n"))
		}, "staged lane.txt mismatch"},
		{
			"private mode",
			func(t *testing.T, f *fixture) { mustTest(t, os.Chmod(f.stage.Coverage, 0o644)) },
			"mode is not 0600",
		},
		{"private symlink", func(t *testing.T, f *fixture) {
			mustTest(t, os.Remove(f.stage.Verdicts))
			mustTest(t, os.Symlink(filepath.Join(f.repo.Organ, "stm.md"), f.stage.Verdicts))
		}, "not a regular non-symlink"},
		{"fingerprint", func(t *testing.T, f *fixture) {
			writeTest(t, filepath.Join(f.repo.Organ, "maps", "old-map.md"), "changed\n", 0o600)
		}, "organ maps changed since preflight"},
		{"map target collision", func(t *testing.T, f *fixture) {
			raw := readTest(t, filepath.Join(f.stage.Maps, "map.md"))
			mustTest(t, os.Rename(filepath.Join(f.stage.Maps, "map.md"), filepath.Join(f.stage.Maps, "old-map.md")))
			f.mapRaw = map[string]string{"old-map.md": raw}
			writePrivateTest(t, filepath.Join(f.stage.Root, "anchor-survivors.txt"), []byte("maps/old-map.md\n"))
			writePrivateTest(
				t,
				filepath.Join(f.stage.Root, "anchor-results.tsv"),
				[]byte("ACCEPT\tmaps/old-map.md\tcanonical map and recorded-tree anchors\n"),
			)
			writePrivateTest(t, f.stage.Verdicts, []byte("CONFIRM\tmaps/old-map.md\tevidence\n"))
			rewriteReadyFromCurrent(t, f)
		}, "map target collision"},
		{
			"preparation collision",
			func(t *testing.T, f *fixture) { mustTest(t, os.Mkdir(filepath.Join(f.stage.Root, "apply"), 0o700)) },
			"apply preparation already exists",
		},
		{"replay", func(t *testing.T, f *fixture) {
			writePrivateTest(t, filepath.Join(f.stage.Root, "APPLIED"), []byte("APPLIED\t2026-08-13T08:00:00+02:00\n"))
		}, "already applied"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := newFixture(
				t,
				map[string]string{"map.md": "Map"},
				[]string{"CONFIRM\tmaps/map.md\tevidence"},
				artifact.HoldReady,
			)
			test.mutate(t, &f)
			before := snapshotOrgan(t, f.repo.Organ)
			_, err := runFixture(f)
			assertErrorContains(t, err, test.want)
			if after := snapshotOrgan(t, f.repo.Organ); !reflect.DeepEqual(after, before) {
				t.Fatalf("organ changed on %s", test.name)
			}
		})
	}
}

func TestArchiveAndSweepNamesDatePrefixFirstThenIncrement(t *testing.T) {
	f := newFixture(
		t,
		map[string]string{"map.md": "Map"},
		[]string{"REFUTE\tmaps/map.md\tevidence"},
		artifact.HoldZeroYield,
	)
	writePrivateTest(t, filepath.Join(f.repo.Organ, "archive", "2026-08-12-map.md"), []byte("occupied\n"))
	writePrivateTest(t, filepath.Join(f.repo.Organ, "dreamer", "2026-08-12.md"), []byte("occupied\n"))
	result, err := runFixture(f)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Sweep != "2026-08-12-2.md" ||
		!reflect.DeepEqual(result.ArchivedMaps, []string{"archive/2026-08-12-2-map.md"}) {
		t.Fatalf("collision names = %#v", result)
	}
	if readTest(t, filepath.Join(f.repo.Organ, "archive", "2026-08-12-map.md")) != "occupied\n" ||
		readTest(t, filepath.Join(f.repo.Organ, "dreamer", "2026-08-12.md")) != "occupied\n" {
		t.Fatal("existing archive or sweep was overwritten")
	}
}

func TestMapFingerprintMatchesLegacyRowStream(t *testing.T) {
	directory := t.TempDir()
	writeTest(t, filepath.Join(directory, "b.md"), "b", 0o600)
	writeTest(t, filepath.Join(directory, "a.md"), "a", 0o600)
	writeTest(t, filepath.Join(directory, "ignored.txt"), "ignored", 0o600)
	var rows strings.Builder
	for _, name := range []string{"a.md", "b.md"} {
		sum := sha256.Sum256([]byte(strings.TrimSuffix(name, ".md")))
		fmt.Fprintf(&rows, "%s  %s\n", hex.EncodeToString(sum[:]), name)
	}
	total := sha256.Sum256([]byte(rows.String()))
	want := hex.EncodeToString(total[:])
	got, err := MapFingerprint(directory)
	if err != nil || got != want {
		t.Fatalf("MapFingerprint() = %s, %v; want %s", got, err, want)
	}
}

func TestRestampPreservesDisplayRangesAndExactlyOneSessionID(t *testing.T) {
	raw := canonicalMap("Subject", strings.Repeat("a", 12), strings.Repeat("b", 12))
	reader := fixedGitReader{objects: map[string]gate.GitObject{
		"a.txt": {Hash: strings.Repeat("1", 40), Type: artifact.GitBlob},
		"docs":  {Hash: strings.Repeat("2", 40), Type: artifact.GitTree},
	}}
	got, err := restampMap([]byte(raw), "2026-08-12", strings.Repeat("f", 40), reader)
	if err != nil {
		t.Fatalf("restampMap() error = %v", err)
	}
	wantParts := []string{
		"Provenance: 2026-08-12 · sid deadbeef",
		"- `a.txt:1-2` — blob `111111111111`",
		"- `docs` — tree `222222222222`",
	}
	for _, want := range wantParts {
		if !bytes.Contains(got, []byte(want)) {
			t.Fatalf("restamped map lacks %q:\n%s", want, got)
		}
	}
}

func TestPrivateFileOwnerCheckIsMechanicallyEnforced(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private")
	writeTest(t, path, "x", 0o600)
	assertErrorContains(t, validatePrivateFile(path, os.Getuid()+1), "wrong owner")
}

func TestCommitRollsBackAFailureAfterInstallingANewMap(t *testing.T) {
	root := t.TempDir()
	organRoot := filepath.Join(root, "organ")
	for _, directory := range []string{
		organRoot, filepath.Join(organRoot, "maps"), filepath.Join(organRoot, "archive"),
		filepath.Join(organRoot, "dreamer"), filepath.Join(organRoot, "agents"),
	} {
		mustTest(t, os.Mkdir(directory, 0o700))
	}
	writeTest(t, filepath.Join(organRoot, "stm.md"), "before\n", 0o600)
	stageRoot := filepath.Join(root, "stage")
	prepRoot := filepath.Join(stageRoot, "apply")
	mustTest(t, os.Mkdir(stageRoot, 0o700))
	mustTest(t, os.Mkdir(prepRoot, 0o700))
	mustTest(t, os.Mkdir(filepath.Join(prepRoot, "maps"), 0o700))
	writeTest(t, filepath.Join(prepRoot, "maps", "new.md"), "# New\n", 0o600)
	before := snapshotOrgan(t, organRoot)

	// The missing parent sorts after the legitimate derived targets, forcing a
	// failure only after the new map has been installed. Rollback must restore
	// byte identity instead of leaving that partial mutation behind.
	prepared := preparation{
		root: prepRoot, sweepTarget: "2026-08-12.md", explorerArchive: "NONE", appliedMaps: []string{"maps/new.md"},
		derived: map[string][]byte{
			filepath.Join(organRoot, "stm.md"):           []byte("after\n"),
			filepath.Join(organRoot, "zzz-missing", "x"): []byte("fail\n"),
		},
		sweepRaw: []byte("sweep\n"), appliedRaw: []byte("APPLIED\tnow\n"),
	}
	err := commit(
		artifact.RepoContext{Organ: organRoot},
		artifact.StageLayout{Root: stageRoot},
		organState{agents: true},
		prepared,
	)
	assertErrorContains(t, err, "atomic replace directory is not a real directory")
	if after := snapshotOrgan(t, organRoot); !reflect.DeepEqual(after, before) {
		t.Fatalf("commit rollback left partial organ mutation\nbefore=%#v\nafter=%#v", before, after)
	}
}

func TestCommandGitReaderSourceAdmitsNoGitWriteVerb(t *testing.T) {
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(current), "..", "gate", "anchors.go"))
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	for _, forbidden := range []string{`"add"`, `"commit"`, `"checkout"`, `"reset"`, `"stash"`, `"merge"`, `"push"`, `"tag"`} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("Git adapter source contains write verb %s", forbidden)
		}
	}
	if !strings.Contains(source, `"rev-parse"`) || !strings.Contains(source, `"cat-file"`) ||
		!strings.Contains(source, `"GIT_OPTIONAL_LOCKS=0"`) {
		t.Fatal("Git adapter lost its read-only verbs or optional-lock pin")
	}
}

func newFixture(t *testing.T, maps map[string]string, verdictRows []string, requestedHold artifact.HoldState) fixture {
	t.Helper()
	root := t.TempDir()
	repoRoot := filepath.Join(root, "repo")
	mustTest(t, os.Mkdir(repoRoot, 0o700))
	writeTest(t, filepath.Join(repoRoot, "a.txt"), "alpha\n", 0o600)
	mustTest(t, os.Mkdir(filepath.Join(repoRoot, "docs"), 0o700))
	writeTest(t, filepath.Join(repoRoot, "docs", "b.txt"), "beta\n", 0o600)
	gitTest(t, repoRoot, "init", "-q")
	gitTest(t, repoRoot, "config", "user.email", "dreamer@example.invalid")
	gitTest(t, repoRoot, "config", "user.name", "Dreamer Test")
	gitTest(t, repoRoot, "add", "a.txt", "docs/b.txt")
	gitTest(t, repoRoot, "commit", "-q", "-m", "fixture")
	recorded := gitOutputTest(t, repoRoot, "rev-parse", "HEAD")
	aHash := gitOutputTest(t, repoRoot, "rev-parse", recorded+":a.txt")
	docsHash := gitOutputTest(t, repoRoot, "rev-parse", recorded+":docs")

	organRoot := filepath.Join(repoRoot, ".professor", "stm")
	for _, directory := range []string{
		filepath.Join(repoRoot, ".professor"), organRoot, filepath.Join(organRoot, "maps"),
		filepath.Join(organRoot, "dreamer"), filepath.Join(organRoot, "archive"),
	} {
		mustTest(t, os.Mkdir(directory, 0o700))
	}
	oldMap := canonicalMap("Old", aHash[:12], docsHash[:12])
	writePrivateTest(t, filepath.Join(organRoot, "maps", "old-map.md"), []byte(oldMap))
	writePrivateTest(
		t,
		filepath.Join(organRoot, "stm.md"),
		[]byte("# old index\n- Old -> maps/old-map.md\n- retained keeper\n"),
	)
	registry := filepath.Join(root, "registry")
	mustTest(t, os.Mkdir(registry, 0o700))
	repo := artifact.RepoContext{RepoRoot: repoRoot, Organ: organRoot, Registry: registry}
	laneContext := artifact.LaneContext{AgentType: "Explore", Lane: "explorer"}
	stage, err := organ.NewStage(repo, laneContext.Lane, time.Date(2026, 8, 12, 3, 4, 5, 0, time.UTC))
	if err != nil {
		t.Fatalf("organ.NewStage() error = %v", err)
	}
	f := fixture{
		repo:         repo,
		lane:         laneContext,
		stage:        stage,
		recordedTree: recorded,
		aHash:        aHash,
		docsHash:     docsHash,
		mapRaw:       map[string]string{},
	}

	fingerprint, err := MapFingerprint(filepath.Join(organRoot, "maps"))
	if err != nil {
		t.Fatal(err)
	}
	meta := map[string]string{
		"repo-root.txt": repoRoot, "organ.txt": organRoot, "agent-type.txt": laneContext.AgentType,
		"lane.txt": laneContext.Lane, "repo-head.txt": recorded, "run-date.txt": "2026-08-12",
		"maps.sha256": fingerprint,
	}
	for name, value := range meta {
		writePrivateTest(t, filepath.Join(stage.Meta, name), []byte(value+"\n"))
	}
	path := filepath.Join(root, "transcript.jsonl")
	writeTest(t, path, "{}\n", 0o600)
	pathsRaw := []byte(path + "\n")
	pin := sha256.Sum256(pathsRaw)
	writePrivateTest(t, stage.Paths, pathsRaw)
	writePrivateTest(t, stage.Pin, []byte(hex.EncodeToString(pin[:])+"\n"))
	coverage := "1\tREAD\tread fixture\n" +
		"CONDUCT\ttechnique\tNONE\tchecked\n" +
		"CONDUCT\tprior\tNONE\tchecked\n" +
		"CONDUCT\tbaseline\tNONE\tchecked\n" +
		"END-OF-RUN\n"
	writePrivateTest(t, stage.Coverage, []byte(coverage))
	window := "window-mode\tcorpus-file\ncorpus-file\t" + filepath.Join(root, "corpus.txt") + "\n" +
		"corpus-file-sha256\t" + strings.Repeat("0", 64) + "\n" +
		"agent-type\tExplore\nlane\texplorer\ncutoff-exclusive\tNONE\n" +
		"enumerated-at\t2026-08-12T03:04:05Z\n"
	writePrivateTest(t, filepath.Join(stage.Meta, "window.tsv"), []byte(window))
	census := artifact.Census{
		WindowMetaCount:               1,
		AgentMetaCount:                1,
		PairedTranscriptCount:         1,
		SelectedPairedTranscriptCount: 1,
	}
	writePrivateTest(t, filepath.Join(stage.Root, "census.tsv"), []byte(artifact.RenderCensus(census)))
	writePrivateTest(t, filepath.Join(stage.Root, "gaps.tsv"), nil)

	names := make([]string, 0, len(maps))
	for name := range maps {
		names = append(names, name)
	}
	sort.Strings(names)
	var survivors, anchorRows strings.Builder
	for _, name := range names {
		raw := canonicalMap(maps[name], aHash[:12], docsHash[:12])
		f.mapRaw[name] = raw
		writePrivateTest(t, filepath.Join(stage.Maps, name), []byte(raw))
		fmt.Fprintf(&survivors, "maps/%s\n", name)
		fmt.Fprintf(&anchorRows, "ACCEPT\tmaps/%s\tcanonical map and recorded-tree anchors\n", name)
	}
	writePrivateTest(t, filepath.Join(stage.Root, "anchor-survivors.txt"), []byte(survivors.String()))
	writePrivateTest(t, filepath.Join(stage.Root, "anchor-results.tsv"), []byte(anchorRows.String()))
	writePrivateTest(t, stage.Verdicts, []byte(renderRows(verdictRows)))
	rewriteReadyFromCurrent(t, &f)
	readyRaw := readTest(t, filepath.Join(stage.Root, "READY-FOR-APPLY"))
	if !strings.HasPrefix(readyRaw, string(requestedHold)+"\t") {
		t.Fatalf("fixture derived hold %q, caller expected %s", readyRaw, requestedHold)
	}
	return f
}

func rewriteReadyFromCurrent(t *testing.T, f *fixture) {
	t.Helper()
	inputs, rawByPath, err := readStagedMaps(f.stage.Maps)
	if err != nil {
		t.Fatal(err)
	}
	post, err := gate.Anchors(f.recordedTree, inputs, CommandGitReader{Repo: f.repo.RepoRoot})
	if err != nil {
		t.Fatal(err)
	}
	survivorRaw := readTest(t, filepath.Join(f.stage.Root, "anchor-survivors.txt"))
	survivors, err := parseMapPathList([]byte(survivorRaw), rawByPath)
	if err != nil {
		t.Fatal(err)
	}
	verdicts, err := artifact.ParseVerdicts(readTest(t, f.stage.Verdicts))
	if err != nil {
		t.Fatal(err)
	}
	normalized, err := artifact.NormalizeVerdicts(survivors, verdicts)
	if err != nil {
		t.Fatal(err)
	}
	state, yield, err := DeriveHold(post.Accepted, normalized)
	if err != nil {
		t.Fatal(err)
	}
	writePrivateTest(t, filepath.Join(f.stage.Meta, "apply-yield.txt"), []byte(fmt.Sprintf("%d\n", yield)))
	writePrivateTest(
		t,
		filepath.Join(f.stage.Root, "READY-FOR-APPLY"),
		[]byte(string(state)+"\t2026-08-13T08:00:00+02:00\n"),
	)
}

func canonicalMap(title, aHash, docsHash string) string {
	return "# " + title + "\n\n" +
		"## Question\n\nWhat is " + title + "?\n\n" +
		"## Answer\n\nThe answer.\n\n" +
		"## Derivation trail\n\nDerived carefully.\n\n" +
		"Provenance: 2000-01-01 · sid deadbeef\n\n" +
		"## Anchors\n\n" +
		"- `a.txt:1-2` — blob `" + aHash + "`\n" +
		"- `docs` — tree `" + docsHash + "`\n"
}

func runFixture(f fixture) (Result, error) {
	return Run(context.Background(), Request{Repo: f.repo, Lane: f.lane, Stage: f.stage}, Dependencies{
		Git: CommandGitReader{Repo: f.repo.RepoRoot}, Clock: func() time.Time { return applyNow },
	})
}

func assertSweepGolden(t *testing.T, f fixture) {
	t.Helper()
	window := readTest(t, filepath.Join(f.stage.Meta, "window.tsv"))
	census := readTest(t, filepath.Join(f.stage.Root, "census.tsv"))
	paths := readTest(t, f.stage.Paths)
	pin := strings.TrimSuffix(readTest(t, f.stage.Pin), "\n")
	distill := readTest(t, filepath.Join(f.stage.Root, "anchor-results.tsv"))
	post := readTest(t, filepath.Join(f.stage.Root, "anchor-postrefine.tsv"))
	lanes := readTest(t, filepath.Join(f.repo.Organ, "lanes.tsv"))
	verdicts := readTest(t, f.stage.NormalizedVerdicts)
	ops := readTest(t, filepath.Join(f.stage.Root, "apply", "ops.tsv"))
	// Build the golden independently while retaining the legacy empty-block
	// spelling. The expanded row uses the exact pinned transcript path.
	want := "# Dreamer sweep — 2026-08-12\n\n## Coverage\n\n" + window +
		"paths-sha256\t" + pin + "\n" + census +
		"\n### Paths\n\n```text\n" + paths + "```\n" +
		"\n### Typed gaps\n\n```text\n```\n" +
		"\n### Coverage\n\n```text\n" + strings.TrimSuffix(paths, "\n") + "\tREAD\tread fixture\n```\n" +
		"\n## Gate results\n\n### Distill anchor gate\n\n```text\n" + distill + "```\n" +
		"\n### Post-verify anchor gate\n\n```text\n" + post + "```\n" +
		"\n### Lane membership\n\n```text\n" + lanes + "```\n" +
		"\n## Verdicts\n\n```text\n" + verdicts + "```\n" +
		"\n## Ops\n\n```text\n" + ops + "```\n" +
		"\nEND-OF-SWEEP\nApplied: 2026-08-13T09:10:11+02:00\n"
	got := readTest(t, filepath.Join(f.repo.Organ, "dreamer", "2026-08-12.md"))
	if got != want {
		t.Fatalf("sweep golden mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

type failingGitReader struct{}

func (failingGitReader) Resolve(string, string) (gate.GitObject, bool, error) {
	return gate.GitObject{}, false, errors.New("fixture Git failure")
}

type fixedGitReader struct{ objects map[string]gate.GitObject }

func (reader fixedGitReader) Resolve(_, path string) (gate.GitObject, bool, error) {
	object, ok := reader.objects[path]
	return object, ok, nil
}

func flipped(value string) string {
	if value[0] == '0' {
		return "1" + value[1:]
	}
	return "0" + value[1:]
}

func snapshotOrgan(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if relative == "tmp" || strings.HasPrefix(relative, "tmp"+string(filepath.Separator)) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			result[relative+"/"] = entry.Type().String()
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		result[relative] = string(raw)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func gitTest(t *testing.T, directory string, arguments ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, arguments...)...)
	command.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(arguments, " "), err, output)
	}
}

func gitOutputTest(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, arguments...)...)
	command.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(arguments, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}

func writePrivateTest(t *testing.T, path string, raw []byte) {
	t.Helper()
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeTest(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

func readTest(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	return string(raw)
}

func existsTest(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func mustTest(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func assertErrorContains(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want substring %q", err, want)
	}
}
