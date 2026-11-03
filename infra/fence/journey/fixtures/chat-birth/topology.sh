#!/usr/bin/env bash
# Stage before scenario; act after normal capture; compare the exact projection.
topology_prepare() { python3 "$FIXTURE/topology.py" prepare "$@"; }
topology_before_entry() { python3 "$FIXTURE/topology.py" before-entry "$@"; }
topology_after_entry() { python3 "$FIXTURE/topology.py" after-entry "$@"; }
topology_assert() { python3 "$FIXTURE/topology.py" assert "$@"; }
topology_expected() { python3 "$FIXTURE/topology.py" expected "$@"; }
topology_supported() { python3 "$FIXTURE/topology.py" supported "$@"; }
