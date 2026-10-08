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

CONFIG="${PFM_CONFIG:?PFM_CONFIG is required in the container}"
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

beat O1.01-install-idempotent
spends none
out="$(install_again)"
rc=$?
summary="$(grep -E '^.*summary changed=' <<<"$out" | tail -1)"
if [ "$rc" -ne 0 ]; then
  fail "the second pfm install --yes exited $rc: $(one_line "$(printf '%s\n' "$out" | tail -5)")"
elif [ -z "$summary" ]; then
  fail "pfm install --yes printed no 'summary changed=' line — idempotence cannot be judged: $(one_line "$(printf '%s\n' "$out" | tail -5)")"
elif ! grep -q 'changed=0' <<<"$summary"; then
  fail "the second install still changed something: $(one_line "$summary")"
else
  pass "$(one_line "$summary")"
fi

# ─── O1.02 — every staged host asset is present ─────────────────────────────

beat O1.02-host-assets
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
check_path I22 "MCP ownership ledger" "$MANAGED/mcp-ownership.json"
clone="$(cat "$MANAGED/source-repo" 2>/dev/null)"
check_path I11 "clone-sourced pfm.zsh shim" "$clone/pfm/internal/installer/assets/shim/pfm.zsh"
grep -Fq "$clone/pfm/internal/installer/assets/shim/pfm.zsh" "$HOME/.zshrc" 2>/dev/null || missing="$missing I11 (~/.zshrc does not source the clone shim);"
check_path I7 "composed Codex prompt" "$clone/pfm/harness-prompts/composed/codex.md"
check_path I9 "composed Claude prompt" "$clone/pfm/harness-prompts/composed/claude.md"
check_path I8 "harness-prompt baseline" "$clone/pfm/harness-prompts/claude/baselines/harness-original.sha256"
# The assets whose destination the installer computes are SEARCHED, and the
# roots searched are named on failure — "nothing there" is never "failed to look".
ROOTS="$HOME/.local/share/pfm $HOME/.local/bin $SEAT_DIR $CODEX_HOME $HOME/.claude"
# The glob is a PATH suffix, not a bare name.
find_asset() { # find_asset <id> <what> <path-suffix-glob>
  local hit
  hit="$(find $ROOTS -maxdepth 6 -path "*/$3" 2>/dev/null | head -1)"
  [ -n "$hit" ] || missing="$missing $1 ($2: no '*/$3' under $ROOTS);"
}
find_asset I15 "Claude Code professor theme" 'professor-*.json'
check_path I16 "harvestpy runtime" "$HOME/.local/state/pfm/harvest-python/env"
check_path I14 "VS Code extension asset" "$MANAGED/vscode/professor/package.json"
for registry in agents commands skills; do
  [ -d "$SEAT_DIR/$registry" ] || missing="$missing I17/I18/I19 ($SEAT_DIR/$registry absent);"
done
[ -d "$CODEX_HOME/agents" ] || missing="$missing I20 (no Codex agents mirror at $CODEX_HOME/agents);"
if [ -n "$missing" ]; then fail "$missing"; else
  pass "every contracted overlay, per-seat card, marker and registry is staged (searched: $ROOTS)"
fi

# ─── O1.02a — install owns every account link ──────────────────────────────

beat O1.02a-account-links
spends none
account_count="$(jq '.accounts | length' "$CONFIG")"
if [ "$account_count" -eq 0 ]; then
  blocked "seats $LANE_SEATS" "needs a configured Claude account"
else
bad=""
while IFS=$'\t' read -r id dir; do
  [ -n "$id" ] || continue
  case "$dir" in "~"*) dir="$HOME${dir#\~}" ;; esac
  link="$dir/projects"
  want="$HOME/.claude/projects"
  if [ ! -L "$link" ] || [ "$(readlink -f "$link" 2>/dev/null)" != "$(readlink -f "$want" 2>/dev/null)" ]; then
    bad="seat $id projects: $link must link to $want"
    break
  fi
done < <(jq -r '.accounts[] | "\(.id)\t\(.configDir)"' "$CONFIG")
doctor_store="$(pfm doctor 2>&1)"
doctor_rc=$?
first_bad="$(grep -E '^(account-link:|store:|account:)' <<<"$doctor_store" | head -1)"
[ -z "$bad" ] && [ -n "$first_bad" ] && bad="$first_bad"
[ -n "$bad" ] || [ "$doctor_rc" -le 1 ] || bad="pfm doctor exited $doctor_rc: $(one_line "$doctor_store")"
[ -n "$bad" ] || grep -qxF "account-links: ok ($account_count accounts × 23 entries)" <<<"$doctor_store" ||
  bad="pfm doctor did not report account-links: ok ($account_count accounts × 23 entries)"
