package main

import (
	"bytes"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

func TestHarvestRuntimeCarriesConfiguredScholarlyProviders(t *testing.T) {
	home := t.TempDir()
	config := pfmconfig.Defaults(home, nil)
	config.Harvester.Cache.Dir = home + "/cache"
	config.Harvester.Scholarly.DOIMirrorURL = "https://mirror.example/doi-mirror"
	config.Harvester.Scholarly.IPFSCatalogURL = "https://ipfs-catalog.example"
	config.Harvester.Scholarly.DOIViewerURL = "https://doi-viewer.example"
	config.Harvester.Scholarly.MD5CatalogURL = "https://md5-catalog.example"
	config.Harvester.Scholarly.GoogleScholarURL = "https://scholar.example"
	config.Harvester.Scholarly.ContactEmail = "ops@example.com"
	runtime := harvestRuntime(commandRuntime{Config: config})

	for name, values := range map[string]struct{ got, want string }{
		"DOIMirrorURL":     {runtime.DOIMirrorURL, "https://mirror.example/doi-mirror"},
		"IPFSCatalogURL":   {runtime.IPFSCatalogURL, "https://ipfs-catalog.example"},
		"DOIViewerURL":     {runtime.DOIViewerURL, "https://doi-viewer.example"},
		"MD5CatalogURL":    {runtime.MD5CatalogURL, "https://md5-catalog.example"},
		"GoogleScholarURL": {runtime.GoogleScholarURL, "https://scholar.example"},
	} {
		if values.got != values.want {
			t.Errorf("runtime %s = %q, want %q", name, values.got, values.want)
		}
	}
	if runtime.ContactEmail != "ops@example.com" {
		t.Fatalf("runtime ContactEmail = %q, want sibling scholarly setting preserved", runtime.ContactEmail)
	}
}

// TestHarvestDispatchReachesHarvestcli: `pfm harvest <verb>` reaches the
// harvestcli verbs, `download` included, through main's dispatch.
func TestHarvestDispatchReachesHarvestcli(t *testing.T) {
	for verb, usage := range map[string]string{
		"download": "usage: pfm harvest download",
		"ask":      "usage: pfm harvest ask",
	} {
		var stdout, stderr bytes.Buffer
		if code := run([]string{"harvest", verb}, &stdout, &stderr); code != 2 ||
			!strings.Contains(stderr.String(), usage) {
			t.Errorf("pfm harvest %s: code=%d stderr=%q, want 2 and %q", verb, code, stderr.String(), usage)
		}
	}
}
