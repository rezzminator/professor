"""Read-only account paths and native stats-cache identity."""
import json
import os
from pathlib import Path
import stat


def default_account_path(home, account):
    """Mirror pfm/internal/config.DefaultAccountDir for observer-only reads."""
    if type(account) is not int or account <= 0:
        raise ValueError(f"OBSERVE-ACCOUNT-ID: {account!r}")
    return Path(home) / ".cc" / str(account)


def _account_ids(chat):
    values = [chat.get("account"), chat.get("prior_account"), *chat.get("prior_accounts", [])]
    result = []
    for value in values:
        if isinstance(value, str) and value.isdecimal():
            value = int(value)
        if type(value) is int and value > 0 and value not in result:
            result.append(value)
    return result


def _account_path(chat, account):
    paths = chat.get("account_config_dirs", {})
    if not isinstance(paths, dict):
        raise ValueError(f"OBSERVE-ACCOUNT-PATHS: {chat.get('session_id', '')}")
    configured = paths.get(str(account), paths.get(account))
    if configured:
        return Path(configured)
    for birth in chat.get("process_births", []):
        environment = birth.get("environment", {}) if isinstance(birth, dict) else {}
        if account == chat.get("account") and environment.get("CLAUDE_CONFIG_DIR"):
            return Path(environment["CLAUDE_CONFIG_DIR"])
    return default_account_path(os.environ["HOME"], account)


def account_stats_cache(chat):
    """Read owned current/prior Claude stats caches without following guessed owners."""
    if chat.get("engine") not in {"claude", "cc"}:
        return "n/a:non-claude"
    result = {}
    for account in _account_ids(chat):
        path = _account_path(chat, account) / "stats-cache.json"
        entry = {"account": account, "path": str(path)}
        try:
            path.lstat()
        except FileNotFoundError:
            entry.update({"state": "n/a:absent", "regular": False, "symlink": False,
                          "identity": "n/a:absent", "bytes": "n/a:absent", "parsed": "n/a:absent"})
            result[str(account)] = entry
            continue
        link_stat = path.lstat()
        entry["symlink"] = stat.S_ISLNK(link_stat.st_mode)
        entry["regular"] = stat.S_ISREG(link_stat.st_mode) and not entry["symlink"]
        entry["state"] = "symlink" if entry["symlink"] else "regular-file" if entry["regular"] else "other"
        try:
            target_stat = path.stat()
            entry["identity"] = {"device": target_stat.st_dev, "inode": target_stat.st_ino}
            raw = path.read_bytes()
            parsed = json.loads(raw)
        except (OSError, ValueError) as error:
            raise ValueError(f"OBSERVE-ACCOUNT-STATS-CACHE-READ: account={account}: {error}") from error
        if not isinstance(parsed, dict):
            raise ValueError(f"OBSERVE-ACCOUNT-STATS-CACHE-SHAPE: account={account}")
        entry["document"] = parsed
        entry["bytes"] = len(raw)
        entry["parsed"] = {"version": parsed.get("version"), "last_computed_date": parsed.get("lastComputedDate"),
                            "daily_activity_count": len(parsed.get("dailyActivity", [])) if isinstance(parsed.get("dailyActivity", []), list) else None,
                            "model_usage_count": len(parsed.get("modelUsage", {})) if isinstance(parsed.get("modelUsage", {}), dict) else None}
        result[str(account)] = entry
    return result


