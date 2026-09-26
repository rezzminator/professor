package doctor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/agentrole"
	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestDoctorRecognizesThenFailedAsSatelliteMetadata(t *testing.T) {
	root := jailTest(t)
	sidDir := filepath.Join(root, "sid")
	for name, content := range map[string]string{
		"cc-1-2-3": "/transcripts/live.jsonl", "cc-1-2-3.then-failed": "prompt preserved for retry",
		"reload-cc-1-2-3.log": "completed fleet reload", "reload-vsct.log": "completed bunker reload",
		"reload-probe.log": "not a fleet reload", ".open.uuid": "lock metadata",
		"cc-1-2-3.not-metadata": "invalid",
	} {
		if err := os.WriteFile(filepath.Join(sidDir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	entries, invalid, err := crumbHealth(sidDir)
	if err != nil {
		t.Fatal(err)
	}
	if entries != 7 || invalid != 2 {
		t.Fatalf("crumbHealth() entries=%d invalid=%d", entries, invalid)
	}
}

func TestDoctorIgnoresBunkerCrumbsAndOpenLockDirectories(t *testing.T) {
	root := jailTest(t)
	sidDir := filepath.Join(root, "sid")
	for name, content := range map[string]string{
		"cc-1-2-3": "/transcripts/live.jsonl", "vsct": "/transcripts/bunker.jsonl",
		"vsct.%187": "/transcripts/bunker.jsonl", "rotten": "neither a crumb nor sid metadata",
	} {
		if err := os.WriteFile(filepath.Join(sidDir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, directory := range []string{".open.88888888-8888-4888-8888-888888888888", "rotten-directory"} {
		if err := os.Mkdir(filepath.Join(sidDir, directory), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	entries, invalid, err := crumbHealth(sidDir)
	if err != nil {
		t.Fatal(err)
	}
	if entries != 6 || invalid != 2 {
		t.Fatalf("crumbHealth() entries=%d invalid=%d, want 6 and 2", entries, invalid)
	}
}

// TestDoctorCrumbsAcceptPfmScratchDirectories pins the SID dir's own scratch
// purposes: `pfm doctor --verbose` and `pfm chat read` write under
// <SIDDir>/{pfm-doctor,chat-loads}, so those directories are pfm's, never rot —
// while any other directory there still is.
func TestDoctorCrumbsAcceptPfmScratchDirectories(t *testing.T) {
	root := jailTest(t)
	sidDir := filepath.Join(root, "sid")
	for _, directory := range append(paths.SIDScratchDirs(), "rotten-directory") {
		if err := os.Mkdir(filepath.Join(sidDir, directory), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	entries, invalid, err := crumbHealth(sidDir)
	if err != nil {
		t.Fatal(err)
	}
	if entries != 3 || invalid != 1 {
		t.Fatalf("crumbHealth() entries=%d invalid=%d, want 3 and 1 (only rotten-directory)", entries, invalid)
	}
}

func TestDoctorCrumbsAcceptStatuslineEffortRecords(t *testing.T) {
	root := jailTest(t)
	sidDir := filepath.Join(root, "sid")
	for _, name := range []string{
		paths.SIDEffortPrefix + "4a86bf3b-7fa4-4b5c-bb69-96b32b6f7dca",
		paths.SIDEffortPrefix,
	} {
		if err := os.WriteFile(filepath.Join(sidDir, name), []byte(`{"effort":"xhigh"}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	entries, invalid, err := crumbHealth(sidDir)
	if err != nil {
		t.Fatal(err)
	}
	if entries != 2 || invalid != 1 {
		t.Fatalf(
			"crumbHealth() entries=%d invalid=%d, want a session's effort record accepted and a bare prefix rejected",
			entries,
			invalid,
		)
	}
}

// TestDoctorCrumbsAcceptLiveRolePrompts writes through agentrole's own
// writer, so a renamed seat prompt fails here before the audit calls it rot.
func TestDoctorCrumbsAcceptLiveRolePrompts(t *testing.T) {
	root := jailTest(t)
	sidDir := filepath.Join(root, "sid")
	if err := agentrole.WriteSeatPrompt(sidDir, "cc-1", "%7", "role body"); err != nil {
		t.Fatal(err)
	}
	entries, invalid, err := crumbHealth(sidDir)
	if err != nil {
		t.Fatal(err)
	}
	if entries != 1 || invalid != 0 {
		t.Fatalf("crumbHealth() entries=%d invalid=%d, want the live role prompt accepted", entries, invalid)
	}
}

// TestDoctorCrumbsAcceptHarnessCaptureConfigDirs pins the harness-prompt
// capture's config directory, in flight or left by a crash, as pfm's own.
func TestDoctorCrumbsAcceptHarnessCaptureConfigDirs(t *testing.T) {
	root := jailTest(t)
	sidDir := filepath.Join(root, "sid")
	if _, err := os.MkdirTemp(sidDir, paths.SIDHarnessConfigDirPrefix); err != nil {
		t.Fatal(err)
	}
	entries, invalid, err := crumbHealth(sidDir)
	if err != nil {
		t.Fatal(err)
	}
	if entries != 1 || invalid != 0 {
		t.Fatalf("crumbHealth() entries=%d invalid=%d, want the harness config dir accepted", entries, invalid)
	}
}

// TestDoctorCrumbsAcceptHeadlessScratchFiles writes both headless scratch
// files the way writePreparedFile does, beside one unknown name that stays rot.
func TestDoctorCrumbsAcceptHeadlessScratchFiles(t *testing.T) {
	root := jailTest(t)
	sidDir := filepath.Join(root, "sid")
	for _, pattern := range []string{paths.SIDExchangeScratchPattern, paths.SIDCaptureScratchPattern} {
		if _, _, err := atomicfile.WriteScratch(sidDir, pattern, []byte("scratch")); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(sidDir, "exchange-notes.txt"), []byte("unknown"), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, invalid, err := crumbHealth(sidDir)
	if err != nil {
		t.Fatal(err)
	}
	if entries != 3 || invalid != 1 {
		t.Fatalf(
			"crumbHealth() entries=%d invalid=%d, want both scratch files accepted and the unknown name counted",
			entries,
			invalid,
		)
	}
}
