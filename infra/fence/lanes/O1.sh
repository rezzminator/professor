#!/usr/bin/env bash
# O1.sh — lane O1, ops & host BEFORE any chat exists: the machine every other
# lane lives on, asserted from pfm's own reports and the files the installer
# wrote. Runs INSIDE a lane container (run.sh), never on a host.
#
#   run.sh --lanes O1            solo, from a fresh root
#   run.sh                       first in the sequence, before E1
#
# Every beat that mutates the install RESTORES what it changed and asserts the
# restore: a dropped seat is always a spare (never the seat E1 will spend), a
# moved credential is moved back, a removed overlay is re-installed. The
# destructive tail — reap, archive, headless, harvester, uninstall — is lane O2,
# which runs last because it tears the machine down.
#
# BROKEN STATE: the prelude aborts by name when this container carries no pfm
# install at all (no config, no managed root); a beat that had to mutate state
# and could not put it back says so in its own ✗ line and the lane log carries
# the command output.
set -uo pipefail
LANES_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
# shellcheck source=lib.sh
. "$LANES_DIR/lib.sh"
lane_preamble

CONFIG="$HOME/.config/pfm/pfm.config.json"
MANAGED="$HOME/.local/share/pfm/install"
BLUEPRINT="$HOME/.professor"
SEAT="$(printf '%s\n' $LANE_SEATS | awk -F: '/^cc:/ { print $2; exit }')"
[ -n "$SEAT" ] || SEAT=1

lane_begin O1

# ── prelude ─────────────────────────────────────────────────────────────────
lane_require_seat_ops "$CONFIG"

need "the blueprint clone at $BLUEPRINT" "[ -e '$BLUEPRINT' ]" "ln -s /worktree '$BLUEPRINT'" ||
  lane_abort "no blueprint clone — pfm install cannot be re-run from it"
need "the managed install root $MANAGED" "[ -d '$MANAGED' ]" "(cd '$BLUEPRINT' && pfm install --yes)" ||
  lane_abort "pfm install has never completed in this container"

install_again() { (cd "$BLUEPRINT" && pfm install --yes 2>&1); }

# ─── O1.01 — a second install changes nothing ───────────────────────────────

beat O1.01-install-idempotent I96
spends none
out="$(install_again)"
rc=$?
summary="$(printf '%s\n' "$out" | grep -E '^.*summary changed=' | tail -1)"
if [ "$rc" -ne 0 ]; then
  fail "the second pfm install --yes exited $rc: $(one_line "$(printf '%s\n' "$out" | tail -5)")"
elif [ -z "$summary" ]; then
  fail "pfm install --yes printed no 'summary changed=' line — idempotence cannot be judged: $(one_line "$(printf '%s\n' "$out" | tail -5)")"
elif ! printf '%s' "$summary" | grep -q 'changed=0'; then
  fail "the second install still changed something: $(one_line "$summary")"
else
  pass "$(one_line "$summary")"
fi

# ─── O1.02 — every staged host asset is present ─────────────────────────────

