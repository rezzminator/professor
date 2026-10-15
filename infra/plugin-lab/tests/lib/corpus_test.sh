#!/usr/bin/env bash
# lib/corpus.sh: deterministic word text, about 4 characters per token, a unique last word.
set -uo pipefail
# shellcheck source=../testlib.sh
source "$(dirname "$0")/../testlib.sh"
lab_lib corpus
LAB_RUN=$T/run; mkdir -p "$LAB_RUN/work"
need_corpus block-01 15000
f=$LAB_RUN/work/corpus/block-01.txt
size=$(wc -c < "$f" | tr -d ' ')
expect_eq 'about 4 characters per token' "$(( size >= 58000 && size <= 62000 ))" 1
expect_eq 'ends with its unique last word' "$(tr -s ' \n' '\n\n' < "$f" | tail -n 1)" block-01-END
a=$(sha256_of "$(cat "$f")"); need_corpus block-01 15000
expect_eq 'deterministic' "$(sha256_of "$(cat "$f")")" "$a"
need_corpus block-02 15000
expect_eq 'two names differ' "$([[ "$(head -c 200 "$f")" != "$(head -c 200 "$LAB_RUN/work/corpus/block-02.txt")" ]] && echo differ)" differ
finish
