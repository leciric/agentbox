#!/bin/sh
# Runs an agent's Android emulator:
# - an SDK home the agent can write to, linking to the SDK that AgentBox shares
#   read-only at /opt/android-sdk, so builds can add what they need;
# - a Pixel 7 AVD on the chosen system image, in ~/.android;
# - the emulator, without a window, on KVM, rendering on the agent's GPU when
#   it has one and in software (SwiftShader) otherwise;
# - the device's screen as H.264 video, and input into it, served on
#   127.0.0.1:5902 by `agentbox android serve` (internal/devicestream), which
#   reads the screen from the emulator's gRPC endpoint and encodes it with
#   ffmpeg.
# Runs as the agent's user. AgentBox reaches the stream's port through an
# Incus proxy device, so nothing listens on the agent's network.
# Usage: android.sh link | start <image> <memory MB> <cores> [auto|host|swiftshader_indirect] | wait | stream | status | stop
#        android.sh screenshot <file> | logcat <since seconds> [package] | install <apk> | record <file> <seconds>
set -eu

shared=/opt/android-sdk
export ANDROID_HOME="$HOME/.local/share/android-sdk"
export ANDROID_SDK_ROOT="$ANDROID_HOME"
export ANDROID_AVD_HOME="$HOME/.android/avd"
export PATH="$ANDROID_HOME/platform-tools:$ANDROID_HOME/emulator:$PATH"
export ADB="$ANDROID_HOME/platform-tools/adb"
agentbox=/usr/local/bin/agentbox
stream_port=5902
display=98
avd=agentbox
state="$HOME/.local/state/agentbox"
uid=$(id -u)
mkdir -p "$state" "$ANDROID_AVD_HOME"

emulator_running() { pgrep -u "$uid" -f qemu-system-x86_64 >/dev/null; }
booted() { [ "$(adb shell getprop sys.boot_completed 2>/dev/null | tr -d '\r')" = 1 ]; }
streaming() { pgrep -u "$uid" -f "$agentbox android serve" >/dev/null; }
listening() { ss -ltn "sport = :$1" | grep -q LISTEN; }
wait_for() {
  i=0
  until "$@"; do
    i=$((i + 1))
    [ "$i" -lt 150 ] || return 1
    sleep 0.1
  done
}
# render_node is the GPU's DRM render node, when "GPU for agents" gave the
# agent one (gpu.go), found as browser.sh finds it. WSL's GPU (/dev/dxg) has
# none, and the emulator can't use it: it stays on SwiftShader there.
render_node() {
  for node in /dev/dri/renderD*; do
    if [ -c "$node" ] && [ -r "$node" ] && [ -w "$node" ]; then
      echo "$node"
      return
    fi
  done
}
# gpu_failure is the emulator log's line saying it can't render on the GPU,
# if there is one. The emulator either exits ("Failed to find memory type for
# ColorBuffers", FATAL) or, with no renderer, keeps running and never boots
# ("Could not start renderer").
gpu_failure() {
  grep -E '^(ERROR|FATAL) *\| *(Could not start renderer|Failed to initialize GL emulation)|^FATAL' "$state/emulator.log" 2>/dev/null |
    head -n 1 | sed 's/^[A-Z]* *| *//'
}
require_booted() {
  emulator_running && booted || { echo "the emulator isn't running: start it with agentbox android start" >&2; exit 1; }
}

