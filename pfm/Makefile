# pfm — one entry point for building, gating, installing, and keeping the
# fleet's own processes honest.
#
# This file does NOT reimplement the repo gates. `.claude/scripts/dev.sh` owns
# `test`, `iso`, `verify`, and friends for every project; duplicating them here
# would create a second definition of "green" that drifts from the first. The
# targets below delegate to it, and add only what it does not own: the host
# mirror install, and the stale-process problem that install creates.
#
# THE STALE-PROCESS PROBLEM, stated once because every target below exists
# because of it: replacing ~/.local/bin/pfm does not touch a process already
# running the old image. The kernel keeps the replaced inode alive for it
# (Linux and macOS alike), so a
# long-lived `pfm mcp serve` keeps serving yesterday's tool descriptions and a
# days-old `pfm ls` picker keeps writing yesterday's decisions over today's
# fixes. Both fail SILENTLY — the binary on disk is new, the behaviour is old,
# and nothing in between says a word. `make install` therefore restarts the
# daemon every time and reports every other stale process it can see.

SHELL := /usr/bin/env bash
# Real exit codes: a failing command in a pipe fails the target, instead of the
# last command in the pipe deciding the verdict.
.SHELLFLAGS := -eu -o pipefail -c
.DEFAULT_GOAL := help

REPO    := $(abspath $(dir $(abspath $(lastword $(MAKEFILE_LIST))))..)
DEV     := $(REPO)/.claude/scripts/dev.sh
BIN     := $(HOME)/.local/bin/pfm
LOCAL   := pfm.dev
UNIT    := pfm-mcp.service
# The macOS twin of UNIT: the launch agent pfm install wires for the daemon.
AGENT   := com.professor.pfm.mcp
# Stamped into every build below so `pfm --version` names the release it came
# from instead of the bare "dev" a plain `go build` leaves behind (issue #14
# F6). $(REPO)/VERSION, not a bare "VERSION" — this file runs from pfm/.
VERSION := $(shell cat $(REPO)/VERSION 2>/dev/null)
LDFLAGS := -ldflags "-X main.version=$(VERSION)"
# Pinned tools (infra/tools.env): the host keeps them in TOOLS_BIN (gitignored
# tmp/), the fence image in /usr/local/bin. A tool missing from both is
# TOOLCHAIN-MISSING, never "use whatever golangci-lint is on PATH".
# Keyed by OS/arch: the fence mounts this tree read-only from a darwin host,
# so a linux container must never pick the host's binary — it finds nothing
# under its own key and falls through to PATH (/usr/local/bin in the image).
TOOLS_BIN ?= $(REPO)/tmp/tools/$(shell go env GOOS)-$(shell go env GOARCH)/bin
LINT  := $(firstword $(wildcard $(TOOLS_BIN)/golangci-lint) $(shell command -v golangci-lint 2>/dev/null))
COVER := $(firstword $(wildcard $(TOOLS_BIN)/go-test-coverage) $(shell command -v go-test-coverage 2>/dev/null))
# Coverage outputs: the fence mounts the tree read-only, so dev.sh iso points
# COVER_DIR at container HOME; on the host it lands in gitignored tmp/.
COVER_DIR ?= $(REPO)/tmp/cover
# lint-new judges lines changed since this ref: a wave against develop; CI
# passes origin/main so a release is judged on everything since the last one.
LINT_BASE ?= origin/develop
# The first provisional fenced capture uses conservative serial concurrency;
# W5 re-measures the sweep and may ratchet this toward the observed knee.
TESTFLAGS ?= -p 1 -parallel 1

.PHONY: help build tools fmt fmt-check lint lint-new vet arch cover test testflags iso timing sweep gate install host-install rollback \
        mcp-restart mcp-status stale sweep-stale clean

help: ## List every target
	@echo "pfm targets — run as: make <target>"
	@echo
	@grep -hE '^[a-z-]+:.*?## ' $(MAKEFILE_LIST) \
	  | sort \
	  | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'
	@echo
	@echo "Gates delegate to $(DEV) — this file never redefines them."

# ---------------------------------------------------------------- build ----

build: ## Compile to ./pfm.dev (a local artifact, never the shipped path)
	go build $(LDFLAGS) -o $(LOCAL) ./cmd/pfm

tools: ## Install the pinned tools from infra/tools.env into TOOLS_BIN
	TOOLS_BIN=$(TOOLS_BIN) bash $(REPO)/infra/tools.sh

# need-tool <var>: name the missing tool as TOOLCHAIN-MISSING instead of a bare
# "command not found" three lines into a recipe.
define need-tool
	@test -x "$($(1))" || { echo "$(1): TOOLCHAIN-MISSING — run 'make tools' (infra/tools.env)" >&2; exit 1; }