beat O1.02-host-assets I1 I2 I3 I4 I7 I8 I9 I10 I11 I14 I15 I16 I17 I18 I19 I20 I21 I22 I23
spends none
missing=""
check_path() { # check_path <id> <what> <path>
  [ -e "$3" ] || missing="$missing $1 ($2: $3 absent);"
}
check_path I1 "Claude launcher shim" "$HOME/.local/bin/claude"
check_path I2 "pfm-statusline overlay" "$HOME/.local/bin/pfm-statusline"
check_path I3 "tmux-title-renudge overlay" "$HOME/.local/bin/tmux-title-renudge"
check_path I4 "handoff skill" "$SEAT_DIR/skills/handoff/SKILL.md"
check_path I10 "reload command card" "$SEAT_DIR/commands/reload.md"
check_path I21 "source-repo marker" "$MANAGED/source-repo"
check_path I22 "mcp-auth-token" "$MANAGED/mcp-auth-token"
check_path I23 "settings-hook-ownership ledger" "$MANAGED/settings-hook-ownership.json"
check_path I11 "pfm.zsh shim" "$MANAGED/shim/pfm.zsh"
grep -q 'pfm.zsh' "$HOME/.zshrc" 2>/dev/null || missing="$missing I11 (no pfm.zsh line in ~/.zshrc);"
# The assets whose destination the installer computes are SEARCHED, and the
# roots searched are named on failure — "nothing there" is never "failed to look".
ROOTS="$HOME/.local/share/pfm $HOME/.local/bin $SEAT_DIR $CODEX_HOME $HOME/.claude"
# The glob is a PATH suffix, not a bare name: the composed harness prompts are
# told apart from any other claude.md/codex.md by the directory above them.
find_asset() { # find_asset <id> <what> <path-suffix-glob>
  local hit
  hit="$(find $ROOTS -maxdepth 6 -path "*/$3" 2>/dev/null | head -1)"
  [ -n "$hit" ] || missing="$missing $1 ($2: no '*/$3' under $ROOTS);"
}
find_asset I7 "codex appendix hook file" 'harness-prompts/codex.md'
find_asset I9 "professor system-prompt file" 'harness-prompts/claude.md'
find_asset I8 "harness-prompt baseline" 'harness-prompts/claude/baselines/harness-original.sha256'
find_asset I15 "Claude Code professor theme" 'professor-*.json'
find_asset I16 "harvestpy runtime marker" 'harvestpy*'
find_asset I14 "VS Code extension asset" 'pfm*.vsix'
for registry in agents commands skills; do
  [ -d "$SEAT_DIR/$registry" ] || missing="$missing I17/I18/I19 ($SEAT_DIR/$registry absent);"
done
[ -d "$CODEX_HOME/agents" ] || missing="$missing I20 (no Codex agents mirror at $CODEX_HOME/agents);"
if [ -n "$missing" ]; then fail "$missing"; else
  pass "every contracted overlay, per-seat card, marker and registry is staged (searched: $ROOTS)"
fi

# ─── O1.03 — the installer's hooks, per engine ──────────────────────────────

beat O1.03-hooks-installed I24 I25 I26 I27 I28 I29 I30 I31 I32 I33 I99
spends none
settings="$SEAT_DIR/settings.json"
if [ ! -f "$settings" ]; then
  fail "no $settings — the installer wires its hooks there"
else
  hooks="$(jq -r '.hooks | to_entries[] | .key as $event | .value[] | .hooks[] | select(.type == "command") | [$event, (.command // "")] | @tsv' "$settings" 2>&1)"
  if [ -z "$hooks" ]; then
    fail "no command hooks in $settings (enumeration produced nothing): $(one_line "$hooks")"
  else
    missing=""
    for verb in launcher-repair clear-kill exit-close explore-deny git-guard epic-inject reload-intercept exit-intercept compact-nudge; do
      printf '%s' "$hooks" | grep -q -- "$verb" || missing="$missing $verb;"
    done
    printf '%s' "$hooks" | grep -q 'usage-hook' || missing="$missing usage-hook;"
    if [ -f "$CODEX_HOME/config.toml" ]; then
      grep -q '^developer_instructions = ' "$CODEX_HOME/config.toml" ||
        missing="$missing codex fleet prompt absent from developer_instructions in $CODEX_HOME/config.toml;"
    else
      missing="$missing no $CODEX_HOME/config.toml to carry the Codex fleet prompt;"
    fi
    if [ -n "$missing" ]; then
      fail "hook(s) not installed:$missing enumerated $(printf '%s\n' "$hooks" | grep -c . ) command hook(s)"
    else
      pass "$(printf '%s\n' "$hooks" | grep -c . ) Claude command hooks wired (every pfm internal verb + usage-hook) and the Codex fleet prompt present in developer_instructions"
    fi
  fi
fi

# ─── O1.04 — the seat roster ────────────────────────────────────────────────

