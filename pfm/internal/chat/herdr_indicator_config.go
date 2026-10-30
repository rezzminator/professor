package chat

import (
	"fmt"
	"os"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

// herdrIndicatorConfig supplies the tab status mode from the live config.
// A stat on each read detects live edits without repeatedly decoding the config.
type herdrIndicatorConfig struct {
	path    string
	info    os.FileInfo
	loaded  bool
	runtime pfmconfig.Runtime
	err     error
	load    func(string) (pfmconfig.Runtime, error)
}

func (settings *herdrIndicatorConfig) refresh() {
	info, err := os.Stat(settings.path)
	if err != nil && !os.IsNotExist(err) {
		settings.loaded = false
		settings.err = fmt.Errorf("stat Herdr indicator config %s: %w", settings.path, err)
		return
	}
	if info != nil && !info.Mode().IsRegular() {
		settings.loaded = false
		settings.err = fmt.Errorf("herdr indicator config %s is not a regular file", settings.path)
		return
	}
	unchanged := info == nil && settings.info == nil
	if info != nil && settings.info != nil {
		prior := settings.info
		unchanged = os.SameFile(prior, info) && prior.Size() == info.Size() && prior.Mode() == info.Mode() &&
			prior.ModTime().Equal(info.ModTime())
	}
	if settings.loaded && unchanged {
		return
	}
	settings.loaded, settings.info = true, info
	load := settings.load
	if load == nil {
		load = pfmconfig.LoadRuntime
	}
	settings.runtime, settings.err = load(settings.path)
}

func (settings *herdrIndicatorConfig) mode() (string, error) {
	settings.refresh()
	if settings.err != nil {
		return "", fmt.Errorf("load Herdr tab status configuration: %w", settings.err)
	}
	return settings.runtime.Config.Chat.HerdrTabStatus, nil
}
