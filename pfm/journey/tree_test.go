//go:build journey

package journey

import "github.com/rezzminator/professor/pfm/journey/harness"

// tree is the owner's day: install, then a plain pfm and a Herdr session side by side, the Herdr one
// starting a Claude chat from the picker, reloading it and ending it; then the uninstall.
func tree() harness.Node[State] {
	return harness.Do("install", install,
		harness.Together("day", nil,
			harness.Do("plain-pfm", plainPfm),
			harness.Do("herdr", herdr,
				harness.Do("claude-from-picker", claudeFromPicker,
					harness.Do("reload-typed", reloadTyped,
						harness.Do("end", end),
					),
				),
			),
		),
		harness.Do("uninstall", uninstall),
	)
}