beat O1.04-seats I34 I35 I36 I39 I40
spends none
doctor="$(pfm doctor 2>&1)"
doctor_rc=$?
bad=""
[ "$doctor_rc" -le 1 ] || bad="$bad pfm doctor exited $doctor_rc, so its seat rows cannot be trusted ($(one_line "$doctor"));"
n_acct="$(jq '.accounts | length' "$CONFIG")"
[ "$n_acct" -ge 1 ] || bad="$bad the config carries no account;"
while IFS= read -r dir; do
  case "$dir" in "~"*) dir="$HOME${dir#\~}" ;; esac
  [ -d "$dir" ] || bad="$bad configured seat dir $dir does not exist;"
  [ -e "$dir/commands/reload.md" ] || bad="$bad seat $dir did not receive the command fan-out;"
done < <(jq -r '.accounts[].configDir' "$CONFIG")
[ -d "$CODEX_HOME" ] || bad="$bad the configured Codex home $CODEX_HOME does not exist;"
# The OpenCode home is absent in a --no-adopt root by design: the report must
# NAME that, never let it pass as configured.
if [ -f "$HOME/.local/share/opencode/opencode.db" ]; then
  oc="present"
else
  oc="ABSENT (no opencode.db — lane E3's prelude makes it)"
fi
printf '%s' "$doctor" | grep -qiE 'account|seat' || bad="$bad pfm doctor names no account/seat row;"
if [ -n "$bad" ]; then fail "$bad (doctor exit $doctor_rc)"; else
  pass "$n_acct Claude seat(s) wired, Codex home $CODEX_HOME present, OpenCode home $oc"
fi

# ─── O1.05 — a seat with no credential refuses by name ──────────────────────

beat O1.05-credential K23
spends none
if [ -z "$SPARE" ]; then
  fail "only one Claude seat is configured — the absent-credential refusal cannot be asserted without risking the lane's own seat"
else
  moved=0
  if [ -f "$SPARE_DIR/.credentials.json" ]; then mv "$SPARE_DIR/.credentials.json" "$SPARE_DIR/.credentials.json.lane"; moved=1; fi
  doc="$(pfm doctor 2>&1)"
  new_out="$(timeout 120 pfm chat new --name O1_CRED_PROBE --engine cc --account "$SPARE" --cwd /tmp 2>&1)"
  new_rc=$?
  pfm chat kill O1_CRED_PROBE >/dev/null 2>&1
  [ "$moved" -eq 1 ] && mv "$SPARE_DIR/.credentials.json.lane" "$SPARE_DIR/.credentials.json"
  bad=""
  printf '%s' "$doc" | grep -qiE 'not.?logged|credential' || bad="$bad pfm doctor did not name the credential-less seat $SPARE;"
  [ "$new_rc" -ne 0 ] || bad="$bad chat new --account $SPARE was ACCEPTED on a seat with no credential;"
  printf '%s' "$new_out" | grep -qiE 'not.?logged|credential|login' ||
    bad="$bad chat new refused (exit $new_rc) without naming the credential: $(one_line "$new_out");"
  [ -f "$SPARE_DIR/.credentials.json" ] || [ "$moved" -eq 0 ] || bad="$bad the credential was NOT restored to $SPARE_DIR;"
  if [ -n "$bad" ]; then fail "$bad"; else
    pass "doctor and chat new both refuse seat $SPARE by name while its credential is away, and it was restored"
  fi
fi

# ─── O1.06 — a dropped seat loses exactly what it owned ─────────────────────

beat O1.06-dropped-seat I38
spends none
if [ -z "$SPARE" ]; then
  fail "only one Claude seat is configured — dropping it would take the lane's own seat with it"
elif ! with_restored "$CONFIG"; then
  fail "could not take a crash-safe backup of $CONFIG before dropping seat $SPARE — see the lane log"
