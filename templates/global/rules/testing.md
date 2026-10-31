---
paths:
  - "**/*_test.*"
  - "**/*.test.*"
  - "**/*.spec.*"
  - "**/test_*.py"
  - "**/conftest.py"
  - "**/test/**"
  - "**/tests/**"
  - "**/__tests__/**"
  - "**/testdata/**"
  - "**/fixtures/**"
  - "**/integration/**"
  - "**/e2e/**"
  - "**/hermetic/**"
  - "**/lanes/**"
  - "**/suites/**"
---

# Writing tests

- Assert the exact value that tells correct from broken (the code, the actor, the destination, the stored row): compare the whole string or the parsed field. Never a substring inside a larger string, existence, truthiness, a length alone, any one of several values, an exception class without its code, or a status or exit code alone.
- The expected side is a literal, a fixture or a golden the test does not write; never a value computed by the code under test or read from its own templates or message files.
- A new test counts only after you watched it fail: against the unfixed code for a regression, otherwise by breaking one statement of the code it guards.
- A test runs the code; it never reads a source file as its subject.
- A test never asserts that something removed stays absent.
- A guard is tested in both directions, a bound at the boundary and one step either side.
- A test name claims a property (retries, idempotent, durable) only when the body creates the failure it survives.
- Wait on a named condition with a bounded poll, never a sleep or fixed delay; fake only the clock.
- Fakes live in the project's one fakes directory.
- A fake passes the real implementation's contract tests.
- A test never swaps a product module inline.
- Production code never picks a fake from configuration.
- A read of state other tests write filters on an id this test created: never a count, the first row or "the only" row.
- Tests differing only in a literal are one table-driven test.
- A missing dependency or credential is an error, never a self-skip.
- A flaky test is a defect to fix, never retried or quarantined.
- Names use domain words, never a bug id, date or "case N".
- Fixtures are synthetic, with no host-absolute path, process id or personal data.
