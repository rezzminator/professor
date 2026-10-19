# 2026-10-19 — /proc/PID/io on init and shim processes counts reaped children

Observed: while hunting the fence VM's daily disk writes, the container init and containerd shim showed the largest write_bytes; those counters include every reaped child, so they named the parent, not the writer. The real writer was per-call scratch on the VM disk (fixed in d4db64e3, scratch moved to a capped tmpfs).

Amend: judgment — attribute disk writes from the VM's block-device and journal counters, or from short-lived children while they run, never from a long-lived parent's /proc io.