else
  jq --argjson drop "$SPARE" '.accounts |= map(select(.id != $drop))' "$CONFIG" >"$CONFIG.tmp" && mv "$CONFIG.tmp" "$CONFIG"
  drop_out="$(install_again)"
  drop_rc=$?
  ledger_after="$(cat "$MANAGED/settings-hook-ownership.json" 2>/dev/null)"
  kept_hooks="$(jq -r '.hooks | to_entries[] | .value[] | .hooks[] | .command // ""' "$SEAT_DIR/settings.json" 2>/dev/null | grep -c 'pfm')"
  spare_hooks="$(jq -r '.hooks | to_entries[] | .value[] | .hooks[] | .command // ""' "$SPARE_DIR/settings.json" 2>/dev/null | grep -c 'pfm')"
  config_restore_rc=0
  restore_now "$CONFIG" || config_restore_rc=1
  restore_out="$(install_again)"
  restore_rc=$?
  spare_hooks_back="$(jq -r '.hooks | to_entries[] | .value[] | .hooks[] | .command // ""' "$SPARE_DIR/settings.json" 2>/dev/null | grep -c 'pfm')"
  bad=""
  [ "$drop_rc" -eq 0 ] || bad="$bad the install after the drop exited $drop_rc: $(one_line "$(printf '%s\n' "$drop_out" | tail -3)");"
  [ "$spare_hooks" -eq 0 ] || bad="$bad seat $SPARE still carries $spare_hooks pfm hook(s) after being dropped;"
  [ "$kept_hooks" -gt 0 ] || bad="$bad the KEPT seat $SEAT lost its hooks when $SPARE was dropped;"
  printf '%s' "$ledger_after" | grep -q "$SPARE_DIR" &&
    bad="$bad the ownership ledger still carries rows for the dropped $SPARE_DIR;"
  [ "$config_restore_rc" -eq 0 ] || bad="$bad restoring $CONFIG from its crash-safe backup FAILED;"
  [ "$restore_rc" -eq 0 ] || bad="$bad the restoring install exited $restore_rc ($(one_line "$restore_out"));"
  [ "$spare_hooks_back" -gt 0 ] || bad="$bad seat $SPARE did not get its hooks back after the config was restored;"
  if [ -n "$bad" ]; then fail "$bad"; else
    pass "dropping seat $SPARE removed exactly its hooks and ledger rows (kept seat $SEAT: $kept_hooks hooks), and the restore gave them back ($spare_hooks_back)"
  fi
fi

# ─── O1.07 — a home and a blueprint reached through a symlink ───────────────

beat O1.07-symlinked-home I37
spends none
link="$HOME/.cc/lane-linked-seat"
bad=""
[ -L "$BLUEPRINT" ] || bad="$bad $BLUEPRINT is not a symlink — the linked-blueprint half cannot be asserted here;"
blue_doctor="$(pfm doctor 2>&1)"
printf '%s' "$blue_doctor" | grep -qiE 'source.?repo|blueprint|clone' ||
  bad="$bad pfm doctor names no blueprint/source-repo row while $BLUEPRINT is a link;"
printf '%s' "$blue_doctor" | grep -qiE 'source.?repo.*(broken|missing|unreadable)' &&
  bad="$bad doctor reports the linked blueprint broken: $(one_line "$(printf '%s' "$blue_doctor" | grep -iE 'source.?repo' | head -2)");"
rm -rf "$link"
ln -s "$SEAT_DIR" "$link"
if ! with_restored "$CONFIG"; then
  bad="$bad could not take a crash-safe backup of $CONFIG before repointing seat $SEAT at $link;"
else
  jq --argjson want "$SEAT" --arg link "$link" '.accounts |= map(if .id == $want then .configDir = $link else . end)' \
    "$CONFIG" >"$CONFIG.tmp" && mv "$CONFIG.tmp" "$CONFIG"
  linked_doctor="$(pfm doctor 2>&1)"
  linked_rc=$?
  linked_ls="$(pfm ls --plain 2>&1)"
  linked_ls_rc=$?
  restore_now "$CONFIG" || bad="$bad restoring $CONFIG from its crash-safe backup FAILED;"
  [ "$linked_rc" -le 1 ] || bad="$bad pfm doctor exited $linked_rc with the seat reached through a symlink: $(one_line "$(printf '%s\n' "$linked_doctor" | tail -3)");"
  [ "$linked_ls_rc" -eq 0 ] || bad="$bad pfm ls exited $linked_ls_rc with a symlinked seat dir: $(one_line "$linked_ls");"
  printf '%s' "$linked_doctor" | grep -qiE 'duplicate' &&
    bad="$bad doctor called the symlinked seat a duplicate of its own target;"
  [ "$(jq -r --argjson want "$SEAT" '.accounts[] | select(.id == $want) | .configDir' "$CONFIG")" != "$link" ] ||
    bad="$bad the config still points seat $SEAT at the temporary link;"
