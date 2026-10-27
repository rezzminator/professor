# Release checks. `make release-check` runs, locally, exactly the checks the
# develop -> main pull request's jobs in .github/workflows/release.yml run, and
# those jobs call these same targets, so a release never fails on a check
# nobody ran. Run `git fetch origin main` first: `release` reads origin/main.
# pfm's own build and gate targets live in pfm/Makefile (`make -C pfm help`).

SHELL := /usr/bin/env bash
.SHELLFLAGS := -eu -o pipefail -c

# CI=true on runners, where setup-node gives Node 22; locally mise does.
RUN := $(if $(CI),,mise exec node@22 --)
# CI's engine-sync action exports the binary it built from plugins/engine-sync.
ENGINE_SYNC_BIN ?= engine-sync
DIST := tmp/dist
RELEASE_VERSION ?= v$(shell cat VERSION)
PLATFORMS := linux/amd64 linux/arm64 darwin/arm64 darwin/amd64

.PHONY: release-check check build release-binary release

release-check: check build release
	@echo "NOT RUN LOCALLY: check job's Scan — the HOL AI Plugin Scanner is a GitHub Action (hashgraph-online/ai-plugin-scanner-action) with no local install"

# The check job: the Codex and OpenCode mirrors the scanner scores.
check:
	"$(ENGINE_SYNC_BIN)" build .

# Every build-job leg, one platform after another.
build:
	@for p in $(PLATFORMS); do $(MAKE) --no-print-directory release-binary GOOS="$${p%/*}" GOARCH="$${p#*/}"; done

# One platform's binary; GOOS and GOARCH from the caller.
# Public release binaries deliberately use -trimpath so no builder path ships.
# They will NOT hash-match a local `env -u GOFLAGS` build, which deliberately
# omits -trimpath; both hashes are correct for their respective build contracts.
release-binary:
	mkdir -p $(DIST)
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) go -C pfm build -trimpath -ldflags "-X main.version=$(RELEASE_VERSION)" -o "../$(DIST)/pfm_$(RELEASE_VERSION)_$(GOOS)_$(GOARCH)" ./cmd/pfm

# The release job: VERSION moved past origin/main's, and its authored notes
# releases/v{VERSION}.md exist and pass the grammar.
release:
	@v="$$(cat VERSION)"; \
	main="$$(git show origin/main:VERSION)" || { echo "FAIL could not read VERSION on origin/main (git fetch origin main first)"; exit 1; }; \
	$(RUN) node -e 'const p = (s) => { const m = /^(\d+)\.(\d+)\.(\d+)$$/.exec(s); if (!m) { console.log("FAIL version " + s + " is not X.Y.Z"); process.exit(1); } return m.slice(1).map(Number); }; const [v, main] = process.argv.slice(1); const a = p(v), b = p(main); if ((a[0] - b[0] || a[1] - b[1] || a[2] - b[2]) <= 0) { console.log("FAIL version " + v + " is not past main'"'"'s " + main); process.exit(1); } console.log("version " + v + " is past main'"'"'s " + main);' "$$v" "$$main"; \
	notes="releases/v$$v.md"; \
	[[ -f "$$notes" ]] || { echo "missing authored release notes: $$notes" >&2; exit 1; }; \
	$(RUN) node scripts/release-check.mjs notes "$$notes" --version "v$$v"
