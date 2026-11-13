---
paths:
  - "pfm/**"
  - "scripts/**"
  - ".claude/scripts/**"
  - "infra/fence/journey/**"
---

# Every OS difference in pfm lives in its door

Binds every agent writing pfm code, its tests, the journey or a shell script.

- pfm is one binary on two kernels, Linux and macOS; its doors: `pfm/internal/gather` (process table, executable, environment, terminals, memory, CPU, sockets, signals), `pfm/internal/deps` (`Runner`, `Process`: exec, detach, process-group signals), `pfm/internal/servicemanager` (systemd or launchd), `pfm/internal/paths`, `pfm/internal/atomicfile`; for shell, `scripts/platform.sh`.
- Inside a door, OS-specific code is a `_linux.go` / `_darwin.go` pair; a runtime OS test selects only data (`deps` registry `Platforms`).
- Outside a door no production code, test or journey step branches on the OS name, reads `/proc` or a cgroup, or runs a one-OS command; exempt: `pfm/internal/mockengine`, `pfm/internal/hostfixture`, `pfm/cmd/mock-engine`, which build fake process trees; guard: `pfm/scripts/arch-check.sh` C29 `os-door`, C32 `os-door-test`, C30 `shell-platform`.
- Kernels differ in behaviour, not only spelling (a terminal hangup, a signal to a group whose members exited, the controlling terminal vs stdin's, a process still exiting, a path's maximum length): each door has a test that runs on both kernels, and a door's fake or fixture answers as each kernel does, never as Linux alone.
- A test waits on a named condition, never on the Linux fence's speed (root, fast, container).
- A change to a door, a door's fake or the journey lands only after a green macOS run, `bash infra/fence/journey/run.sh --ci-mac`; it pushes, so it runs only on the owner's ask (`CLAUDE.md` § Fence).