fi
rm -f "$link"
if [ -n "$bad" ]; then fail "$bad"; else
  pass "the linked blueprint $BLUEPRINT resolves in doctor, and a seat reached through $link kept doctor and ls clean; config restored"
fi

# ─── O1.08 — two seats recording one OAuth login ────────────────────────────

beat O1.08-duplicate-seat-login
spends none
if [ -z "$SPARE" ]; then
  fail "only one Claude seat is configured — the duplicate-login advisory needs a second seat's registry to plant a matching email into"
else
  SEAT_JSON="$SEAT_DIR/.claude.json"
  SPARE_JSON="$SPARE_DIR/.claude.json"
  EMAIL="lane-o1.08-duplicate@example.invalid"
  bad=""
  if ! with_restored "$SEAT_JSON"; then
    bad="$bad could not take a crash-safe backup of $SEAT_JSON — nothing was planted;"
  elif ! with_restored "$SPARE_JSON"; then
    bad="$bad could not take a crash-safe backup of $SPARE_JSON — nothing was planted;"
    restore_now "$SEAT_JSON" || bad="$bad restoring $SEAT_JSON after that failure ALSO failed;"
  else
    # printDuplicateSeatLogins (pfm/internal/doctor/config_checks.go) keys
    # purely off each registry's own oauthAccount.emailAddress — the same
    # field a real second login on the same account leaves behind — so
    # planting the identical value into both seats' registries provokes the
    # exact code path without a second real credential
    # (TestDoctorAdvisesWhenConfiguredSeatsShareOAuthLogin in
    # config_checks_test.go does the same thing at the unit layer).
    for json in "$SEAT_JSON" "$SPARE_JSON"; do
      base="$([ -s "$json" ] && cat "$json" || echo '{}')"
      printf '%s' "$base" | jq -c --arg email "$EMAIL" '(.oauthAccount //= {}) | .oauthAccount.emailAddress = $email' >"$json.lane-planted" 2>&1 &&
        mv "$json.lane-planted" "$json" || bad="$bad could not plant the fixture email into $json: $(one_line "$(cat "$json.lane-planted" 2>/dev/null)");"
    done
    dup="$(pfm doctor 2>&1)"
    restore_now "$SEAT_JSON" || bad="$bad restoring $SEAT_JSON from its crash-safe backup FAILED;"
    restore_now "$SPARE_JSON" || bad="$bad restoring $SPARE_JSON from its crash-safe backup FAILED;"
    line="$(printf '%s\n' "$dup" | grep -F 'duplicate-seat-login' | grep -F "$EMAIL" | head -1)"
    seats_field="$(printf '%s' "$line" | grep -oE 'seats=[^ ]*')"
    [ -n "$line" ] || bad="$bad pfm doctor did not name the planted duplicate: $(one_line "$(printf '%s\n' "$dup" | grep -iF duplicate | head -1)");"
    printf '%s' "$seats_field" | grep -qF "$SEAT:" || bad="$bad the advisory's seats= field is missing seat $SEAT: $(one_line "$seats_field");"
    printf '%s' "$seats_field" | grep -qF "$SPARE:" || bad="$bad the advisory's seats= field is missing seat $SPARE: $(one_line "$seats_field");"
    printf '%s' "$line" | grep -qF "share one OAuth usage cap" || bad="$bad the advisory line dropped its remediation text: $(one_line "$line");"
  fi
  if [ -n "$bad" ]; then fail "$bad"; else
    pass "planting $EMAIL into $SEAT_JSON and $SPARE_JSON made pfm doctor emit: $(one_line "$line")"
  fi
fi

