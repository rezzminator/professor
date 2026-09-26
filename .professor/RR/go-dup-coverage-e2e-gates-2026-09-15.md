# RR — Mechanically enforcing duplicate-code, coverage, and integration-test gates in a Go 1.24 tmux CLI monorepo

Question: rr — research query, sources cited. Context: a Go 1.24 CLI monorepo (`pfm`, stdlib flag, tmux-driven, modernc sqlite, tests run inside a Docker fence via `go test ./...`, a `Makefile` with fmt-check/vet/arch/test, currently NO golangci-lint config, NO coverage gate, NO integration tests beyond a small `e2e/` package with `-tags e2e -p 1`). We want to enforce three things MECHANICALLY (in CI + a Makefile gate), and need the current best tooling with exact configuration snippets and known pitfalls: (1) cross-file/cross-package duplicate code detection; (2) coverage law ≥80% per-package and total without third-party services; (3) integration-test harnesses for a Go CLI driving tmux and subprocesses.

## Answer

Use **dupl (via golangci-lint v2) as the only cross-package clone detector that survives variable renaming** — it suffix-trees over AST *node types*, not token text — backed by goconst/gocritic for the narrow local cases, and gate CI on `--new-from-merge-base` so the existing corpus does not block you. For coverage, **native `go test -coverprofile -coverpkg=./...` plus `go build -cover` + `GOCOVERDIR` + `go tool covdata textfmt` merged into one profile, thresholded by vladopajic/go-test-coverage v2** (per-file/per-package/total, exclude-paths, base-branch diff), no SaaS. For e2e, **`rogpeppe/go-internal/testscript` txtar scripts over the instrumented binary**, with tmux exec'd on a scratch socket and `capture-pane` polled, not slept on.

---

## 1. Duplicate-code detection — ranked

