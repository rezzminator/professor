#!/usr/bin/env bash
# Prepare is before scenario staging; before-entry acts in the owner's shell.
edge_prepare() { python3 "$FIXTURE/edge.py" prepare "$@"; }
edge_before_entry() { python3 "$FIXTURE/edge.py" before-entry "$@"; }
# After-entry takes the actual captured CHAT JSON, after the ordinary birth.
edge_after_entry() { python3 "$FIXTURE/edge.py" after-entry "$@"; }
edge_assert() { python3 "$FIXTURE/edge.py" assert "$@"; }
edge_supported() { python3 "$FIXTURE/edge.py" supported "$1"; }
edge_expected() { python3 "$FIXTURE/edge.py" expected "$1"; }
