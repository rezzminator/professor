// Package apply turns a fully gated dreamer stage into durable organ files.
// It deliberately separates preparation from mutation: every fallible gate,
// restamp, render, and collision check completes below stage/apply before the
// first organ path is changed.
package apply

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"hostops/pfm/internal/dream/artifact"
	"hostops/pfm/internal/dream/gate"
	"hostops/pfm/internal/dream/lane"
	"hostops/pfm/internal/dream/organ"
)

const noExplorerArchive = "NONE"

var (
	objectIDPattern  = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
	sha256Pattern    = regexp.MustCompile(`^[0-9a-f]{64}$`)
	mapNamePattern   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*\.md$`)
	mapPathPattern   = regexp.MustCompile(`^maps/[a-z0-9][a-z0-9-]*\.md$`)
	laneNamePattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	readyLinePattern = regexp.MustCompile(`^(READY|ZERO-SURVIVORS|ZERO-YIELD)\t(.+)$`)
)

// GitReader is the apply package's entire Git boundary. Implementations must
// resolve objects at the explicit recorded tree; Run never asks for HEAD.
type GitReader interface {
	Resolve(tree, path string) (gate.GitObject, bool, error)
}

// CommandGitReader is the read-only rev-parse/cat-file adapter shared with the
// anchor gate. It sets GIT_OPTIONAL_LOCKS=0 on every command.
type CommandGitReader = gate.CommandGitReader

type Request struct {
	Repo  artifact.RepoContext
	Lane  artifact.LaneContext
	Stage artifact.StageLayout
}

type Dependencies struct {
	Git   GitReader
	Clock func() time.Time
}

type Result struct {
	State        artifact.HoldState
	Sweep        string
	AppliedMaps  []string
	ArchivedMaps []string
	AppliedAt    time.Time
}

type stagedInput struct {
	today               string
	recordedTree        string
	recordedFingerprint string
	paths               gate.PinnedPaths
	coverage            artifact.Coverage
	expandedCoverage    string
	normalized          []artifact.NormalizedVerdict
	normalizedRaw       string
	postAnchors         gate.AnchorResult
	postAnchorsRaw      string
	postSurvivorsRaw    string
	hold                artifact.HoldState
	yield               int
	maps                []gate.MapInput
	mapRaw              map[string][]byte
	windowRaw           []byte
	censusRaw           []byte
	gapsRaw             []byte
	distillAnchorsRaw   []byte
}

type organState struct {
	fingerprint  string
	previousMaps []string
	membership   artifact.LaneMembership
	lanesPresent bool
	lanesRaw     []byte
	stmRaw       []byte
	explorerRaw  []byte
	explorer     bool
	agents       bool
}

type preparation struct {
	root            string
	sweepTarget     string
	explorerArchive string
	appliedMaps     []string
	archivedMaps    []string
	derived         map[string][]byte
	state           artifact.HoldState
	sweepRaw        []byte
	appliedRaw      []byte
}

// Run validates and prepares the whole apply before mutating the organ. A
// failed gate or preparation therefore leaves the organ byte-for-byte alone.
func Run(ctx context.Context, request Request, dependencies Dependencies) (Result, error) {
	if ctx == nil {
		return Result{}, errors.New("apply requires a context")
	}
	if dependencies.Git == nil {
		return Result{}, errors.New("apply requires a Git reader")
	}
	if dependencies.Clock == nil {
		return Result{}, errors.New("apply requires a clock")
	}
	now := dependencies.Clock()
	if now.IsZero() {
		return Result{}, errors.New("apply clock returned zero time")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, fmt.Errorf("apply canceled before validation: %w", err)
	}

	layout, err := organ.ValidateStage(request.Repo, request.Stage.Root)
	if err != nil {
		return Result{}, fmt.Errorf("validate apply stage: %w", err)
	}
	if layout != request.Stage {
		return Result{}, errors.New("supplied stage layout does not match its repository and root")
	}
	if !laneNamePattern.MatchString(request.Lane.Lane) || request.Lane.AgentType == "" ||
		hasControl(request.Lane.AgentType) {
		return Result{}, errors.New("invalid apply lane context")
	}
	if err := rejectReplayOrPreparation(layout); err != nil {
		return Result{}, err
	}

	input, err := validateStageArtifacts(request, layout, dependencies.Git)
	if err != nil {
		return Result{}, err
	}
	state, err := readOrganState(request.Repo)
	if err != nil {
		return Result{}, err
	}
	if state.fingerprint != input.recordedFingerprint {
		return Result{}, fmt.Errorf(
			"organ maps changed since preflight; recorded %s, current %s",
			input.recordedFingerprint,
			state.fingerprint,
		)
	}
	if err := ctx.Err(); err != nil {
		return Result{}, fmt.Errorf("apply canceled before preparation: %w", err)
	}

	prepared, err := prepare(request, layout, input, state, now, dependencies.Git)
	if err != nil {
		return Result{}, err
	}
	// These derived artifacts are intentionally overwritten from the raw,
	// mechanically re-run gates. A hand-edited normalized file is not input.
	if err := privateAtomicReplace(layout.NormalizedVerdicts, []byte(input.normalizedRaw)); err != nil {
		return Result{}, fmt.Errorf("replace normalized verdicts from raw: %w", err)
	}
	if err := privateAtomicReplace(
		filepath.Join(layout.Root, "anchor-postrefine.tsv"),
		[]byte(input.postAnchorsRaw),
	); err != nil {
		return Result{}, fmt.Errorf("replace post-refine anchor results: %w", err)
	}
	if err := privateAtomicReplace(
		filepath.Join(layout.Root, "anchor-postrefine-survivors.txt"),
		[]byte(input.postSurvivorsRaw),
	); err != nil {
		return Result{}, fmt.Errorf("replace post-refine anchor survivors: %w", err)
	}

	if err := ctx.Err(); err != nil {
		return Result{}, fmt.Errorf("apply canceled before organ mutation: %w", err)
	}
	if err := revalidateOrganState(request.Repo, state, prepared); err != nil {
		return Result{}, err
	}
	if err := commit(request.Repo, layout, state, prepared); err != nil {
		return Result{}, err
	}
	return Result{
		State: prepared.state, Sweep: prepared.sweepTarget,
		AppliedMaps:  append([]string(nil), prepared.appliedMaps...),
		ArchivedMaps: append([]string(nil), prepared.archivedMaps...), AppliedAt: now,
	}, nil
}

func validateStageArtifacts(request Request, layout artifact.StageLayout, git GitReader) (stagedInput, error) {
	identity := []struct {
		name string
		want string
	}{
		{"repo-root.txt", request.Repo.RepoRoot},
		{"organ.txt", request.Repo.Organ},
		{"agent-type.txt", request.Lane.AgentType},
		{"lane.txt", request.Lane.Lane},
	}
	for _, row := range identity {
		got, err := readPrivateLine(filepath.Join(layout.Meta, row.name))
		if err != nil {
			return stagedInput{}, err
		}
		if got != row.want {
			return stagedInput{}, fmt.Errorf("staged %s mismatch: got %q, want %q", row.name, got, row.want)
		}
	}
	recordedTree, err := readPrivateLine(filepath.Join(layout.Meta, "repo-head.txt"))
	if err != nil {
		return stagedInput{}, err
	}
	if !objectIDPattern.MatchString(recordedTree) {
		return stagedInput{}, errors.New("staged recorded tree is not a Git object id")
	}
	rootTree, found, err := git.Resolve(recordedTree, "")
	if err != nil {
		return stagedInput{}, fmt.Errorf("verify staged recorded tree: %w", err)
	}
	if !found || rootTree.Type != artifact.GitTree || !objectIDPattern.MatchString(rootTree.Hash) {
		return stagedInput{}, errors.New("staged recorded tree does not resolve to a repository tree")
	}
	today, err := readPrivateLine(filepath.Join(layout.Meta, "run-date.txt"))
	if err != nil {
		return stagedInput{}, err
	}
	if parsed, parseErr := time.Parse("2006-01-02", today); parseErr != nil || parsed.Format("2006-01-02") != today {
		return stagedInput{}, errors.New("staged run date is invalid")
	}
	recordedFingerprint, err := readPrivateLine(filepath.Join(layout.Meta, "maps.sha256"))
	if err != nil {
		return stagedInput{}, err
	}
	if !sha256Pattern.MatchString(recordedFingerprint) {
		return stagedInput{}, errors.New("staged map-pool fingerprint is not one SHA-256")
	}

	pathsRaw, err := readPrivate(layout.Paths)
	if err != nil {
		return stagedInput{}, err
	}
	pinRaw, err := readPrivate(layout.Pin)
	if err != nil {
		return stagedInput{}, err
	}
	pinned, err := gate.Pin(pathsRaw, pinRaw)
	if err != nil {
		return stagedInput{}, fmt.Errorf("PIN gate: %w", err)
	}
	coverageRaw, err := readPrivate(layout.Coverage)
	if err != nil {
		return stagedInput{}, err
	}
	coverage, err := artifact.ParseCoverage(string(coverageRaw), len(pinned.Paths))
	if err != nil {
		return stagedInput{}, fmt.Errorf("COVERAGE gate: %w", err)
	}
	if _, err := gate.Coverage(pinned, coverage); err != nil {
		return stagedInput{}, fmt.Errorf("COVERAGE gate: %w", err)
	}

	maps, rawByPath, err := readStagedMaps(layout.Maps)
	if err != nil {
		return stagedInput{}, err
	}
	postAnchors, err := gate.Anchors(recordedTree, maps, git)
	if err != nil {
		return stagedInput{}, fmt.Errorf("post-refine ANCHORS gate: %w", err)
	}
	postAnchorsRaw := renderAnchorResults(maps, postAnchors)
	postSurvivorsRaw := renderPaths(postAnchors.Accepted)

	survivorRaw, err := readPrivate(filepath.Join(layout.Root, "anchor-survivors.txt"))
	if err != nil {
		return stagedInput{}, err
	}
	survivors, err := parseMapPathList(survivorRaw, rawByPath)
	if err != nil {
		return stagedInput{}, fmt.Errorf("distill anchor survivors: %w", err)
	}
	verdictRaw, err := readPrivate(layout.Verdicts)
	if err != nil {
		return stagedInput{}, err
	}
	verdicts, err := artifact.ParseVerdicts(string(verdictRaw))
	if err != nil {
		return stagedInput{}, fmt.Errorf("VERDICTS gate: %w", err)
	}
	verdictResult, err := gate.Verdicts(survivors, verdicts)
	if err != nil {
		return stagedInput{}, fmt.Errorf("VERDICTS gate: %w", err)
	}
	normalizedRaw := artifact.RenderNormalizedVerdicts(verdictResult.Normalized)
	hold, yield, err := DeriveHold(postAnchors.Accepted, verdictResult.Normalized)
	if err != nil {
		return stagedInput{}, err
	}
	if err := validateReady(layout, hold, yield); err != nil {
		return stagedInput{}, err
	}

	windowRaw, err := readPrivate(filepath.Join(layout.Meta, "window.tsv"))
	if err != nil {
		return stagedInput{}, err
	}
	if err := validateWindowIdentity(windowRaw, request.Lane); err != nil {
		return stagedInput{}, err
	}
	censusRaw, err := readPrivate(filepath.Join(layout.Root, "census.tsv"))
	if err != nil {
		return stagedInput{}, err
	}
	census, err := artifact.ParseCensus(string(censusRaw))
	if err != nil {
		return stagedInput{}, fmt.Errorf("parse staged census: %w", err)
	}
	if artifact.RenderCensus(census) != string(censusRaw) {
		return stagedInput{}, errors.New("staged census is not canonical")
	}
	gapsRaw, err := readPrivate(filepath.Join(layout.Root, "gaps.tsv"))
	if err != nil {
		return stagedInput{}, err
	}
	gapCount, err := validateGaps(gapsRaw)
	if err != nil {
		return stagedInput{}, err
	}
	if census.SelectedPairedTranscriptCount != len(pinned.Paths) || census.CoverageGapCount != gapCount {
		return stagedInput{}, errors.New("staged census does not match paths and typed gaps")
	}
	distillAnchorsRaw, err := readPrivate(filepath.Join(layout.Root, "anchor-results.tsv"))
	if err != nil {
		return stagedInput{}, err
	}
	if err := validateAnchorLedger(distillAnchorsRaw, maps, survivors); err != nil {
		return stagedInput{}, fmt.Errorf("distill anchor ledger: %w", err)
	}
	for _, derived := range []string{layout.NormalizedVerdicts, filepath.Join(layout.Root, "anchor-postrefine.tsv"), filepath.Join(layout.Root, "anchor-postrefine-survivors.txt")} {
		if err := rejectUnsafeReplaceTarget(derived); err != nil {
			return stagedInput{}, err
		}
	}

	return stagedInput{
		today: today, recordedTree: recordedTree, recordedFingerprint: recordedFingerprint,
		paths: pinned, coverage: coverage, expandedCoverage: artifact.RenderExpandedCoverage(coverage, pinned.Paths),
		normalized: verdictResult.Normalized, normalizedRaw: normalizedRaw,
		postAnchors: postAnchors, postAnchorsRaw: postAnchorsRaw, postSurvivorsRaw: postSurvivorsRaw,
		hold: hold, yield: yield, maps: maps, mapRaw: rawByPath,
		windowRaw: windowRaw, censusRaw: censusRaw, gapsRaw: gapsRaw, distillAnchorsRaw: distillAnchorsRaw,
	}, nil
}

// DeriveHold applies invariant 13 to post-refiner survivors and normalized raw
// verdicts. All-UNRULED and mixed REFUTE/UNRULED nights are ZERO-YIELD.
func DeriveHold(postSurvivors []string, normalized []artifact.NormalizedVerdict) (artifact.HoldState, int, error) {
	post := make(map[string]struct{}, len(postSurvivors))
	for _, path := range postSurvivors {
		if !mapPathPattern.MatchString(path) {
			return "", 0, fmt.Errorf("invalid post-refine survivor: %s", path)
		}
		if _, duplicate := post[path]; duplicate {
			return "", 0, fmt.Errorf("duplicate post-refine survivor: %s", path)
		}
		post[path] = struct{}{}
	}
	yield := 0
	for _, row := range normalized {
		if _, survives := post[row.MapPath]; !survives {
			continue
		}
		if row.Kind == artifact.NormalizedConfirm || row.Kind == artifact.NormalizedAmend {
			yield++
		}
	}
	if len(post) == 0 {
		return artifact.HoldZeroSurvivors, 0, nil
	}
	if yield == 0 {
		return artifact.HoldZeroYield, 0, nil
	}
	return artifact.HoldReady, yield, nil
}

func prepare(
	request Request,
	layout artifact.StageLayout,
	input stagedInput,
	state organState,
	now time.Time,
	git GitReader,
) (preparation, error) {
	root := filepath.Join(layout.Root, "apply")
	if err := os.Mkdir(root, 0o700); err != nil {
		return preparation{}, fmt.Errorf("create apply preparation %s: %w", root, err)
	}
	for _, name := range []string{"maps", "refuted", "surfaces", "surfaces/agents", "surfaces-second", "surfaces-second/agents", "legacy"} {
		if err := os.Mkdir(filepath.Join(root, name), 0o700); err != nil {
			return preparation{}, fmt.Errorf("create apply preparation directory %s: %w", name, err)
		}
	}
	for _, name := range state.previousMaps {
		if err := copyPrivateExclusive(
			filepath.Join(request.Repo.Organ, "maps", name),
			filepath.Join(root, "maps", name),
		); err != nil {
			return preparation{}, fmt.Errorf("copy existing map into preparation: %w", err)
		}
	}

	post := make(map[string]struct{}, len(input.postAnchors.Accepted))
	for _, path := range input.postAnchors.Accepted {
		post[path] = struct{}{}
	}
	reservedArchives := make(map[string]struct{})
	var applied, archived, applyRows, archiveRows, operations []string
	for _, row := range input.normalized {
		sourceRaw, exists := input.mapRaw[row.MapPath]
		if !exists {
			return preparation{}, fmt.Errorf("normalized verdict points to missing staged map: %s", row.MapPath)
		}
		base := filepath.Base(row.MapPath)
		switch row.Kind {
		case artifact.NormalizedConfirm, artifact.NormalizedAmend:
			if _, survives := post[row.MapPath]; !survives {
				operations = append(operations, "NOT-APPLIED\t"+row.MapPath+"\tpost-refine anchor rejection")
				continue
			}
			target := filepath.Join(request.Repo.Organ, "maps", base)
			if exists, err := pathExists(target); err != nil {
				return preparation{}, err
			} else if exists {
				return preparation{}, fmt.Errorf("map target collision: %s", target)
			}
			restamped, err := restampMap(sourceRaw, input.today, input.recordedTree, git)
			if err != nil {
				return preparation{}, fmt.Errorf("restamp %s: %w", row.MapPath, err)
			}
			candidate := filepath.Join(root, "maps", base)
			if err := writePrivateExclusive(candidate, restamped); err != nil {
				return preparation{}, err
			}
			finalGate, err := gate.Anchors(
				input.recordedTree,
				[]gate.MapInput{{Name: base, Text: string(restamped)}},
				git,
			)
			if err != nil {
				return preparation{}, fmt.Errorf("final ANCHORS gate for %s: %w", row.MapPath, err)
			}
			if len(finalGate.Accepted) != 1 || len(finalGate.Rejected) != 0 {
				return preparation{}, fmt.Errorf("restamped map failed final ANCHORS gate: %s", row.MapPath)
			}
			applied = append(applied, row.MapPath)
			applyRows = append(applyRows, fmt.Sprintf("%s\t%s\t%s", row.Kind, row.MapPath, row.Evidence))
			operations = append(operations, fmt.Sprintf("APPLY-%s\t%s\t%s", row.Kind, row.MapPath, row.Evidence))
		case artifact.NormalizedRefute:
			archiveName, err := availableArchiveName(request.Repo.Organ, base, input.today, reservedArchives)
			if err != nil {
				return preparation{}, err
			}
			reservedArchives[archiveName] = struct{}{}
			refuted := append([]byte(nil), sourceRaw...)
			refuted = append(refuted, []byte("\nVerdict: REFUTE — "+row.Evidence+"\n")...)
			if err := writePrivateExclusive(filepath.Join(root, "refuted", archiveName), refuted); err != nil {
				return preparation{}, err
			}
			archived = append(archived, "archive/"+archiveName)
			archiveRows = append(archiveRows, archiveName+"\t"+row.MapPath)
			operations = append(operations, "ARCHIVE-REFUTE\t"+row.MapPath+"\tarchive/"+archiveName)
		case artifact.NormalizedUnruled:
			operations = append(operations, "NOT-APPLIED\t"+row.MapPath+"\tunruled")
		default:
			return preparation{}, fmt.Errorf("unsupported normalized verdict %q", row.Kind)
		}
	}

	finalMaps, err := listMapNames(filepath.Join(root, "maps"))
	if err != nil {
		return preparation{}, err
	}
	membership, err := lane.BuildMembership(lane.MembershipRequest{
		FinalMaps: finalMaps, PreviousMaps: state.previousMaps, Existing: state.membership,
		LedgerPresent: state.lanesPresent, CurrentLane: request.Lane.Lane,
	})
	if err != nil {
		return preparation{}, fmt.Errorf("build lane membership: %w", err)
	}
	lanesRaw := artifact.RenderLaneMembership(membership)
	if err := writePrivateExclusive(filepath.Join(root, "lanes.tsv"), []byte(lanesRaw)); err != nil {
		return preparation{}, err
	}
	first, err := lane.RenderSurfaces(filepath.Join(root, "maps"), string(state.stmRaw), membership)
	if err != nil {
		return preparation{}, fmt.Errorf("render first surfaces: %w", err)
	}
	second, err := lane.RenderSurfaces(filepath.Join(root, "maps"), string(state.stmRaw), membership)
	if err != nil {
		return preparation{}, fmt.Errorf("render second surfaces: %w", err)
	}
	if first.STM != second.STM || !reflect.DeepEqual(first.Agents, second.Agents) {
		return preparation{}, errors.New("lane surfaces are not byte-stable")
	}
	// A map with no lesson indexes on its bare title, which teaches an agent
	// nothing until it opens the body. Report the count so the gap is visible
	// instead of reading as a healthy surface.
	if first.MissingLesson > 0 {
		operations = append(operations, fmt.Sprintf(
			"TITLE-ONLY\t%d\tmaps carry no `## Lesson`; their surface row floats a bare title",
			first.MissingLesson,
		))
	}
	if err := writePrivateExclusive(filepath.Join(root, "surfaces", "stm.md"), []byte(first.STM)); err != nil {
		return preparation{}, err
	}
	if err := writePrivateExclusive(filepath.Join(root, "surfaces-second", "stm.md"), []byte(second.STM)); err != nil {
		return preparation{}, err
	}
	derived := map[string][]byte{
		filepath.Join(request.Repo.Organ, "lanes.tsv"): []byte(lanesRaw),
		filepath.Join(request.Repo.Organ, "stm.md"):    []byte(first.STM),
	}
	lanes := sortedKeys(first.Agents)
	for _, mapLane := range lanes {
		body := []byte(first.Agents[mapLane])
		if err := writePrivateExclusive(filepath.Join(root, "surfaces", "agents", mapLane+".md"), body); err != nil {
			return preparation{}, err
		}
		if err := writePrivateExclusive(
			filepath.Join(root, "surfaces-second", "agents", mapLane+".md"),
			[]byte(second.Agents[mapLane]),
		); err != nil {
			return preparation{}, err
		}
		derived[filepath.Join(request.Repo.Organ, "agents", mapLane+".md")] = body
		operations = append(operations, fmt.Sprintf("SURFACE\tagents/%s.md\t%d map rows", mapLane, lineCount(body)))
	}

	explorerArchive := noExplorerArchive
	if state.explorer {
		name, err := availableArchiveName(request.Repo.Organ, "explorer-index.md", input.today, reservedArchives)
		if err != nil {
			return preparation{}, err
		}
		reservedArchives[name] = struct{}{}
		explorerArchive = name
		if err := writePrivateExclusive(filepath.Join(root, "legacy", name), state.explorerRaw); err != nil {
			return preparation{}, err
		}
		operations = append(operations, "MIGRATE-SURFACE\texplorer-index.md\tarchive/"+name)
	}
	sweepTarget, err := availableSweepName(request.Repo.Organ, input.today)
	if err != nil {
		return preparation{}, err
	}
	applyRaw := renderRows(applyRows)
	archiveRaw := renderRows(archiveRows)
	opsRaw := renderRows(operations)
	for name, raw := range map[string][]byte{
		"apply-candidates.tsv": []byte(applyRaw), "archive-plan.tsv": []byte(archiveRaw), "ops.tsv": []byte(opsRaw),
		"explorer-archive.txt": []byte(explorerArchive + "\n"), "sweep-target.txt": []byte(sweepTarget + "\n"),
	} {
		if err := writePrivateExclusive(filepath.Join(root, name), raw); err != nil {
			return preparation{}, err
		}
	}
	sweepRaw := renderSweep(input, lanesRaw, opsRaw, now)
	if err := writePrivateExclusive(filepath.Join(root, "sweep.md"), sweepRaw); err != nil {
		return preparation{}, err
	}
	appliedRaw := []byte("APPLIED\t" + now.Format(time.RFC3339) + "\n")
	if err := writePrivateExclusive(filepath.Join(root, "APPLIED"), appliedRaw); err != nil {
		return preparation{}, err
	}
	return preparation{
		root: root, sweepTarget: sweepTarget, explorerArchive: explorerArchive,
		appliedMaps: applied, archivedMaps: archived, derived: derived,
		state: input.hold, sweepRaw: sweepRaw, appliedRaw: appliedRaw,
	}, nil
}

