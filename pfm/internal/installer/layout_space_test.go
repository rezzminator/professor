package installer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// spaceFixture is a layout env whose probe puts every path under the clone on
// device 2 and everything else on device 1, with free bytes per device.
func spaceFixture(t *testing.T, free map[uint64]uint64) (LayoutEnv, *[]string) {
	t.Helper()
	env := layoutFixture(t)
	probed := &[]string{}
	env.spaceProbe = func(dir string) (uint64, uint64, error) {
		*probed = append(*probed, dir)
		device := uint64(1)
		if dir == env.Clone || pathWithin(dir, env.Clone) {
			device = 2
		}
		return device, free[device], nil
	}
	return env, probed
}

// spaceFindings is one journaled file rewrite, one cross-filesystem config
// move, and the findings apply never acts on.
func spaceFindings(t *testing.T, env LayoutEnv) ([]LayoutFinding, []string, uint64, uint64) {
	t.Helper()
	if err := os.Remove(env.ConfigPath); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(env.LegacyConfigDir, "pfm.config.json")
	layoutWrite(t, legacy, `{"version":2,"padding":"xxxxxxxxxx"}`)
	zshrc := filepath.Join(env.Home, ".zshrc")
	planned := filepath.Join(env.Home, ".local", "share", "pfm", "install", "tree")
	layoutWrite(t, filepath.Join(planned, "a"), "aaaa")
	layoutWrite(t, filepath.Join(planned, "nested", "b"), "bbbbbbbb")
	findings := []LayoutFinding{
		{Row: layoutRowZshrc, Verdict: VerdictRepoint, Path: zshrc},
		{Row: layoutRowConfig, Verdict: VerdictMove, Source: legacy, Path: env.ConfigPath},
		{Row: layoutRowCacheDB, Verdict: VerdictOK, Path: env.CacheDB},
		{Row: layoutRowSharedDB, Verdict: VerdictRefuse, Path: env.StateDB},
		{Row: layoutRowStagedPrompts, Verdict: VerdictRemove, Path: env.StateDB, Err: errors.New("unreadable")},
	}
	size := func(path string) uint64 {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		return uint64(info.Size())
	}
	journalNeed := size(zshrc) + size(legacy) + 4 + 8
	return findings, []string{planned, filepath.Join(env.Home, "absent")}, journalNeed, size(legacy)
}

func TestCheckInstallSpaceCountsJournalCopiesAndCrossFilesystemMoves(t *testing.T) {
	env, _ := spaceFixture(t, map[uint64]uint64{1: 0, 2: 0})
	findings, planned, journalNeed, moveNeed := spaceFindings(t, env)
	err := CheckInstallSpace(env, findings, planned)
	migrations := filepath.Join(env.Home, ".local", "state", "pfm", "migrations")
	for _, want := range []string{
		fmt.Sprintf("not enough free space on %s: need %d bytes + margin %d, have 0 — nothing changed",
			migrations, journalNeed, uint64(1)<<30),
		fmt.Sprintf("not enough free space on %s: need %d bytes + margin %d, have 0 — nothing changed",
			filepath.Dir(env.ConfigPath), moveNeed, uint64(1)<<30),
	} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("CheckInstallSpace() err=%v, want a line %q", err, want)
		}
	}
}

func TestCheckInstallSpaceRefusesBelowTheMarginAndPassesAtIt(t *testing.T) {
	margin := uint64(1) << 30
	for _, test := range []struct {
		name   string
		spare  int64
		refuse bool
	}{
		{"one byte short", -1, true},
		{"exactly enough", 0, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			free := map[uint64]uint64{}
			env, _ := spaceFixture(t, free)
			findings, planned, journalNeed, moveNeed := spaceFindings(t, env)
			free[1] = uint64(int64(journalNeed+margin) + test.spare)
			free[2] = moveNeed + margin
			err := CheckInstallSpace(env, findings, planned)
			if test.refuse != (err != nil) {
				t.Fatalf("CheckInstallSpace() err=%v, want refusal=%t", err, test.refuse)
			}
			if err != nil && !strings.Contains(err.Error(), fmt.Sprintf("have %d — nothing changed", free[1])) {
				t.Fatalf("refusal lacks the free bytes: %v", err)
			}
		})
	}
}

func TestCheckInstallSpaceMarginIsATenthOfALargeNeed(t *testing.T) {
	env, _ := spaceFixture(t, map[uint64]uint64{1: 0, 2: 0})
	sparse := filepath.Join(env.Home, "sparse")
	file, err := os.Create(sparse)
	if err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(file.Truncate(20<<30), file.Close()); err != nil {
		t.Fatal(err)
	}
	err = CheckInstallSpace(env, nil, []string{sparse})
	want := fmt.Sprintf("need %d bytes + margin %d, have 0", uint64(20)<<30, uint64(2)<<30)
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("CheckInstallSpace() err=%v, want %q", err, want)
	}
}

func TestCheckInstallSpaceNeverReadsAFailedProbeAsEnough(t *testing.T) {
	env := layoutFixture(t)
	env.spaceProbe = func(string) (uint64, uint64, error) { return 0, 0, errors.New("statfs denied") }
	migrations := filepath.Join(env.Home, ".local", "state", "pfm", "migrations")
	err := CheckInstallSpace(env, nil, []string{filepath.Join(env.Home, ".zshrc")})
	want := "could not measure free space on " + migrations + ": statfs denied"
	if err == nil || err.Error() != want {
		t.Fatalf("CheckInstallSpace() err=%v, want %q", err, want)
	}
}

func TestLayoutMoveReadsFreeSpaceThroughTheProbe(t *testing.T) {
	env, probed := spaceFixture(t, map[uint64]uint64{1: 1 << 40, 2: 3})
	source := filepath.Join(env.Home, "source.txt")
	layoutWrite(t, source, "more than three bytes")
	err := moveLayoutPath(env, source, filepath.Join(env.Clone, "moved.txt"))
	if err == nil || !strings.Contains(err.Error(), "insufficient free space: need 21 bytes, have 3") {
		t.Fatalf("moveLayoutPath() err=%v probed=%v, want the probe's free space", err, *probed)
	}
}
