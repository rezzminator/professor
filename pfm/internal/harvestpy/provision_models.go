package harvestpy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// OCRModelDownloadBytes is the first-run OCR model download as staged: the
// models directory after a cold `pfm install` on linux-arm64 held 555 MB
// (docling layout+table plus the latin, zh, ja and ru RapidOCR models). The
// bake-off's 1.06 GB counted its whole hub cache, other engines included.
const OCRModelDownloadBytes int64 = 555_000_000

// ocrStagingTimeout bounds the whole staging run (a cold 1.06 GB download
// plus one warm-up conversion per script).
const ocrStagingTimeout = 45 * time.Minute

// ocrStagedSets are the staging markers converter.py's stage_models writes
// (staged-<set>.json) and its require_staged checks before every OCR read.
// Arabic is absent: its RapidOCR model needs python-bidi, which the pinned
// environment does not carry, so converter.py names it instead of staging it.
var ocrStagedSets = []string{"docling", "latin", "zh", "ja", "ru"}

// ErrOCRModelsOffline names an offline install whose OCR models were never
// staged: scanned PDFs fail by name until an install runs with network.
var ErrOCRModelsOffline = errors.New("OCR models are not staged and the install is offline")

// OCRStageOptions is one staging run: the harvest-python state root, its
// platform, whether the install is offline, and where the install's own
// output goes (Announce names the download and its size before it starts).
type OCRStageOptions struct {
	Root     string
	Platform Platform
	Offline  bool
	Announce func(string)
}

// OCRStaging is what a staging run found or did.
type OCRStaging struct {
	ModelRoot     string
	AlreadyStaged bool
	Bytes         int64
	Hebrew        string
	Skipped       map[string]string
}

// OCRModelRoot is the staged-model directory under the harvest-python root.
func OCRModelRoot(root string) string { return filepath.Join(root, "models") }

func missingOCRMarkers(modelRoot string) []string {
	var missing []string
	for _, set := range ocrStagedSets {
		if _, err := os.Stat(filepath.Join(modelRoot, "staged-"+set+".json")); err != nil {
			missing = append(missing, set)
		}
	}
	return missing
}

// StageOCRModels downloads the OCR models into the state dir once, through
// the provisioned interpreter, so every later read runs offline. A warm
// cache is a no-download answer; an offline install with a cold cache is
// ErrOCRModelsOffline, never a silent skip.
func StageOCRModels(ctx context.Context, options OCRStageOptions) (OCRStaging, error) {
	announce := options.Announce
	if announce == nil {
		announce = func(string) {}
	}
	modelRoot := OCRModelRoot(options.Root)
	staging := OCRStaging{ModelRoot: modelRoot}
	missing := missingOCRMarkers(modelRoot)
	if len(missing) == 0 {
		staging.AlreadyStaged = true
		return staging, nil
	}
	if options.Offline {
		return staging, fmt.Errorf("%w (missing: %s)", ErrOCRModelsOffline, strings.Join(missing, ", "))
	}
	announce(fmt.Sprintf(
		"downloading the OCR models (docling layout+table and one RapidOCR model per script, about %.2f GB) into %s",
		float64(OCRModelDownloadBytes)/1e9, modelRoot,
	))
	current := RuntimeRoot(options.Root, options.Platform)
	converter := NewConverter(Runtime{
		Python:       filepath.Join(current, "project", ".venv", "bin", "python"),
		Script:       filepath.Join(current, "project", "converter.py"),
		ModelRoot:    modelRoot,
		ModelStaging: true,
	})
	defer func() { _ = converter.Close() }()
	bounded, cancel := context.WithTimeout(ctx, ocrStagingTimeout)
	defer cancel()
	line, stderr, err := converter.request(bounded, []byte(`{"op":"stage_models"}`))
	if err != nil {
		return staging, fmt.Errorf("stage OCR models into %s: %w", modelRoot, err)
	}
	var response struct {
		OK     bool                       `json:"ok"`
		Error  string                     `json:"error"`
		Bytes  int64                      `json:"bytes"`
		Hebrew string                     `json:"hebrew"`
		Staged map[string]json.RawMessage `json:"staged"`
	}
	if err := json.Unmarshal(line, &response); err != nil {
		return staging, fmt.Errorf("decode OCR staging answer: %w (stderr: %s)", err, stderrTail(stderr))
	}
	if !response.OK {
		return staging, fmt.Errorf("stage OCR models: %s (stderr: %s)", response.Error, stderrTail(stderr))
	}
	staging.Bytes, staging.Hebrew = response.Bytes, response.Hebrew
	staging.Skipped = map[string]string{}
	for set, raw := range response.Staged {
		var skipped struct {
			Skipped string `json:"skipped"`
		}
		if json.Unmarshal(raw, &skipped) == nil && skipped.Skipped != "" {
			staging.Skipped[set] = skipped.Skipped
		}
	}
	if still := missingOCRMarkers(modelRoot); len(still) > 0 {
		return staging, fmt.Errorf(
			"stage OCR models: staging answered ok but left no marker for %s",
			strings.Join(still, ", "),
		)
	}
	return staging, nil
}
