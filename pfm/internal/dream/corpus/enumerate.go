package corpus

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"hostops/pfm/internal/dream/artifact"
)

const windowNone = "NONE"

// Enumerate builds a deterministic transcript selection and census. It does
// not write the stage; Write persists the returned typed result.
//
// RULING-02 guard: if Codex rollouts are ever added at this boundary, dreamer
// seat rollouts must be excluded by construction so the organ cannot ingest its
// own distill/refiner output. Today only Claude agent-*.meta.json pairs enter.
func Enumerate(
	ctx artifact.RepoContext,
	lane artifact.LaneContext,
	selection Selection,
	now time.Time,
) (Result, error) {
	if now.IsZero() {
		return Result{}, fmt.Errorf("enumeration time is missing")
	}
	if err := validateInputs(ctx, lane, selection); err != nil {
		return Result{}, err
	}
	if selection.CorpusFile != "" {
		return enumerateCorpusFile(lane, selection.CorpusFile, now)
	}
	return enumerateRegistry(ctx, lane, selection.BootstrapCount, now)
}

func validateInputs(ctx artifact.RepoContext, lane artifact.LaneContext, selection Selection) error {
	if lane.AgentType == "" || lane.Lane == "" || hasControl(lane.AgentType) || hasControl(lane.Lane) {
		return fmt.Errorf("agent type and lane must be non-empty and control-free")
	}
	if selection.BootstrapCount < 0 {
		return fmt.Errorf("bootstrap count must be a positive integer")
	}
	if selection.BootstrapCount > 0 && selection.CorpusFile != "" {
		return fmt.Errorf("bootstrap count and corpus file are mutually exclusive")
	}
	if selection.CorpusFile != "" {
		if !filepath.IsAbs(selection.CorpusFile) || hasControl(selection.CorpusFile) {
			return artifact.ErrorAt(
				selection.CorpusFile,
				fmt.Errorf("corpus file requires an absolute control-free path: %s", selection.CorpusFile),
			)
		}
		return nil
	}
	if ctx.Organ == "" || !filepath.IsAbs(ctx.Organ) {
		return fmt.Errorf("organ requires an absolute path: %s", ctx.Organ)
	}
	if ctx.Registry == "" || !filepath.IsAbs(ctx.Registry) {
		return fmt.Errorf("registry requires an absolute path: %s", ctx.Registry)
	}
	return nil
}

func enumerateCorpusFile(lane artifact.LaneContext, path string, now time.Time) (Result, error) {
	raw, err := readRegularFile(path)
	if err != nil {
		return Result{}, artifact.ErrorAt(path, fmt.Errorf("read corpus file %s: %w", path, err))
	}
	listed := make([]string, 0)
	for _, line := range splitLines(raw) {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !filepath.IsAbs(line) {
			return Result{}, artifact.ErrorAt(line, fmt.Errorf("corpus path is not absolute: %s", line))
		}
		if hasControl(line) {
			return Result{}, artifact.ErrorAt(line, fmt.Errorf("corpus path contains a control character: %q", line))
		}
		info, err := os.Stat(line)
		if err != nil {
			return Result{}, artifact.ErrorAt(line, fmt.Errorf("corpus path is not a readable file: %s: %w", line, err))
		}
		if !info.Mode().IsRegular() {
			return Result{}, artifact.ErrorAt(line, fmt.Errorf("corpus path is not a readable file: %s", line))
		}
		listed = append(listed, line)
	}
	paths := sortedUnique(listed)
	pathsRaw, err := RenderPaths(paths)
	if err != nil {
		return Result{}, err
	}
	sourceDigest := digest(raw)
	window := Window{
		Mode:             WindowExplicitCorpus,
		CorpusFile:       path,
		CorpusFileSHA256: sourceDigest,
		AgentType:        lane.AgentType,
		Lane:             lane.Lane,
		CutoffExclusive:  windowNone,
		EnumeratedAt:     now,
	}
	return Result{
		Paths:       paths,
		PathsSHA256: digest([]byte(pathsRaw)),
		Census: artifact.Census{
			WindowMetaCount:                  len(listed),
			AgentMetaCount:                   len(listed),
			PairedTranscriptCount:            len(listed),
			SelectedPairedTranscriptCount:    len(paths),
			OmittedPairedTranscriptCount:     len(listed) - len(paths),
			CoverageGapCount:                 0,
			ExcludedOtherAgentOrInvalidCount: 0,
			InvalidMetaCount:                 0,
		},
		Window:            window,
		CorpusFileBytes:   append([]byte(nil), raw...),
		CutoffDescription: "corpus-file " + path,
	}, nil
}

