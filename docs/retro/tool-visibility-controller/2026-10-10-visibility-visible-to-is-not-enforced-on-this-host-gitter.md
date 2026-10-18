# 2026-10-10 — `visibility.visible-to` is not enforced on this host; GITTER-ONLY held only through the description

Observed: tool-visibility-controller is missing from `enabledPlugins` in both `~/.claude/settings.json` and `~/.cc/2/settings.json`. The main chat's agent list shows conflict-resolver (`visible-to: [gitter]`) and every TRACER-ONLY, RR-ONLY and INVESTIGATOR-ONLY helper. A haiku test relay spawned conflict-resolver directly 9 times once its prompt carried the user's authorization. The only refusal came from the model reading "GITTER-ONLY" in the description.

Amend: judgment — either enable tool-visibility-controller on the host (a fleet-wide change and its own task), or have `pfm doctor` warn when an installed agent declares `visibility` while the plugin is off.

Resolved: 2026-10-18 — judgment: `tool-visibility-controller@tool-visibility-controller-dev` is enabled in both account settings, and conflict-resolver and sub-tracer are absent from a sub-agent's agent list
