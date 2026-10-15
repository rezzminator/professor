# shellcheck shell=bash
# lib/corpus.sh: reading material (D15): deterministic numbered word text at about
# 4 characters per token, ending with the unique last word {name}-END.

need_corpus() { # <name> <tokens>: writes {run}/work/corpus/{name}.txt
  local f=$LAB_RUN/work/corpus/$1.txt
  mkdir -p "${f%/*}" || { lab_need_fail "need_corpus $1: cannot create ${f%/*}"; return; }
  awk -v name="$1" -v want="$(( $2 * 4 ))" 'BEGIN {
    n = split("oak fern moss pine reed sage teal plum rust jade iris lime opal ruby wren yarn", v, " ")
    k = 7; for (i = 1; i <= length(name); i++) k = (k * 33 + index("abcdefghijklmnopqrstuvwxyz0123456789-", substr(name, i, 1))) % 104729
    head = name " corpus\n"; printf "%s", head; total = length(head); line = 1
    while (total < want - length(name) - 5) {
      l = sprintf("%05d", line)
      for (j = 0; j < 12; j++) { k = (k * 31 + 7) % 104729; l = l " " v[k % n + 1] (k % 1000) }
      l = l "\n"; printf "%s", l; total += length(l); line++
    }
    printf "%s-END\n", name
  }' > "$f" || lab_need_fail "need_corpus $1: cannot write $f"
}
