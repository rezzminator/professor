package corpus

import (
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

func RenderPaths(paths []string) (string, error) {
	canonical := sortedUnique(paths)
	if len(canonical) != len(paths) {
		return "", fmt.Errorf("paths are not unique")
	}
	for index, path := range paths {
		if index > 0 && path <= paths[index-1] {
			return "", fmt.Errorf("paths are not sorted")
		}
		if !filepath.IsAbs(path) || path == "" || hasControl(path) {
			return "", fmt.Errorf("path is blank, relative, or contains a control character: %q", path)
		}
	}
	if len(paths) == 0 {
		return "", nil
	}
	return strings.Join(paths, "\n") + "\n", nil
}

func RenderGaps(gaps []Gap) (string, error) {
	canonical := sortedUniqueGaps(gaps)
	if len(canonical) != len(gaps) {
		return "", fmt.Errorf("gaps are not unique")
	}
	for index, gap := range gaps {
		if gap.Kind != GapMissingTranscript || !filepath.IsAbs(gap.Meta) || !filepath.IsAbs(gap.Transcript) ||
			hasControl(gap.Meta) || hasControl(gap.Transcript) {
			return "", fmt.Errorf("invalid coverage gap at index %d", index)
		}
		if index > 0 {
			previous := gaps[index-1]
			if gap.Kind < previous.Kind ||
				(gap.Kind == previous.Kind && gap.Meta < previous.Meta) ||
				(gap.Kind == previous.Kind && gap.Meta == previous.Meta && gap.Transcript < previous.Transcript) {
				return "", fmt.Errorf("gaps are not sorted")
			}
		}
	}
	var rendered strings.Builder
	for _, gap := range gaps {
		fmt.Fprintf(&rendered, "%s\t%s\t%s\n", gap.Kind, gap.Meta, gap.Transcript)
	}
	return rendered.String(), nil
}

func RenderWindow(window Window) (string, error) {
	if window.AgentType == "" || window.Lane == "" || hasControl(window.AgentType) || hasControl(window.Lane) {
		return "", fmt.Errorf("window agent type and lane must be non-empty and control-free")
	}
	if window.EnumeratedAt.IsZero() {
		return "", fmt.Errorf("window enumerated-at is missing")
	}
	var rendered strings.Builder
	switch window.Mode {
	case WindowExplicitCorpus:
		if !filepath.IsAbs(window.CorpusFile) || hasControl(window.CorpusFile) || !isSHA256(window.CorpusFileSHA256) ||
			window.CutoffExclusive != windowNone {
			return "", fmt.Errorf("invalid explicit corpus window")
		}
		fmt.Fprintf(&rendered, "window-mode\t%s\n", window.Mode)
		fmt.Fprintf(&rendered, "corpus-file\t%s\n", window.CorpusFile)
		fmt.Fprintf(&rendered, "corpus-file-sha256\t%s\n", window.CorpusFileSHA256)
	case WindowBootstrap:
		if window.BootstrapCount <= 0 || window.CutoffExclusive != windowNone {
			return "", fmt.Errorf("bootstrap window requires a positive count")
		}
		fmt.Fprintf(&rendered, "window-mode\t%s\n", window.Mode)
		fmt.Fprintf(&rendered, "bootstrap-count\t%d\n", window.BootstrapCount)
	case WindowSweepCutoff:
		if window.NewestAppliedSweep == "" || window.CutoffSource == "" || window.CutoffExclusive == "" ||
			window.CutoffTime.IsZero() {
			return "", fmt.Errorf("sweep window is incomplete")
		}
		if hasControl(window.NewestAppliedSweep) || hasControl(string(window.CutoffSource)) ||
			hasControl(window.CutoffExclusive) {
			return "", fmt.Errorf("sweep window contains a control character")
		}
		switch window.CutoffSource {
		case CutoffEnumeratedAt, CutoffApplied, CutoffFilenameDate:
			if window.NewestAppliedSweep == windowNone {
				return "", fmt.Errorf("sweep cutoff source requires a completed sweep")
			}
		case CutoffBootstrap:
			if window.NewestAppliedSweep != windowNone || window.CutoffExclusive != "7 days ago" {
				return "", fmt.Errorf("bootstrap cutoff has inconsistent sweep metadata")
			}
		default:
			return "", fmt.Errorf("unknown cutoff source %q", window.CutoffSource)
		}
		fmt.Fprintf(&rendered, "window-mode\t%s\n", window.Mode)
		fmt.Fprintf(&rendered, "newest-applied-sweep\t%s\n", window.NewestAppliedSweep)
		fmt.Fprintf(&rendered, "cutoff-source\t%s\n", window.CutoffSource)
	default:
		return "", fmt.Errorf("unknown window mode %q", window.Mode)
	}
	fmt.Fprintf(&rendered, "agent-type\t%s\n", window.AgentType)
	fmt.Fprintf(&rendered, "lane\t%s\n", window.Lane)
	fmt.Fprintf(&rendered, "cutoff-exclusive\t%s\n", window.CutoffExclusive)
	fmt.Fprintf(&rendered, "enumerated-at\t%s\n", window.EnumeratedAt.Format(time.RFC3339Nano))
	return rendered.String(), nil
}

func isSHA256(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func renderCandidates(candidates []Candidate) (string, error) {
	var rendered strings.Builder
	for index, candidate := range candidates {
		if candidate.MTime.IsZero() || !filepath.IsAbs(candidate.Meta) || !filepath.IsAbs(candidate.Transcript) ||
			hasControl(candidate.Meta) || hasControl(candidate.Transcript) {
			return "", fmt.Errorf("invalid bootstrap candidate at index %d", index)
		}
		if index > 0 {
			previous := candidates[index-1]
			if candidate.MTime.After(previous.MTime) ||
				(candidate.MTime.Equal(previous.MTime) && candidate.Meta <= previous.Meta) {
				return "", fmt.Errorf("bootstrap candidates are not deterministically ranked")
			}
		}
		seconds := candidate.MTime.Unix()
		fmt.Fprintf(
			&rendered,
			"%d.%09d\t%s\t%s\n",
			seconds,
			candidate.MTime.Nanosecond(),
			candidate.Meta,
			candidate.Transcript,
		)
	}
	return rendered.String(), nil
}
