// Package professor is the blueprint's template store: it scaffolds a project once, pins its baseline, reports drift against the store, and prints the project-side doctor row.
//
// professor.go: StubStoreRunnerForTest, the seam every store git call crosses.
// baseline.go: the project's baseline file: Load, Save, BaselinePath, PinSummary.
// project.go: ResolveProjectRoot and PrintDoctor, the project-side doctor row and its report.
// project_decisions.go: the keep and accept decisions on report rows and their human output.
// project_open.go: OpenProjectRows, the rows a project's report still holds open.
// project_updates.go: RunProjectUpdates, each UPDATED row's diff and the commits since its pin.
// render.go: RenderScaffold, filling a scaffold's tokens from the manifest answers.
// scaffold.go: Scaffold, deploying the store's mapped templates into a project once.
// source_repo.go: DiscoverSourceRepo and IsSourceRepo, finding the source clone without git.
// store.go: ResolveStore and InspectStore, the template store, its SHA and its git calls.
// update_dispatch.go: RunProjectUpdate, the `pfm update` verbs on a project.
package professor

import (
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

// storeRunner runs every git call the store makes; StubStoreRunnerForTest swaps it.
var storeRunner deps.Runner = obs.Runner(deps.RealRunner{})

// StubStoreRunnerForTest replaces the store's git runner and returns the call
// that restores the one it replaced, so a test reads a store its fake git built.
func StubStoreRunnerForTest(runner deps.Runner) func() {
	previous := storeRunner
	storeRunner = runner
	return func() { storeRunner = previous }
}
