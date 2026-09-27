package installer

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// layoutSpaceMarginFloor is the least headroom an applying install keeps on
// every filesystem it writes: max(1 GiB, a tenth of the need).
const layoutSpaceMarginFloor = uint64(1) << 30

// probeSpace reads the device and free bytes of the filesystem holding dir
// through env.spaceProbe; nil is the default statfs probe.
func (env LayoutEnv) probeSpace(dir string) (device, free uint64, err error) {
	if env.spaceProbe != nil {
		return env.spaceProbe(dir)
	}
	return statfsSpaceProbe(dir)
}

// layoutDevice widens a stat device number, whose type differs by kernel.
func layoutDevice[T ~int32 | ~uint32 | ~uint64](device T) uint64 { return uint64(device) }

// statfsSpaceProbe is the one statfs reader: the device of the nearest
// existing ancestor of dir (Lstat) and the bytes available to an unprivileged
// writer there.
func statfsSpaceProbe(dir string) (device, free uint64, err error) {
	existing := filepath.Clean(dir)
	for {
		info, err := os.Lstat(existing)
		if err == nil {
			stat, ok := info.Sys().(*syscall.Stat_t)
			if !ok {
				return 0, 0, fmt.Errorf("%s has no device identity", existing)
			}
			var space syscall.Statfs_t
			if err := syscall.Statfs(existing, &space); err != nil {
				return 0, 0, err
			}
			return layoutDevice(stat.Dev), space.Bavail * uint64(space.Bsize), nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return 0, 0, err
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return 0, 0, fmt.Errorf("no existing ancestor of %s", dir)
		}
		existing = parent
	}
}

// layoutSpaceNeed is the bytes one filesystem must take, and the first
// directory that charged it — the name a refusal gives the filesystem.
type layoutSpaceNeed struct {
	dir   string
	bytes uint64
}

// CheckInstallSpace refuses an applying install before its first change when
// a filesystem cannot take what the run will write: every path the journal
// will copy (the layout findings apply acts on, through layoutSnapshotPaths,
// and the installer's planned writes) is charged to the migrations directory's
// filesystem; the source of every cross-filesystem move is charged to its
// destination's. Each needs free >= need + max(1 GiB, need/10). A probe that
// fails refuses: an unmeasured filesystem is never read as having room.
func CheckInstallSpace(env LayoutEnv, findings []LayoutFinding, planned []string) error {
	migrations := filepath.Join(env.Home, ".local", "state", "pfm", "migrations")
	copies := append([]string(nil), planned...)
	type move struct{ source, destination string }
	moves := []move{}
	for _, finding := range findings {
		if !layoutFindingApplies(finding) {
			continue
		}
		paths, err := layoutSnapshotPaths(env, finding)
		if err != nil {
			return fmt.Errorf("plan layout %s %s: %w", finding.Row, finding.Path, err)
		}
		copies = append(copies, paths...)
		if finding.Row == layoutRowStateDB && env.ConfigPath != "" {
			copies = append(copies, layoutConfigMigrationPaths(env)...)
		}
		switch {
		case finding.Row == layoutRowStateDB || finding.Row == layoutRowCacheDB:
			for _, suffix := range []string{"", layoutDBWAL, layoutDBSHM} {
				moves = append(moves, move{finding.Source + suffix, finding.Path + suffix})
			}
		case finding.Verdict == VerdictMove:
			moves = append(moves, move{finding.Source, finding.Path})
		case finding.Row == layoutRowSessionStore && finding.Verdict == VerdictMerge:
			moves = append(moves, move{finding.Path, layoutSessionStore(env, finding)})
		}
	}
	needs := map[uint64]*layoutSpaceNeed{}
	order := []uint64{}
	free := map[uint64]uint64{}
	charge := func(dir string, bytes uint64) error {
		device, available, err := env.probeSpace(dir)
		if err != nil {
			return fmt.Errorf("could not measure free space on %s: %w", dir, err)
		}
		if needs[device] == nil {
			needs[device] = &layoutSpaceNeed{dir: dir}
			order = append(order, device)
			free[device] = available
		}
		needs[device].bytes += bytes
		return nil
	}
	journalBytes := uint64(0)
	seen := map[string]bool{}
	for _, path := range copies {
		if path == "" || seen[filepath.Clean(path)] {
			continue
		}
		seen[filepath.Clean(path)] = true
		size, err := layoutApparentBytes(path)
		if err != nil {
			return fmt.Errorf("size %s for the space preflight: %w", path, err)
		}
		journalBytes += size
	}
	if err := charge(migrations, journalBytes); err != nil {
		return err
	}
	for _, candidate := range moves {
		size, err := layoutApparentBytes(candidate.source)
		if err != nil || size == 0 {
			if err != nil {
				return fmt.Errorf("size %s for the space preflight: %w", candidate.source, err)
			}
			continue
		}
		sourceDevice, _, err := env.probeSpace(candidate.source)
		if err != nil {
			return fmt.Errorf("could not measure free space on %s: %w", candidate.source, err)
		}
		destination := filepath.Dir(candidate.destination)
		destinationDevice, _, err := env.probeSpace(destination)
		if err != nil {
			return fmt.Errorf("could not measure free space on %s: %w", destination, err)
		}
		if sourceDevice != destinationDevice {
			if err := charge(destination, size); err != nil {
				return err
			}
		}
	}
	refusals := []string{}
	for _, device := range order {
		need := needs[device]
		margin := max(layoutSpaceMarginFloor, need.bytes/10)
		if free[device] < need.bytes+margin {
			refusals = append(refusals, fmt.Sprintf(
				"not enough free space on %s: need %d bytes + margin %d, have %d — nothing changed",
				need.dir, need.bytes, margin, free[device]))
		}
	}
	if len(refusals) > 0 {
		return errors.New(strings.Join(refusals, "\n"))
	}
	return nil
}

// layoutFindingApplies mirrors ApplyLayout's choice of the findings it acts
// on, a database held by a service included: apply stops the service first.
func layoutFindingApplies(finding LayoutFinding) bool {
	if finding.Err != nil || finding.Verdict == VerdictOK {
		return false
	}
	if finding.Verdict != VerdictRefuse {
		return true
	}
	return (finding.Row == layoutRowStateDB || finding.Row == layoutRowCacheDB) &&
		strings.HasPrefix(finding.Detail, "held by pid ")
}

// layoutApparentBytes sums the apparent size of the regular files at and
// under path; an absent path is zero.
func layoutApparentBytes(path string) (uint64, error) {
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	} else if err != nil {
		return 0, err
	}
	total := uint64(0)
	err := filepath.WalkDir(path, func(_ string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		total += uint64(info.Size())
		return nil
	})
	return total, err
}

// PlanHarvestJournal names what an applying install journals for the
// harvester: its whole root, before a re-provision — which installHarvest runs
// when provisioning is on and Check does not report the environment healthy.
// A dry run never reaches that Check, so the space preflight asks here.
func PlanHarvestJournal(ctx context.Context, options Options) []string {
	if !options.ProvisionHarvest || options.HarvestProvisioner == nil {
		return nil
	}
	planner := &engine{options: options}
	root := harvestPythonRoot(options.Home)
	check, err := options.HarvestProvisioner.Check(ctx, root, planner.harvestPlatform())
	if err == nil && check.Healthy {
		return nil
	}
	return []string{root}
}
