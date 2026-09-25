#!/usr/bin/env bash
# record.sh — renders one section of the README deck (deck.html) to a card for
# README.md: a still, docs/img/cards/<section>.png, or with --motion the
# section's scene as a looping animated WebP, docs/img/cards/<section>.webp.
#
#   infra/readme-cards/record.sh <section-id> [output.png]
#   infra/readme-cards/record.sh --motion [SECS=24] <section-id> [output.webp]
#   FPS=8 QUALITY=70 infra/readme-cards/record.sh --motion 16 fleet   (a canvas scene: lighter)
#
# The deck is the source: every README card is one <section id="…"> of it,
# rendered by headless Chrome with the deck's own fonts, every other section
# hidden. A still runs the deck in still mode (window.__still — no scene plays,
# the section rests in its opening state) at 2× and is cropped to its ink; a
# motion card lets the scene play and screenshots the section at 12 fps for
# SECS seconds (capture.mjs, DevTools protocol; window.__motion tells a scene
# that loops on the page to play once and hold), then folds the frames that did
# not change into pauses and writes a looping animated WebP (Pillow). A design change is a re-run,
# never a re-crop.
#
# Runs OUTSIDE the command sandbox: Chrome fetches the deck's Google Fonts and
# writes the frames; sandboxed, it renders fallback fonts or nothing. Needs
# Google Chrome, Pillow, and for --motion node ≥ 22 (its own WebSocket).
#
# BROKEN STATE: a missing tool or an unknown section id is named (exit 1)
# before Chrome starts; a frame Chrome did not write, a still that is blank
# (nothing but background), or a motion run with fewer frames than one second
# is named and nothing lands in docs/img.
set -euo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
HERE="$ROOT/infra/readme-cards"
PROJECT="$(basename "$ROOT")"; PROJECT="${PROJECT#.}"
WORK="/tmp/$PROJECT/readme-cards"
MOTION=0 SECS=24 FPS="${FPS:-10}" QUALITY="${QUALITY:-78}" # FPS / QUALITY from the environment: a canvas scene (every frame distinct) wants fewer, lighter frames
if [ "${1:-}" = --motion ]; then
  MOTION=1; shift
  case "${1:-}" in ''|*[!0-9]*) ;; *) SECS="$1"; shift ;; esac
fi
SECTION="${1:-}"
[ -n "$SECTION" ] || { echo "usage: record.sh [--motion [SECS]] <section-id> [output]" >&2; exit 2; }
if [ "$MOTION" -eq 1 ]; then OUT="${2:-$ROOT/docs/img/cards/$SECTION.webp}"; else OUT="${2:-$ROOT/docs/img/cards/$SECTION.png}"; fi

missing() { echo "readme-cards: TOOLCHAIN-MISSING — $1" >&2; exit 1; }
CHROME="/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
for c in google-chrome chromium "$CHROME"; do command -v "$c" >/dev/null 2>&1 && { CHROME="$c"; break; }; done
[ -x "$CHROME" ] || command -v "$CHROME" >/dev/null || missing "Google Chrome (or chromium on PATH)"
python3 -c 'import PIL' 2>/dev/null || missing "Pillow (python3 -m pip install pillow)"
[ "$MOTION" -eq 0 ] || command -v node >/dev/null || missing "node (capture.mjs drives Chrome over DevTools)"
grep -q "id=\"$SECTION\"" "$HERE/deck.html" || { echo "readme-cards: no <section id=\"$SECTION\"> in deck.html — ids: $(grep -oE '<section[^>]* id="[^"]+"' "$HERE/deck.html" | sed -E 's/.*id="([^"]+)"/\1/' | tr '\n' ' ')" >&2; exit 1; }

