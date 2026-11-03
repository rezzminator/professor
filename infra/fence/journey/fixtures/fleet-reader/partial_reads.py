#!/usr/bin/env python3
"""Drive partial reads through public entries while preserving good native records.

run SLUG CHAT OWNER RESOURCES PREFIX writes checks, receipts and a complete bundle.
Resources: bad_chat; good_chats (fresh Claude/Codex/OpenCode, index only);
tokens_good_chat (fresh Claude sonnet-4-6); usage_cache/config_dir/idle_stop (statusline);
config_dir (headless).
"""
from contextlib import contextmanager
import json
import os
from pathlib import Path
import sys

from forecast import HERE, action, check
from bootstrap import wait
from chat import inline, refresh
import faults
from observation import bundle, process_snapshot, report
from observation_process import engine_births
from runtime import read_json, write_json, write_text
from projection import selected, text_fleet_checks
import statusline

TOKENS = {"input": 1000, "output": 100, "cache_read": 0, "cache_creation": 0, "context_window": 200000}
BAD_LINE = '{"type":"user","message":\n'
HEADLESS_DIAGNOSTIC = "could not read Claude usage: json: cannot unmarshal string into Go value of type map[string]jsontext.Value"


def carried(value):
    matches = [row for row in read_json(os.environ["JOURNEY_CARRY"])["chats"]
               if row["key"] == value["key"] and row["session_id"] == value["session_id"]
               and row["terminal"] == value["terminal"]]
    if len(matches) != 1:
        raise ValueError(f"PARTIAL-READ-CARRY: exact owned actor required: {value['key']}")
    return matches[0]


def transcript(chat):
    value = json.loads(report(["pfm", "chat", "read", chat["session_id"], "--tail", "1", "--json"]))
    path = faults.owned_path(value["path"])
    if not path.is_file() or path.suffix != ".jsonl":
        raise ValueError(f"PARTIAL-READ-TRANSCRIPT: native JSONL file required: {path}")
    return path


def warning(records, message, field, value):
    return [row for row in records if row.get("msg") == message and row.get(field) == value]


def index_checks(observed, chats, bad_path):
    checks = []
    for chat in chats:
        row = selected(observed, chat)
        if chat["engine"] == "opencode":
            records = row["stored"]["transcript"]["attribution"]
            checks.append(check("indexed-opencode-prompt", [turn_input("partial-index-opencode")[1]],
                                [item["first_prompt"] for item in records if item["id"] == chat["session_id"]]))
        else:
            entries = row["stored"]["transcript"]["entries"]
            checks.append(check("indexed-" + chat["engine"] + "-answer", ["partial-index-" + chat["engine"] + "-answer"],
                                [item["text"] for item in entries if item["role"] == "assistant"]))
    rows = warning(observed["activity"]["records"], "index.unparsed_lines", "path", str(bad_path))
    checks.append(check("named-unparsed-transcript", [1], sorted({row["count"] for row in rows})))
    return checks


def token_checks(document, chat, unreadable_path):
    rows = [row for row in document["runs"] if row["sid"] == chat["session_id"]]
    fields = ("engine", "model", "calls", "tok", "usd")
    expected = {"engine": "claude", "model": "claude-sonnet-4-6", "calls": 1,
                "tok": {"in": 1000, "out": 100, "cw5": 0, "cw1": 0, "cr": 0}, "usd": 0.0045}
    return [check("retained-priced-usage", [expected], [{key: row[key] for key in fields} for row in rows]),
            check("named-unreadable-transcript", [f"{unreadable_path}: read {unreadable_path}: is a directory"],
                  document["scan"]["readErrors"])]


def named_checks(observed, chat):
    row = selected(observed, chat)
    medal = {1: "🥇", 2: "🥈", 3: "🥉"}[chat["account"]]
    return [check("preserved-good-name", ["partial-reader-good"], [item["name"] for item in row["picker"]]),
            check("preserved-good-tab", medal + " PARTIAL-READER-GOOD", row["terminal"]["tab"]["tab"]["label"]),
            check("preserved-good-store", ["partial-reader-good"], [item["name"] for item in row["stored"]["chat"]
                  if item["uuid"] == chat["session_id"]])]


def name_warning_checks(records, path):
    rows = warning(records, "index.unreadable", "path", str(path))
    return [check("named-name-sync-partial-read", [{"kind": "claude transcript",
        "err": f'open "{path}": open {path}: no such device or address'}],
        [{"kind": kind, "err": error} for kind, error in sorted({(row["kind"], row["err"]) for row in rows})])]