func renderSweep(input stagedInput, lanesRaw, operationsRaw string, now time.Time) []byte {
	var out bytes.Buffer
	fmt.Fprintf(&out, "# Dreamer sweep — %s\n\n", input.today)
	out.WriteString("## Coverage\n\n")
	out.Write(input.windowRaw)
	fmt.Fprintf(&out, "paths-sha256\t%s\n", input.paths.Digest)
	out.Write(input.censusRaw)
	writeSweepBlock(&out, "### Paths", input.paths.Raw)
	writeSweepBlock(&out, "### Typed gaps", input.gapsRaw)
	writeSweepBlock(&out, "### Coverage", []byte(input.expandedCoverage))
	out.WriteString("\n## Gate results\n")
	writeSweepBlock(&out, "### Distill anchor gate", input.distillAnchorsRaw)
	writeSweepBlock(&out, "### Post-verify anchor gate", []byte(input.postAnchorsRaw))
	writeSweepBlock(&out, "### Lane membership", []byte(lanesRaw))
	out.WriteString("\n## Verdicts\n")
	writeSweepBlock(&out, "", []byte(input.normalizedRaw))
	out.WriteString("\n## Ops\n")
	writeSweepBlock(&out, "", []byte(operationsRaw))
	fmt.Fprintf(&out, "\nEND-OF-SWEEP\nApplied: %s\n", now.Format(time.RFC3339))
	return out.Bytes()
}