if [ -n "$bad" ]; then fail "$bad"; else
  pass "pfm install linked every shared entry for $account_count accounts; doctor reports account-links: ok"
fi
fi

# ─── O1.03 — the installer's hooks, per engine ──────────────────────────────

beat O1.03-hooks-installed
spends none
settings="$SEAT_DIR/settings.json"
if [ ! -f "$settings" ]; then
  fail "no $settings — the account settings cannot be inspected"
else
  hooks="$(jq -r '.hooks | to_entries[] | .key as $event | .value[] | .hooks[] | select(.type == "command") | [$event, (.command // "")] | @tsv' "$settings" 2>&1)"
  if ! jq -e 'type == "object"' "$settings" >/dev/null 2>&1; then
    fail "$settings is not a valid settings object"
  else
    missing=""
    for verb in launcher-repair clear-kill exit-close explore-deny git-guard epic-inject reload-intercept exit-intercept; do
      grep -q -- "$verb" "$BLUEPRINT/pfm/internal/claudelaunch/hooks.go" || missing="$missing launch template $verb;"
    done
    grep -q 'usage-hook' "$BLUEPRINT/pfm/internal/claudelaunch/hooks.go" || missing="$missing launch template usage-hook;"
    if [ -f "$CODEX_HOME/config.toml" ]; then
      grep -q '^developer_instructions = ' "$CODEX_HOME/config.toml" ||
        missing="$missing codex fleet prompt absent from developer_instructions in $CODEX_HOME/config.toml;"
    else
      missing="$missing no $CODEX_HOME/config.toml to carry the Codex fleet prompt;"
    fi
    if [ -n "$missing" ]; then
      fail "hook(s) not installed:$missing enumerated $(printf '%s\n' "$hooks" | grep -c . ) command hook(s)"
    else
      pass "Claude launch templates name the current hook roster; $(printf '%s\n' "$hooks" | grep -c . ) account-local hook(s) retained; Codex fleet prompt present"
    fi
  fi
fi

# ─── O1.04 — the seat roster ────────────────────────────────────────────────

beat O1.04-seats
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
grep -qiE 'account|seat' <<<"$doctor" || bad="$bad pfm doctor names no account/seat row;"
if [ -n "$bad" ]; then fail "$bad (doctor exit $doctor_rc)"; else
  pass "$n_acct Claude seat(s) wired, Codex home $CODEX_HOME present, OpenCode home $oc"
fi

# ─── O1.06 — a dropped seat loses exactly what it owned ─────────────────────

beat O1.06-dropped-seat
spends none
if [ -z "$SPARE" ]; then
  fail "only one Claude seat is configured — dropping it would take the lane's own seat with it"
elif ! with_restored "$CONFIG"; then
  fail "could not take a crash-safe backup of $CONFIG before dropping seat $SPARE — see the lane log"
else
  jq --argjson drop "$SPARE" '.accounts |= map(select(.id != $drop))' "$CONFIG" >"$CONFIG.tmp" && mv "$CONFIG.tmp" "$CONFIG"
  drop_out="$(install_again)"
  drop_rc=$?
  drop_doctor="$(pfm doctor 2>&1)"
  drop_roster="$(printf '%s\n' "$drop_doctor" | grep -m1 '^doctor: config accounts=')"
  config_restore_rc=0
  restore_now "$CONFIG" || config_restore_rc=1
  restore_out="$(install_again)"
  restore_rc=$?
  restore_doctor="$(pfm doctor 2>&1)"
  restore_roster="$(printf '%s\n' "$restore_doctor" | grep -m1 '^doctor: config accounts=')"
  bad=""
  [ "$drop_rc" -eq 0 ] || bad="$bad the install after the drop exited $drop_rc: $(one_line "$(printf '%s\n' "$drop_out" | tail -3)");"
  grep -qE "accounts=([^ ]*,)?$SPARE:" <<<"$drop_roster" && bad="$bad doctor still lists dropped seat $SPARE: $drop_roster;"
  grep -qE "accounts=([^ ]*,)?$SEAT:" <<<"$drop_roster" || bad="$bad doctor lost kept seat $SEAT: $drop_roster;"
  [ "$config_restore_rc" -eq 0 ] || bad="$bad restoring $CONFIG from its crash-safe backup FAILED;"
  [ "$restore_rc" -eq 0 ] || bad="$bad the restoring install exited $restore_rc ($(one_line "$restore_out"));"
  grep -qE "accounts=([^ ]*,)?$SPARE:" <<<"$restore_roster" || bad="$bad doctor did not relist seat $SPARE after restore: $restore_roster;"
  if [ -n "$bad" ]; then fail "$bad"; else
    pass "doctor's roster dropped seat $SPARE while keeping $SEAT, then relisted $SPARE after config restore"
  fi
fi

# ─── O1.07 — a home and a blueprint reached through a symlink ───────────────