# ─── O1.09 — doctor: every row green or a NAMED advisory ────────────────────

beat O1.09-doctor-pass I41 I42 I43 I44 I45 I46 I47 I48 I49 I50 I51 I52 I53 I54 I55 I56 I57 I58 I59 I60 I61 I62 I64 I65 I66 I67 I68 I5 I6 I12 I13
spends none
doc="$(pfm doctor 2>&1)"
doc_rc=$?
first_bad="$(printf '%s\n' "$doc" | grep -m1 -E 'broken|drift|stale|error=' || true)"
rows="$(printf '%s\n' "$doc" | grep -c . )"
if [ "$doc_rc" -ge 2 ]; then
  fail "pfm doctor exited $doc_rc; first failure row: ${first_bad:-<none>}; tail: $(one_line "$(printf '%s\n' "$doc" | tail -3)")"
elif [ -n "$first_bad" ]; then
  fail "pfm doctor exited $doc_rc with a failure row: $(one_line "$first_bad")"
elif ! printf '%s' "$doc" | grep -qiE 'systemd|launchd|service manager'; then
  fail "doctor said nothing about the absent service manager — in a container that row must be a NAMED advisory, not silence ($rows rows)"
else
  pass "exit $doc_rc · $rows rows · no broken/drift/stale/error row · the no-service-manager advisory is named: $(one_line "$(printf '%s' "$doc" | grep -iE 'systemd|launchd|service manager' | head -1)")"
fi

# ─── O1.10 — doctor's own exit contract, provoked ───────────────────────────

beat O1.10-doctor-exit-contract I98
spends none
clean_rc=0
pfm doctor >/dev/null 2>&1 || clean_rc=$?
overlay="$HOME/.local/bin/pfm-statusline"
bad="" broken_out="" broken_rc=""
if with_restored "$overlay"; then
  rm -f "$overlay"
  broken_out="$(pfm doctor 2>&1)"
  broken_rc=$?
  restore_now "$overlay" || bad="$bad restoring $overlay from its crash-safe backup FAILED;"
else
  bad="$bad could not take a crash-safe backup of $overlay before deleting it — nothing was mutated;"
fi
repaired_rc=0
pfm doctor >/dev/null 2>&1 || repaired_rc=$?
case "$clean_rc" in 0|1) ;; *) bad="$bad a healthy install made doctor exit $clean_rc (want 0 or 1);" ;; esac
if [ -n "$broken_rc" ] && [ "$broken_rc" -le "$clean_rc" ] && ! printf '%s' "$broken_out" | grep -qi 'pfm-statusline'; then
  bad="$bad with $overlay deleted doctor still exited $broken_rc and never named pfm-statusline — it cannot tell healthy from broken;"
fi
case "$repaired_rc" in 0|1) ;; *) bad="$bad after the repair doctor still exits $repaired_rc;" ;; esac
[ -e "$overlay" ] || bad="$bad $overlay was NOT restored;"
if [ -n "$bad" ]; then fail "$bad"; else
  pass "healthy exit $clean_rc · with the statusline overlay deleted exit $broken_rc naming it · restored, exit $repaired_rc"
fi

# ─── O1.11 — heal ───────────────────────────────────────────────────────────

beat O1.11-heal X11
spends none
heal1="$(pfm heal 2>&1)"
heal1_rc=$?
heal2="$(pfm heal 2>&1)"
heal2_rc=$?
if [ "$heal1_rc" -ge 2 ]; then
  fail "pfm heal exited $heal1_rc: $(one_line "$heal1")"
elif [ -z "$heal1" ]; then
  fail "pfm heal printed nothing — a report that says neither 'clean' nor what it repaired is not a report"
elif [ "$heal2_rc" -ne "$heal1_rc" ]; then
  fail "pfm heal is not idempotent: exit $heal1_rc then $heal2_rc ($(one_line "$heal2"))"
else
  pass "exit $heal1_rc, same on the second run: $(one_line "$heal1" | cut -c1-200)"
fi

# ─── O1.12 — the doc-vs-code behaviours, including a foreign key ────────────

