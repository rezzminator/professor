"""chatlog kit: the mechanics of reading transcripts, so a reader only reads and a count is code.

One event model (model.py) that every engine module (engines/claude.py, codex.py, opencode.py)
maps its raw records into; digest.py renders it as the `pfm transcript show` digest; labels.py
computes every mechanical label from it; cost/ prices each model call; locate.py resolves a
target and a session's sub-agents; rows.py, writes.py and cli.py are the verbs.
"""
