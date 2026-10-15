# 2026-09-21 — a wait rule in a lead prompt does not hold; a blocking command does

Observed: across three tracer/mapper designs the lead met "end your message to wait" with `true`/`sleep` calls (33 of 86 tool calls in one run, each re-sending its whole context); replacing the rule with one blocking script call (`ledger wait DIR n…`, Bash timeout 600000) ended it. Same shape for "stay inside your bucket": readers roamed until their search tools were removed.

Amend: judgment — a lead that fans out gets a blocking wait command, and a reader that must not search holds no search tool; neither is asked for in prose.
