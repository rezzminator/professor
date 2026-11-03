#!/usr/bin/env bash
# Per-run HOME and release installs inside the private machine, as its user.
set -euo pipefail
HERE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
. "$HERE/../../../scripts/platform.sh" || exit 2
platform_in_fence || { echo 'journey-provision: requires the fence' >&2; exit 2; }
TOOLS=/usr/local/libexec/pfm-journey
trap 'rc=$?; echo "journey-provision: FAILED ${1:-unknown} line=$LINENO exit=$rc" >&2; exit "$rc"' ERR

case "${1:-}" in
  home)
    : "${JOURNEY_ROOT:?run root required}" "${JOURNEY_MCP_PORT:?run MCP port required}" "${PFM_CONFIG:?run config required}"
    [ "$(id -u)" = 1000 ] || { echo 'journey-provision: home requires uid 1000' >&2; exit 2; }
    case "$HOME" in "$JOURNEY_ROOT/home") ;; *) echo 'journey-provision: HOME is outside run root' >&2; exit 2 ;; esac
    [ "${PFM_HOME:-$HOME}" = "$HOME" ] || { echo 'journey-provision: PFM_HOME must equal HOME' >&2; exit 2; }
    mkdir -p "$HOME/.local/bin" "$HOME/.local/share/pfm-journey" "$HOME/.config/herdr" \
      "$HOME/.config/herdr-shield" "$HOME/.local/share/herdr-shield/plugin/bin" \
      "$JOURNEY_ROOT/state" "$JOURNEY_ROOT/sid" "$JOURNEY_ROOT/tmux" "$JOURNEY_ROOT/tmp/scratch"
    chmod 0700 "$HOME/.config/herdr" "$HOME/.config/herdr-shield"
    install -m 0755 "$TOOLS/pfm-previous" "$HOME/.local/bin/pfm"
    ln -sfn "$TOOLS/herdr-shield" "$HOME/.local/bin/herdr-shield"
    # The previous release resolves the same relay protocol through its former executable name.
    ln -sfn "$TOOLS/herdr-shield" "$HOME/.local/bin/herdr-keep"
    install -m 0755 "$TOOLS/herdr-shield" "$HOME/.local/share/herdr-shield/plugin/bin/herdr-shield"
    install -m 0644 "$TOOLS/herdr-plugin.toml" "$HOME/.local/share/herdr-shield/plugin/herdr-plugin.toml"
    install -m 0755 "$HERE/fixtures/herdr-key.sh" "$HOME/.local/bin/journey-herdr-key"
    jq --arg sid "$JOURNEY_ROOT/sid" --arg trace "$JOURNEY_ROOT/beat.json" --arg records "$JOURNEY_ROOT/mock-records" \
      '.jail.sid_dir = $sid | .action_file = $trace | .record_dir = $records | .steps = [{type:"turn",reply:"journey-upgrade-ok",busy_ms:1000}]' \
      "$HERE/scenarios/default.json" >"$JOURNEY_ROOT/scenario.json"
    python3 "$HERE/marketplace.py" "$JOURNEY_ROOT/scenario.json"
    : >"$HOME/.zshrc"
    : >"$HOME/.bashrc"
    printf '%s\n' '[ ! -r "$HOME/.bashrc" ] || . "$HOME/.bashrc"' >"$HOME/.bash_profile"
    printf '%s\n' 'PS1="journey%% "' >>"$HOME/.zshrc"
    printf '%s\n' 'PS1="journey$ "' >>"$HOME/.bashrc"
    python3 - "$HERE/fixtures/root-inventory.json" <<'PY'
import datetime, json, os, pathlib, shlex, sys
home = pathlib.Path(os.environ['HOME'])
root = pathlib.Path(os.environ['JOURNEY_ROOT'])
inventory = json.loads(pathlib.Path(sys.argv[1]).read_text())
def write(path, value):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(value, sort_keys=True) + '\n')
    path.chmod(0o600)