# link fills the writable SDK home with links into the shared SDK. Versioned
# packages are linked one by one, so a build can install others next to them.
link() {
  [ -d "$shared/emulator" ] || { echo "the Android SDK isn't shared with this agent: start the emulator with agentbox android start" >&2; exit 1; }
  mkdir -p "$ANDROID_HOME"
  for entry in "$shared"/*; do
    name=$(basename "$entry")
    case "$name" in
    licenses)
      mkdir -p "$ANDROID_HOME/licenses"
      for f in "$entry"/*; do
        [ -e "$ANDROID_HOME/licenses/$(basename "$f")" ] || cp "$f" "$ANDROID_HOME/licenses/"
      done
      ;;
    system-images)
      for image in "$entry"/*/*/*; do
        [ -d "$image" ] || continue
        rel=${image#"$shared"/}
        mkdir -p "$ANDROID_HOME/$(dirname "$rel")"
        [ -e "$ANDROID_HOME/$rel" ] || ln -s "$image" "$ANDROID_HOME/$rel"
      done
      ;;
    platforms | build-tools | ndk | cmake | sources | add-ons | extras | cmdline-tools)
      mkdir -p "$ANDROID_HOME/$name"
      for child in "$entry"/*; do
        [ -e "$child" ] || continue
        [ -e "$ANDROID_HOME/$name/$(basename "$child")" ] || ln -s "$child" "$ANDROID_HOME/$name/"
      done
      ;;
    *)
      [ -e "$ANDROID_HOME/$name" ] || ln -s "$entry" "$ANDROID_HOME/$name"
      ;;
    esac
  done
}

write_avd() {
  image=$1 memory=$2 cores=$3 gpu_mode=$4
  sysdir=$(printf '%s' "$image" | tr ';' '/')
  [ -f "$ANDROID_HOME/$sysdir/system.img" ] || { echo "$image isn't installed in the shared Android SDK" >&2; exit 1; }
  platform=$(printf '%s' "$image" | cut -d';' -f2)
  api=$(printf '%s' "$platform" | sed 's/^android-//; s/\..*//')
  tag=$(printf '%s' "$image" | cut -d';' -f3)
  playstore=no
  [ "$tag" != google_apis_playstore ] || playstore=yes
  dir="$ANDROID_AVD_HOME/$avd.avd"
  # Data from another system image doesn't boot.
  if [ -f "$dir/config.ini" ] && ! grep -qx "image.sysdir.1=$sysdir/" "$dir/config.ini"; then
    rm -rf "$dir"
  fi
  mkdir -p "$dir"
  cat >"$ANDROID_AVD_HOME/$avd.ini" <<EOF
avd.ini.encoding=UTF-8
path=$dir
path.rel=avd/$avd.avd
target=android-$api
EOF
  cat >"$dir/config.ini" <<EOF
avd.ini.encoding=UTF-8
AvdId=$avd
avd.ini.displayname=AgentBox Pixel 7
PlayStore.enabled=$playstore
abi.type=x86_64
hw.cpu.arch=x86_64
hw.cpu.ncore=$cores
hw.ramSize=${memory}M
vm.heapSize=512M
hw.device.manufacturer=Google
hw.device.name=pixel_7
hw.lcd.width=1080
hw.lcd.height=2400
hw.lcd.density=420
hw.initialOrientation=portrait
hw.keyboard=yes
hw.mainKeys=no
hw.gpu.enabled=yes
hw.gpu.mode=$gpu_mode
hw.audioInput=no
hw.audioOutput=no
hw.camera.back=none
hw.camera.front=none
hw.sdCard=no
disk.dataPartition.size=6G
image.sysdir.1=$sysdir/
tag.id=$tag
target=android-$api
showDeviceFrame=no
fastboot.forceColdBoot=yes
EOF
  printf '%s\n' "$image" >"$state/android-image"
}

# start_display starts an X display for the emulator to render through on
# the GPU (-gpu host), which nobody looks at: on the GPU's render node, Xvnc
# gives its clients GLX on the GPU. Its VNC server only listens on a socket of
# the agent's user, as it can't listen on nothing. On SwiftShader the emulator
# needs no display.
start_display() {
  pgrep -u "$uid" -f "Xvnc :$display " >/dev/null && return
  # A snapshot taken while the display ran keeps its lock files.
  rm -f "/tmp/.X$display-lock" "/tmp/.X11-unix/X$display" "$state/android-display.rfb"
  node=$(render_node)
  setsid Xvnc ":$display" -geometry 64x64 -depth 24 -desktop android -rfbport -1 \
    -rfbunixpath "$state/android-display.rfb" -SecurityTypes None ${node:+-rendernode $node} >"$state/android-display.log" 2>&1 </dev/null &
  wait_for test -e "/tmp/.X11-unix/X$display" || { echo "the Android display didn't start: see $state/android-display.log" >&2; exit 1; }
}

stop_display() {
  pkill -u "$uid" -f "Xvnc :$display " || true
}

