#!/usr/bin/env bash
# Renders the app's icons in assets/ from AgentBox's logo, the desktop app's
# (desktop/build/icon.svg): lucide's Box in white on the violet gradient.
#
#   icon.png                     iOS, 1024², full bleed: iOS rounds it itself
#   android-icon-background.png  the adaptive icon's back layer: the gradient
#   android-icon-foreground.png  its front layer: the box, inside the 66 dp
#                                safe zone of the 108 dp canvas, so no
#                                launcher's mask cuts it
#   android-icon-monochrome.png  the same box, for Android 13's themed icons
#   favicon.png                  the web build's, the desktop's icon as is
#
# Needs rsvg-convert (librsvg2-bin).
set -euo pipefail
cd "$(dirname "$0")/.."

# The desktop's gradient and shine, and its box (lucide's, on a 24 unit grid).
gradient='<linearGradient id="bg" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="#a78bfa"/><stop offset="0.55" stop-color="#7c3aed"/><stop offset="1" stop-color="#4f46e5"/></linearGradient><linearGradient id="shine" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="#ffffff" stop-opacity="0.28"/><stop offset="0.5" stop-color="#ffffff" stop-opacity="0"/></linearGradient>'
box='<path d="M21 8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16Z"/><path d="m3.3 7 8.7 5 8.7-5"/><path d="M12 22V12"/>'

# glyph SIZE SCALE: the box centred on a SIZE square, SCALE pixels a unit.
glyph() {
  local t; t=$(awk -v s="$1" -v k="$2" 'BEGIN { printf "%.2f", s / 2 - 12 * k }')
  echo "<g transform=\"translate($t $t) scale($2)\" fill=\"none\" stroke=\"#ffffff\" stroke-width=\"1.9\" stroke-linecap=\"round\" stroke-linejoin=\"round\">$box</g>"
}

render() { rsvg-convert -w "$2" -h "$2" -o "assets/$1" /dev/stdin; }

# The box takes 48% of the tile, as on the desktop's (225 of 464 px).
render icon.png 1024 <<SVG
<svg xmlns="http://www.w3.org/2000/svg" width="1024" height="1024"><defs>$gradient</defs><rect width="1024" height="1024" fill="url(#bg)"/><rect width="1024" height="1024" fill="url(#shine)"/>$(glyph 1024 27.3)</svg>
SVG

# 432 px is 108 dp at 4x. A launcher shows about the middle 72 dp (288 px),
# where the box takes 48% again.
render android-icon-background.png 432 <<SVG
<svg xmlns="http://www.w3.org/2000/svg" width="432" height="432"><defs>$gradient</defs><rect width="432" height="432" fill="url(#bg)"/><rect width="432" height="432" fill="url(#shine)"/></svg>
SVG
for layer in foreground monochrome; do
  render "android-icon-$layer.png" 432 <<SVG
<svg xmlns="http://www.w3.org/2000/svg" width="432" height="432">$(glyph 432 7.7)</svg>
SVG
done

rsvg-convert -w 48 -h 48 -o assets/favicon.png ../desktop/build/icon.svg
