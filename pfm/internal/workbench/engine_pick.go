package workbench

import pfmengine "github.com/rezzminator/professor/pfm/internal/engine"

// PickEngine chooses an enabled engine with an available account.
func PickEngine(bench Bench, preferred pfmengine.ID, usable func(pfmengine.ID) bool) (pfmengine.ID, bool) {
	if bench.Enables(preferred) && usable(preferred) {
		return preferred, true
	}
	for _, id := range bench.Engines {
		if id != preferred && usable(id) {
			return id, true
		}
	}
	return "", false
}
