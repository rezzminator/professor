# 2026-10-18 — an ALL-CAPS brace token in an agent prompt fails the templates gate

Observed: transcript-reader.md and the investigator used `{DIR}` as a format slot; `iso gate templates` failed its placeholders step (`1 of 107 markdown-template tokens are absent from PLACEHOLDERS.md`, 7x `{DIR}`) and held a host install for one gate round.

Amend: templates/global/commands/quality/prompt.md — a runtime slot in a prompt is lowercase (`{dir}`), or registered under docs/PLACEHOLDERS.md § Runtime metavariables before the gate.
