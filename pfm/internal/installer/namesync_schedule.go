package installer

import (
	"fmt"
	"strconv"
	"time"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

// nameSyncTimerUnit is the systemd timer whose OnUnitInactiveSec carries the
// poll. It names both the embedded asset stageAssets renders and the unit
// wireUnits links, so the two can never be spelled differently.
const nameSyncTimerUnit = "pfm-name-sync.timer"

// The two markers each scheduler asset carries in place of a hard-coded poll.
// launchd counts whole seconds; systemd takes a duration it can parse itself.
const (
	launchdIntervalMarker = "__PFM_NAME_SYNC_INTERVAL_SECONDS__"
	systemdIntervalMarker = "__PFM_NAME_SYNC_INTERVAL__"
)

// nameSyncInterval resolves the poll ONE time for both schedulers. A zero or
// sub-minimum value is the shipped default rather than a broken unit: the
// machine config already refuses an unusable interval loudly at load time, so
// anything reaching here without one is a caller that never had a config.
func nameSyncInterval(options Options) time.Duration {
	if options.NameSyncInterval < pfmconfig.MinNameSyncInterval {
		return pfmconfig.DefaultNameSyncInterval
	}
	return options.NameSyncInterval
}

// renderNameSyncLaunchAgent puts the interval into the launch agent as whole
// seconds — launchd's StartInterval takes an integer and nothing else.
func renderNameSyncLaunchAgent(content []byte, options Options) ([]byte, error) {
	seconds := int64(nameSyncInterval(options) / time.Second)
	rendered, err := replaceSingleAssetMarker(
		string(content), launchdIntervalMarker, strconv.FormatInt(seconds, 10),
	)
	if err != nil {
		return nil, err
	}
	return []byte(rendered), nil
}

// renderNameSyncTimerAsset puts the SAME interval into the systemd timer's
// OnUnitInactiveSec. systemd parses "15m0s" as written, so the duration's own
// string is handed over rather than a second spelling of it.
func renderNameSyncTimerAsset(content []byte, options Options) ([]byte, error) {
	rendered, err := replaceSingleAssetMarker(
		string(content), systemdIntervalMarker, nameSyncInterval(options).String(),
	)
	if err != nil {
		return nil, err
	}
	return []byte(rendered), nil
}

// nameSyncScheduleSummary is the one line install prints about the poll, so a
// host that lowered it can see the value that actually landed in both units.
func nameSyncScheduleSummary(options Options) string {
	interval := nameSyncInterval(options)
	return fmt.Sprintf(
		"name-sync interval %s (launchd StartInterval=%d, systemd OnUnitInactiveSec=%s)",
		interval, int64(interval/time.Second), interval,
	)
}
