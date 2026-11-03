#!/usr/bin/env bash
# Prepare prints the input fixture path; parent stages it with chat.py scenario.
boot_prepare() { python3 "$FIXTURE/boot.py" prepare "$@"; }
# After entry opens startup gates; parent captures the actual chat exactly once.
boot_after_entry() { python3 "$FIXTURE/boot.py" after-entry "$@"; }
# Assert follows capture and prints the exact JSON projection for lane assertions.
boot_assert() { python3 "$FIXTURE/boot.py" assert "$@"; }
boot_stdout_after_entry() { python3 "$FIXTURE/boot.py" stdout-after-entry "$@"; }
boot_supported() { python3 "$FIXTURE/boot.py" supported "$1"; }
