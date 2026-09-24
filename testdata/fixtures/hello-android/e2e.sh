#!/bin/sh
# An end-to-end check of Remedy on the running emulator, driven with adb: adds a
# medication and checks that the list shows it.
# Usage: ./e2e.sh [medication name]
set -eu
name=${1:-Amoxicillin}
app=dev.agentbox.remedy

dump() { adb shell uiautomator dump /sdcard/ui.xml >/dev/null 2>&1 && adb shell cat /sdcard/ui.xml; }
# center prints the middle of the first element matching a pattern.
center() {
  dump | tr '>' '\n' | grep -m1 "$1" |
    sed -n 's/.*bounds="\[\([0-9]*\),\([0-9]*\)\]\[\([0-9]*\),\([0-9]*\)\]".*/\1 \2 \3 \4/p' |
    awk '{ print int(($1 + $3) / 2), int(($2 + $4) / 2) }'
}

adb shell am start -W -n "$app/.MainActivity" >/dev/null
field=$(center 'content-desc="Medication name"')
[ -n "$field" ] || { echo "FAIL: Remedy shows no medication field"; exit 1; }
adb shell input tap $field
# Typing only lands once the keyboard is connected to the field.
i=0
until adb shell dumpsys input_method | grep -q 'mServedView=.*EditText'; do
  i=$((i + 1))
  [ "$i" -lt 20 ] || break
  sleep 0.5
done
adb shell input text "$(printf '%s' "$name" | sed 's/ /%s/g')"
adb shell input tap $(center 'content-desc="Add medication"')
sleep 1
if dump | grep -q "text=\"$name\""; then
  echo "PASS: $name is in the list"
else
  echo "FAIL: $name isn't in the list"
  exit 1
fi
