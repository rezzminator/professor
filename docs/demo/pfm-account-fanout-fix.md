# pfm account registries

Global Claude agents, commands and skills are written once into the shared store `~/.claude`. The per-account loop was replaced by one write into this store; every configured account links its shared registry entries there. Adding an account creates its links rather than another copy of each registry.

`wireGlobalCommands`, `wireGlobalSkills` and `wireGlobalSkill` use the installer's store config directory. `codexgen.GlobalAgentsOptions.ClaudeConfigDirs` receives only the store directory and selects the store's agent registry. Codex roles are separate regular files, because Codex opens them without following symlinks.

`pfm install` runs read-only host checks before building missing store entries and account links. Real shared entries in an account directory are BLOCK rows; `pfm doctor` prints the merge or removal fix. Existing store contents survive installation. Doctor checks the store registry once and checks every account link against it.

Source-fetched skills are stored under `~/.local/share/pfm/install/skills/{name}` and linked into `~/.claude/skills` and `~/.agents/skills`. A failed fetch keeps the last fetched copy. Dead pfm-owned symlinks in Claude command, agent and skill registries are reported and pruned; live foreign links and regular files survive.

`pfm/internal/installer/global_fanout_test.go` covers store-only writes and registry health. `pfm/internal/installer/claude_store_test.go` covers the shared entries and account links. `pfm/internal/installer/registry_orphans_test.go` covers dead-link inspection, installation and uninstall.
