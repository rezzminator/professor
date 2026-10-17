#!/usr/bin/env bash
# engine-sync.sh — sourced: ONE definition of how a fence container gets the
# engine-sync binary, the single writer of every Codex and OpenCode artifact
# (plugins/engine-sync/docs/interface.md).
#
#   . "$REPO_ROOT/infra/fence/engine-sync.sh"
#   engine_sync_build <out>         # build ./cmd/engine-sync from the source onto <out>
#   engine_sync_link_stable <bin>   # the plugin wrapper's stable link onto <bin>
#
# The source is $ENGINE_SYNC_FENCE_SRC, default /engine-sync: fence-env.sh
# resolves it on the host and docker-compose.yml and lanes/container.sh mount it
# read-only there; go writes nothing beside it. The binary lands under a
# temporary name in <out>'s directory and is renamed onto <out>, so a gate step
# never execs a half-written file another step is still building. A caller
# exports ENGINE_SYNC_BIN=<out> for the pfm it runs.
#
# BROKEN STATE: an absent source prints `engine-sync: source unavailable:
# {cause}` (fence-env.sh's PFM_DEV_ENGINE_SYNC_MISSING, or the mount without a
# go.mod) and returns 1 with go never run; a failed build prints go's output and
# `engine-sync: build FAILED from {source} (go exit N)`, removes its temporary
# file and returns 1. Neither leaves a binary at <out>.
#
# engine_sync_link_stable makes the link the engine-sync plugin wrapper makes at
# a Claude session start: {data}/bin/engine-sync -> <bin>, {data} resolved as pfm's
# enginesync.DataHome does ($ENGINE_SYNC_HOME, else $XDG_DATA_HOME/engine-sync,
# else $HOME/.local/share/engine-sync). `pfm install` links ~/.local/bin/engine-sync
# to that stable path and skips the PATH link while it is absent, so a container
# that runs no Claude session calls this before `pfm install`. BROKEN STATE:
# `engine-sync: cannot link {stable} to {bin}`, return 1.

engine_sync_build() { # engine_sync_build <out>
  local out="$1" src="${ENGINE_SYNC_FENCE_SRC:-/engine-sync}" tmp rc=0
  if [ -n "${PFM_DEV_ENGINE_SYNC_MISSING:-}" ]; then
    echo "engine-sync: source unavailable: $PFM_DEV_ENGINE_SYNC_MISSING" >&2
    return 1
  fi
  if [ ! -f "$src/go.mod" ]; then
    echo "engine-sync: source unavailable: $src has no go.mod" >&2
    return 1
  fi
  mkdir -p "$(dirname "$out")" || { echo "engine-sync: cannot create $(dirname "$out")" >&2; return 1; }
  tmp="$(mktemp "$out.XXXXXX")" || { echo "engine-sync: cannot create a temporary file beside $out" >&2; return 1; }
  GOTOOLCHAIN=local go -C "$src" build -o "$tmp" ./cmd/engine-sync || rc=$?
  if [ "$rc" -ne 0 ]; then
    rm -f "$tmp"
    echo "engine-sync: build FAILED from $src (go exit $rc)" >&2
    return 1
  fi
  mv -f "$tmp" "$out" || { rm -f "$tmp"; echo "engine-sync: cannot move the build onto $out" >&2; return 1; }
}

engine_sync_link_stable() { # engine_sync_link_stable <bin>
  local bin="$1" data
  if [[ "${ENGINE_SYNC_HOME:-}" == /* ]]; then data="$ENGINE_SYNC_HOME"
  elif [[ "${XDG_DATA_HOME:-}" == /* ]]; then data="$XDG_DATA_HOME/engine-sync"
  else data="$HOME/.local/share/engine-sync"; fi
  { mkdir -p "$data/bin" && ln -sfn "$bin" "$data/bin/engine-sync"; } ||
    { echo "engine-sync: cannot link $data/bin/engine-sync to $bin" >&2; return 1; }
}
