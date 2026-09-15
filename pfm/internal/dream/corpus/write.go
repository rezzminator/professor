package corpus

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"hostops/pfm/internal/dream/artifact"
)

// Write persists a previously enumerated result into an existing private
// stage. Every generated file is mode 0600. Explicit corpus bytes are copied
// from Result, not read again from their source path.
func Write(stage artifact.StageLayout, result Result) error {
	if err := requireStageDirectory(stage.Root, "stage root"); err != nil {
		return err
	}
	if err := requireStageDirectory(stage.Meta, "stage meta"); err != nil {
		return err
	}
	for _, field := range []struct {
		name string
		path string
	}{
		{name: "stage meta", path: stage.Meta},
		{name: "paths", path: stage.Paths},
		{name: "pin", path: stage.Pin},
	} {
		if !within(stage.Root, field.path) {
			return fmt.Errorf("%s path is outside stage root: %s", field.name, field.path)
		}
	}
	pathsRaw, err := RenderPaths(result.Paths)
	if err != nil {
		return fmt.Errorf("render paths: %w", err)
	}
	actualDigest := digest([]byte(pathsRaw))
	if result.PathsSHA256 != actualDigest {
		return fmt.Errorf(
			"paths digest mismatch: result has %s, rendered paths have %s",
			result.PathsSHA256,
			actualDigest,
		)
	}
	if result.Census.SelectedPairedTranscriptCount != len(result.Paths) {
		return fmt.Errorf(
			"census selected count %d does not match %d paths",
			result.Census.SelectedPairedTranscriptCount,
			len(result.Paths),
		)
	}
	if result.Census.CoverageGapCount != len(result.Gaps) {
		return fmt.Errorf(
			"census gap count %d does not match %d gaps",
			result.Census.CoverageGapCount,
			len(result.Gaps),
		)
	}
	if result.Census.PairedTranscriptCount != result.Census.SelectedPairedTranscriptCount+result.Census.OmittedPairedTranscriptCount {
		return fmt.Errorf("census paired count is inconsistent with selected and omitted counts")
	}
	if result.Census.WindowMetaCount != result.Census.AgentMetaCount+result.Census.ExcludedOtherAgentOrInvalidCount {
		return fmt.Errorf("census window count is inconsistent with agent and excluded counts")
	}
	if _, err := artifact.ParseCensus(artifact.RenderCensus(result.Census)); err != nil {
		return fmt.Errorf("invalid census: %w", err)
	}
	gapsRaw, err := RenderGaps(result.Gaps)
	if err != nil {
		return fmt.Errorf("render gaps: %w", err)
	}
	windowRaw, err := RenderWindow(result.Window)
	if err != nil {
		return fmt.Errorf("render window: %w", err)
	}
	candidateRaw := ""
	if result.Window.Mode == WindowBootstrap {
		if len(result.Selected) != len(result.Paths) {
			return fmt.Errorf(
				"bootstrap candidate count %d does not match %d paths",
				len(result.Selected),
				len(result.Paths),
			)
		}
		candidateRaw, err = renderCandidates(result.Selected)
		if err != nil {
			return fmt.Errorf("render bootstrap selection: %w", err)
		}
	}
	if result.Window.Mode == WindowExplicitCorpus && digest(result.CorpusFileBytes) != result.Window.CorpusFileSHA256 {
		return fmt.Errorf("corpus file digest mismatch before stage write")
	}

	writes := []struct {
		path string
		raw  []byte
	}{
		{stage.Paths, []byte(pathsRaw)},
		{stage.Pin, []byte(actualDigest + "\n")},
		{filepath.Join(stage.Root, "gaps.tsv"), []byte(gapsRaw)},
		{filepath.Join(stage.Root, "census.tsv"), []byte(artifact.RenderCensus(result.Census))},
		{filepath.Join(stage.Meta, "window.tsv"), []byte(windowRaw)},
	}
	if result.Window.Mode == WindowBootstrap {
		writes = append(writes, struct {
			path string
			raw  []byte
		}{filepath.Join(stage.Meta, "bootstrap-selection.tsv"), []byte(candidateRaw)})
	}
	if result.Window.Mode == WindowExplicitCorpus {
		writes = append(writes, struct {
			path string
			raw  []byte
		}{filepath.Join(stage.Meta, "corpus-file.txt"), result.CorpusFileBytes})
	}
	for _, write := range writes {
		if err := writePrivate(write.path, write.raw); err != nil {
			return fmt.Errorf("write corpus artifact %s: %w", write.path, err)
		}
	}
	return nil
}

func within(root, path string) bool {
	if !filepath.IsAbs(root) || !filepath.IsAbs(path) {
		return false
	}
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func requireStageDirectory(path, name string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("%s is not absolute: %s", name, path)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("stat %s %s: %w", name, path, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is not a real directory: %s", name, path)
	}
	return nil
}

func writePrivate(path string, raw []byte) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("output path is not absolute: %s", path)
	}
	directory := filepath.Dir(path)
	if err := requireStageDirectory(directory, "output directory"); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".dream-corpus-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(raw); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	keep = true
	return nil
}
