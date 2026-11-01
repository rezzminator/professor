# RR — How tmux-/process-heavy projects test OS-coupled behaviour safely (incl. macOS) and kill processes safely

Question: find how projects similar to ours test OS-coupled behaviour safely and on macOS, and how they kill processes safely — to inform a redesign of our own. (pfm: Go CLI running AI agent chats in tmux panes/Herdr; spawns tmux servers, processes, Unix sockets, launchd/systemd units, scans the process table and kills; tests run real tmux in a Linux Docker "fence"; on a dev Mac they damaged the live setup; ~40 fail on GitHub macOS runners.) Questions: 1 mock vs real and the seam; 2 host isolation; 3 macOS testing incl. 104-byte sun_path, no /proc, launchd; 4 safe kill (pid reuse, start time, pidfd, pgid, argv, env markers); 5 patterns/anti-patterns ranked.

## Sub-areas

1. tmux's own regress suite — settled when the regress harness files show how tmux is invoked (socket flag, config, env) and whether it runs on macOS CI, with path:line.
   Lens: software testing (clone-distiller) — method: read the harness source; sources: tmux/tmux regress/*.sh, Makefile, CI workflows.
2. AI agent tmux fleet managers (claude-squad and kin) — settled when at least one such project's tmux/process seam (interface vs real), test isolation and kill path are shown with path:line.
   Lens: application engineering (clone-distiller) — method: read the code seam and tests; sources: smtg-ai/claude-squad source and tests.
3. Process-table libraries (gopsutil, go-ps) — settled when the macOS and Linux process-read implementations, their test approach (real vs fixture) and any pid-reuse guard (create-time) are shown with path:line.
   Lens: systems programming (clone-distiller) — method: read per-OS implementation and tests; sources: shirou/gopsutil source.
4. Process supervisors that run things in tmux / from pid files (overmind, goreman, supervisord, s6, brew services) — settled when overmind's spawn/kill path and tests are shown with path:line and at least one pid-file supervisor's stale-pid guard is cited.
   Lens: operations / SRE (clone-distiller for overmind; web for others) — method: read kill paths and pid-file handling; sources: DarthSim/overmind source, supervisord/s6 docs and source.
5. Safe-kill primitives across Linux and macOS — settled when pidfd (pidfd_open/pidfd_send_signal, Go os.Process pidfd use), start-time comparison (/proc/pid/stat field 22; macOS proc_pidinfo/sysctl kp_proc.p_starttime), and process-group kill semantics are each cited to man pages, kernel/Go source or docs.
   Lens: OS kernel / security (TOCTOU race analysis) — method: read primary API specs and race analyses; sources: man pages, Go source and issues, LWN.
6. macOS testing and host isolation for OS-integration suites — settled when GitHub macOS runner facts, a local macOS VM option (Tart/Lume/Anka), sandbox-exec status, sun_path 104-byte handling in real test suites, launchd testing approach, and systemd's own VM/nspawn-based integration testing are each cited.
   Lens: CI / release engineering — method: compare runner and VM documentation and real projects' CI configs; sources: GitHub runner-images docs, Tart docs, Apple man pages, Go issues, systemd test/ docs.
