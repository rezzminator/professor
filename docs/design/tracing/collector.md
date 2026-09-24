# collector

`collector` copies exact code text for a caller: a signature, a struct, a line range, a table's columns, the line an insert goes after, the test names in a file that assert something. The caller hands it numbered orders. It turns them into one `codeprobe.py collect` plan. The script writes the verbatim text to a file, and the collector returns the script's manifest. The shared script, verbs and checks are specified in [codeprobe.md](codeprobe.md); this file holds what is the collector's own.

## Contents

- [The run](#the-run)
- [Why the text stays in a file](#why-the-text-stays-in-a-file)
- [The manifest](#the-manifest)
- [Measured](#measured)
- [Open items](#open-items)

## The run

1. `codeprobe.py verbs` prints the verbs and the plan syntax.
2. One `collect ROOT --expect IDS` call carries every order under the caller's own number, one command per thing an order names.
3. At most one retry into the same DIR, correcting each MISS from the names the script offers. An order or command the retry drops returns as a MISS; a third run prints the same manifest again.
4. The final message is the manifest, `COLLECTED` to `END`.

The agent holds `Bash` only. Every read and search goes through the script, which refuses data dumps and fixture records.

## Why the text stays in a file

The final message is written by the model, so any text in it is retyped. In four runs where haiku retyped a script-written return into its message, the copy differed from the file on 31 of 710, 10 of 449, 11 of 453 and 61 of 431 lines. The damage was shifted indentation, duplicated line numbers and inserted lines. In every run where the script wrote the file, all of its lines matched the repo. The collector therefore returns the manifest, and the caller reads the text from the file the manifest names. That costs the caller one `Read`, and several collectors' files can be read in one message.

## The manifest

One line per order under the caller's number, the `@ path:FROM-TO` header of every block the file holds for it, each MISS with its reason, `END`. The caller checks completeness against its own list of orders: a missing number, a MISS, or an order whose headers do not cover what was asked all show in the manifest without opening the file.

## Measured

Two real extraction briefs from spec-writer transcripts — a Go one of 10 orders and a Python one of 9 — each run four times on successive revisions. Cost at assumed list rates; the line check compares every numbered line of the file, or of the message where it was retyped, against the repo.

| Revision | Brief | Requests | Tools | Cost (USD) | Seconds | Text faithful | Orders |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Old: `general-purpose` on `haiku` | Go | 12 | 16 | 0.17 | 134 | — | — |
| Old: `general-purpose` on `haiku` | Python | 7 | 13 | 0.12 | 87 | — | — |
| 1: text retyped into the message | Go | 9 | 8 | 0.13 | 132 | 31 of 710 lines differ | 2 commands dropped, no MISS |
| 1 | Python | 7 | 6 | 0.10 | 112 | 10 of 449 differ, lines invented | 9 orders renumbered as 18 |
| 2: manifest and file, plan history | Go | 18 | 17 | 0.13 | 100 | file exact | one sub-item silently absent |
| 2 | Python | 8 | 7 | 0.12 | 122 | retyped: 11 of 453 differ | renumbered as 19 |
| 3: `--expect`, zero-hit grep is a MISS | Go | 9 | 21 | 0.13 | 125 | never ran the script | hand-assembled |
| 3 | Python | 6 | 5 | 0.06 | 51 | manifest, file exact | caller's 9, MISS carried |
| 4: one-call prompt, `verbs`, `consts` | Go | 6 | 5 | 0.11 | 85 | manifest, file exact | caller's 10, one sub-item absent |
| 4 | Python | 6 | 5 | 0.10 | 111 | retyped: 61 of 431 differ | caller's 9, MISS carried |

- The script's file matched the repo on every line in every run where it ran.
- Plan history and `--expect` ended silent drops and renumbering once the script ran.
- The manifest was the final message in 3 of 6 runs from revision 2 on. A brief that demands the text inline ("return verbatim text, each block headed by its path") pulls haiku into retyping it.

## Open items

- Haiku keeps the manifest as its final message only half the time. Three ways out: a brief written to the collector's interface, the caller running `collect` itself (no model, exact text), or a stronger model.
- A sub-item an order names but no command covers still leaves only the manifest's missing header as the signal.
- The do-not-retype footer in the return file has not been through a measured run.
