package dream

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// legacyBatteryChecks preserves the 25 user-facing PASS names from
// tests/test-dreamer-night.sh. Each row points at the Go tests carrying that
// assertion's behavior; TestLegacyBatteryNamesMapToRealGoTests makes a rename
// or accidental test removal loud instead of letting traceability decay.
var legacyBatteryChecks = []struct {
	name    string
	goTests []string
}{
	{"coverage mismatch fails closed", []string{"TestCoverageMismatchFailsClosed", "TestPinFailsClosed"}},
	{
		"coverage is keyed by index and the engine expands it back to paths",
		[]string{"TestCoverageIsIndexKeyedAndExpandsPaths", "TestCoverageExpandsIndicesBackToPinnedPaths"},
	},
	{"flipped anchor hash is rejected", []string{"TestFlippedAnchorHashIsRejected"}},
	{
		"canonical live anchors pass",
		[]string{"TestParseAnchorRowCanonicalGrammar", "TestAnchorsUseRecordedTreeEvenAfterHeadMoves"},
	},
	{"legacy 40-character anchors reject", []string{"TestParseAnchorRowRejectsRetiredAndUnsafeForms"}},
	{
		"the retired commit-row grammar rejects, no organ retains it, and both seats carry the trigger law",
		[]string{
			"TestParseAnchorRowRejectsRetiredAndUnsafeForms",
			"TestRuntimePromptsCarryTheMechanicalArtifactLaws",
			"TestLiveOrgansCarryNoRetiredAnchorRows",
		},
	},
	{
		"comma-separated anchor ranges reject and the distill prompt teaches the canonical form",
		[]string{"TestParseAnchorRowRejectsRetiredAndUnsafeForms", "TestRuntimePromptsCarryTheMechanicalArtifactLaws"},
	},
	{
		"verdict gate rules valid lines and marks omissions UNRULED",
		[]string{"TestVerdictsPreserveRulesAndMarkOmissionsUnruled", "TestMissingVerdictBecomesUnruled"},
	},
	{"surfaces regenerate byte-stable", []string{"TestRenderSurfacesSortsGloballyAndPerLaneAndPreservesNonMapBullets"}},
	{
		"repo parameterization resolves both registries and rejects wrong or missing organs",
		[]string{
			"TestResolveDerivesOrganAndEncodedRegistry",
			"TestResolveRejectsNonCanonicalAndNonRootRepositories",
			"TestValidateRejectsIntermediateLedgerAndBadSkeleton",
		},
	},
	{
		"cutoff precedence and empty window skip the seat, log, ledger, and staging retention",
		[]string{"TestCutoffPrecedenceAndBootstrapFallback", "TestNightEmptyCorpusSucceedsBeforeLogsSeatsAndGates"},
	},
	{
		"a lane profile that exists only globally still runs",
		[]string{
			"TestResolveProfileIsOrganFirstAndNamesBothMissingPaths",
			"TestNightWiresOrganLockAndProfileAndFailsOnRefinerMapSetChange",
		},
	},
	{
		"bootstrap-count selects newest paired metas with deterministic ties, honest full census, and loud zero-survivor HOLD",
		[]string{
			"TestEnumerateBootstrapMatchesBatteryCensusAndTieBreak",
			"TestNightZeroSurvivorsIsLoudAndOffersNoApply",
		},
	},
	{
		"nudge is silent when healthy and emits at most one failure-or-stale line",
		[]string{
			"TestNudgePrefersOrganLocalFailureAndEmitsAtMostOneLine",
			"TestNudgeIsSilentWithoutOrganAndForRecentHealthySweep",
			"TestNudgeStaleLineUsesNewestCompletedSweep",
		},
	},
	{
		"hook prefers agents/explorer.md and a moved anchor renders DRIFTED",
		[]string{
			"TestClaudeHookPrefersGeneratedExplorerSurfaceOverLegacyFallback",
			"TestClaudeHookPreservesOrderedToolInputAndAnnotatesDrift",
		},
	},
	{
		"both hooks resolve any repository organ, hermetically",
		[]string{"TestHooksStayRepositoryHermeticAndStripWorktree"},
	},
	{
		"morning wrapper runs sequentially, names each lane, and continues after failure",
		[]string{"TestMorningContinuesAfterFailureAndDiscoversLanesWithoutDuplicateExplorer"},
	},
	{
		"morning wrapper discovers organ lanes once and never duplicates explorer.md",
		[]string{"TestMorningContinuesAfterFailureAndDiscoversLanesWithoutDuplicateExplorer"},
	},
	{
		"explorer surface stays byte-identical to the live organ under lanes",
		[]string{"TestLiveExplorerSurfaceRegeneratesByteIdentically"},
	},
	{
		"lane membership backfills a pre-lane pool and fails closed on a killed map",
		[]string{
			"TestBuildMembershipBackfillsLegacyAndAssignsNewMaps",
			"TestBuildMembershipPreservesLedgerAndFailsClosedOnOldHole",
		},
	},
	{
		"each lane is injected only its own surface, and a lane-less type gets nothing",
		[]string{"TestHooksAreLaneIsolatedAndUnsafeOrMissingLanesStaySilent"},
	},
	{
		"an organ-local lane profile takes precedence over the same global lane",
		[]string{"TestResolveProfileIsOrganFirstAndNamesBothMissingPaths"},
	},
	{
		"corpus-file pins an explicit corpus, records its digest and lane, and fails closed on a ghost path",
		[]string{
			"TestExplicitCorpusReadsOnceCopiesExactBytesAndDeduplicates",
			"TestExplicitCorpusRejectsGhostRelativeControlAndNonregularPaths",
			"TestNightGhostCorpusPathNamesExactTranscriptInDurableMarker",
		},
	},
	{
		"lane windows are independent and a lane without a profile cannot run",
		[]string{
			"TestEnumerateRollingWindowIsStrictlyExclusiveAndLaneScoped",
			"TestResolveProfileIsOrganFirstAndNamesBothMissingPaths",
		},
	},
	{
		"the dreamer distills on luna and refuses any other model",
		[]string{"TestNightLunaLawIsTheFirstEffectBarrier", "TestSeatLawRunsBeforeEveryEffect"},
	},
}

