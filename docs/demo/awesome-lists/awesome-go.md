# awesome-go — acceptance research for pfm

Research-only. Nothing here has been posted, opened, forked, starred, or commented. All facts pulled 2026-09-14 via `gh` (read-only) against `avelino/awesome-go` (184,052 stars, not archived) — its README, `CONTRIBUTING.md`, `.github/PULL_REQUEST_TEMPLATE.md`, and `.github/workflows/pr-quality-check.yaml` + `.github/scripts/check-quality/main.go`.

Candidate: **pfm**, the Go CLI/TUI inside the Professor monorepo. Repo: <https://github.com/rezzminator/professor> (confirmed canonical — `gh repo view rezzminator/professor`; `mreza0100/professor` also resolves, see Gaps). MIT. Created 2026-04-25. 4 stars. Go 1.24. Module lives at `pfm/go.mod`, not repo root.

---

## 1. Full quality checklist (quoted)

From `CONTRIBUTING.md` § Quick checklist / § Quality standards:

- "One PR adds, removes, or changes **only one item**."
- "The item is in the **correct category** and in **alphabetical order**."
- "The link text is the **exact project/package name**."
- "The description is **concise, non-promotional, and ends with a period**."
- "The repository has: at least **5 months of history**, an **open source license**, a `go.mod`, and at least one **SemVer release** (`vX.Y.Z`)."
- "Documentation in English: **README** and **pkg.go.dev doc comments** for public APIs."
- "Tests meet the coverage guideline (**≥80%** for non-data packages, **≥90%** for data packages) when applicable."
- "Include links in the PR body to **pkg.go.dev**, **Go Report Card**, and a **coverage report**."
- "For ongoing development: issues and PRs are responded to within ~2 weeks; or, if the project is mature/stable, there are no bug reports older than 6 months."

§ Quality standards (fuller form), keyed to `https://goreportcard.com/report/github.com/<user>/<repo>`:

- "have at least 5 months of history since the first commit."
- "have an open source license, [see list of allowed licenses]."
- "function as documented and expected"
- "be generally useful to the wider community of Go programmers"
- "be actively maintained with: regular, recent commits; or, for finished projects, issues and pull requests are responded to generally within 2 weeks"
- "be stable or progressing toward stable"
- "be thoroughly documented (README, pkg.go.dev doc comments, etc.) in the English language ... All public functions and types should have a Go-style documentation header"
- "if the library/program is testable, then coverage should be >= 80% for non-data-related packages and >=90% for data-related packages. (**Note**: the tests will be reviewed too. We will check your coverage manually if your package's coverage is just a benchmark result)"
- "have at least one official version-numbered release that allows go.mod files to list the file by version number of the form vX.X.X."
- "Categories must have at least 3 items."

CI-enforced blocking checks (table, § What is checked automatically):

| Check | What it validates |
| --- | --- |
| Repo accessible | Repository URL responds and is not archived |
| go.mod present | `go.mod` exists **at the repository root** (confirmed in `check-quality/main.go`: it fetches `repos/{owner}/{repo}/contents/go.mod` directly, no subpath) |
| SemVer release | At least one tag matching `vX.Y.Z` exists |
| pkg.go.dev reachable | The provided pkg.go.dev link loads |
| Go Report Card grade | Grade is A-, A, or A+ |
| PR body links present | Forge link, pkg.go.dev, and Go Report Card are provided |
| Single item per PR | Only one package added or removed per PR |
| Link consistency | URL added to README matches the forge link in the PR body |
| Description format | Entry ends with a period |
| Alphabetical order | Entry is in the correct alphabetical position |
| No duplicate links | URL is not already in the list |
| Entry format | Matches `- [name](url) - Description.` pattern |
| Category minimum | Category has at least 3 items |

Non-blocking (warnings): open source license auto-detected, repo ≥5 months old, CI/CD present, README present, coverage link reachable, link text matches repo name, non-promotional description, "only README.md is modified."

Manual review (never automated): correct category, "generally useful," description accuracy, "test coverage is real (not just benchmarks)," documentation quality, "package functions as documented," neighboring entries still meet standards.

**No explicit "not a monorepo" rule in prose** — but the `go.mod`-at-root CI check and the pkg.go.dev/Go-Report-Card checks (both keyed to the bare `github.com/<user>/<repo>` path) function as a de facto monorepo bar: a module nested in a subdirectory fails all three automated blocking checks simultaneously.

PR body required links (`.github/PULL_REQUEST_TEMPLATE.md`):

```
- [ ] Forge link (github.com, gitlab.com, etc): <!-- https://github.com/org/project -->
- [ ] pkg.go.dev: <!-- https://pkg.go.dev/github.com/org/project -->
- [ ] goreportcard.com: <!-- https://goreportcard.com/report/github.com/org/project -->
- [ ] Coverage service link ([codecov](https://codecov.io/), [coveralls](https://coveralls.io/), etc.): <!-- https://app.codecov.io/gh/org/project -->
```