func writeSweepBlock(out *bytes.Buffer, heading string, raw []byte) {
	if heading != "" {
		out.WriteString("\n")
		out.WriteString(heading)
		out.WriteString("\n")
	}
	out.WriteString("\n```text\n")
	out.Write(raw)
	out.WriteString("```\n")
}

// MapFingerprint reproduces the legacy map-pool pin: each sorted *.md file is
// hashed, rows are "<sha256>  <basename>\n", then the row stream is hashed.
func MapFingerprint(mapsDirectory string) (string, error) {
	names, err := listMapNames(mapsDirectory)
	if err != nil {
		return "", err
	}
	var rows bytes.Buffer
	for _, name := range names {
		raw, err := readRegular(filepath.Join(mapsDirectory, name))
		if err != nil {
			return "", err
		}
		sum := sha256.Sum256(raw)
		fmt.Fprintf(&rows, "%s  %s\n", hex.EncodeToString(sum[:]), name)
	}
	total := sha256.Sum256(rows.Bytes())
	return hex.EncodeToString(total[:]), nil
}

func validateReady(layout artifact.StageLayout, derived artifact.HoldState, yield int) error {
	raw, err := readPrivate(filepath.Join(layout.Root, "READY-FOR-APPLY"))
	if err != nil {
		return err
	}
	line, err := exactLine(raw, "READY-FOR-APPLY")
	if err != nil {
		return err
	}
	match := readyLinePattern.FindStringSubmatch(line)
	if match == nil {
		return errors.New("READY-FOR-APPLY has invalid grammar")
	}
	if _, err := time.Parse(time.RFC3339, match[2]); err != nil {
		return fmt.Errorf("READY-FOR-APPLY has invalid timestamp: %w", err)
	}
	if artifact.HoldState(match[1]) != derived {
		return fmt.Errorf("READY-FOR-APPLY state mismatch: staged %s, derived %s", match[1], derived)
	}
	yieldLine, err := readPrivateLine(filepath.Join(layout.Meta, "apply-yield.txt"))
	if err != nil {
		return err
	}
	parsed, err := strconv.Atoi(yieldLine)
	if err != nil || parsed < 0 || strconv.Itoa(parsed) != yieldLine {
		return errors.New("staged apply yield is invalid")
	}
	if parsed != yield {
		return fmt.Errorf("staged apply yield mismatch: staged %d, derived %d", parsed, yield)
	}
	return nil
}

