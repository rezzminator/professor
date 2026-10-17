#!/usr/bin/env bash
# lib/meter.sh: the LAB_METER seam (D9) — spend read, unpriced and failed meters never read as $0.
set -uo pipefail
# shellcheck source=../testlib.sh
source "$(dirname "$0")/../testlib.sh"
lab_lib run meter
LAB_RUN=$T/run; mkdir -p "$LAB_RUN/config/projects"; LAB_SID=s1; LAB_METER=$FAKES/meter
FAKE_METER_TOTAL=0.42 lab_meter_read; rc=$?
expect_eq 'spend read' "$rc $LAB_USD" '0 0.42'
out=$(FAKE_METER_UNPRICED=1 lab_meter_read 2>&1); rc=$?
expect_eq 'unpriced calls are an error' "$rc $out" '2 ERROR meter unpriced calls 1'
# pfm tokens drops an undated call and a malformed line from its total: either is spend the meter missed
out=$(FAKE_METER_NO_TS=1 lab_meter_read 2>&1); rc=$?
expect_eq 'undated calls are an error' "$rc $out" '2 ERROR meter calls without a timestamp 1'
out=$(FAKE_METER_BAD_LINES=2 lab_meter_read --final 2>&1); rc=$?
expect_eq 'malformed lines are an error at the final read' "$rc $out" '2 ERROR meter malformed lines 2'
FAKE_METER_BAD_LINES=2 FAKE_METER_TOTAL=0.30 lab_meter_read; rc=$?
expect_eq 'a poll reads past a line mid-append' "$rc $LAB_USD" '0 0.30'
out=$(FAKE_METER_FAIL=1 lab_meter_read 2>&1); rc=$?
expect_eq 'failed meter is an error' "$rc ${out%%:*}" '2 ERROR meter'
LAB_USD=1.79 LAB_CAP=1.80; expect_eq 'under cap' "$(lab_over_cap; echo $?)" 1
LAB_USD=1.80; expect_eq 'at cap stops' "$(lab_over_cap; echo $?)" 0
finish
