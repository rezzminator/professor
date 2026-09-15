#!/usr/bin/env bash
# record.sh — re-records the README hero GIF (docs/img/pfm-fleet.gif).
#
#   infra/readme-gif/record.sh [--keep] [output.gif]
#
# Starts the dev fence container on THIS checkout (read-only mount, own HOME),
# runs fleet.sh inside it, records fleet.tape to frame layers with VHS, draws
# the window chrome (chrome.py), and encodes in two stages. Work files land in
# tmp/readme-gif/, including four verification stills the caller must read.
# --keep leaves the container running for re-takes (re-run fleet.sh is not
# needed; re-run this script's recording half by passing --keep again).
#
# Runs OUTSIDE the command sandbox: VHS drives headless Chrome, ttyd, and the
# Docker socket, and a sandboxed VHS exits 0 having written nothing.
#
# BROKEN STATE: a missing tool is TOOLCHAIN-MISSING (exit 1) before anything
# starts; VHS writing no frames, an empty GIF, or a still that fails to extract
# each fails loudly — a run that cannot show its stills produced no GIF.
set -euo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
HERE="$ROOT/infra/readme-gif"
WORK="$ROOT/tmp/readme-gif"
NAME=pfm-readme-gif
KEEP=0
OUT="$ROOT/docs/img/pfm-fleet.gif"
for arg in "$@"; do
  case "$arg" in
    --keep) KEEP=1 ;;
    -*) echo "usage: record.sh [--keep] [output.gif]" >&2; exit 2 ;;
    *) OUT="$arg" ;;
  esac
done

missing() { echo "readme-gif: TOOLCHAIN-MISSING — $1" >&2; exit 1; }
command -v docker >/dev/null || missing "docker"
docker info >/dev/null 2>&1 || missing "the docker daemon is not reachable ('docker info' failed)"
command -v vhs >/dev/null || missing "vhs (brew install vhs)"
command -v ffmpeg >/dev/null || missing "ffmpeg"
command -v ffprobe >/dev/null || missing "ffprobe"
python3 -c 'import PIL' 2>/dev/null || missing "python3 with Pillow"
if ! compgen -G "$HOME/Library/Fonts/FiraCodeNerdFontMono-*" >/dev/null &&
   ! find /usr/share/fonts "$HOME/.local/share/fonts" -name 'FiraCodeNerdFontMono-*' -print -quit 2>/dev/null | grep -q .; then
  echo "readme-gif: WARNING — FiraCode Nerd Font Mono not found; VHS falls back to its default font and the prompt's powerline glyphs render as boxes" >&2
fi

# 1. The fence, exactly as dev.sh iso mounts it.
git_common="$(git -C "$ROOT" rev-parse --git-common-dir)"
[[ "$git_common" == /* ]] || git_common="$ROOT/$git_common"
git_common="$(cd "$git_common" && pwd -P)"
git_dir="$(cd "$(git -C "$ROOT" rev-parse --absolute-git-dir)" && pwd -P)"
case "$git_dir" in
  "$git_common") git_dir_rel="." ;;
  "$git_common"/*) git_dir_rel="${git_dir#"$git_common"/}" ;;
  *) echo "readme-gif: git dir $git_dir is outside common dir $git_common" >&2; exit 1 ;;
esac
export PFM_DEV_WORKTREE="$ROOT" PFM_DEV_GIT_COMMON="$git_common" PFM_DEV_GIT_DIR_REL="$git_dir_rel"
if ! docker ps --format '{{.Names}}' | grep -qx "$NAME"; then
  docker rm -f "$NAME" >/dev/null 2>&1 || true
  docker compose -f "$ROOT/infra/docker-compose.yml" run -d --build --name "$NAME" pfm-dev sleep infinity >/dev/null
  docker exec -w /tmp "$NAME" bash /worktree/infra/readme-gif/fleet.sh
else
  echo "readme-gif: reusing running container $NAME (fleet already up)"
fi

# 2. Record frame layers.
mkdir -p "$WORK"
rm -rf "$WORK/frames"
(cd "$WORK" && vhs "$HERE/fleet.tape" >/dev/null)
frames=$(find "$WORK/frames" -name 'frame-text-*.png' 2>/dev/null | wc -l | tr -d ' ')
[ "$frames" -gt 0 ] || { echo "readme-gif: VHS wrote no frames — was it run inside the command sandbox?" >&2; exit 1; }

# 3. Chrome, composite, encode. Two stages: error-diffusion dithering over the
#    frosted glass's color range runs for tens of minutes; ffv1 + bayer takes seconds.
read -r W H < <(python3 -c "from PIL import Image; print(*Image.open('$WORK/frames/frame-text-00001.png').size)")
read -r X Y < <(python3 "$HERE/chrome.py" "$W" "$H" "pfm — /work/webapp" "$WORK/chrome.png")
(cd "$WORK/frames" && ffmpeg -hide_banner -loglevel error -y \
  -framerate 50 -i frame-text-%05d.png -framerate 50 -i frame-cursor-%05d.png -i ../chrome.png \
  -filter_complex "[0][1]overlay=format=auto,format=rgba,colorkey=0x1e1e2e:0.02:0.06[term];[2]loop=loop=-1:size=1:start=0,setpts=N/50/TB[bg];[bg][term]overlay=$X:$Y:shortest=1,fps=25,scale=1200:-1:flags=lanczos" \
  -c:v ffv1 ../composite.mkv)
ffmpeg -hide_banner -loglevel error -y -i "$WORK/composite.mkv" -vf "palettegen=max_colors=256:stats_mode=full" "$WORK/palette.png"
ffmpeg -hide_banner -loglevel error -y -i "$WORK/composite.mkv" -i "$WORK/palette.png" \
  -lavfi "paletteuse=dither=bayer:bayer_scale=3:diff_mode=rectangle" "$OUT"
[ -s "$OUT" ] || { echo "readme-gif: encode produced an empty $OUT" >&2; exit 1; }

# 4. Verification stills: prompt, picker, Limits, cosmos (fractions of the take).
duration=$(ffprobe -v error -show_entries format=duration -of csv=p=0 "$OUT")
for spec in prompt:0.04 picker:0.25 limits:0.66 cosmos:0.88; do
  label=${spec%%:*}
  at=$(python3 -c "print(round($duration * ${spec#*:}, 2))")
  ffmpeg -hide_banner -loglevel error -y -ss "$at" -i "$OUT" -frames:v 1 "$WORK/still-$label.png"
  [ -s "$WORK/still-$label.png" ] || { echo "readme-gif: still $label at ${at}s failed to extract" >&2; exit 1; }
done

if [ "$KEEP" -eq 0 ]; then docker rm -f "$NAME" >/dev/null; fi
printf 'gif: %s (%s, %ss)\n' "$OUT" "$(du -h "$OUT" | cut -f1 | tr -d ' ')" "$duration"
printf 'still: %s\n' "$WORK"/still-{prompt,picker,limits,cosmos}.png
[ "$KEEP" -eq 1 ] && echo "container: $NAME kept (docker rm -f $NAME to tear down)"
exit 0