func readOrganState(repo artifact.RepoContext) (organState, error) {
	fingerprint, err := MapFingerprint(filepath.Join(repo.Organ, "maps"))
	if err != nil {
		return organState{}, err
	}
	previous, err := listMapNames(filepath.Join(repo.Organ, "maps"))
	if err != nil {
		return organState{}, err
	}
	stmRaw, err := readRegular(filepath.Join(repo.Organ, "stm.md"))
	if err != nil {
		return organState{}, err
	}
	state := organState{fingerprint: fingerprint, previousMaps: previous, stmRaw: stmRaw}
	lanesPath := filepath.Join(repo.Organ, "lanes.tsv")
	if exists, err := pathExists(lanesPath); err != nil {
		return organState{}, err
	} else if exists {
		state.lanesRaw, err = readRegular(lanesPath)
		if err != nil {
			return organState{}, err
		}
		state.membership, err = artifact.ParseLaneMembership(string(state.lanesRaw))
		if err != nil {
			return organState{}, fmt.Errorf("parse organ lane membership: %w", err)
		}
		state.lanesPresent = true
	} else {
		state.membership = artifact.LaneMembership{}
	}
	explorerPath := filepath.Join(repo.Organ, "explorer-index.md")
	if exists, err := pathExists(explorerPath); err != nil {
		return organState{}, err
	} else if exists {
		state.explorerRaw, err = readRegular(explorerPath)
		if err != nil {
			return organState{}, err
		}
		state.explorer = true
	}
	agentsPath := filepath.Join(repo.Organ, "agents")
	if exists, err := pathExists(agentsPath); err != nil {
		return organState{}, err
	} else if exists {
		info, err := os.Lstat(agentsPath)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return organState{}, fmt.Errorf("organ agents path is not a real directory: %s", agentsPath)
		}
		state.agents = true
	}
	return state, nil
}

