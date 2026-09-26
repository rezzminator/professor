package harvestmcp

import (
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
)

// TestRuntimeFromConfigCarriesTheDownloadLimits pins the bridge from
// harvester.config.json to the runtime: the download and resource caps reach
// the server, and the TTLs are marked configured so defaults never override them.
func TestRuntimeFromConfigCarriesTheDownloadLimits(t *testing.T) {
	var harvester config.HarvesterConfig
	harvester.Cache.Dir = "cache"
	harvester.Harvest.MaxDownloadBytes = 1 << 20
	harvester.Harvest.MaxResourceBytes = 1 << 10
	runtime := RuntimeFromConfig("home", harvester)
	if runtime.Home != "home" || runtime.CacheDir != "cache" || !runtime.TTLsConfigured {
		t.Fatalf("runtime lost its paths or TTL flag: %+v", runtime)
	}
	if runtime.MaxDownloadBytes != 1<<20 || runtime.MaxResourceBytes != 1<<10 {
		t.Fatalf("download limits not carried: download=%d resource=%d",
			runtime.MaxDownloadBytes, runtime.MaxResourceBytes)
	}
}
