package main

import (
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