func TestLegacyBatteryNamesMapToRealGoTests(t *testing.T) {
	if len(legacyBatteryChecks) != 25 {
		t.Fatalf("legacy battery mapping has %d checks, want 25", len(legacyBatteryChecks))
	}
	tests := discoverDreamTestFunctions(t)
	seenNames := make(map[string]bool, len(legacyBatteryChecks))
	for _, check := range legacyBatteryChecks {
		t.Run(check.name, func(t *testing.T) {
			if check.name == "" || strings.HasPrefix(check.name, "PASS ") {
				t.Fatalf("legacy PASS name is not canonical: %q", check.name)
			}
			if seenNames[check.name] {
				t.Fatalf("duplicate legacy PASS name: %s", check.name)
			}
			seenNames[check.name] = true
			if len(check.goTests) == 0 {
				t.Fatal("legacy check has no Go test mapping")
			}
			for _, name := range check.goTests {
				if !tests[name] {
					t.Errorf("mapped Go test does not exist under internal/dream: %s", name)
				}
			}
		})
	}
}

func discoverDreamTestFunctions(t *testing.T) map[string]bool {
	t.Helper()
	root := filepath.Join(moduleRoot(t), "internal", "dream")
	result := make(map[string]bool)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		for _, declaration := range parsed.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if ok && strings.HasPrefix(function.Name.Name, "Test") {
				result[function.Name.Name] = true
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result) == 0 {
		t.Fatal("dream Go test discovery returned no tests")
	}
	return result
}
