package testjail

import "errors"

// maxRSSKB: Darwin reports ru_maxrss in bytes.
func maxRSSKB(v int64) int64 { return v / 1024 }

// runDelaySeconds: Darwin has no per-thread scheduler-delay counter.
func runDelaySeconds() (float64, error) {
	return 0, errors.New("run delay: not available on darwin")
}

// psiDir is unused on Darwin: it has no pressure stall information.
const psiDir = ""

// readPSI: Darwin has no pressure stall information.
func readPSI(string) (map[string]float64, error) {
	return nil, errors.New("psi: not available on darwin")
}