def rename_birth_checks(before, after, chat):
    prior = engine_births(before, chat["engine"])
    if len(prior) != 1:
        raise ValueError(f"PARTIAL-NAME-ENGINE-BIRTH: one actual native engine required: {prior!r}")
    return [check("rename-keeps-native-birth", [{key: row[key] for key in ("pid", "start")} for row in prior],
                  [{key: row[key] for key in ("pid", "start")} for row in engine_births(after, chat["engine"])])]


def account_warning_checks(records, path):
    wanted = f"statusline: usage cache {path}: decode usage cache {path}: unexpected end of JSON input (missing windows show ERR)"
    actual = sorted({row.get("err") for row in records
                     if row.get("msg") == "statusline.warn" and str(path) in str(row.get("err", ""))})
    return [check("named-partial-account-cause", [wanted], actual)]


def account_error_checks(output):
    import re
    return [check("partial-account-error-cell", ["7d-fable-used:ERR"],
                  re.findall(r"\b7d-fable-used:[^\s│]+", statusline.plain(output)))]


def headless_checks(output, processes, births, config_dir, actual_config_dir):
    if len(births) != 1 or type(births[0].get("pid")) is not int or not births[0].get("start"):
        raise ValueError(f"PARTIAL-HEADLESS-BIRTH: exact fresh native child required: {births!r}")
    lines = output.splitlines()
    envelopes = [json.loads(line) for line in lines if line.startswith('{"engine":')]
    if len(envelopes) != 1:
        raise ValueError(f"PARTIAL-HEADLESS-ENVELOPE: expected one native result, got {len(envelopes)}")
    result = envelopes[0]
    return [check("retained-headless-answer", "partial-reader-headless-answer", result.get("result")),
            check("headless-engine", "cc", result.get("engine")),
            check("headless-model", "claude-sonnet-4-6", result.get("model")),
            check("retained-headless-usage", {"input_tokens": 1000, "cached_input_tokens": 0,
                  "cache_creation_input_tokens": 0, "output_tokens": 100}, result.get("usage")),
            check("retained-headless-cost", 0.0012, result.get("total_cost_usd")),
            check("named-headless-partial-usage", [HEADLESS_DIAGNOSTIC], result.get("diagnostics")),
            check("headless-visible-diagnostic", ["pfm headless: engine diagnostic: " + HEADLESS_DIAGNOSTIC],
                  [line for line in lines if not line.startswith('{"engine":')]),
            check("headless-children-reaped", [], [birth for birth in births
                  if processes.get(birth["pid"], {}).get("start") == birth["start"]]),
            check("headless-account", config_dir, actual_config_dir)]


def turn_input(label, tokens=TOKENS, rates=None):
    steps = [{"type": "turn", "reply": label + "-answer", "busy_ms": 20, "tokens": tokens}]
    if rates is not None:
        steps[0]["rate_limits"] = rates
    return steps, label + " mock-engine: " + json.dumps(steps, separators=(",", ":"), sort_keys=True)


def headless_inputs(prefix):
    import shlex
    import uuid
    faults.owned_path(Path(prefix).parent)
    path, records, witness = str(prefix) + ".headless-scenario.json", str(prefix) + ".headless-records", str(prefix) + ".headless-account.txt"
    if any(Path(value).exists() or Path(value).is_symlink() for value in (path, records, witness)):
        raise ValueError("PARTIAL-HEADLESS-INPUT-EXISTS: fresh native scenario/records/witness required")
    setup = read_json(os.environ["MOCK_ENGINE_SCENARIO"])
    setup.update({"session_id": str(uuid.uuid4()), "model": "claude-sonnet-4-6", "record_dir": records,
                  "action_file": os.environ["PFM_BEAT_FILE"], "headless_usage_error": True, "steps": [
        {"type": "tool_call", "tool": "Bash", "input": {
            "command": "printf '%s\\n' \"$CLAUDE_CONFIG_DIR\" > " + shlex.quote(witness)}},
        {"type": "turn", "reply": "partial-reader-headless-answer", "busy_ms": 20, "tokens": TOKENS}]})
    write_json(path, setup)
    return Path(path), Path(records), Path(witness)


