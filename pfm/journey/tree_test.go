//go:build journey

package journey

import "github.com/rezzminator/professor/pfm/journey/harness"

// tree is the owner's day: install; then, side by side, a plain pfm, the default Herdr session with a
// Claude chat from the picker taken through a turn, a reload and its end, and a Herdr session of its
// own with a Claude chat from its picker; then the whole machine judged at the join; then the uninstall.
func tree() harness.Node[State] {
	return harness.Do("install", install,
		harness.Together("day", nil,
			harness.Do("plain-pfm", plainPfm),
			harness.Do("herdr", herdr,
				harness.Do("claude-from-picker", claudeFromPicker,
					harness.Do("turn", turn,
						harness.Do("reload-typed", reloadTyped,
							harness.Do("end", end),
						),
					),
				),
			),
			harness.Do("herdr-attention", herdrSession,
				harness.Do("claude-from-picker", claudeFromPicker),
			),
		),
		harness.Do("fleet", fleet),
		harness.Do("uninstall", uninstall),
	)
}
