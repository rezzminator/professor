package harvestmcp

import (
	"testing"
	"time"

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

// TestRuntimeFromConfigCarriesTheConverterPool: convert.workers, convert.queue
// and convert.timeoutSeconds reach the runtime the converter pool is sized
// from.
func TestRuntimeFromConfigCarriesTheConverterPool(t *testing.T) {
	var harvester config.HarvesterConfig
	harvester.Convert.Workers, harvester.Convert.Queue, harvester.Convert.Timeout = 3, 12, 4*time.Minute
	runtime := RuntimeFromConfig("home", harvester)
	if runtime.ConvertWorkers != 3 || runtime.ConvertQueue != 12 || runtime.ConvertTimeout != 4*time.Minute {
		t.Fatalf("converter pool settings not carried: workers=%d queue=%d timeout=%s",
			runtime.ConvertWorkers, runtime.ConvertQueue, runtime.ConvertTimeout)
	}
}