endef

fmt: ## Rewrite formatting in place (gofumpt + gci + golines via golangci-lint fmt), both kernels
	$(call need-tool,LINT)
	@# Build-tagged files load only for the current GOOS, so each supported
	@# kernel is formatted in turn; golines splits a long line and gofumpt
	@# regroups the result, so a pass can leave a file the next pass still
	@# changes: run until a pass is a no-op.
	@for pass in 1 2 3; do \
	  for goos in linux darwin; do GOOS=$$goos $(LINT) fmt ./... 2>&1 | grep -v '^level=' || true; done; \
	  if [ -z "$$(GOOS=linux $(LINT) fmt --diff ./... 2>/dev/null)$$(GOOS=darwin $(LINT) fmt --diff ./... 2>/dev/null)" ]; then echo "fmt: stable after $$pass pass(es)"; exit 0; fi; \
	done; echo "fmt: did not converge in 3 passes — a formatter fight; run 'make fmt-check' to see the files" >&2; exit 1

fmt-check: ## Fail if anything is unformatted on either kernel, naming the files
	$(call need-tool,LINT)
	@diff="$$(for goos in linux darwin; do GOOS=$$goos $(LINT) fmt --diff ./... 2>&1 || true; done)"; \
	files="$$(echo "$$diff" | grep '^diff ' | awk '{print $$NF}' | sed 's|^b/||' | sort -u || true)"; \
	if [ -n "$$files" ]; then \
	  echo "UNFORMATTED — $$(echo "$$files" | wc -l | tr -d ' ') file(s), run 'make fmt' (first 20):"; \
	  echo "$$files" | head -20; echo "--- first hunks:"; echo "$$diff" | grep -E '^(diff|[-+][^-+])' | head -30; exit 1; \
	elif [ -n "$$diff" ]; then \
	  echo "fmt-check: golangci-lint fmt did not run cleanly — NO file was checked:"; echo "$$diff" | tail -5; exit 2; \
	fi; \
	echo "fmt: clean (linux + darwin)"

lint: ## Every finding in the module — the burn-down view
	$(call need-tool,LINT)
	$(LINT) run ./...

# Inside the fence a linked worktree's .git names a host path; dev.sh iso hands
# the mounted git dir over as PFM_DEV_REPO_GIT_DIR, and golangci-lint's own
# `git diff` gets it through GIT_DIR — scoped to THIS command only: exported
# fence-wide it hijacks every `git init` the test suite runs.
FENCE_GIT := $(if $(PFM_DEV_REPO_GIT_DIR),GIT_DIR=$(PFM_DEV_REPO_GIT_DIR) GIT_WORK_TREE=$(PFM_DEV_REPO_WORK_TREE) GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=safe.directory GIT_CONFIG_VALUE_0=*,)
lint-new: ## Findings on lines changed since LINT_BASE (origin/develop) — the gate view
	$(call need-tool,LINT)
	$(FENCE_GIT) $(LINT) run --new-from-merge-base=$(LINT_BASE) ./...

vet: ## go vet the whole module
	go vet ./...

cover: ## Unit coverage (cross-package) + any e2e GOCOVERDIR run, merged and thresholded
	$(call need-tool,COVER)
	rm -rf "$(COVER_DIR)/unit" && mkdir -p "$(COVER_DIR)/unit"
	go test ./... -count=1 -coverpkg=./... -covermode=atomic -args -test.gocoverdir="$(COVER_DIR)/unit"
	@dirs="$(COVER_DIR)/unit"; \
	if ls "$(COVER_DIR)/e2e"/covmeta.* >/dev/null 2>&1; then dirs="$$dirs,$(COVER_DIR)/e2e"; fi; \
	echo "cover: merging $$dirs"; \
	go tool covdata textfmt -i="$$dirs" -o "$(COVER_DIR)/merged.out"
	$(COVER) --config=.testcoverage.yml --profile="$(COVER_DIR)/merged.out" \
	  --breakdown-file-name="$(COVER_DIR)/breakdown.json"

arch: ## Architecture ratchet: size ceilings, one façade per mechanism, no new twins (baselines in .arch/)
	bash scripts/arch-check.sh

# ----------------------------------------------------------------- gate ----
# Both delegate. dev.sh is the single definition of this project's gate: it
# runs `go test $(TESTFLAGS) -json ./...`, pipes the stream through
# `scripts/test-timing.sh --check` (its own "pfm: test timing (budget)" row —
# a run over pfm/.testtiming.yml's budgets is red, offender named), and the
# e2e row the same way. TESTFLAGS travels through the environment so this
# file stays a delegate, never a second definition of `go test`'s flags.