func enumerateRegistry(
	ctx artifact.RepoContext,
	lane artifact.LaneContext,
	bootstrapCount int,
	now time.Time,
) (Result, error) {
	bootstrap := bootstrapCount > 0
	window := Window{
		AgentType:    lane.AgentType,
		Lane:         lane.Lane,
		EnumeratedAt: now,
	}
	if bootstrap {
		window.Mode = WindowBootstrap
		window.BootstrapCount = bootstrapCount
		window.CutoffExclusive = windowNone
	} else {
		cutoff, err := Cutoff(ctx.Organ, lane.Lane, now)
		if err != nil {
			return Result{}, fmt.Errorf("derive corpus cutoff: %w", err)
		}
		window = cutoff
		window.AgentType = lane.AgentType
		window.Lane = lane.Lane
		window.EnumeratedAt = now
	}

	metas, err := metadataFiles(ctx.Registry, window.CutoffTime, bootstrap)
	if err != nil {
		return Result{}, err
	}
	census := artifact.Census{WindowMetaCount: len(metas)}
	gaps := make([]Gap, 0)
	pairs := make([]Candidate, 0)
	for _, meta := range metas {
		agentType, err := agentType(meta.path)
		if err != nil {
			if isInvalidMetadata(err) {
				census.InvalidMetaCount++
				census.ExcludedOtherAgentOrInvalidCount++
				continue
			}
			return Result{}, artifact.ErrorAt(meta.path, fmt.Errorf("read registry metadata %s: %w", meta.path, err))
		}
		if agentType != lane.AgentType {
			census.ExcludedOtherAgentOrInvalidCount++
			continue
		}
		census.AgentMetaCount++
		transcript := strings.TrimSuffix(meta.path, ".meta.json") + ".jsonl"
		if err := validateRepresentableRegistryPath(meta.path, transcript); err != nil {
			return Result{}, err
		}
		info, err := os.Stat(transcript)
		if err != nil {
			if os.IsNotExist(err) {
				gaps = append(gaps, Gap{Kind: GapMissingTranscript, Meta: meta.path, Transcript: transcript})
				continue
			}
			return Result{}, artifact.ErrorAt(transcript, fmt.Errorf("stat paired transcript %s: %w", transcript, err))
		}
		if !info.Mode().IsRegular() {
			gaps = append(gaps, Gap{Kind: GapMissingTranscript, Meta: meta.path, Transcript: transcript})
			continue
		}
		census.PairedTranscriptCount++
		pairs = append(pairs, Candidate{Meta: meta.path, Transcript: transcript, MTime: meta.mtime})
	}

	selected := append([]Candidate(nil), pairs...)
	if bootstrap {
		sort.Slice(selected, func(i, j int) bool {
			if !selected[i].MTime.Equal(selected[j].MTime) {
				return selected[i].MTime.After(selected[j].MTime)
			}
			return selected[i].Meta < selected[j].Meta
		})
		if len(selected) > bootstrapCount {
			selected = selected[:bootstrapCount]
		}
	}
	paths := make([]string, 0, len(selected))
	for _, candidate := range selected {
		paths = append(paths, candidate.Transcript)
	}
	paths = sortedUnique(paths)
	gaps = sortedUniqueGaps(gaps)
	census.SelectedPairedTranscriptCount = len(paths)
	census.OmittedPairedTranscriptCount = census.PairedTranscriptCount - len(paths)
	census.CoverageGapCount = len(gaps)
	pathsRaw, err := RenderPaths(paths)
	if err != nil {
		return Result{}, err
	}
	description := window.CutoffExclusive
	if bootstrap {
		description = fmt.Sprintf("bootstrap-count %d", bootstrapCount)
	}
	return Result{
		Paths:             paths,
		PathsSHA256:       digest([]byte(pathsRaw)),
		Census:            census,
		Window:            window,
		Gaps:              gaps,
		Selected:          selected,
		CutoffDescription: description,
	}, nil
}