config = {
    'version': 2,
    'accounts': [{'id': n, 'configDir': f'~/.cc/{n}', 'emoji': glyph, 'claude': {'theme': inventory['claude_themes'][str(n)]}}
                 for n, glyph in zip(inventory['claude_accounts'], ['🥇', '🥈', '🥉'])],
    'claude': {'systemPrompt': 'professor', 'permissionMode': 'bypass', 'cache1h': True},
    'codex': {'homes': [{'id': 1, 'home': '~/.codex', 'emoji': '🥇'}]},
    'mcp': {'servers': {'chat': {'enabled': True}}, 'http': {'port': int(os.environ['JOURNEY_MCP_PORT'])}},
    'ask': {'engine': 'codex'},
}
write(pathlib.Path(os.environ['PFM_CONFIG']), config)
write(root / 'beat.json', {'run': os.environ['PFM_RUN'], 'beat': 'root/prepare'})
write(root / 'state/update-check.json', {'current': inventory['previous_release'], 'latest': inventory['previous_release'], 'release_url': 'https://example.invalid/releases', 'checked_at': datetime.datetime.now(datetime.timezone.utc).isoformat()})
# These user settings match the shipped login-shell exports before either release installs.
shell_env = {}
shim = pathlib.Path('/opt/pfm-journey/current/source/pfm/internal/installer/assets/shim/pfm.zsh')
for line in shim.read_text().splitlines():
    if line.startswith('export CLAUDE_'):
        for assignment in shlex.split(line)[1:]:
            key, value = assignment.split('=', 1)
            if '$' in value:
                raise SystemExit('ROOT-SHELL-ENV: expected a literal export for ' + key)
            shell_env[key] = value
write(home / '.claude/settings.json', {'env': shell_env})
trust = {str(home / 'work' / project): {'hasTrustDialogAccepted': True} for project in inventory['projects']}
for account in inventory['claude_accounts']:
    seat = home / '.cc' / str(account)
    write(seat / '.claude.json', {'hasCompletedOnboarding': True, 'oauthAccount': {'emailAddress': f'seat{account}@journey.invalid'}, 'projects': trust, 'cachedGrowthBookFeatures': {'tengu_plugin_hooks_modules': inventory['claude_function_hook_modules']}})
    # Invented fixtures are generated here; no credential file is ever copied.
    write(seat / '.credentials.json', {'claudeAiOauth': {'accessToken': f'journey-fixture-claude-{account}', 'refreshToken': f'journey-fixture-refresh-{account}', 'expiresAt': 4102444800000, 'scopes': ['user:inference', 'user:profile']}})
write(home / '.codex/auth.json', {'OPENAI_API_KEY': 'journey-fixture-codex'})
write(home / '.local/share/opencode/auth.json', {'openai': {'type': 'api', 'key': 'journey-fixture-opencode'}})
write(home / '.config/opencode/opencode.jsonc', {'model': 'openai/journey-fixture'})
for project in inventory['projects']:
    (home / 'work' / project).mkdir(parents=True, exist_ok=True)
for relative in inventory['nested']:
    (home / 'work' / relative).mkdir(parents=True, exist_ok=True)
write(home / '.config/herdr-shield/config.json', {'shell': str(home / '.local/share/pfm/install/bin/pfm-herdr-shell'), 'args': ['-l']})
(home / '.config/herdr/config.toml').write_text('''onboarding = false
[update]
version_check = false
manifest_check = false
[terminal]
default_shell = "''' + str(home / '.local/bin/herdr-shield') + '''"
shell_mode = "non_login"
[session]
resume_agents_on_restore = false
[ui.toast]
delivery = "off"
[ui.sound]
enabled = false
''')
with (home / '.config/herdr/config.toml').open('a') as config_file:
    config_file.write('[keys]\nnew_tab = ""\nnew_workspace = ""\nsplit_vertical = ""\n')
    for key, operation in [('prefix+c', 'tab'), ('prefix+shift+n', 'workspace'), ('prefix+v', 'split')]:
        command = str(home / '.local/bin/journey-herdr-key') + ' ' + operation
        config_file.write('[[keys.command]]\nkey = ' + json.dumps(key) + '\ntype = "shell"\ncommand = ' + json.dumps(command) + '\n')
    for key, operation in [('prefix+shift+c', 'tab'), ('prefix+shift+v', 'split'), ('prefix+shift+minus', 'split-down')]:
        command = str(home / '.local/bin/journey-herdr-key') + ' ' + operation
        config_file.write('[[keys.command]]\nkey = ' + json.dumps(key) + '\ntype = "shell"\ncommand = ' + json.dumps(command) + '\n')