test: ## Host test suite (delegates to dev.sh; TESTFLAGS -> `go test`, piped through the timing check)
	cd "$(REPO)" && TESTFLAGS="$(TESTFLAGS)" bash .claude/scripts/dev.sh test pfm

testflags:
	@printf '%s\n' '$(TESTFLAGS)'

iso: ## Containerised test suite (delegates to dev.sh iso; same TESTFLAGS)
	cd "$(REPO)" && TESTFLAGS="$(TESTFLAGS)" bash .claude/scripts/dev.sh iso test pfm

timing: ## Print the most recently written suite timing TSV
	@python3 -c 'from pathlib import Path; import sys; files = [p for p in Path(sys.argv[1]).rglob("*.tsv") if p.name.startswith(("unit", "e2e"))]; p = max(files, key=lambda p: p.stat().st_mtime) if files else None; sys.exit("timing: no suite TSV — run the test gate first") if p is None else print(str(p) + "\n" + p.read_text(), end="")' "$(REPO)/tmp/timing"

sweep: ## Measure the fenced concurrency sweep and publish its report
	bash scripts/test-sweep.sh --preflight
	cd "$(REPO)" && bash .claude/scripts/dev.sh iso run 'bash pfm/scripts/test-sweep.sh --run --wait-quiet --label fence --out /pfm-timing/sweep-fence.tsv --json /pfm-timing/sweep-json'
	bash scripts/test-sweep.sh --report "$(REPO)/tmp/timing/sweep-fence.tsv"

gate: fmt-check lint-new vet arch test iso ## Everything a change must pass before it merges
	@echo "gate: all passed"

# -------------------------------------------------------------- install ----

install: ## Swap in a new build atomically, restart the daemon, then sweep stale processes — any failure rolls back and sweeps nothing
	@$(MAKE) --no-print-directory host-install
	@if ! $(MAKE) --no-print-directory mcp-restart; then \
	  echo "install: FAILED at the MCP restart — rolling back to $(BIN).prev; no process was touched" >&2; \
	  $(MAKE) --no-print-directory rollback; \
	  exit 1; \
	fi
	@$(MAKE) --no-print-directory sweep-stale
	@echo
	@echo "install: complete — binary swapped, MCP daemon restarted, stale processes swept."
	@# Order is the safety: nothing is killed until the new binary is in place
	@# AND the daemon is serving it. A failed build or smoke test changes
	@# nothing; a failed restart restores the previous binary. Only a fully
	@# successful install sweeps, so a failure never leaves the host with the
	@# old processes gone and no working replacement.

host-install: ## Build + smoke-test a new pfm beside ~/.local/bin/pfm, then swap it in atomically (previous kept as pfm.prev)
	@mkdir -p "$$(dirname "$(BIN)")"; \
	tmp="$$(mktemp "$(BIN).new.XXXXXX")"; \
	trap 'rm -f "$$tmp"' EXIT; \
	go build $(LDFLAGS) -o "$$tmp" ./cmd/pfm || { echo "host-install: build FAILED — $(BIN) untouched" >&2; exit 1; }; \
	reported="$$("$$tmp" --version 2>&1)" || { echo "host-install: smoke test FAILED ($$reported) — $(BIN) untouched" >&2; exit 1; }; \
	case "$$reported" in *"$(VERSION)"*) ;; \
	  *) echo "host-install: smoke test reported '$$reported', not $(VERSION) — $(BIN) untouched" >&2; exit 1;; esac; \
	kept=""; if [ -x "$(BIN)" ]; then cp -f "$(BIN)" "$(BIN).prev" && kept="; previous kept at $(BIN).prev"; fi; \
	mv -f "$$tmp" "$(BIN)"; \
	echo "installed: $(BIN) ($$reported$$kept)"
	@# Never write onto the running path directly: an executing binary gives
	@# ETXTBSY. The build lands BESIDE the target — same directory, same
	@# filesystem — so the final mv is a rename(2): atomic, never a
	@# copy-over-the-running-image. Processes already running keep their own
	@# image, which is exactly why the sweep below has to exist.

rollback: ## Restore ~/.local/bin/pfm from pfm.prev atomically and restart the daemon on it
	@[ -x "$(BIN).prev" ] || { echo "rollback: no $(BIN).prev to restore — $(BIN) left as is" >&2; exit 1; }
	@tmp="$$(mktemp "$(BIN).rollback.XXXXXX")"; \
	cp -f "$(BIN).prev" "$$tmp" && chmod 755 "$$tmp" && mv -f "$$tmp" "$(BIN)" \
	  || { rm -f "$$tmp"; echo "rollback: FAILED to restore $(BIN) from $(BIN).prev" >&2; exit 1; }; \
	echo "rollback: restored $(BIN) from $(BIN).prev"
	@$(MAKE) --no-print-directory mcp-restart \
	  || echo "rollback: the daemon did not come back on the restored binary either — make mcp-status" >&2

