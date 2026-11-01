---
visible-to:
  include: ["flights-foreman", "flights-foreman-sonnet", "flights-lander"]
---
# Tests: the fewest that reach the most

Binds every agent writing tests.

- Extend before you add: a new behavior or failure becomes a table row or assertion in the existing test driving its path. A new test needs a path no test reaches; a new test file, a source file with none.
- Test each decision once, at the lowest layer that sees it; never a process boot for what a unit call proves.
- Tests differing only in a literal are one table-driven test.
- Assert the exact value telling correct from broken: the whole string or parsed field, never a substring, existence, truthiness, bare length, status or exit code.
- Expect a literal, fixture or golden, never a value the code under test computed.
- Watch every test fail once: a regression on the unfixed code, else by breaking one statement it guards.
- A fixture never performs a step the code under test owns.
- An absence assertion sits beside a presence assertion proving the subject ran.
- Pin a fixed user-visible failure in the existing test of the surface the user saw (the round trip, not the parser).
- A test runs code, never reads source as its subject.
- A test never asserts that something removed stays absent.
- Test a guard both ways, a bound at the boundary and one step either side.
- Fakes and recordings enter through the one seam production code calls.
- Poll a named condition, bounded; never a fixed sleep or wait.
- A test owns every resource it touches under a run-unique name and removes it on exit.
- A read of shared state filters on an id the test created.
- Test changes stay inside the code change's blast radius.
- Delete a replaced suite before writing its replacement.
- A failing assertion prints expected, actual and the path of any artifact it read.
- A missing dependency or credential is an error, never a skip.
- Fix a flaky test; never retry or quarantine it.
- Names are domain words, never a bug id, date or "case N".
