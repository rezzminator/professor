//go:build race

package harvest

// raceEnabled reports whether this test binary was built with -race: the
// race detector allocates on the instrumented paths, so an allocation
// ceiling (testing.AllocsPerRun) holds only without it.
const raceEnabled = true