write(root / 'inventory.json', inventory)
PY
    python3 "$HERE/projects.py"
    python3 "$HERE/root-baseline.py"
    printf 'journey-provision: HOME prepared %s\n' "$JOURNEY_ROOT"
    ;;
  install-previous|install-current|stage-current)
    : "${JOURNEY_ROOT:?run root required}" "${PFM_CONFIG:?run config required}"
    export PATH="$HOME/.local/bin:$PATH"
    systemctl --user import-environment $(compgen -e)
    if [ "$1" = install-previous ]; then source=/opt/pfm-journey/previous/source; binary="$TOOLS/pfm-previous"
    else source=/opt/pfm-journey/current/source; binary="$TOOLS/pfm-under-test"; fi
    # The fixture source has no Git metadata: install never writes a Git config.
    snapshot="$HOME/.professor.source"
    mkdir -p "$snapshot"
    rsync -a --delete --exclude='.git' --exclude='.credentials.json' --exclude='.worktrees' --exclude='/pfm.config.json' \
      --exclude='/pfm/harness-prompts/generated' "$source/" "$snapshot/"
    ln -sfn "$snapshot" "$HOME/.professor"
    install -m 0755 "$binary" "$HOME/.local/bin/pfm"
    . /worktree/infra/fence/engine-sync.sh
    engine_sync_link_stable "$TOOLS/engine-sync"
    ln -sfn "$PFM_CONFIG" "$snapshot/pfm.config.json"
    python3 - "$snapshot" <<'PY'
import pathlib, sys
sys.path.insert(0, '/worktree/infra/fence/journey')
from projects import git, source_transport
snapshot = pathlib.Path(sys.argv[1])
if not (snapshot / '.git').exists():
    git(snapshot, 'init', '-q', '-b', 'develop')
    git(snapshot, 'config', 'remote.origin.url', 'https://github.com/journey/fixture')
git(snapshot, 'add', '-A')
git(snapshot, 'commit', '--allow-empty', '-q', '-m', 'fixture release snapshot')
source_transport(snapshot)
PY
    if [ "$1" = install-previous ]; then
      # The previous installer must render its wrapper while the owner's shield config stays intact.
      (cd "$snapshot" && HERDR_CONFIG_PATH="$JOURNEY_ROOT/install-herdr.toml" pfm install --yes)
      python3 "$HERE/root-baseline.py" --previous
      python3 "$HERE/previous-units.py"
      bash "$HERE/provision.sh" service-env
      bash "$HERE/provision.sh" historical-stats-link
    elif [ "$1" != stage-current ]; then (cd "$snapshot" && pfm install --yes); fi
    ;;
  historical-stats-link)
    # Explicit historical residue: the selected prior release no longer creates this link.
    python3 - "$HERE/fixtures/root-inventory.json" <<'PY'
import json, os, pathlib, sys
fixture = json.loads(pathlib.Path(sys.argv[1]).read_text())['historical_residue']['stats_cache_link']
home = pathlib.Path(os.environ['HOME'])
target = pathlib.Path(os.environ['JOURNEY_ROOT']) / fixture['shared_target']
target.parent.mkdir(parents=True, exist_ok=True)
target.write_text('{}\n')
link = home / '.cc' / str(fixture['account']) / 'stats-cache.json'
if link.exists() or link.is_symlink():
    raise SystemExit('HISTORICAL-STATS-OCCUPIED: ' + str(link))
link.symlink_to(target)
print('HISTORICAL-STATS PASS: explicit account residue -> shared synthetic cache')
PY
    ;;
  service-env)
    python3 - <<'PY'
import json, os, pathlib
root = pathlib.Path(os.environ['JOURNEY_ROOT'])
prefix = os.environ['PFM_UNIT_PREFIX']
keys = [key for key in os.environ if key.startswith(('PFM_', 'JOURNEY_', 'MOCK_ENGINE_', 'XDG_')) or key in {'HOME','USER','LOGNAME','SHELL','PATH','DBUS_SESSION_BUS_ADDRESS'}]
for suffix in ['mcp.service', 'schedule.service', 'name-sync.service']:
    directory = pathlib.Path(os.environ['HOME']) / '.config/systemd/user' / (prefix + '-' + suffix + '.d')
    directory.mkdir(parents=True, exist_ok=True)
    (directory / 'journey.conf').write_text('[Service]\n' + ''.join('Environment=' + json.dumps(key + '=' + os.environ[key]) + '\n' for key in sorted(keys)))
(root / 'beat.json').write_text(json.dumps({'run':os.environ['PFM_RUN'],'beat':'root/prepare'}) + '\n')
PY
    ;;
  *) echo 'usage: provision.sh home|historical-stats-link|service-env|stage-current|install-previous|install-current' >&2; exit 2 ;;
esac
