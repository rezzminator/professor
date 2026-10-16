# 2026-10-16 — probe chats in a fresh scratch clone stop at Claude Code's folder-trust dialog

Observed: an /ultimate lab launched 16 probe chats with `mcp__chat-professor__new` into fresh clones under `/tmp/professor/`; all 16 halted at the folder-trust dialog (pfm presses nothing there), so a whole round produced zero runs and 14 minutes went to waiting. Pre-trusting a fixed pool of reusable slot directories in every account's Claude config fixed it; slot reuse also kept the trust list from growing per run.

Amend: templates/global/skills/iterative-improvement/SKILL.md#Round — step 4's isolated runner runs in a pre-trusted, reused slot directory (or the skill names how pfm trusts one), and the first probe is confirmed past startup before the rest launch.