type metadataFile struct {
	path  string
	mtime time.Time
}

func metadataFiles(registry string, cutoff time.Time, all bool) ([]metadataFile, error) {
	info, err := os.Stat(registry)
	if err != nil {
		return nil, artifact.ErrorAt(registry, fmt.Errorf("stat registry %s: %w", registry, err))
	}
	if !info.IsDir() {
		return nil, artifact.ErrorAt(registry, fmt.Errorf("registry is not a directory: %s", registry))
	}
	var metas []metadataFile
	err = filepath.WalkDir(registry, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return artifact.ErrorAt(path, fmt.Errorf("walk registry at %s: %w", path, walkErr))
		}
		if entry.IsDir() {
			return nil
		}
		name := entry.Name()
		if !strings.HasPrefix(name, "agent-") || !strings.HasSuffix(name, ".meta.json") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return artifact.ErrorAt(path, fmt.Errorf("stat registry metadata %s: %w", path, err))
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if all || info.ModTime().After(cutoff) {
			metas = append(metas, metadataFile{path: path, mtime: info.ModTime()})
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("enumerate registry %s: %w", registry, err)
	}
	sort.Slice(metas, func(i, j int) bool { return metas[i].path < metas[j].path })
	return metas, nil
}

func agentType(path string) (string, error) {
	raw, err := readRegularFile(path)
	if err != nil {
		return "", err
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		if err == nil {
			err = fmt.Errorf("metadata root is not an object")
		}
		return "", invalidMetadataError{cause: err}
	}
	value, ok := object["agentType"]
	if !ok {
		return "", invalidMetadataError{cause: fmt.Errorf("agentType is missing")}
	}
	var agentType string
	if err := json.Unmarshal(value, &agentType); err != nil {
		return "", invalidMetadataError{cause: fmt.Errorf("agentType is not a string: %w", err)}
	}
	return agentType, nil
}

type invalidMetadataError struct {
	cause error
}

func (err invalidMetadataError) Error() string {
	return err.cause.Error()
}

func isInvalidMetadata(err error) bool {
	_, ok := err.(invalidMetadataError)
	return ok
}

func readRegularFile(path string) (raw []byte, returnErr error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := file.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close %s: %w", path, err))
		}
	}()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file")
	}
	raw, err = io.ReadAll(file)
	if err != nil {
		return nil, err
	}
	return raw, nil
}

func validateRepresentableRegistryPath(paths ...string) error {
	for _, path := range paths {
		if !filepath.IsAbs(path) || hasControl(path) {
			return fmt.Errorf("registry path cannot be represented safely: %s", path)
		}
	}
	return nil
}

func hasControl(value string) bool {
	return strings.IndexFunc(value, unicode.IsControl) >= 0
}

func sortedUnique(values []string) []string {
	sorted := append([]string(nil), values...)
	sort.Strings(sorted)
	result := sorted[:0]
	for _, value := range sorted {
		if len(result) == 0 || value != result[len(result)-1] {
			result = append(result, value)
		}
	}
	return result
}

func sortedUniqueGaps(values []Gap) []Gap {
	sorted := append([]Gap(nil), values...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Kind != sorted[j].Kind {
			return sorted[i].Kind < sorted[j].Kind
		}
		if sorted[i].Meta != sorted[j].Meta {
			return sorted[i].Meta < sorted[j].Meta
		}
		return sorted[i].Transcript < sorted[j].Transcript
	})
	result := sorted[:0]
	for _, value := range sorted {
		if len(result) == 0 || value != result[len(result)-1] {
			result = append(result, value)
		}
	}
	return result
}

func digest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func splitLines(raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}
	lines := strings.Split(string(raw), "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}