func revalidateOrganState(repo artifact.RepoContext, before organState, prepared preparation) error {
	current, err := readOrganState(repo)
	if err != nil {
		return fmt.Errorf("revalidate organ before mutation: %w", err)
	}
	if current.fingerprint != before.fingerprint || current.lanesPresent != before.lanesPresent ||
		!bytes.Equal(current.lanesRaw, before.lanesRaw) || !bytes.Equal(current.stmRaw, before.stmRaw) ||
		current.explorer != before.explorer || !bytes.Equal(current.explorerRaw, before.explorerRaw) {
		return errors.New("organ changed during apply preparation; no organ files written")
	}
	for _, path := range prepared.appliedMaps {
		if exists, err := pathExists(filepath.Join(repo.Organ, path)); err != nil {
			return err
		} else if exists {
			return fmt.Errorf("map target collision after preparation: %s", filepath.Join(repo.Organ, path))
		}
	}
	for _, path := range prepared.archivedMaps {
		if exists, err := pathExists(filepath.Join(repo.Organ, path)); err != nil {
			return err
		} else if exists {
			return fmt.Errorf("archive target collision after preparation: %s", filepath.Join(repo.Organ, path))
		}
	}
	if prepared.explorerArchive != noExplorerArchive {
		archive := filepath.Join(repo.Organ, "archive", prepared.explorerArchive)
		if exists, err := pathExists(archive); err != nil {
			return err
		} else if exists {
			return fmt.Errorf("legacy surface archive collision after preparation: %s", archive)
		}
	}
	sweep := filepath.Join(repo.Organ, "dreamer", prepared.sweepTarget)
	if exists, err := pathExists(sweep); err != nil {
		return err
	} else if exists {
		return fmt.Errorf("sweep target collision after preparation: %s", sweep)
	}
	return nil
}