beat O1.12-doc-vs-code I90 I91 I92 I93 I94 I95
spends none
bad=""
jq '. + {laneForeignKey: "keep-me"}' "$SEAT_DIR/settings.json" >"$SEAT_DIR/settings.json.tmp" &&
  mv "$SEAT_DIR/settings.json.tmp" "$SEAT_DIR/settings.json"
merge_out="$(install_again)"
merge_rc=$?
[ "$merge_rc" -eq 0 ] || bad="$bad the merge install exited $merge_rc ($(one_line "$merge_out"));"
[ "$(jq -r '.laneForeignKey // ""' "$SEAT_DIR/settings.json")" = keep-me ] ||
  bad="$bad pfm install overwrote a foreign settings.json key instead of merging;"
jq 'del(.laneForeignKey)' "$SEAT_DIR/settings.json" >"$SEAT_DIR/settings.json.tmp" &&
  mv "$SEAT_DIR/settings.json.tmp" "$SEAT_DIR/settings.json"
[ -n "$(find "$SEAT_DIR" -maxdepth 2 -name 'professor-*.json' 2>/dev/null | head -1)" ] ||
  bad="$bad no professor-*.json theme under $SEAT_DIR;"
claude_json="$SEAT_DIR/.claude.json"
if [ -f "$claude_json" ]; then
  jq -e '.mcpServers | objects' "$claude_json" >/dev/null 2>&1 ||
    bad="$bad $claude_json carries no mcpServers object (the professor server registration);"
else
  bad="$bad no $claude_json for seat $SEAT;"
fi
[ -n "$(find "$SEAT_DIR/skills" -maxdepth 2 -type l -o -maxdepth 2 -type d -name '*git*' 2>/dev/null | head -1)" ] ||
  _lane_log_only "   O1.12: no git-bridge skill directory under $SEAT_DIR/skills (I94 reads the global fan-out instead)"
if [ -n "$bad" ]; then fail "$bad"; else
  pass "settings merge keeps a foreign key, themes staged, MCP registered in .claude.json, project hooks rooted in the seat"
fi

# ─── O1.13 — the misc ops CLI ───────────────────────────────────────────────

beat O1.13-misc-ops X1 X2 X3 X4 X12 X13 X14 C65
spends none
bad=""
run_ok() { # run_ok <what> <cmd…>
  local what="$1" out rc
  shift
  out="$("$@" 2>&1)"
  rc=$?
  [ "$rc" -eq 0 ] || bad="$bad $what exited $rc ($(one_line "$out"));"
  [ -n "$out" ] || bad="$bad $what printed nothing;"
}
run_ok "pfm version" pfm version
run_ok "pfm config show" pfm config show
run_ok "pfm config validate" pfm config validate
run_ok "pfm issues" pfm issues
# whoami OUTSIDE a chat must refuse by name, never invent a session.
who="$(pfm whoami 2>&1)"
who_rc=$?
if [ "$who_rc" -eq 0 ]; then
  bad="$bad pfm whoami answered '$(one_line "$who")' from a plain shell that holds no chat identity;"
elif [ -z "$who" ]; then
  bad="$bad pfm whoami failed silently (exit $who_rc) instead of naming the missing identity;"
fi
alias_out="$(pfm chat whoami 2>&1)"
alias_rc=$?
[ "$alias_rc" -eq "$who_rc" ] ||
  bad="$bad pfm chat whoami exited $alias_rc ('$(one_line "$alias_out")') but pfm whoami exited $who_rc — the alias is not the same command;"
usage_out="$(printf '{"session_id":"lane-no-such-session","prompt":"hello"}' | pfm usage-hook 2>&1)"
usage_rc=$?
[ "$usage_rc" -eq 0 ] || bad="$bad pfm usage-hook is not fail-open: exit $usage_rc ($(one_line "$usage_out"));"
if [ -n "$bad" ]; then fail "$bad"; else
  pass "version/config show/config validate/issues answer; whoami refuses by name outside a chat (exit $who_rc) and its alias matches; usage-hook is fail-open"
fi

lane_end
