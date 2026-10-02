#!/usr/bin/env bash
# Sourced by the fence's host-side callers. The image label is the freshness
# record; a different builder cannot leave a matching record by accident.

_fence_image_error() {
  printf 'fence image: INPUTS-UNDERIVABLE %s: %s\n' "$1" "$2" >&2
  return 2
}

_fence_image_derive() { # <compose-file> <service>; sets key and image name
  local compose="$1" service="$2" config block line field context dockerfile
  local platform hashes hash path mode entries tree dockerfile_hash key_output
  local -a sha_cmd

  if ! config="$(PFM_DEV_INPUTS_KEY= docker compose -f "$compose" config "$service" 2>&1)"; then
    _fence_image_error "$service" "$config"; return 2
  fi
  _FENCE_IMAGE_NAME="$(sed -n 's/^    image: //p' <<<"$config" | head -1)"
  [ -n "$_FENCE_IMAGE_NAME" ] || { _fence_image_error "$service" 'missing image'; return 2; }
  block="$(awk '/^    build:$/ { inside=1; print; next } inside && /^    [^ ]/ { exit } inside { print }' <<<"$config")"
  context="$(sed -n 's/^      context: //p' <<<"$block" | head -1)"
  [ -n "$context" ] || { _fence_image_error "$service" 'missing build.context'; return 2; }
  while IFS= read -r line; do
    case "$line" in
      '      '[a-zA-Z]*:*)
        field="${line#      }"; field="${field%%:*}"
        case "$field" in context|dockerfile|target|args|labels) ;; *) _fence_image_error "$service" "unsupported build field $field"; return 2 ;; esac ;;
    esac
  done <<<"$block"
  dockerfile="$(sed -n 's/^      dockerfile: //p' <<<"$block" | head -1)"
  [ -n "$dockerfile" ] || { _fence_image_error "$service" 'missing build.dockerfile'; return 2; }
  [ -d "$context" ] || { _fence_image_error "$service" "context not found: $context"; return 2; }
  [ -f "$context/$dockerfile" ] || { _fence_image_error "$service" "dockerfile not found: $dockerfile"; return 2; }
  if command -v sha256sum >/dev/null 2>&1; then sha_cmd=(sha256sum)
  elif command -v shasum >/dev/null 2>&1; then sha_cmd=(shasum -a 256)
  else _fence_image_error "$service" 'TOOLCHAIN-MISSING — neither sha256sum nor shasum'; return 2; fi

  if ! hashes="$(cd "$context" && find . -type f -print0 | LC_ALL=C sort -z | xargs -0 "${sha_cmd[@]}")"; then
    _fence_image_error "$service" 'context file hashing failed'; return 2
  fi
  entries=''; dockerfile_hash=''
  while IFS= read -r line; do
    [ -n "$line" ] || continue
    hash="${line%% *}"; path="${line#*  }"; path="${path#./}"
    [ -f "$context/$path" ] || { _fence_image_error "$service" "unreadable context file: $path"; return 2; }
    mode='-'
    [ -z "$(find "$context/$path" -maxdepth 0 -perm -u+x -print)" ] || mode=x
    entries+="${path}"$'\t'"f $path $hash $mode"$'\n'
    [ "$path" != "$dockerfile" ] || dockerfile_hash="$hash"
  done <<<"$hashes"
  [ -n "$dockerfile_hash" ] || { _fence_image_error "$service" "dockerfile not in context: $dockerfile"; return 2; }
  while IFS= read -r -d '' path; do
    path="${path#./}"
    if [ -d "$context/$path" ] && [ ! -L "$context/$path" ]; then
      [ -n "$path" ] || path='.'
      entries+="${path}"$'\t'"d $path"$'\n'
    else
      entries+="${path}"$'\t'"l $path $(readlink "$context/$path")"$'\n'
    fi
  done < <(cd "$context" && find . \( -type d -o -type l \) -print0 | LC_ALL=C sort -z)
  tree="$(printf '%s' "$entries" | LC_ALL=C sort -t $'\t' -k1,1 | cut -f2-)"
  platform="${DOCKER_DEFAULT_PLATFORM:-}"
  if ! key_output="$(
    { printf 'pfm.fence.inputs v1\nservice %s\n' "$service"
      while IFS= read -r line; do case "$line" in '      context: '*) ;; *) printf '%s\n' "$line" ;; esac; done <<<"$block"
      printf 'platform %s\ndockerfile %s\n%s\n' "$platform" "$dockerfile_hash" "$tree"
    } | "${sha_cmd[@]}"
  )"; then
    _fence_image_error "$service" 'manifest hashing failed'; return 2
  fi
  _FENCE_IMAGE_KEY="${key_output%% *}"
  [[ "$_FENCE_IMAGE_KEY" =~ ^[0-9a-f]{64}$ ]] || { _fence_image_error "$service" 'invalid sha256 result'; return 2; }
}

fence_image_key() {
  _fence_image_derive "$1" "$2" || return 2
  printf '%s\n' "$_FENCE_IMAGE_KEY"
}

fence_image_prepare() {
  local label error_file error_line
  unset PFM_DEV_INPUTS_KEY FENCE_IMAGE_BUILD
  _fence_image_derive "$1" "$2" || return 2
  export PFM_DEV_INPUTS_KEY="$_FENCE_IMAGE_KEY"
  FENCE_IMAGE_BUILD=()
  error_file="$(mktemp)" || { unset PFM_DEV_INPUTS_KEY FENCE_IMAGE_BUILD; _fence_image_error "$2" 'cannot capture image inspect'; return 2; }
  if label="$(docker image inspect --format '{{index .Config.Labels "pfm.fence.inputs"}}' "$_FENCE_IMAGE_NAME" 2>"$error_file")"; then
    rm -f "$error_file"
    if [ "$label" = "$_FENCE_IMAGE_KEY" ]; then
      printf 'fence image: %s current (inputs %s)\n' "$_FENCE_IMAGE_NAME" "${_FENCE_IMAGE_KEY:0:12}" >&2
      return 0
    fi
    [ -n "$label" ] && [ "$label" != '<no value>' ] || label=unkeyed
    FENCE_IMAGE_BUILD=(--build)
    printf 'fence image: %s rebuilds — inputs %s -> %s\n' "$_FENCE_IMAGE_NAME" "${label:0:12}" "${_FENCE_IMAGE_KEY:0:12}" >&2
  else
    IFS= read -r error_line <"$error_file" || true
    rm -f "$error_file"
    FENCE_IMAGE_BUILD=(--build)
    printf 'fence image: %s rebuilds — %s\n' "$_FENCE_IMAGE_NAME" "$error_line" >&2
  fi
}
