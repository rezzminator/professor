# 2026-10-11 — a flight brief ordered "commit the live pfm.config.json", which is gitignored

Observed: the plugin-list flight could not commit its R8 config edit; pfm.config.json is ignored (`.gitignore:2`), and the old pfm binary rejected the new key, so the edit had to wait until after `make host-install`.

Amend: CLAUDE.md#Vocabulary — name pfm.config.json as the host's untracked live config: a flight edits it in place after the host install and never commits it.
