# 2026-10-15 — a go-ahead sent before a session reload ran the old agent definitions

Observed: a go-ahead was sent to a chat whose agent files had just changed; Claude loads agent definitions at session start, so the chat ran the OLD definitions.

Amend: pfm/harness-prompts/ — before sending work that depends on a changed agent file, reload the chat (`pfm chat reload --sock`), verify the loaded agent file, then send.
