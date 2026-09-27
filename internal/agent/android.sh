#!/bin/sh
# Runs an agent's Android emulator:
# - an SDK home the agent can write to, linking to the SDK that AgentBox shares
#   read-only at /opt/android-sdk, so builds can add what they need;
# - a Pixel 7 AVD on the chosen system image, in ~/.android;
# - the emulator, without a window, on KVM;
# - a display with a VNC server (TigerVNC's Xvnc, on 127.0.0.1:5901) showing
#   the device full-screen through scrcpy, which also passes input to it.
# Runs as the agent's user. AgentBox reaches the VNC port through an Incus
# proxy device, so nothing listens on the agent's network.
# Usage: android.sh link | start <image> <memory MB> <cores> [gpu mode] | wait | view | status | stop
#        android.sh screenshot <file> | logcat <since seconds> [package] | install <apk> | record <file> <seconds>
set -eu

shared=/opt/android-sdk
export ANDROID_HOME="$HOME/.local/share/android-sdk"
export ANDROID_SDK_ROOT="$ANDROID_HOME"
export ANDROID_AVD_HOME="$HOME/.android/avd"
export PATH="$ANDROID_HOME/platform-tools:$ANDROID_HOME/emulator:$PATH"
export ADB="$ANDROID_HOME/platform-tools/adb"
scrcpy=/opt/scrcpy/scrcpy
display=98
avd=agentbox
state="$HOME/.local/state/agentbox"
uid=$(id -u)
mkdir -p "$state" "$ANDROID_AVD_HOME"

emulator_running() { pgrep -u "$uid" -f qemu-system-x86_64 >/dev/null; }
booted() { [ "$(adb shell getprop sys.boot_completed 2>/dev/null | tr -d '\r')" = 1 ]; }
viewing() { pgrep -u "$uid" -f "scrcpy .*--window-title=agentbox-android" >/dev/null; }
# scrcpy's server runs on the device once a client has connected. Two clients
# starting at once push the same server and break each other, so each waits.
device_server() { adb shell pgrep -f com.genymobile.scrcpy >/dev/null 2>&1; }
wait_for_device_server() {
  i=0
  until device_server; do
    i=$((i + 1))
    [ "$i" -lt 100 ] || return 0
    sleep 0.1
  done
}
listening() { ss -ltn "sport = :$1" | grep -q LISTEN; }
wait_for() {
  i=0
  until "$@"; do
    i=$((i + 1))
    [ "$i" -lt 150 ] || return 1
    sleep 0.1
  done
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
  image=$1 memory=$2 cores=$3 gpu_mode=${4:-swiftshader_indirect}
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

start_display() {
  if ! pgrep -u "$uid" -f "Xvnc :$display " >/dev/null; then
    # A snapshot taken while the display ran keeps its lock files.
    rm -f "/tmp/.X$display-lock" "/tmp/.X11-unix/X$display"
    setsid Xvnc ":$display" -geometry 540x1200 -depth 24 -desktop android \
      -rfbport 5901 -localhost -SecurityTypes None -AlwaysShared >"$state/android-display.log" 2>&1 </dev/null &
    wait_for test -e "/tmp/.X11-unix/X$display" || { echo "the Android display didn't start: see $state/android-display.log" >&2; exit 1; }
    wait_for listening 5901 || { echo "the Android display's VNC server didn't start: see $state/android-display.log" >&2; exit 1; }
  fi
  if ! pgrep -u "$uid" -f "matchbox-window-manager -display :$display" >/dev/null; then
    setsid matchbox-window-manager -display ":$display" -use_titlebar no >"$state/android-wm.log" 2>&1 </dev/null &
  fi
}

# view shows the device on the display, full-screen, with input.
view() {
  # scrcpy is optional in the base image (image.Options), so an agent on an
  # image built without it says so rather than failing as a missing command.
  [ -x "$scrcpy" ] || {
    echo "this agent's machine has no scrcpy, so its emulator can't be shown: rebuild the base image with it (agentbox image build --android), then create the agent again" >&2
    exit 1
  }
  require_booted
  start_display
  viewing && return
  DISPLAY=":$display" setsid "$scrcpy" --no-audio --max-fps=30 --stay-awake --window-borderless \
    --window-title=agentbox-android >"$state/scrcpy.log" 2>&1 </dev/null &
  wait_for viewing || { echo "scrcpy didn't start: see $state/scrcpy.log" >&2; exit 1; }
  wait_for_device_server
}

case "${1:-}" in
link)
  link
  ;;
start)
  [ $# -ge 4 ] && [ $# -le 5 ] || { echo "usage: android.sh start <image> <memory MB> <cores> [gpu mode]" >&2; exit 2; }
  gpu_mode=${5:-swiftshader_indirect}
  link
  if ! emulator_running; then
    write_avd "$2" "$3" "$4" "$gpu_mode"
    # A snapshot taken while the emulator ran keeps its locks.
    rm -f "$ANDROID_AVD_HOME/$avd.avd"/*.lock
    setsid "$ANDROID_HOME/emulator/emulator" -avd "$avd" -no-window -gpu "$gpu_mode" -no-audio -no-snapshot \
      -no-boot-anim -no-metrics >"$state/emulator.log" 2>&1 </dev/null &
  fi
  start_display
  adb start-server >/dev/null 2>&1 || true
  ;;
wait)
  i=0
  until emulator_running && booted; do
    emulator_running || { echo "the emulator stopped while starting:" >&2; tail -n 15 "$state/emulator.log" >&2; exit 1; }
    i=$((i + 1))
    [ "$i" -lt 300 ] || { echo "Android didn't finish starting within 5 minutes: see $state/emulator.log" >&2; exit 1; }
    sleep 1
  done
  view
  ;;
view)
  view
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
  viewing && shown=1
  echo "running=$running"
  echo "booted=$up"
  echo "view=$shown"
  echo "image=$(cat "$state/android-image" 2>/dev/null || true)"
  echo "release=$release"
  ;;
stop)
  pkill -u "$uid" -f "scrcpy .*--window-title=agentbox-android" || true
  if emulator_running; then
    adb emu kill >/dev/null 2>&1 || true
    i=0
    while emulator_running && [ "$i" -lt 100 ]; do
      sleep 0.2
      i=$((i + 1))
    done
    pkill -u "$uid" -f qemu-system-x86_64 || true
  fi
  pkill -u "$uid" -f "matchbox-window-manager -display :$display" || true
  pkill -u "$uid" -f "Xvnc :$display " || true
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
  if viewing; then wait_for_device_server; fi
  [ -x "$scrcpy" ] || { echo "this agent's machine has no scrcpy to record with: rebuild the base image with agentbox image build --android" >&2; exit 1; }
  exec "$scrcpy" --no-window --no-audio --no-control --max-fps=30 --record="$2" --record-format=mp4 --time-limit="$3"
  ;;
*)
  echo "usage: android.sh link|start|wait|view|status|stop|screenshot|logcat|install|record" >&2
  exit 2
  ;;
esac
