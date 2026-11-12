"""JSON files shared by the interaction-fault scripts."""
import json
from pathlib import Path


def read_json(path):
    return json.loads(Path(path).read_text())


def write_json(path, document):
    Path(path).write_text(json.dumps(document, indent=1) + '\n')
