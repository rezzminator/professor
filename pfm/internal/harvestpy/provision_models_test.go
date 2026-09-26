package harvestpy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestStageOCRModelsWarmOrOfflineNeverDownloads: a warm model root answers
// without starting anything; an offline install with a cold root is the
// named ErrOCRModelsOffline listing what is missing, and announces nothing.
func TestStageOCRModelsWarmOrOfflineNeverDownloads(t *testing.T) {
	root := t.TempDir()
	announced := 0
	options := OCRStageOptions{Root: root, Offline: true, Announce: func(string) { announced++ }}
	_, err := StageOCRModels(context.Background(), options)
	if !errors.Is(err, ErrOCRModelsOffline) || !strings.Contains(err.Error(), "docling") {
		t.Fatalf("offline cold staging = %v, want ErrOCRModelsOffline naming the missing sets", err)
	}
	if err := os.MkdirAll(OCRModelRoot(root), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, set := range ocrStagedSets {
		if err := os.WriteFile(
			filepath.Join(OCRModelRoot(root), "staged-"+set+".json"),
			[]byte(`{"files":[]}`),
			0o600,
		); err != nil {
			t.Fatal(err)
		}
	}
	options.Offline = false
	staging, err := StageOCRModels(context.Background(), options)
	if err != nil || !staging.AlreadyStaged || announced != 0 {
		t.Fatalf("warm staging = %+v, %v, announced %d; want already staged, no download", staging, err, announced)
	}
}

// TestStageOCRModelsRestagesArabicRecordedAsSkipped: a root staged before
// python-bidi was pinned holds every marker but Arabic's. The fast path must
// not answer "already staged" (the reader then asks for `pfm install` forever):
// staging runs, and a set the converter skipped again is named with its reason.
func TestStageOCRModelsRestagesArabicRecordedAsSkipped(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(OCRModelRoot(root), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, set := range []string{"docling", "latin", "zh", "ja", "ru"} {
		marker := filepath.Join(OCRModelRoot(root), "staged-"+set+".json")
		if err := os.WriteFile(marker, []byte(`{"files":[]}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	python := filepath.Join(RuntimeRoot(root, Platform{}), "project", ".venv", "bin", "python")
	if err := os.MkdirAll(filepath.Dir(python), 0o700); err != nil {
		t.Fatal(err)
	}
	fake, err := os.ReadFile(fakePython(t, `
import json,sys
for line in sys.stdin:
    reason = "no Arabic OCR: RapidOCR's Arabic model needs python-bidi"
    print(json.dumps({"ok":True,"bytes":1,"hebrew":"","staged":{"ar":{"skipped":reason}}}), flush=True)
`))
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(RuntimeRoot(root, Platform{}), "project", "converter.py")
	if err := os.WriteFile(script, []byte("# fake"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(python, fake, 0o700); err != nil {
		t.Fatal(err)
	}
	announced := 0
	staging, err := StageOCRModels(
		context.Background(),
		OCRStageOptions{Root: root, Announce: func(string) { announced++ }},
	)
	if staging.AlreadyStaged || announced == 0 {
		t.Fatalf("a root without Arabic's marker answered already staged (%+v, announced %d)", staging, announced)
	}
	if err == nil || !strings.Contains(err.Error(), "ar (skipped: no Arabic OCR") {
		t.Fatalf("staging = %v, want the error to name ar with the reason it was skipped", err)
	}
}