class PartialRead:
    def __init__(self, chat, owner, resources, prefix):
        self.chat, self.owner, self.resources, self.prefix = carried(chat), owner, resources, str(prefix)
        faults.owned_path(Path(prefix).parent)
        self.checks, self.receipts, self.inputs = [], [], []
        self.changed_keys = set()

    def command(self, label, arguments):
        prefix = self.prefix + "." + label
        receipt = action(self.owner, arguments, prefix)
        self.receipts.append({"prefix": prefix, "argv": arguments})
        return receipt

    def bad_chat(self):
        chat = carried(self.resources["bad_chat"])
        good = [self.chat, *self.resources.get("good_chats", [])]
        if self.resources.get("tokens_good_chat"):
            good.append(self.resources["tokens_good_chat"])
        if chat["engine"] != "claude" or chat["session_id"] in {row["session_id"] for row in good}:
            raise ValueError("PARTIAL-READ-BAD-ACTOR: separate owned Claude transcript required")
        return chat

    def turn(self, chat, label, tokens=TOKENS, rates=None):
        self.changed_keys.add(chat["key"])
        prompts = Path(chat["records"]) / "prompts"
        cursor = prompts.stat().st_size if prompts.exists() else 0
        steps, expected = turn_input(label, tokens, rates)
        script = self.prefix + "." + label + ".steps.json"
        write_json(script, steps)
        inline(chat["terminal"], script, label)
        wait("native partial-read submitted input", lambda: prompts.exists() and
             prompts.read_bytes()[cursor:].decode().rstrip("\n") == expected, 10)
        def completed():
            if chat["engine"] == "opencode":
                rows = selected(bundle(self.prefix + ".target.json"), chat)["stored"]["transcript"]["attribution"]
                return any(row["id"] == chat["session_id"] and row["first_prompt"] == expected
                           and row["assistant_count"] >= 1 for row in rows)
            value = json.loads(report(["pfm", "chat", "read", chat["session_id"], "--tail", "1", "--json"]))
            return [{key: row[key] for key in ("role", "text")} for row in value["entries"]] == [
                {"role": "assistant", "text": label + "-answer"}]
        wait("native partial-read response completed", completed, 20)
        wait("native partial-read turn idle", lambda: json.loads(report(
            ["pfm", "chat", "status", chat["session_id"], "--json"]))["state"] == "idle", 20)
        path = self.prefix + ".input-" + str(len(self.inputs)) + ".txt"
        write_text(path, prompts.read_bytes()[cursor:].decode().rstrip("\n"))
        self.inputs.append({"path": path, "expected": expected, "actual": Path(path).read_text()})

    @contextmanager
    def malformed(self):
        chat = self.bad_chat()
        self.turn(chat, "partial-bad-source")
        path = transcript(chat)
        with faults.temporary_text(path, path.read_text() + BAD_LINE):
            yield path

    @contextmanager
    def unreadable(self):
        chat = self.bad_chat()
        self.turn(chat, "partial-unreadable-source")
        path = transcript(chat)
        # The socket's different size/mtime forces a fresh read; the index deliberately skips symlinks.
        with faults.file_read_error(path, kind="socket"):
            yield path

    def index(self):
        chats = [carried(row) for row in self.resources["good_chats"]]
        if sorted(row["engine"] for row in chats) != ["claude", "codex", "opencode"]:
            raise ValueError("PARTIAL-INDEX-ENGINES: one fresh actor per native engine required")
        for chat in chats:
            self.turn(chat, "partial-index-" + chat["engine"])
        with self.malformed() as path:
            result = self.command("command", ["pfm", "index", "--full"])
            self.checks.append(check("partial-index-exit", 0, result["exit_code"]))
        observed = bundle(self.prefix + ".target.json")
        self.checks += index_checks(observed, chats, path)
        # The public roster is the OpenCode identity presence arm alongside its indexed nonce.
        result = self.command("roster", ["pfm", "ls", "--all", "--tsv"])
        self.checks += text_fleet_checks(result["output"], {"chats": chats})

    def tokens(self):
        good = carried(self.resources["tokens_good_chat"])
        self.turn(good, "partial-tokens")
        good_path, bad_path = transcript(good), transcript(carried(self.resources["bad_chat"]))
        roots = list(dict.fromkeys([str(good_path.parent), str(bad_path.parent)]))
        output = self.prefix + ".tokens.json"
        arguments = ["pfm", "tokens", "--session", good["session_id"], "--out", output,
                     "--prices", str(HERE.parents[2] / "pfm/internal/tokens/testdata/fixtures/model-cost.json")]
        for root in roots:
            arguments += ["--root", root]
        with faults.file_read_error(bad_path):
            result = self.command("command", arguments)
            self.checks.append(check("partial-tokens-exit", 1, result["exit_code"]))
            self.checks += token_checks(read_json(output), good, str(bad_path))

    def names(self):
        before = selected(bundle(self.prefix + ".target.json"), self.chat)
        self.changed_keys.add(self.chat["key"])
        result = self.command("rename", ["pfm", "chat", "name", self.chat["session_id"], "partial-reader-good"])
        self.checks.append(check("rename-exit", 0, result["exit_code"]))
        with self.unreadable() as path:
            result = self.command("command", ["pfm", "name-sync", "--apply"])
            self.checks.append(check("name-sync-exit", 0, result["exit_code"]))
        self.chat = refresh(self.chat["branch"], self.chat.get("role", self.chat["origin"]))
        observed = bundle(self.prefix + ".target.json")
        self.checks += named_checks(observed, self.chat)
        after = selected(observed, self.chat)
        self.checks += rename_birth_checks(before, after, self.chat)
        self.checks += name_warning_checks(observed["activity"]["records"], path)

    def account(self):
        cache = faults.owned_path(self.resources["usage_cache"], preserve=True)
        records = Path(self.chat["records"]) / "statusline-callbacks.jsonl"
        self.turn(self.chat, "partial-account", statusline.TOKENS, statusline.QUOTA)
        wanted = f"statusline: usage cache {cache}: decode usage cache {cache}: unexpected end of JSON input (missing windows show ERR)"
        with faults.temporary_text(cache, "{", 0o600):
            cursor = records.stat().st_size
            wait("actual named usage-cache read failure", lambda: warning(bundle(self.prefix + ".target.json")["activity"]["records"],
                 "statusline.warn", "err", wanted), 30)
            rows = statusline.ready_rows(records, cursor, self.chat, {"5h": "89%", "7d": "34%"})
            self.checks += statusline.receipt_checks(rows[-1], self.chat, self.resources, {"5h": "89%", "7d": "34%"})
            self.checks += account_error_checks(rows[-1]["stdout"])
        observed = bundle(self.prefix + ".target.json")
        self.checks += account_warning_checks(observed["activity"]["records"], cache)

    def headless(self):
        scenario, records, witness = headless_inputs(self.prefix)
        result = self.command("command", ["pfm", "headless", "exec", "--engine", "claude", "--account", "2",
            "--model", "claude-sonnet-4-6", "--prompt", "partial-reader-headless", "--output-format", "json",
            "--no-session-persistence", "--env", "MOCK_ENGINE_SCENARIO=" + str(scenario)])
        births = [json.loads(line) for line in (records / "birth.json").read_text().splitlines() if line]
        # The native shell tool in the staged scenario records its inherited config directory.
        self.checks += headless_checks(result["output"], process_snapshot(), births, self.resources["config_dir"],
                                      Path(witness).read_text().strip())
        self.checks.append(check("partial-headless-exit", 0, result["exit_code"]))