func rejectReplayOrPreparation(layout artifact.StageLayout) error {
	for path, message := range map[string]string{
		filepath.Join(layout.Root, "APPLIED"): "apply stage was already applied",
		filepath.Join(layout.Root, "apply"):   "apply preparation already exists",
	} {
		exists, err := pathExists(path)
		if err != nil {
			return err
		}
		if exists {
			return fmt.Errorf("%s: %s", message, path)
		}
	}
	return nil
}

func readStagedMaps(directory string) ([]gate.MapInput, map[string][]byte, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, nil, fmt.Errorf("read staged maps: %w", err)
	}
	inputs := make([]gate.MapInput, 0, len(entries))
	rawByPath := make(map[string][]byte, len(entries))
	for _, entry := range entries {
		path := filepath.Join(directory, entry.Name())
		raw, err := readPrivate(path)
		if err != nil {
			return nil, nil, fmt.Errorf("read staged map: %w", err)
		}
		inputs = append(inputs, gate.MapInput{Name: entry.Name(), Text: string(raw)})
		rawByPath["maps/"+entry.Name()] = raw
	}
	sort.Slice(inputs, func(i, j int) bool { return inputs[i].Name < inputs[j].Name })
	return inputs, rawByPath, nil
}

func parseMapPathList(raw []byte, maps map[string][]byte) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if raw[len(raw)-1] != '\n' {
		return nil, errors.New("map path list lacks final newline")
	}
	rows := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	previous := ""
	for index, row := range rows {
		if !mapPathPattern.MatchString(row) {
			return nil, fmt.Errorf("invalid map path: %s", row)
		}
		if _, ok := maps[row]; !ok {
			return nil, fmt.Errorf("map path points to missing staged map: %s", row)
		}
		if index > 0 && row <= previous {
			return nil, errors.New("map path list is not sorted and unique")
		}
		previous = row
	}
	return rows, nil
}

