# aliases.zsh — the live-demo shell inside the fence: one short word per runbook
# command. setup.sh sources this file from the container's ~/.zshrc; it is
# read from the read-only /worktree mount, so an edit on the host is live in
# the next shell. Nothing here is needed by the scripts themselves.
alias storm-up='bash /worktree/infra/demo/storm.sh start'    # storm-up [N=6] [SENDS=30]
alias storm-down='bash /worktree/infra/demo/kill-storm.sh'   # ends + hides the STORM rows only
alias idle-down='bash /worktree/infra/demo/idle.sh down'     # slide chats off the sky (ended + hidden)
alias idle-up='bash /worktree/infra/demo/idle.sh up'         # and back, same transcripts
alias demo-verify='bash /worktree/infra/demo/verify.sh'      # demo-verify [CHECK...] — the deck exercised end to end, ✓/✗ per beat
# headless — slide 11. Prints the two commands to copy and run by hand, in
# order: the vendor's own `claude -p` with every sealing flag spelled out, then
# the pfm line that means the same thing. Runs nothing itself.
headless() {
  cat <<'CMDS'
# 1 — the vendor way: Claude Code headless, sealed by hand (run in ~/headless)
cd ~/headless && claude -p --safe-mode --output-format json \
  --system-prompt-file extractor.md \
  --json-schema "$(cat invoice.schema.json)" \
  --tools "" --setting-sources "" --strict-mcp-config --no-session-persistence \
  "Extract vendor, total, currency, due date. $(cat invoice.txt)" | jq .

# 2 — the pfm way: one flag per intent, any engine, same seat
cd ~/headless && pfm headless exec --engine claude --sealed \
  --system-file extractor.md --files invoice.txt \
  --task "Extract vendor, total, currency, due date." \
  --schema invoice.schema.json --output-format json | jq .
CMDS
}
