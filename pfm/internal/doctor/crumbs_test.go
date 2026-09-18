package doctor

import (
	"os"
	"path/filepath"
	"testing"
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