func validateWindowIdentity(raw []byte, laneContext artifact.LaneContext) error {
	if len(raw) == 0 || raw[len(raw)-1] != '\n' {
		return errors.New("staged window is empty or lacks final newline")
	}
	values := map[string]string{}
	for offset, row := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
		fields := strings.Split(row, "\t")
		if len(fields) != 2 || fields[0] == "" || fields[1] == "" || hasControl(fields[0]) || hasControl(fields[1]) {
			return fmt.Errorf("invalid staged window row %d", offset+1)
		}
		if _, duplicate := values[fields[0]]; duplicate {
			return fmt.Errorf("duplicate staged window key: %s", fields[0])
		}
		values[fields[0]] = fields[1]
	}
	if values["agent-type"] != laneContext.AgentType || values["lane"] != laneContext.Lane {
		return errors.New("staged window agent or lane mismatch")
	}
	return nil
}

func validateGaps(raw []byte) (int, error) {
	if len(raw) == 0 {
		return 0, nil
	}
	if raw[len(raw)-1] != '\n' {
		return 0, errors.New("staged gaps lack final newline")
	}
	rows := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	previous := ""
	for offset, row := range rows {
		fields := strings.Split(row, "\t")
		if len(fields) != 3 || fields[0] != "META-PRESENT-TRANSCRIPT-MISSING" ||
			!filepath.IsAbs(fields[1]) || !filepath.IsAbs(fields[2]) || hasControl(fields[1]) || hasControl(fields[2]) {
			return 0, fmt.Errorf("invalid staged gap row %d", offset+1)
		}
		if offset > 0 && row <= previous {
			return 0, errors.New("staged gaps are not sorted and unique")
		}
		previous = row
	}
	return len(rows), nil
}