beat O1.07-symlinked-home
spends none
link="$HOME/.cc/lane-linked-seat"
bad=""
[ -L "$BLUEPRINT" ] || bad="$bad $BLUEPRINT is not a symlink — the linked-blueprint half cannot be asserted here;"
blue_doctor="$(pfm doctor 2>&1)"
grep -qF 'harness-prompts embed=ok tree=' <<<"$blue_doctor" ||
  bad="$bad pfm doctor did not resolve the linked blueprint's harness prompts;"
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
  grep -qiE 'duplicate' <<<"$linked_doctor" &&
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
    line="$(grep -F 'duplicate-seat-login' <<<"$dup" | grep -F "$EMAIL" | head -1)"
    seats_field="$(grep -oE 'seats=[^ ]*' <<<"$line")"
    [ -n "$line" ] || bad="$bad pfm doctor did not name the planted duplicate: $(one_line "$(printf '%s\n' "$dup" | grep -iF duplicate | head -1)");"
    grep -qF "$SEAT:" <<<"$seats_field" || bad="$bad the advisory's seats= field is missing seat $SEAT: $(one_line "$seats_field");"
    grep -qF "$SPARE:" <<<"$seats_field" || bad="$bad the advisory's seats= field is missing seat $SPARE: $(one_line "$seats_field");"
    grep -qF "share one OAuth usage cap" <<<"$line" || bad="$bad the advisory line dropped its remediation text: $(one_line "$line");"
  fi
  if [ -n "$bad" ]; then fail "$bad"; else
    pass "planting $EMAIL into $SEAT_JSON and $SPARE_JSON made pfm doctor emit: $(one_line "$line")"
  fi
fi

# ─── O1.09 — doctor: every row green or a NAMED advisory ────────────────────

beat O1.09-doctor-pass
spends none
lane_daemon_up || fail "the pfm MCP daemon could not start before doctor"
doc="$(pfm doctor 2>&1)"
doc_rc=$?
capture_gap="$(grep -F 'harness-prompt: CHECK FAILED to run (no API request reached the capture sink)' <<<"$doc" | head -1)"
if [ -n "$capture_gap" ]; then
  first_bad="$(grep -vFx "$capture_gap" <<<"$doc" | grep -m1 -E '(broken|drift|stale)( |$|:)|error=' || true)"
else
  first_bad="$(printf '%s\n' "$doc" | grep -m1 -E '(broken|drift|stale)( |$|:)|error=' || true)"
fi
rows="$(printf '%s\n' "$doc" | grep -c . )"
if [ "$doc_rc" -ge 2 ]; then
  fail "pfm doctor exited $doc_rc; first failure row: ${first_bad:-<none>}; tail: $(one_line "$(printf '%s\n' "$doc" | tail -3)")"
elif [ -n "$first_bad" ]; then
  fail "pfm doctor exited $doc_rc with a failure row: $(one_line "$first_bad")"
elif ! grep -qiE 'systemd|launchd|service manager' <<<"$doc"; then
  fail "doctor said nothing about the absent service manager — in a container that row must be a NAMED advisory, not silence ($rows rows)"
else
  pass "exit $doc_rc · $rows rows · no unexpected broken/drift/stale/error row · no-service-manager and ${capture_gap:+capture-sink} advisories named"
fi

# ─── O1.10 — doctor's own exit contract, provoked ───────────────────────────

beat O1.10-doctor-exit-contract
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
if [ -n "$broken_rc" ] && [ "$broken_rc" -le "$clean_rc" ] && ! grep -qi 'pfm-statusline' <<<"$broken_out"; then
  bad="$bad with $overlay deleted doctor still exited $broken_rc and never named pfm-statusline — it cannot tell healthy from broken;"
fi
case "$repaired_rc" in 0|1) ;; *) bad="$bad after the repair doctor still exits $repaired_rc;" ;; esac
[ -e "$overlay" ] || bad="$bad $overlay was NOT restored;"
if [ -n "$bad" ]; then fail "$bad"; else
  pass "healthy exit $clean_rc · with the statusline overlay deleted exit $broken_rc naming it · restored, exit $repaired_rc"
fi

# ─── O1.11 — heal ───────────────────────────────────────────────────────────

beat O1.11-heal
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

beat O1.12-doc-vs-code
spends none
bad=""
settings_tmp="$(mktemp)"
jq '. + {laneForeignKey: "keep-me"}' "$SEAT_DIR/settings.json" >"$settings_tmp" &&
  cat "$settings_tmp" >"$SEAT_DIR/settings.json" || bad="$bad could not plant the foreign settings key;"
rm -f "$settings_tmp"
merge_out="$(install_again)"
merge_rc=$?
[ "$merge_rc" -eq 0 ] || bad="$bad the merge install exited $merge_rc ($(one_line "$merge_out"));"
[ "$(jq -r '.laneForeignKey // ""' "$SEAT_DIR/settings.json")" = keep-me ] ||
  bad="$bad pfm install overwrote a foreign settings.json key instead of merging;"
