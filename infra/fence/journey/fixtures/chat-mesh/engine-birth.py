#!/usr/bin/env python3
"""Save the actual engine identity and its safe configuration environment."""
import sys

sys.path.insert(0, sys.argv[1])
from observation import terminal, terminal_births
from observation_process import KEYS, engine_births
from runtime import read_json, write_json

chat = read_json(sys.argv[2])
display = terminal(chat["terminal"])
births = engine_births({"terminal": display, "process_births": terminal_births(display)}, chat["engine"])
if len(births) != 1:
    raise ValueError(f"MESH-ENGINE-BIRTH: expected one engine, got {births!r}")
write_json(sys.argv[3], {**births[0], "observed_environment_keys": sorted(KEYS)})