# ---------------------------------------------------------------- mcp ------

mcp-restart: ## Restart the MCP daemon so it serves the binary now on disk
	@if command -v systemctl >/dev/null 2>&1 && \
	    systemctl --user cat $(UNIT) >/dev/null 2>&1; then \
	  systemctl --user restart $(UNIT); \
	  sleep 1; \
	  state="$$(systemctl --user is-active $(UNIT) || true)"; \
	  if [ "$$state" != "active" ]; then \
	    echo "MCP-RESTART-FAILED: $(UNIT) is '$$state' after restart — the fleet has NO chat MCP server" >&2; \
	    exit 1; \
	  fi; \
	  pid="$$(systemctl --user show -p MainPID --value $(UNIT))"; \
	  echo "mcp: $(UNIT) restarted, pid $$pid"; \
	elif [ "$$(uname)" = Darwin ] && launchctl print "gui/$$(id -u)/$(AGENT)" >/dev/null 2>&1; then \
	  agent_pid() { launchctl print "gui/$$(id -u)/$(AGENT)" | awk '$$1 == "pid" && $$2 == "=" { print $$3; exit }'; }; \
	  old="$$(agent_pid)"; \
	  launchctl kickstart -k "gui/$$(id -u)/$(AGENT)"; \
	  pid=""; \
	  for _ in 1 2 3 4 5 6 7 8 9 10; do \
	    pid="$$(agent_pid)"; \
	    [ -n "$$pid" ] && [ "$$pid" != "$$old" ] && break; pid=""; sleep 0.5; \
	  done; \
	  if [ -z "$$pid" ]; then \
	    echo "MCP-RESTART-FAILED: $(AGENT) has no NEW pid 5s after kickstart (was $${old:-none}) — the fleet may have NO fresh chat MCP server" >&2; \
	    exit 1; \
	  fi; \
	  echo "mcp: $(AGENT) restarted, pid $$pid"; \
	elif pgrep -f 'pfm mcp serve' >/dev/null 2>&1; then \
	  echo "mcp: no $(UNIT) unit or $(AGENT) agent; killing unsupervised 'pfm mcp serve' so the next client starts a fresh one"; \
	  pkill -f 'pfm mcp serve'; \
	else \
	  echo "mcp: no daemon running and no $(UNIT) unit or $(AGENT) agent — nothing to restart"; \
	fi
	@# Restarting unconditionally is deliberate. Deciding whether the daemon is
	@# stale needs process introspection that not every platform offers, and a
	@# restart is cheap, supervised, and idempotent — so the guarantee does not
	@# depend on a probe that might not be able to run.

mcp-status: ## Report the daemon, then every pfm process still running a replaced binary
	@if command -v systemctl >/dev/null 2>&1 && \
	    systemctl --user cat $(UNIT) >/dev/null 2>&1; then \
	  echo "unit:  $$(systemctl --user is-active $(UNIT))"; \
	  echo "pid:   $$(systemctl --user show -p MainPID --value $(UNIT))"; \
	elif [ "$$(uname)" = Darwin ] && launchctl print "gui/$$(id -u)/$(AGENT)" >/dev/null 2>&1; then \
	  launchctl print "gui/$$(id -u)/$(AGENT)" | awk '($$1 == "state" || $$1 == "pid") && $$2 == "=" && !seen[$$1]++ { printf "%-6s %s\n", $$1 ":", $$3 }'; \
	else \
	  echo "unit:  not installed"; \
	  pgrep -af 'pfm mcp serve' || echo "no 'pfm mcp serve' process running"; \
	fi
	@"$(BIN)" internal stale --binary "$(BIN)"

# --------------------------------------------------------------- stale -----

stale: ## List pfm processes still running a replaced binary (Linux and macOS)
	@"$(BIN)" internal stale --binary "$(BIN)"
	@# One implementation, in Go (internal/stale): a process is stale when the
	@# file it executes — /proc/<pid>/exe on Linux, lsof's program entry on
	@# macOS — is not the file now at $(BIN), by device and inode. "none" and
	@# "could not look" print differently and the latter exits non-zero.

sweep-stale: ## Kill every pfm process still running a replaced binary, then prove none is left
	@"$(BIN)" internal stale --sweep --binary "$(BIN)"
	@# TERM, KILL after 3s, then a re-scan that fails loudly on a survivor. It
	@# signals only images that are not $(BIN), so the binary just installed —
	@# and the daemon restarted on it — are never touched.

clean: ## Remove local build artifacts
	rm -f $(LOCAL)
	@echo "clean: removed $(LOCAL)"