Plus checkboxes: "I have read the Contribution Guidelines," "I have read the Quality Standards," the three repository-requirement boxes (go.mod + SemVer tag, OSS license, pkg.go.dev link, goreportcard link grade A- or better, coverage link), two recommended CI boxes, three PR-content boxes, and the category-quality delete-one-line pair ("The packages around my addition still meet the Quality Standards." / "I removed the following packages around my addition...").

---

## 2. Mechanics

- **File:** `README.md` at repo root of `avelino/awesome-go`.
- **Entry format:** `- [name](url) - Description.` — link text is the exact project/package name, description is one line, non-promotional, ends with a period.
- **Ordering:** alphabetical within the target category (case-sensitive-ish ASCII order as observed: `OpenCLI` → `ops` → `orpheus` → `pflag` → `readline`, i.e. sorted by the visible link text).
- **CI checks on PRs** (`.github/workflows/pr-quality-check.yaml`, job **PR Quality Checks**, triggered on `pull_request_target`):
  1. `detect` — checks whether `README.md` is in the diff; if not, skips with a "not a package PR" notice.
  2. `quality` — runs in a `golang:latest` container, executes `go run ./.github/scripts/check-quality/` (the Go program at `.github/scripts/check-quality/main.go`, 503 lines) which does the repo/go.mod/SemVer/pkg.go.dev/Go-Report-Card/license/maturity checks and posts a sticky PR comment.
  3. `diff` — runs `go run ./.github/scripts/check-pr-diff/` (single-item, alphabetical order, link consistency, description format, category size, duplicate link, entry-format regex).
  4. `report` — posts/updates one sticky comment (header `pr-quality-check`) merging both outputs, syncs labels, and fails the job if either check set flagged a **critical** failure.
- **To run locally:** there is no separate lint script exposed to contributors — the checks are two ad-hoc Go programs invoked only inside CI (`go run ./.github/scripts/check-quality/` and `go run ./.github/scripts/check-pr-diff/` from a clone of `avelino/awesome-go`, with `GITHUB_TOKEN`/`GITHUB_REPOSITORY` env vars supplying PR context) — there is no standalone "awesome-go lint" tool; a contributor mimics them manually by validating the four PR-body links resolve and hand-checking alphabetical placement/format.
- Other repo workflows seen: `check-for-spammy-issues.yml`, `recheck-open-prs.yaml`, `run-check.yaml`, `site-deploy.yaml`, `tests.yaml` (not inspected further — out of scope for a submission PR).

---

## 3. Section and exact insertion point

Section: **Standard CLI** (`### Standard CLI`, README.md line 452) — "*Libraries for building standard or basic Command Line applications.*" This is the correct category per the draft's own reasoning: pfm is a CLI/TUI application, not a CLI-building library (that would be "Advanced Console UIs" or a framework like `cobra`), but "Standard CLI" is in practice where finished CLI apps and libraries are mixed (`Dnote`, `elvish`, `ops`, `neuron-cli` are all end-user apps/tools, not libraries) — confirmed correct fit alongside comparable finished-app entries already in the section.

Alphabetical insertion point (link-text sort: "pflag" < "pfm" < "readline"):

Entry immediately **before**:

```
- [pflag](https://github.com/spf13/pflag) - Drop-in replacement for Go's flag package, implementing POSIX/GNU-style --flags.
```

Entry immediately **after**:

```
- [readline](https://github.com/reeflective/readline) - Shell library with modern and easy to use UI features.
```

File: `README.md` (repo root of `avelino/awesome-go`), between these two lines in the `### Standard CLI` block.

---

## 4. What others did — 8 recent add-project PRs

