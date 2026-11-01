---
visible-to:
  exclude: [main, Explore, Plan, claude-code-guide, statusline-setup, web-fetch, "rr*", sub-rr, "*tracer", collector, transcript-reader, agent-investigator, clone-distiller, documenter, gitter, pcm, changelogger, "reviewer*", flights-hunter, flights-sub-lander, flights-spec-walker, flights-speccer]
---
# Tests: the fewest that reach the most

- Extend before you add: a new behaviour or failure is asserted inside the existing test that already drives that path, as a table row or an added assertion. A new test needs a path no test reaches; a new test file needs a source file with no test file.
- Test each decision once, at the lowest layer that can see it: never again at a second layer, never a process boot to prove what a unit call proves.
- Tests differing only in a literal are one table-driven test.
- Assert the exact value that tells correct from broken: the whole string or the parsed field, never a substring inside a larger string, existence, truthiness, a length alone or a bare status or exit code.
- The expected side is a literal, fixture or golden, never computed by the code under test.
- A test must be able to fail: watch it fail once, against the unfixed code for a regression, else by breaking one statement it guards.
- A fixture never performs a step the code under test owns.
- An absence assertion sits beside a presence assertion proving the subject ran.
- A user-visible failure, once fixed, is pinned at the surface the user saw (the round trip, not the parser), inside that surface's existing test.
- A test runs the code; it never reads a source file as its subject.
- A test never asserts that something removed stays absent.
- A guard is tested in both directions, a bound at the boundary and one step either side.
- Fakes and recorded responses enter through the one seam production code already calls.
- Never sleep or wait a fixed delay: poll a named condition, bounded.
- A test owns every resource it touches under a run-unique name and removes it on exit.
- A read of shared state filters on an id the test created.
- Test changes stay inside the code change's blast radius.
- A suite being replaced is deleted before its replacement is written.
- A failing assertion prints the expected value, the actual value and the path of any artifact it read.
- A missing dependency or credential is an error, never a skip.
- A flaky test is a defect to fix, never retried or quarantined.
- Names are domain words, never a bug id, date or "case N".