| Tool | Catches renamed-variable re-implementation? | Notes |
|---|---|---|
| **dupl** (1st) | **Yes** — README: "ignores values of AST nodes, it just operates with their types", so `if a == 13 {}` ≡ `if x == 100 {}` | Cross-file/cross-package; the only one in the Go-native stack that does structural clone detection ([mibk/dupl](https://github.com/mibk/dupl)) |
| **golangci-lint v2 `dupl`** (1st, packaging) | Same — it is a thin wrapper; `threshold` = minimum AST-token-sequence length (default 100) | No extra fuzzing beyond dupl ([config docs](https://golangci-lint.run/docs/configuration/file/)) |
| **jscpd** (2nd, polyglot) | **Only with `--ignore-identifiers`** — default is Type-1 exact; the flag substitutes placeholders for names to get Type-2 | Lists Go among 220+ formats; also covers your shell/JS in one pass ([jscpd README](https://github.com/kucherenko/jscpd)) |
| **PMD CPD** (3rd) | **Only with opt-in** `--ignore-identifiers` / `--ignore-literals` / `--ignore-annotations`; default finds "identical duplicates" token-for-token | Go supported ([PMD CPD docs](https://docs.pmd-code.org/latest/pmd_userdocs_cpd.html)) |
| **gocritic dupSubExpr / dupBranchBody** | No — intra-function/intra-expression only | Complementary, not a substitute (mechanism *unverified* — gocritic source not fetched) |
| **goconst** | No — repeated string/numeric **literals** only | Orthogonal; catches magic-value duplication |
| **Simian** | **Unverified** — no primary source fetched in either round | Commercial, Go support unconfirmed |

**Recommended stack:** golangci-lint v2 (`dupl` threshold 100–150, `goconst`, `gocritic`, `unused`, `errcheck`) as the Makefile/CI gate, plus **jscpd with `--ignore-identifiers`** as a second, repo-wide (Go + shell + JS) pass since a monorepo's duplication crosses language boundaries that dupl cannot see.

### `.golangci.yml` (v2 schema — note the `linters:`/`formatters:` split)

```yaml
version: "2"

linters:
  enable:
    - dupl
    - goconst
    - gocritic
    - unused
    - errcheck
  settings:
    dupl:
      threshold: 100
    goconst:
      min-len: 2
      min-occurrences: 3
      ignore-tests: true

formatters:
  enable:
    - gofumpt
    - gci
```

Verified against [golangci-lint's own `.golangci.yml`](https://github.com/golangci/golangci-lint/blob/main/.golangci.yml) — which notably does **not** itself set `issues.new-from-rev`. In the May 2026 release, `dupl` was updated to `c99c5cf5c202` with extended detection and `goconst` to 1.10.0 with an ignore-strings-from-tests option ([changelog](https://golangci-lint.run/docs/product/changelog/)).

### Failing CI on new findings only

Four mutually-exclusive baselines (not stacked, no precedence): `--new` (unstaged/untracked, or vs `HEAD~` on a clean tree), `--new-from-rev=REV`, `--new-from-merge-base=BRANCH`, `--new-from-patch=PATH`. All filter by whether an issue's reported line falls in the changed-line ranges. The docs explicitly warn off `--new` for CI ("can skip linting the current patch if any scripts generate unstaged files") and recommend `--new-from-rev=HEAD~`; for a PR job **`--new-from-merge-base=origin/main` is the more robust choice** ([CLI docs](https://golangci-lint.run/docs/configuration/cli/)).

```make
lint:
	golangci-lint run ./...
lint-ci:
	golangci-lint run --new-from-merge-base=origin/develop ./...
```

---

## 2. Coverage law — ≥80% per-package and total, no SaaS

Two layers:

1. **Unit** — `go test ./... -coverprofile=cover.out -covermode=atomic -coverpkg=./...`. `-coverpkg=./...` is what makes calls into package B from package A's tests count toward B's own files — essential for a CLI where most logic is exercised through command-level tests.
2. **Black-box/integration** — `go build -cover` produces an instrumented binary; running it with `GOCOVERDIR=<dir>` writes `covmeta`/`covcounters`; `go tool covdata textfmt -i=<dir> -o=integration.txt` converts to a legacy profile, and `-i` accepts a **comma-separated list of dirs**, which is how unit and integration data become one number ([go.dev/doc/build-cover](https://go.dev/doc/build-cover), [Go blog: integration test coverage](https://go.dev/blog/integration-test-coverage)).

Threshold enforcement: [vladopajic/go-test-coverage v2](https://github.com/vladopajic/go-test-coverage) reads the profile and enforces `threshold.file` / `threshold.package` / `threshold.total`, supports per-path `override`, `exclude.paths` regexes for generated code, an SVG badge, and a base-branch drop check via `breakdown-file-name` + `diff.base-breakdown-file-name`. It also accepts a comma-separated profile list (`cover_unit.out,cover_integration.out`).

```makefile
.PHONY: cover
cover:
	go test ./... -coverprofile=cover.out -covermode=atomic -coverpkg=./...
	mkdir -p .covdata/unit .covdata/e2e
	go build -cover -o bin/pfm ./cmd/pfm
	GOCOVERDIR=.covdata/e2e ./bin/pfm <smoke invocations>
	go tool covdata textfmt -i=.covdata/unit,.covdata/e2e -o=integration.out
	go-test-coverage --config=./.testcoverage.yml
```

```yaml
# .testcoverage.yml
profile: cover.out,integration.out
threshold:
  file: 70
  package: 80
  total: 80
override:
  - path: ^internal/harvest$
    threshold: 90
exclude:
  paths:
    - \.pb\.go$
    - ^internal/installer/assets
breakdown-file-name: coverage-breakdown.json
diff:
  base-breakdown-file-name: base-coverage-breakdown.json
  threshold: 0.5
```

**Pitfalls (each load-bearing for this repo):**
- A binary killed ungracefully or panicking **never flushes GOCOVERDIR** — only a clean return/`os.Exit` writes counters. Tmux-driven CLI smoke tests that kill the pane will silently lose coverage.
- `-coverpkg` wants import paths, not the literal `main`.
- External `_test` (black-box) packages are covered fine under `-coverpkg=./...`, but coverage is attributed to the package under test — don't expect the `_test` files themselves in the profile.
- `-p 1` (which your e2e already uses) serializes builds and is sometimes needed to avoid flaky instrumentation under parallel module builds.
- Generated code must exist *before* `go build -cover` runs or it lands at 0% and drags `threshold.total` — that is exactly what `exclude.paths` is for.
- Build-tag-gated code (`-tags e2e`) may be excluded from instrumentation entirely — see open questions.

---

## 3. Integration harness for a tmux-driving CLI

**Ranked: testscript > TestMain+os/exec over a `go build -cover` binary > testcontainers-go** (the last is redundant — you already have a Docker fence).

`rogpeppe/go-internal/testscript` runs `.txtar` scripts in a real filesystem sandbox (`$WORK`), registers your CLI's subcommands as in-process "binaries", exposes a `Setup` hook and custom `Cmds`, and `UpdateScripts: true` regenerates golden output. It is the pattern `cmd/go` itself uses ([pkg.go.dev](https://pkg.go.dev/github.com/rogpeppe/go-internal/testscript), [encore.dev writeup](https://encore.dev/blog/testscript-hidden-testing-gem)). **`RunMain` is deprecated — use `Main(m, cmds)`**; the encore article's example is stale.

```go
func TestMain(m *testing.M) {
	os.Exit(testscript.Main(m, map[string]func(){
		"pfm": func() { os.Exit(run(os.Args[1:])) },
	}))
}

func TestCLI(t *testing.T) {
	testscript.Run(t, testscript.Params{
		Dir: "testdata/scripts",
		Setup: func(env *testscript.Env) error {
			env.Setenv("TMUX_TMPDIR", env.WorkDir)
			return nil
		},
		UpdateScripts: os.Getenv("UPDATE_SCRIPTS") == "1",
	})
}
```

```
-- testdata/scripts/smoke.txtar --
exec pfm --version
stdout 'v\d+\.\d+\.\d+'
```

tmux has no native testscript support — exec it as a subprocess, on an isolated socket, and **poll rather than sleep**:

```sh
tmux -L pfmtest -f /dev/null new-session -d -s t 'pfm chat new'
for i in $(seq 1 50); do
  tmux -L pfmtest capture-pane -t t -p | grep -q 'ready' && break
  sleep 0.1
done
tmux -L pfmtest capture-pane -t t -p   # final assertion
tmux -L pfmtest kill-server            # teardown
```
(This tmux pattern is the community convention and was **not** confirmed against a fetched primary source — treat as unverified.)

**How mature Go CLIs do it:**
- **lazygit** — each test is a Go file at `pkg/integration/tests/<feature>/<name>.go` with a Setup phase (shell commands building fixture repos) and a Run phase using a fluent driver, e.g. `t.Views().Commits().Focus().PressKey(keys.Universal.Confirm)`. Tests auto-register into `test_list.go` via `just generate`; `just e2e` runs the whole suite headless (fastest, CI mode), `just e2e commit/new_branch` one test, `just e2e-cli` with a visible UI, `just e2e-tui` an interactive picker, plus `--debug`/`--sandbox` to attach a debugger or take over after setup ([pkg/integration/README.md](https://raw.githubusercontent.com/jesseduffield/lazygit/master/pkg/integration/README.md)). **This is the closest model to pfm's needs** — one headless default path for CI, richer modes for humans, which is how the matrix stays cheap.
- **gopass** — a `tester` struct: `newTester(t)` builds an isolated `t.TempDir()` with env via `t.Setenv()` and GPG fixtures; `run(arg)` / `runCmd(args, in)` shell-split via `shellquote.Split()` and exec the built binary with `CombinedOutput()`; `runWithInputReader` feeds stdin; `initStore()`/`initSecrets()` reuse the same exec path; `teardown()` restores env ([tests/tester.go](https://raw.githubusercontent.com/gopasspw/gopass/master/tests/tester.go)).

Route-multiplication without runtime explosion: the lazygit answer is **one headless mode as the CI default** with per-test selection for local work, not a CI matrix over UI modes.

---

## Open questions

- **Does `--new-from-merge-base` correctly attribute a *new* dupl/goconst finding?** Both linters reason across the whole codebase; line-range filtering may flag a pre-existing sibling clone or miss the new one. Unresolved, and it decides whether the baseline gate is trustworthy. Rebase behaviour of merge-base in CI is also unconfirmed.
- **Build-tag-gated code under `go build -cover`** — whether `-tags e2e` files are instrumented or silently dropped from totals. Directly affects whether an 80% total is honest for this repo.
- **Simian** — no primary source fetched in either round; identifier normalization and Go support remain unverified.
- **gocritic `dupSubExpr`/`dupBranchBody` scope** — asserted as intra-function from design reasoning, not from gocritic's own docs.
- **lazygit's actual keystroke-injection mechanism** (tmux? PTY? direct terminal-model calls?) is not in its README — the one detail that would tell you whether to copy its driver or stay with exec'd tmux.
- **Threshold values in practice** — no cross-repo consensus found for `dupl.threshold` beyond the default 100; 150–200 as a noise-reduction setting is unverified.
- **go-test-coverage badge publication** (git branch vs artifact vs Pages) not established.

Diggers dispatched: 5 (3 round 1, 2 round 2). Reports received: 5. Known fetch failures: golangci-lint false-positives page 404'd (CLI-docs page substituted); gopass `tests/` tree view returned a listing only in round 1 (recovered in round 2 via raw `tester.go`).
