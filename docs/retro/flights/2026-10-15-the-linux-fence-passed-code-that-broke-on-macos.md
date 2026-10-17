# 2026-10-15 — flight plugin-logs: the Linux fence passed code that broke on macOS

Observed: BSD `chmod` rejects `--`; the Linux fence passed the code, and the break surfaced only after the live install.

Amend: templates/global/agents/flights-lander.md (or the testing manual of a unit whose code runs on the host) — host-OS portability is proven before the install by a BSD-behaving shim test or a host smoke run.
