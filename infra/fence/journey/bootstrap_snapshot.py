"""Capture coherent observation and native checkpoint birth receipts."""
import os
from pathlib import Path
from runtime import read_json, write_json


def complete_observation(target):
    import bootstrap
    latest = {}
    def ready():
        nonlocal latest
        latest = bootstrap.bundle(target)
        return latest if not latest.get('errors') else False
    try:
        return bootstrap.wait('complete observation and settled holders', ready, 30)
    except ValueError as error:
        raise ValueError(f"ROOT-OBSERVATION: {latest.get('errors', [str(error)])!r}") from error


def snapshot(lane):
    root = Path(os.environ['JOURNEY_ROOT'])
    carry = read_json(os.environ['JOURNEY_CARRY'])
    target = root / 'checkpoint-target.json'
    write_json(target, carry['owner'])
    observed = complete_observation(target)
    records = {}
    for chat, projection in zip([*carry['chats'], carry['foreign']], [*observed['chats'], observed['foreign_full']]):
        display = projection['terminal']
        terminal_identity = ({key: display['backend'] if key == 'backend' else display['identity'][key]
                              for key in chat['terminal']} if isinstance(display, dict) else display)
        births = projection.get('process_births', [])
        records[chat.get('seat_id') or chat['session_id']] = {
            'status': projection['status'], 'terminal': terminal_identity,
            'births': [{key: birth[key] for key in ('pid','start')} for birth in births] if isinstance(births, list) else []}
    write_json(root / 'checkpoint-births.json', records)
    write_json(Path(os.environ['JOURNEY_OUT']) / 'checkpoints' / (lane + '.births.json'), records)
    print('CHECKPOINT RECEIPTS PASS: observed statuses, terminals and process births captured')