mkdir -p "$WORK" "$(dirname "$OUT")"
STILL="$WORK/$SECTION.html"
# The still page: the deck with the recorder's flag first, then a style that
# leaves only the wanted section (the deck's fixed slide nav hidden too) — at
# the same directory depth as deck.html, so the deck's relative image paths
# (../../docs/img) still resolve.
{
  if [ "$MOTION" -eq 1 ]; then printf '<script>window.__motion = true;</script>\n'; else printf '<script>window.__still = true;</script>\n'; fi
  printf '<style>.wrap > *:not(#%s), body > *:not(.wrap):not(style):not(script):not(link) { display: none !important; } .wrap { padding-block: 24px; gap: 0; } .btn.rp, .ctl.rp { display: none !important; }</style>\n' "$SECTION"
  cat "$HERE/deck.html"
} > "$STILL"
if [ "$MOTION" -eq 1 ]; then
  FRAMES="$WORK/$SECTION.frames"
  rm -rf "$FRAMES"; mkdir -p "$FRAMES"
  node "$HERE/capture.mjs" "$CHROME" "$STILL" "$SECTION" "$FRAMES" "$SECS" "$FPS" 1
  n="$(ls "$FRAMES" | wc -l | tr -d ' ')"
  [ "$n" -ge "$FPS" ] || { echo "readme-cards: only $n frame(s) captured for $SECTION — under one second, no card written" >&2; exit 1; }
  # Frames that did not change are folded into the previous one's duration
  # (the pauses), and the run is written as one looping animated WebP.
  python3 - "$FRAMES" "$OUT" "$FPS" "$QUALITY" <<'EOF2'
import sys, os
from PIL import Image, ImageChops
frames_dir, out, fps, quality = sys.argv[1], sys.argv[2], int(sys.argv[3]), int(sys.argv[4])
names = sorted(n for n in os.listdir(frames_dir) if n.endswith(".png"))
# Frames differ in height when the scene grows the section (capture.mjs measures
# per frame): every frame is set on a canvas of the tallest, in the deck's ground.
sizes = [Image.open(os.path.join(frames_dir, n)).size for n in names]
W, H = max(w for w, _ in sizes), max(h for _, h in sizes)
kept, durations, prev = [], [], None
for n in names:
    im = Image.new("RGB", (W, H), (14, 23, 20))  # --ground #0e1714
    im.paste(Image.open(os.path.join(frames_dir, n)).convert("RGB"), (0, 0))
    if prev is not None and ImageChops.difference(im, prev).getbbox() is None:
        durations[-1] += 1000 // fps
        continue
    kept.append(im); durations.append(1000 // fps); prev = im
if not kept:
    sys.exit("readme-cards: no frames to encode")
kept[0].save(out, save_all=True, append_images=kept[1:], duration=durations, loop=0, quality=quality, method=6)
print(f"readme-cards: {len(names)} frames, {len(kept)} distinct, {sum(durations) / 1000:.1f} s")
EOF2
  echo "readme-cards: $OUT $(du -h "$OUT" | cut -f1) · $n frames over $SECS s"
  exit 0
fi
RAW="$WORK/$SECTION.raw.png"
rm -f "$RAW"
"$CHROME" --headless=new --disable-gpu --hide-scrollbars --force-device-scale-factor=2 \
  --window-size=1400,4000 --virtual-time-budget=5000 --screenshot="$RAW" "file://$STILL" >/dev/null 2>&1 || true
[ -s "$RAW" ] || { echo "readme-cards: Chrome wrote no frame for $SECTION ($RAW) — run outside the sandbox" >&2; exit 1; }
# Crop to the ink: everything that is not the deck's ground colour, plus a margin.
python3 - "$RAW" "$OUT" <<'EOF'
import sys
from PIL import Image, ImageChops
raw, out = sys.argv[1], sys.argv[2]
im = Image.open(raw).convert("RGB")
ground = Image.new("RGB", im.size, (14, 23, 20))  # --ground #0e1714
box = ImageChops.difference(im, ground).point(lambda v: 255 if v > 12 else 0).convert("L").getbbox()
if not box:
    sys.exit("readme-cards: the frame is blank — nothing but background (did the section render?)")
pad = 32
l, t, r, b = box
crop = im.crop((max(0, l - pad), max(0, t - pad), min(im.width, r + pad), min(im.height, b + pad)))
crop.save(out, optimize=True)
print(f"readme-cards: {out} {crop.width}x{crop.height} ({crop.width // 2}x{crop.height // 2} @1x)")
EOF
