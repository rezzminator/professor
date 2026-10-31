# One door per operation

Binds every agent and the main chat on every engine, in code, scripts and agent procedures; a prompt rule restated in two places falls to `/quality:prompt` anti-pattern 3. Twin doors = one operation (a decision, probe, built command, parse, placement or write) implemented more than once, each door owning its copy of the decision: they drift, the door a caller enters picks the behavior, and a fix landed in one door leaves the others broken for good.

1. Before writing an operation, find its door: grep its verb, the data it touches and the outside command or literal it uses. Found → call it, or extend it with the missing case; a second door is never written.
2. A detached, scheduled, batch, headless or remote variant is a parameter of the existing door, not a copy of its body.
3. A decision takes its inputs from its subject, the thing it acts on, not from where the code runs (the caller's environment, cwd or identity), unless the caller is the subject: a timer reviving a session decides from that session's home.
4. Before fixing an operation, list every path that performs it with a closed-world grep. The fix lands in the shared door and reaches every path, or folds the twin doors into one; a fix in one twin door alone is forbidden, whichever door the brief or the bug report names.
5. Twin doors met in scope fold in the same change; out-of-scope ones are reported with their paths, never half-fixed.
6. Tests pin the operation at its door, plus one test per entry path proving it routes through that door.
7. Every builder, lander and reviewer report names the twin doors found in the change and its siblings, each folded or left with its paths.