func validateAnchorLedger(raw []byte, maps []gate.MapInput, survivors []string) error {
	if len(maps) == 0 {
		if len(raw) != 0 || len(survivors) != 0 {
			return errors.New("empty staged map set has nonempty distill anchor artifacts")
		}
		return nil
	}
	if len(raw) == 0 || raw[len(raw)-1] != '\n' {
		return errors.New("distill anchor ledger is empty or lacks final newline")
	}
	expected := make(map[string]struct{}, len(maps))
	for _, input := range maps {
		expected["maps/"+input.Name] = struct{}{}
	}
	accepted := make(map[string]struct{}, len(survivors))
	for _, path := range survivors {
		accepted[path] = struct{}{}
	}
	seen := make(map[string]struct{}, len(maps))
	for offset, row := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
		fields := strings.SplitN(row, "\t", 3)
		if len(fields) != 3 || (fields[0] != "ACCEPT" && fields[0] != "REJECT") || fields[2] == "" {
			return fmt.Errorf("invalid distill anchor ledger row %d", offset+1)
		}
		if _, ok := expected[fields[1]]; !ok {
			return fmt.Errorf("distill anchor ledger names unknown map: %s", fields[1])
		}
		if _, duplicate := seen[fields[1]]; duplicate {
			return fmt.Errorf("duplicate distill anchor ledger row: %s", fields[1])
		}
		seen[fields[1]] = struct{}{}
		_, survivor := accepted[fields[1]]
		if (fields[0] == "ACCEPT") != survivor {
			return fmt.Errorf("distill anchor ledger and survivors disagree: %s", fields[1])
		}
	}
	if len(seen) != len(expected) {
		return errors.New("distill anchor ledger does not account for every staged map")
	}
	return nil
}

func renderAnchorResults(inputs []gate.MapInput, result gate.AnchorResult) string {
	accepted := make(map[string]struct{}, len(result.Accepted))
	for _, path := range result.Accepted {
		accepted[path] = struct{}{}
	}
	rejected := make(map[string]string, len(result.Rejected))
	for _, row := range result.Rejected {
		rejected[row.MapPath] = row.Reason
	}
	var out strings.Builder
	for _, input := range inputs {
		path := "maps/" + input.Name
		if _, ok := accepted[path]; ok {
			fmt.Fprintf(&out, "ACCEPT\t%s\tcanonical map and recorded-tree anchors\n", path)
		} else {
			fmt.Fprintf(&out, "REJECT\t%s\t%s\n", path, rejected[path])
		}
	}
	return out.String()
}

func renderPaths(paths []string) string {
	rows := append([]string(nil), paths...)
	sort.Strings(rows)
	return renderRows(rows)
}

func renderRows(rows []string) string {
	if len(rows) == 0 {
		return ""
	}
	return strings.Join(rows, "\n") + "\n"
}

func availableArchiveName(organRoot, base, today string, reserved map[string]struct{}) (string, error) {
	if !mapNamePattern.MatchString(base) && base != "explorer-index.md" {
		return "", fmt.Errorf("invalid archive basename: %s", base)
	}
	for counter := 1; ; counter++ {
		candidate := today + "-" + base
		if counter > 1 {
			candidate = fmt.Sprintf("%s-%d-%s", today, counter, base)
		}
		if _, used := reserved[candidate]; used {
			continue
		}
		exists, err := pathExists(filepath.Join(organRoot, "archive", candidate))
		if err != nil {
			return "", err
		}
		if !exists {
			return candidate, nil
		}
	}
}

func availableSweepName(organRoot, today string) (string, error) {
	for counter := 1; ; counter++ {
		candidate := today + ".md"
		if counter > 1 {
			candidate = fmt.Sprintf("%s-%d.md", today, counter)
		}
		exists, err := pathExists(filepath.Join(organRoot, "dreamer", candidate))
		if err != nil {
			return "", err
		}
		if !exists {
			return candidate, nil
		}
	}
}

func listMapNames(directory string) ([]string, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("read maps directory %s: %w", directory, err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".md" {
			continue
		}
		if !mapNamePattern.MatchString(entry.Name()) || strings.Contains(entry.Name(), "--") {
			return nil, fmt.Errorf("invalid map filename: %s", entry.Name())
		}
		info, err := os.Lstat(filepath.Join(directory, entry.Name()))
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("map is not a regular non-symlink file: %s", filepath.Join(directory, entry.Name()))
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names, nil
}

func lineCount(raw []byte) int {
	if len(raw) == 0 {
		return 0
	}
	return strings.Count(string(raw), "\n")
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func hasControl(value string) bool {
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}
