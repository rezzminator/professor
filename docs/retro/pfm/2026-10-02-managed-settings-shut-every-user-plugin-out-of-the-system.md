# 2026-10-02 — managed settings shut every user plugin out of the system prompt

Observed: a cache-live-control probe's `prompt.section` hook never ran: the debug log showed 94 of 94 calls `bypassed by cc-plugin-sec-default (tier user)`, and `prompt.context` the same. The trigger is `cc-plugin-sec-default@builtin seated outermost: this machine has managed settings`; the only managed file is pfm's `managed-settings.d/pfm.json` (`cleanupPeriodDays` alone). Every Professor plugin that shapes prompts is limited to `prompt.submit` context on a pfm host. Whether a host without managed settings admits user-tier section hooks is unproven.

Amend: judgment — decide whether pfm keeps a managed-settings file for `cleanupPeriodDays`; until then, a prompt-shaping hook is checked against `--debug-file` for "bypassed by cc-plugin-sec-default" before it is built on.