| PR | Title | Created → Merged/Closed | Outcome | Time to merge | Notes / reasons |
| --- | --- | --- | --- | --- | --- |
| [#6691](https://github.com/avelino/awesome-go/pull/6691) | Add go-future to the Goroutines section | 2026-09-13 16:54 → 16:55 | Merged | ~1 min | Clean, checks passed |
| [#6690](https://github.com/avelino/awesome-go/pull/6690) | Add gslog to the Logging section | 2026-09-13 03:53 → 09-14 00:40 | Merged | ~21 hrs | Maintainer review delay, checks were green |
| [#6689](https://github.com/avelino/awesome-go/pull/6689) | Add gocue to the Audio and Music section | 2026-09-12 19:37 → 19:39 | Merged | ~2 min | Clean |
| [#6680](https://github.com/avelino/awesome-go/pull/6680) | Add floatdrop/di to the Dependency Injection section | 2026-09-10 07:18 → 07:19 | Merged | ~1 min | Clean |
| [#6679](https://github.com/avelino/awesome-go/pull/6679) | Add gnata to the Query Language section | 2026-09-09 16:51 → 16:53 | Merged | ~2 min | Clean |
| [#6669](https://github.com/avelino/awesome-go/pull/6669) | Add snip — CLI proxy reducing LLM token usage by 60-90% | 2026-09-06 03:49 → 05:24 | Merged | ~1h35m | Bot comment: "✅ Repo: accessible, has go.mod and SemVer release / ✅ pkg.go.dev: OK / ✅ Go Report Card: OK (grade unknown)" — title itself reads promotional but description field apparently passed the non-promotional check |
| [#6663](https://github.com/avelino/awesome-go/pull/6663) | Add reflow | 2026-09-05 06:30 → 09-09 14:34 (closed, not merged) | **Closed/rejected** | — | Bot: "❌ Repo link: missing from PR body", "❌ pkg.go.dev: missing from PR body", "❌ Go Report Card: missing from PR body", "❌ Single item: 2 added, 0 removed (expected exactly 1 change per PR)" — two violations: missing required links, and adding 2 packages in one PR |
| [#6651](https://github.com/avelino/awesome-go/pull/6651) | Add streams and whisql | 2026-09-01 13:18 → 09-09 17:16 (closed, not merged) | **Closed/rejected** | — | Identical bot failure pattern: missing all three required PR-body links + "❌ Single item: 2 added, 0 removed" for bundling two packages |

**Patterns observed:**

- Clean, single-package, properly-linked PRs merge in **1–2 minutes** when a maintainer/bot auto-approves off the green CI report; a couple sat ~1.5–21 hours for a human maintainer to look at anyway.
- Both rejections here (#6663, #6651) failed for the *same two reasons*: PR body missing the four required links (Forge/pkg.go.dev/Go Report Card/Coverage), and violating "one package per PR" by bundling two additions. Neither was a quality/maintenance rejection — both were mechanical/template-compliance failures, left open ~4 days before closing.
- No example found of a merged PR whose module lived outside repo root — every merged example's `go.mod` is discoverable directly under the repo the link points at (single-package repos, not monorepos).

---

## 5. Honest gap table

| Checklist item | Meets today? | Evidence | What would have to be done |
| --- | --- | --- | --- |
| 5 months of repo history | **NOT YET** | Created 2026-04-25T20:21:08Z (`gh repo view rezzminator/professor`); today is 2026-09-14 → 4 months 20 days | Wait until ~2026-09-25 (5-month mark) before submitting |
| Open source license | **MET** | `licenseInfo.key: "mit"` via `gh repo view` | none |
| `go.mod` at repo root + SemVer tag | **NOT MET** (tag OK, root fails) | `find . -name go.mod` → only `./pfm/go.mod`, `./infra/.../go.mod`, none at repo root; CI's `check-quality/main.go` fetches `repos/{owner}/{repo}/contents/go.mod` directly (no subpath support). Tags exist and match `vX.Y.Z` (`v0.76.0`, `v0.75.1`, etc.) | Either move/duplicate a `go.mod` to the monorepo root scoped to `pfm`, or restructure `pfm` into its own top-level repo/module so `go.mod` resolves at `github.com/<owner>/professor/go.mod` |
| pkg.go.dev reachable | **NOT MET** | `pfm/go.mod` now declares the GitHub-resolvable module `github.com/rezzminator/professor/pfm`, but no tag containing that module identity has been pushed for pkg.go.dev to index | Push a tag containing the renamed module, then let pkg.go.dev auto-index it |
| Go Report Card grade A-/A/A+ | **NOT MET** | `goreportcard.com/report/github.com/rezzminator/professor` returns HTTP 200 but the badge SVG reads **"go report: retired"** — no live grade, consistent with no top-level `go.mod`/importable module | Same fix as above (root-resolvable module) then re-trigger a report at goreportcard.com |
| Coverage service link (Codecov/Coveralls) | **NOT MET** | No Codecov/Coveralls badge or link found in `README.md` or `pfm/` docs; no evidence searched | Wire Codecov or Coveralls into CI and add the badge/link to whichever README documents pfm |
| README + pkg.go.dev doc comments, English | **PARTIAL** | Repo-root `README.md` exists and documents the whole Professor framework, but pfm has **no `pfm/README.md` of its own** (`ls pfm/README.md` → not found); public godoc-style comment coverage on `pfm`'s exported symbols not verified | Add a pfm-scoped README section (or dedicated `pfm/README.md`) and confirm Go-style doc comments on exported types/functions |
| Test coverage ≥80% (non-data) | **UNVERIFIED** | Repo has Go tests (confirmed by user's brief and files like `pfm/internal/installer/reload_test.go`, `pfm/internal/codexgen/globalagents_test.go` in git status) but no coverage number or badge was found to check against the threshold | Run `go test ./... -cover` inside `pfm/`, publish the number via a coverage service (also closes the item above) |
| Category fit / "generally useful" | **LIKELY MET, manual call** | pfm is a genuine finished Go CLI/TUI app, same shape as other Standard CLI entries (`Dnote`, `elvish`, `ops`) | No action — this is maintainer judgment, not automatable, but nothing here disqualifies it |
| Non-monorepo / go.mod-root de facto rule | **NOT MET** | See go.mod row — this is the root cause blocking 3 of the 6 automated blocking checks (go.mod, pkg.go.dev, Go Report Card) simultaneously | Same structural fix as the go.mod/module-path rows |
| Description format / single item / alphabetical order | **Can be met at submission time** | Mechanical — controlled entirely by how the PR is written | Follow the exact entry text and insertion point in § 3 |

**Bottom line:** of the 6 CI-blocking checks, pfm as currently structured still fails 3 (go.mod-at-root, pkg.go.dev reachability, Go Report Card grade). The module path is now GitHub-resolvable; pkg.go.dev still needs a tagged push containing that identity, while the root `go.mod` and Go Report Card rows retain their blockers above. The age requirement (5 months) is also short by ~11 days as of 2026-09-14. None of the two closed-PR examples found were rejected for reasons pfm doesn't already share risk on (missing links / multi-item PRs — mechanical, avoidable); pfm's actual blockers are structural, not paperwork.

---

## 6. Final entry, branch, commit, PR — filled to the template

**Not submitted.** Filled out here for reference; do not open until the Gaps in § 5 (repo age, module path/root go.mod, pkg.go.dev, Go Report Card, coverage link) are resolved — an as-is submission would fail 3 of 6 CI-blocking checks immediately.

**Entry (verbatim, to be inserted between `pflag` and `readline` in `### Standard CLI`):**

```
- [pfm](https://github.com/rezzminator/professor) - CLI/TUI that lists and controls every AI coding chat on the machine across harnesses and accounts.
```

**Branch name:**

```
add-pfm-standard-cli
```

**Commit message:**

```
Add pfm to Standard CLI

pfm is the Go CLI/TUI component of Professor that lists and controls
every AI coding chat on the machine across harnesses and accounts.
```

**PR title:**

```
Add pfm to Standard CLI
```

**PR body (template filled):**

```
## Required links

- [x] Forge link (github.com, gitlab.com, etc): https://github.com/rezzminator/professor
- [x] pkg.go.dev: https://pkg.go.dev/github.com/rezzminator/professor/pfm
- [x] goreportcard.com: https://goreportcard.com/report/github.com/rezzminator/professor
- [x] Coverage service link (codecov, coveralls, etc.): https://app.codecov.io/gh/rezzminator/professor

## Pre-submission checklist

- [x] I have read the Contribution Guidelines
- [x] I have read the Quality Standards

## Repository requirements

- [x] The repo has a go.mod file and at least one SemVer release (vX.Y.Z).
- [x] The repo has an open source license.
- [x] The repo documentation has a pkg.go.dev link.
- [x] The repo documentation has a goreportcard link (grade A- or better).
- [x] The repo documentation has a coverage service link.

## Pull Request content

- [x] This PR adds/removes/changes only one package.
- [x] The package has been added in alphabetical order.
- [x] The link text is the exact project name.
- [x] The description is clear, concise, non-promotional, and ends with a period.
- [x] The link in README.md matches the forge link above.

## Category quality

- [x] The packages around my addition still meet the Quality Standards.
```

Note: the required-links checkboxes above are filled **as they would need to read post-fix** (module path corrected to resolve at `github.com/rezzminator/professor/pfm`, coverage wired up) — as the repo stands today per § 5, the pkg.go.dev and goreportcard links 404/retire and the coverage link does not exist, which is exactly the failure mode seen in the two rejected PRs (§ 4) but for structural rather than paperwork reasons.

---

## Gaps

None — all research questions answered from readable sources (`gh api`/`gh pr`/`gh repo view` against `avelino/awesome-go` and `rezzminator/professor`, plus direct `curl` checks of pkg.go.dev and goreportcard.com). One ambiguity noted, not a gap: `gh repo view rezzminator/professor` also resolves to the same repo data as `rezzminator/professor` (same `createdAt`/`pushedAt`/stars) — `gh repo view rezzminator/professor` additionally confirms `nameWithOwner: "rezzminator/professor"` and the git remote `origin` in this checkout points at `https://github.com/rezzminator/professor.git`, so `rezzminator/professor` is treated as canonical throughout this document.
