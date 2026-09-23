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