# launch starts the emulator, with the AVD's config.ini written, on gpu mode
# host or swiftshader_indirect. On host its renderer draws through the
# display's GLX; gfxstream's Vulkan fails on Venus, the GPU of AgentBox's VM
# ("Failed to find memory type for ColorBuffers"), so the device gets OpenGL
# ES on the GPU and no Vulkan.
launch() {
  sed -i "s/^hw.gpu.mode=.*/hw.gpu.mode=$1/" "$ANDROID_AVD_HOME/$avd.avd/config.ini"
  # A snapshot taken while the emulator ran keeps its locks.
  rm -f "$ANDROID_AVD_HOME/$avd.avd"/*.lock
  printf '%s\n' "$1" >"$state/android-gpu"
  if [ "$1" = host ]; then
    DISPLAY=":$display" setsid "$ANDROID_HOME/emulator/emulator" -avd "$avd" -no-window -gpu host -feature -Vulkan \
      -no-audio -no-snapshot -no-boot-anim -no-metrics >"$state/emulator.log" 2>&1 </dev/null &
  else
    setsid "$ANDROID_HOME/emulator/emulator" -avd "$avd" -no-window -gpu "$1" -no-audio -no-snapshot \
      -no-boot-anim -no-metrics >"$state/emulator.log" 2>&1 </dev/null &
  fi
  # wait takes an emulator that isn't running as one that stopped.
  wait_for emulator_running || true
}

# fall_back restarts on SwiftShader, which needs no display, an emulator that
# couldn't render on the GPU, keeping why for status.
fall_back() {
  printf '%s\n' "$1" >"$state/android-gpu-problem"
  cp "$state/emulator.log" "$state/emulator-gpu.log"
  pkill -u "$uid" -f qemu-system-x86_64 || true
  i=0
  while emulator_running && [ "$i" -lt 50 ]; do
    sleep 0.2
    i=$((i + 1))
  done
  pkill -9 -u "$uid" -f qemu-system-x86_64 || true
  stop_display
  launch swiftshader_indirect
}

# stream serves the device's screen and input (agentbox android serve).
stream() {
  require_booted
  streaming && return
  setsid "$agentbox" android serve --listen "127.0.0.1:$stream_port" >"$state/android-stream.log" 2>&1 </dev/null &
  wait_for listening "$stream_port" || { echo "the emulator's video stream didn't start: see $state/android-stream.log" >&2; exit 1; }
}

case "${1:-}" in
link)
  link
  ;;
start)
  [ $# -ge 4 ] && [ $# -le 5 ] || { echo "usage: android.sh start <image> <memory MB> <cores> [auto|host|swiftshader_indirect]" >&2; exit 2; }
  gpu_mode=${5:-auto}
  if [ "$gpu_mode" = auto ]; then
    gpu_mode=swiftshader_indirect
    [ -z "$(render_node)" ] || gpu_mode=host
  fi
  link
  if ! emulator_running; then
    # The emulator draws through the display on the GPU, so it starts first.
    [ "$gpu_mode" != host ] || start_display
    write_avd "$2" "$3" "$4" "$gpu_mode"
    rm -f "$state/android-gpu-problem"
    launch "$gpu_mode"
  fi
  adb start-server >/dev/null 2>&1 || true
  ;;
wait)
  i=0
  until emulator_running && booted; do
    if [ "$(cat "$state/android-gpu" 2>/dev/null)" = host ] && failure=$(gpu_failure) && [ -n "$failure" ]; then
      fall_back "$failure"
      i=0
      continue
    fi
    emulator_running || { echo "the emulator stopped while starting:" >&2; tail -n 15 "$state/emulator.log" >&2; exit 1; }
    i=$((i + 1))
    [ "$i" -lt 300 ] || { echo "Android didn't finish starting within 5 minutes: see $state/emulator.log" >&2; exit 1; }
    sleep 1
  done
  stream
  ;;
stream)
  stream
  ;;
status)
  running=0 up=0 shown=0 release=
  if emulator_running; then
    running=1
    if booted; then
      up=1
      release=$(adb shell getprop ro.build.version.release 2>/dev/null | tr -d '\r')
    fi
  fi
  streaming && shown=1
  echo "running=$running"
  echo "booted=$up"
  echo "stream=$shown"
  echo "image=$(cat "$state/android-image" 2>/dev/null || true)"
  echo "release=$release"
  echo "gpu=$(cat "$state/android-gpu" 2>/dev/null || true)"
  echo "gpu_problem=$(cat "$state/android-gpu-problem" 2>/dev/null || true)"
  ;;
stop)
  pkill -u "$uid" -f "$agentbox android serve" || true
  # Earlier AgentBoxes showed the device with scrcpy on a window manager.
  pkill -u "$uid" -f "scrcpy .*--window-title=agentbox-android" || true
  pkill -u "$uid" -f "matchbox-window-manager -display :$display" || true
  if emulator_running; then
    adb emu kill >/dev/null 2>&1 || true
    i=0
    while emulator_running && [ "$i" -lt 100 ]; do
      sleep 0.2
      i=$((i + 1))
    done
    pkill -u "$uid" -f qemu-system-x86_64 || true
  fi
  stop_display
  ;;
screenshot)
  require_booted
  adb exec-out screencap -p >"$2"
  ;;
logcat)
  require_booted
  since=$(($(date +%s) - $2))
  if [ -n "${3:-}" ]; then
    app_uid=$(adb shell pm list packages -U "$3" 2>/dev/null | tr -d '\r' | grep -m1 "^package:$3 " | sed 's/.*uid://')
    [ -n "$app_uid" ] || { echo "$3 isn't installed on the emulator" >&2; exit 1; }
    adb logcat -d -v threadtime -T "$since.000" --uid="$app_uid"
  else
    adb logcat -d -v threadtime -T "$since.000"
  fi
  ;;
install)
  require_booted
  adb install -r "$2"
  ;;
record)
  require_booted
  # Stopped with SIGTERM, it finishes the file.
  exec "$agentbox" android capture "$2" "$3"
  ;;
*)
  echo "usage: android.sh link|start|wait|stream|status|stop|screenshot|logcat|install|record" >&2
  exit 2
  ;;
esac
