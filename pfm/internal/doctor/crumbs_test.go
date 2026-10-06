package doctor

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/agentrole"
	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestDoctorLeakedProbeHomes(t *testing.T) {
	now := time.Date(2026, time.October, 6, 12, 0, 0, 0, time.UTC)
	for _, name := range []string{"stale", "missing", "scan fails", "entry info fails"} {
		t.Run(name, func(t *testing.T) {
			sidDir := filepath.Join(t.TempDir(), "sid")
			var output bytes.Buffer
			want, wantWarnings := "", 0
			readDir := os.ReadDir
			switch name {
			case "stale":
				if err := os.Mkdir(sidDir, 0o700); err != nil {
					t.Fatal(err)
				}
				for _, fixture := range []struct {
					name string
					age  time.Duration
				}{
					{"pfm-probe-home-a", 11 * time.Minute},
					{"pfm-probe-home-b", time.Minute},
					{"pfm-probe-home-boundary", 10 * time.Minute},
					{paths.SIDHarnessConfigDirPrefix + "old", time.Hour},
				} {
					path := filepath.Join(sidDir, fixture.name)
					if err := os.Mkdir(path, 0o700); err != nil {
						t.Fatal(err)
					}
					at := now.Add(-fixture.age)
					if err := os.Chtimes(path, at, at); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.WriteFile(
					filepath.Join(sidDir, "pfm-probe-home-file"),
					[]byte("file"),
					0o600,
				); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(sidDir, "pfm-probe-home-a")
				want = "doctor: warning probe_home " + path +
					" left 11m0s ago by a probe that never cleaned up — remove it: rm -rf " + path + "\n"
				wantWarnings = 1
			case "scan fails":
				if err := os.WriteFile(sidDir, []byte("file"), 0o600); err != nil {
					t.Fatal(err)
				}
				_, err := os.ReadDir(sidDir)
				want = fmt.Sprintf("doctor: warning probe_home could not look: %v\n", err)
				wantWarnings = 1
			case "entry info fails":
				path := filepath.Join(sidDir, "pfm-probe-home-a")
				if err := os.MkdirAll(path, 0o700); err != nil {
					t.Fatal(err)
				}
				readDir = func(path string) ([]os.DirEntry, error) {
					entries, err := os.ReadDir(path)
					if err != nil {
						return nil, err
					}
					if err := os.Rename(path, path+"-moved"); err != nil {
						t.Fatal(err)
					}
					_, err = entries[0].Info()
					want = fmt.Sprintf("doctor: warning probe_home could not look: %v\n", err)
					return entries, nil
				}
				wantWarnings = 1
			}
			var warnings int
			if name == "entry info fails" {
				warnings = printLeakedProbeHomesWith(&output, sidDir, now, readDir)
			} else {
				warnings = printLeakedProbeHomes(&output, sidDir, now)
			}
			if warnings != wantWarnings || output.String() != want {
				t.Fatalf("warnings=%d output=%q, want %d/%q", warnings, output.String(), wantWarnings, want)
			}
		})
	}
}

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

func TestDoctorCrumbsAcceptHarnessPromptRecords(t *testing.T) {
	root := jailTest(t)
	sidDir := filepath.Join(root, "sid")
	if err := agentrole.WriteHarnessPromptRecord(sidDir, "cc-1", "", filepath.Join(root, "alt.md")); err != nil {
		t.Fatal(err)
	}
	entries, invalid, err := crumbHealth(sidDir)
	if err != nil {
		t.Fatal(err)
	}
	if entries != 1 || invalid != 0 {
		t.Fatalf("crumbHealth() entries=%d invalid=%d, want the harness prompt record accepted", entries, invalid)
	}
}

// TestDoctorCrumbsAcceptHarnessCaptureConfigDirs pins the harness-prompt
// capture's config directory, in flight or left by a crash, as pfm's own.
func TestDoctorCrumbsAcceptHarnessCaptureConfigDirs(t *testing.T) {
	root := jailTest(t)
	sidDir := filepath.Join(root, "sid")
	// The harness capture's config dir and a dependency probe's throwaway
	// engine home: both live only while their run does, a crash leaves one.
	for _, prefix := range []string{paths.SIDHarnessConfigDirPrefix, paths.SIDEngineProbeHomePrefix} {
		if _, err := os.MkdirTemp(sidDir, prefix); err != nil {
			t.Fatal(err)
		}
	}
	entries, invalid, err := crumbHealth(sidDir)
	if err != nil {
		t.Fatal(err)
	}
	if entries != 2 || invalid != 0 {
		t.Fatalf("crumbHealth() entries=%d invalid=%d, want both throwaway config dirs accepted", entries, invalid)
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
