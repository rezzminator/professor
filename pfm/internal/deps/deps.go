// Package deps owns the external-command contract for pfm: which commands it may run on which
// platform, how it starts, signals and waits on them, and how it probes what is installed.
//
// Entry points: Registry and Resolve (registry.go) gate a command by platform; Runner, with
// RealRunner and FakeRunner (runner.go), runs one; Probe (probe.go) reads the installed versions.
// Files:
//   - registry.go: the command registry, its platform gates and the resolved binary paths.
//   - runner.go: the process runner, real and fake: run, start, detach, kill, group signals.
//   - runner_script.go: the FakeRunner's Run answers: Script, Handle (computed at call time), the ledger.
//   - test_allowance.go: AllowInTest, a test's leave to run a real command a door refuses in tests.
//   - runner_output.go: StartOutput, the output and exit a FakeRunner child plays at Start.
//   - runner_group_linux.go, runner_group_darwin.go: GroupGone, when a refused group signal means the group is gone.
//   - probe.go: the version and self-doctor probes and their results.
//   - probe_home.go: the throwaway engine home a probe runs an engine in.
//   - commit_gate.go: the commit section a deadline may not interrupt once it starts.
//   - environment.go: the child environments: one key set once, git's repository variables dropped.
//   - dial.go: the unix socket client door.
package deps