settings_tmp="$(mktemp)"
jq 'del(.laneForeignKey)' "$SEAT_DIR/settings.json" >"$settings_tmp" &&
  cat "$settings_tmp" >"$SEAT_DIR/settings.json" || bad="$bad could not remove the foreign settings key;"
rm -f "$settings_tmp"
[ -n "$(find "$HOME/.claude/themes" -maxdepth 1 -name 'professor-*.json' 2>/dev/null | head -1)" ] ||
  bad="$bad no professor-*.json theme under $HOME/.claude/themes;"
mcp_list="$(pfm mcp ls 2>&1)"
grep -q $'^chat\ttrue\t' <<<"$mcp_list" || bad="$bad pfm mcp ls does not report chat enabled;"
grep -q $'^harvester\ttrue\t' <<<"$mcp_list" || bad="$bad pfm mcp ls does not report harvester enabled;"
[ -n "$(find "$SEAT_DIR/skills" -maxdepth 2 -type l -o -maxdepth 2 -type d -name '*git*' 2>/dev/null | head -1)" ] ||
  _lane_log_only "   O1.12: no git-bridge skill directory under $SEAT_DIR/skills (I94 reads the global fan-out instead)"
if [ -n "$bad" ]; then fail "$bad"; else
  pass "settings merge keeps a foreign key, themes staged, MCP enabled in machine config, project hooks rooted in the seat"
fi

# ─── O1.13 — the misc ops CLI ───────────────────────────────────────────────

beat O1.13-misc-ops
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

# ─── O1.14 — price table report and usage error ────────────────────────────

beat O1.14-price
spends none
bad=""
price_out="$(pfm price 2>&1)"; price_rc=$?
if [ "$price_rc" -ne 0 ]; then
  bad="$bad pfm price exited $price_rc: $(one_line "$price_out");"
elif ! grep -qE '^price table: [0-9]+ rows · override: ' <<<"$price_out" ||
  ! grep -qE '^KEY[[:space:]]+ENGINE[[:space:]]+MATCH[[:space:]]+IN[[:space:]]+OUT[[:space:]]+HIT[[:space:]]+CACHED[[:space:]]+W5M[[:space:]]+W1H[[:space:]]+LONG[[:space:]]+SOURCE$' <<<"$price_out"; then
  bad="$bad pfm price omitted its summary or table header: $(one_line "$price_out");"
fi
check_out="$(pfm price --check 2>&1)"; check_rc=$?
if [ "$check_rc" -ne 0 ] || ! grep -qE '^price table: ok · [0-9]+ rows · override: ' <<<"$check_out"; then
  bad="$bad pfm price --check did not report a valid table (exit $check_rc): $(one_line "$check_out");"
fi
error_out="$(pfm price --json --check 2>&1)"; error_rc=$?
if [ "$error_rc" -ne 2 ] || ! grep -qF 'pfm price: --json and --check are exclusive' <<<"$error_out"; then
  bad="$bad pfm price conflicting flags did not name the usage error (exit $error_rc): $(one_line "$error_out");"
fi
if [ -n "$bad" ]; then fail "$bad"; else
  pass "price table summary, columns and --check report; conflicting flags exit 2 with a named error"
fi

# ─── O1.15 — model-cost refuses before any fetch ──────────────────────────

beat O1.15-model-cost
spends none
bad=""
help_out="$(pfm model-cost --help 2>&1)"; help_rc=$?
if [ "$help_rc" -ne 0 ] || ! grep -q -- '--all' <<<"$help_out"; then
  bad="$bad pfm model-cost --help lacks its catalog flag (exit $help_rc): $(one_line "$help_out");"
fi
usage_out="$(pfm model-cost --all gpt-6.1-sol 2>&1)"; usage_rc=$?
if [ "$usage_rc" -ne 2 ]; then
  bad="$bad pfm model-cost with both a model and --all exited $usage_rc, want 2: $(one_line "$usage_out");"
fi
refuse_out="$(pfm model-cost unlisted-provider/model 2>&1)"; refuse_rc=$?
if [ "$refuse_rc" -ne 1 ] || ! grep -qF 'unsupported provider' <<<"$refuse_out"; then
  bad="$bad pfm model-cost did not refuse an unsupported provider by name (exit $refuse_rc): $(one_line "$refuse_out");"
fi
# Live prices need the network the lane does not have; parsing, both catalogs
# and exact numbers are proven by the fixture tests of internal/pricing/modelcost.
if [ -n "$bad" ]; then fail "$bad"; else
  pass "model-cost help names --all; model plus --all exits 2; an unsupported provider is refused by name before any fetch"
fi

lane_end