def run(slug, chat_file, owner_file, resources_file, prefix):
    operation = {"index-keeps-good-engine-sessions-after-bad-record": "index", "tokens-names-partial-transcript-read": "tokens",
                 "name-sync-preserves-good-name-after-partial-read": "names",
                 "statusline-warns-on-partial-account-read": "account", "headless-names-partial-usage": "headless",
                 "headless-retains-valid-answer-after-partial-read": "headless"}.get(slug)
    if operation is None:
        raise ValueError(f"PARTIAL-READ-SLUG: {slug}")
    reader = PartialRead(read_json(chat_file), read_json(owner_file), read_json(resources_file), prefix)
    stop = None
    if operation == "account":
        stop = faults.owned_path(reader.resources["idle_stop"], preserve=True)
        actual = read_json(reader.chat["scenario"]).get("idle_statusline_refresh", {}).get("stop_when_exists")
        if str(stop) != actual:
            raise ValueError("PARTIAL-IDLE-STOP-MISMATCH: stop gate must belong to this actual actor scenario")
    try:
        write_json(str(prefix) + ".target.json", reader.chat["terminal"])
        getattr(reader, operation)()
        observed = bundle(str(prefix) + ".target.json")
        write_json(str(prefix) + ".observation.json", observed)
        write_json(str(prefix) + ".chat.json", reader.chat)
        write_json(str(prefix) + ".checks.json", reader.checks)
        names = [row["name"] for row in read_json(os.environ["JOURNEY_CARRY"])["chats"] if row["key"] in reader.changed_keys]
        write_json(str(prefix) + ".evidence.json", {"operation": operation, "receipts": reader.receipts,
                   "inputs": reader.inputs, "changed_names": names})
    finally:
        if stop is not None:
            write_text(stop, "complete\n")


if __name__ == "__main__":
    try:
        if sys.argv[1] != "run":
            raise ValueError("usage: partial_reads.py run SLUG CHAT OWNER RESOURCES PREFIX")
        run(*sys.argv[2:])
    except (OSError, ValueError, KeyError, TypeError, RuntimeError) as error:
        print(f"PARTIAL-READ-ERROR: {error}", file=sys.stderr)
        sys.exit(2)
