// Package journey is pfm's end-to-end suite: an owner's day typed into real terminals on a disposable
// machine (infra/fence/journey/run.sh on Linux, the CI runner on macOS), every step checked against
// the state underneath. It drives the owner's real paths with no jail, so it runs only where
// testjail.Disposable holds.
//
// Entry points: TestMain and TestJourney in journey_test.go, built under the journey tag; this file
// carries no code, so the package exists for every build. The other files:
//
//   - tree_test.go: the tree, the one file that says what runs after what.
//   - state_test.go: State, the value handed down the tree.
//   - fleet_test.go: reads of pfm's state the steps share, the fleet-consistency check and the log gate.
//   - fleet_whole_test.go: the whole machine's check at the joins (fleetWhole): zombies and spinning pfm processes.
//   - services_{linux,darwin}_test.go: pfm's services as the OS's service manager lists them.
//   - shield_{linux,darwin}_test.go: the holder door, herdr-shield's holders (Linux only; a Mac has none).
//   - step_*_test.go: one owner step each.
package journey
