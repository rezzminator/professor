package installping

import (
	"fmt"
	"regexp"
	"slices"
)

// The contract's field patterns (infra/install-ping/src/ping.mjs holds the
// server's copy; the two judge the same body).
var (
	idPattern      = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	versionPattern = regexp.MustCompile(`^v?\d+\.\d+\.\d+(-[0-9A-Za-z.-]{1,32})?$`)
	archPattern    = regexp.MustCompile(`^[a-z0-9_]{1,16}$`)
	pluginPattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)
)

// maxPlugins is the most plugin names one ping carries.
const maxPlugins = 32

// product is the counter's name for pfm.
const product = "pfm"

// Ping is the JSON body the counter takes.
type Ping struct {
	ID      string   `json:"id"`
	Product string   `json:"product"`
	Version string   `json:"version"`
	OS      string   `json:"os"`
	Arch    string   `json:"arch"`
	Plugins []string `json:"plugins"`
	Event   string   `json:"event"`
}

// BuildPing is pfm's ping for the given fields, judged as the counter judges it:
// plugins become sorted and unique, a name outside the pattern is dropped and
// at most 32 are kept; any other field the counter would refuse is an error.
func BuildPing(id, version, goos, goarch string, plugins []string, event string) (Ping, error) {
	switch {
	case !idPattern.MatchString(id):
		return Ping{}, fmt.Errorf("install id %q is not a lowercase UUIDv4", id)
	case !versionPattern.MatchString(version):
		return Ping{}, fmt.Errorf("version %q is not [v]MAJOR.MINOR.PATCH[-suffix]", version)
	case !slices.Contains([]string{"darwin", "linux", "windows", "freebsd"}, goos):
		return Ping{}, fmt.Errorf("OS %q is not one the counter takes", goos)
	case !archPattern.MatchString(goarch):
		return Ping{}, fmt.Errorf("arch %q is not one the counter takes", goarch)
	case event != EventInstall && event != EventDaily:
		return Ping{}, fmt.Errorf("event %q is neither %s nor %s", event, EventInstall, EventDaily)
	}
	names := []string{}
	for _, name := range plugins {
		if pluginPattern.MatchString(name) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	names = slices.Compact(names)
	if len(names) > maxPlugins {
		names = names[:maxPlugins]
	}
	return Ping{ID: id, Product: product, Version: version, OS: goos, Arch: goarch, Plugins: names, Event: event}, nil
}
