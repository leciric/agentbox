#!/bin/sh
# bench.sh: how fast an agent's desktop shows GPU frames, run inside an agent
# with "GPU for agents" on. Starts its own Xvnc displays at 1920x1080, with
# and without the render node, and measures:
#   - a WebGL2 cube in Chromium, light (k=0) and with a heavy fragment shader
#     (k=64), with each GPU option and SwiftShader: the fps the page draws, and
#     how many different frames per second reach the screen (x11grab at 30
#     fps, as a recording sees it: 30 is the most it can tell);
#   - glmark2 on and off screen, GPU and llvmpipe (GLMARK=0 skips it).
# Each with no VNC client (as while recording), with an idle one that never
# asks for a frame (what browser.sh keeps connected), and, with VIEWER=1,
# with xtigervncviewer at the Desktop tab's settings (Tight, quality 2,
# compression 9: desktop/src/renderer/lib/vncSession.ts).
# Needs chromium, node, ffmpeg, bc, glmark2-x11 and, for VIEWER=1, tigervnc-viewer.
set -u
here=$(cd "$(dirname "$0")" && pwd)
node=$(find /dev/dri -name "renderD*" 2>/dev/null | sort | head -1)
[ -n "$node" ] || { echo "no render node: is GPU for agents on?" >&2; exit 1; }
secs=${SECS:-5}
nosync="vblank_mode=0 MESA_VK_WSI_PRESENT_MODE=immediate"

started=""
cleanup() { for p in $started; do kill "$p" 2>/dev/null; done; }
trap cleanup EXIT INT TERM
xvnc() { # xvnc <display> <port> [args]: start an Xvnc for this run
  d=$1 port=$2; shift 2
  Xvnc "$d" -geometry 1920x1080 -depth 24 -rfbport "$port" -localhost -SecurityTypes None "$@" >/dev/null 2>&1 &
  started="$started $!"
  sleep 2
}

cube() { # cube <display> <label> <k> <env> [chromium flags]
  d=$1 label=$2 k=$3 envs=$4; shift 4
  prof=$(mktemp -d)
  # shellcheck disable=SC2086 # envs is a list of assignments
  env DISPLAY="$d" $envs chromium --no-first-run --no-default-browser-check --password-store=basic \
    --disable-dev-shm-usage --user-data-dir="$prof" --remote-debugging-port=9333 --window-position=0,0 \
    --window-size=1920,1080 --kiosk "$@" "file://$here/cube.html?k=$k" >/dev/null 2>&1 &
  pid=$!
  out=$(timeout 60 node "$here/cube.mjs" 9333 "$secs" 2>/dev/null | tail -1)
  shown=$(timeout 30 ffmpeg -nostdin -loglevel info -f x11grab -framerate 30 -video_size 1920x1080 -t "$secs" -i "$d" \
    -vf mpdecimate=hi=64:lo=32:frac=0.1,showinfo -f null - 2>&1 | grep -c 'Parsed_showinfo.*n: *[0-9]')
  kill "$pid" 2>/dev/null; wait "$pid" 2>/dev/null; rm -rf "$prof" 2>/dev/null
  fps=$(printf %s "$out" | sed -n 's/.*"fps":\([0-9.]*\).*/\1/p')
  printf '%-4s %-5s %-50s page %5.1f fps, screen %4.1f fps\n' "$d" "k=$k" "$label" "${fps:-0}" "$(echo "$shown / $secs" | bc -l)"
}

glm() { # glm <display> <label> <env> [glmark2 args]
  d=$1 label=$2 envs=$3; shift 3
  # shellcheck disable=SC2086
  score=$(timeout 300 env DISPLAY="$d" $envs glmark2 -b build -b texture -b shading:shading=phong -b refract "$@" 2>&1 |
    sed -n 's/.*glmark2 Score: \([0-9]*\).*/\1/p')
  printf '%-4s %-56s %6s\n' "$d" "glmark2 $label" "${score:-failed}"
}

run() { # run <display>
  d=$1
  for k in 0 64; do
    cube "$d" "ANGLE GL (default)" "$k" "" --enable-gpu-rasterization
    cube "$d" "ANGLE GL, vblank_mode=0" "$k" "$nosync" --enable-gpu-rasterization
    cube "$d" "ANGLE GL, --disable-gpu-vsync" "$k" "" --enable-gpu-rasterization --disable-gpu-vsync
    cube "$d" "ANGLE Vulkan (Venus)" "$k" "" --enable-gpu-rasterization --use-angle=vulkan
    cube "$d" "ANGLE Vulkan, MESA_VK_WSI_PRESENT_MODE=immediate" "$k" "$nosync" --enable-gpu-rasterization --use-angle=vulkan
    cube "$d" "SwiftShader" "$k" "" --use-angle=swiftshader --enable-unsafe-swiftshader
  done
  if [ "${GLMARK:-1}" = 1 ]; then
    glm "$d" "on screen, GPU" ""
    glm "$d" "on screen, GPU, vblank_mode=0" "$nosync"
    glm "$d" "off screen, GPU" "" --off-screen
    glm "$d" "on screen, llvmpipe" "LIBGL_ALWAYS_SOFTWARE=1"
    glm "$d" "off screen, llvmpipe" "LIBGL_ALWAYS_SOFTWARE=1" --off-screen
  fi
}

xvnc :97 5997 -rendernode "$node"
echo "render node $node: $(glxinfo -B -display :97 2>/dev/null | sed -n 's/OpenGL renderer string: //p')"
xvnc :96 5996
echo "== no VNC client; :97 has -rendernode, :96 hasn't"
run :97
GLMARK=0 run :96
echo "== an idle VNC client on :97 (browser.sh's agentbox-vnc-clock)"
bash -c 'exec 3<>/dev/tcp/127.0.0.1/5997 && printf "RFB 003.008\n\001\001" >&3 && cat <&3 >/dev/null' &
started="$started $!"
sleep 1
run :97
if [ "${VIEWER:-0}" = 1 ]; then
  xvnc :95 5995
  DISPLAY=:95 xtigervncviewer -Shared -RemoteResize=0 -AutoSelect=0 -PreferredEncoding=Tight -QualityLevel=2 -CompressLevel=9 localhost::5997 >/dev/null 2>&1 &
  started="$started $!"
  sleep 2
  echo "== xtigervncviewer connected to :97 as the Desktop tab (and the idle client)"
  GLMARK=0 run :97
fi
