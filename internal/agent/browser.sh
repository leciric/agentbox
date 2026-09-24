#!/bin/sh
# Runs an agent's browser:
# - a display with a VNC server built in (TigerVNC's Xvnc, on 127.0.0.1:5900),
#   which resizes when the viewer asks, and shares the clipboard;
# - a small desktop on it: the AgentBox wallpaper, a big mouse cursor, openbox
#   (window manager), tint2 as a centred dock (launchers, the window list and a
#   clock) and pcmanfm and a terminal for the agent to browse files or run
#   commands from the display itself;
# - Chromium, maximized, with the DevTools protocol on 127.0.0.1:9222.
# Runs as the agent's user. AgentBox reaches both ports through Incus proxy
# devices, so nothing listens on the agent's network.
# Usage: browser.sh start|stop|theme
set -eu

display=99
export DISPLAY=":$display"
state="$HOME/.local/state/agentbox"
profile="$HOME/.config/agentbox/browser"
config="$HOME/.config"
uid=$(id -u)
mkdir -p "$state" "$profile"

# What the agentbox binary writes into the agent before running this script
# (wallpaper.go, theme.go). AGENTBOX_SHARE is the seam the Go tests write a
# palette through; in an agent it is never set.
share=${AGENTBOX_SHARE:-/usr/local/share/agentbox}

# The wallpaper travels with the binary; a missing one just leaves the solid
# colour.
wallpaper="$share/wallpaper.png"

# The desktop's colours arrive the same way (theme.go): AgentBox's own by
# default, and the host's Omarchy theme when it is following one. The values
# below are what the desktop wears when the file isn't there at all, which is
# every agent whose binary predates it.
theme_name=agentbox
theme_mode=dark
theme_background="#0b0b11"
theme_surface="#16161f"
theme_foreground="#e9e7f5"
theme_muted="#8b8ba7"
theme_accent="#8b5cf6"

# read_theme takes the file apart by hand instead of sourcing it: a colour
# should not be able to become a command, however well the side that writes
# the file checks it. Only these keys are read, and only a six-digit hex
# colour is taken.
read_theme() {
  [ -f "$share/theme.conf" ] || return 0
  while IFS='=' read -r key value; do
    case "$value" in
    '#'[0-9a-fA-F][0-9a-fA-F][0-9a-fA-F][0-9a-fA-F][0-9a-fA-F][0-9a-fA-F]) ;;
    *) case "$key" in theme_name | theme_mode) ;; *) continue ;; esac ;;
    esac
    case "$key" in
    theme_name) theme_name=$value ;;
    theme_mode) theme_mode=$value ;;
    theme_background) theme_background=$value ;;
    theme_surface) theme_surface=$value ;;
    theme_foreground) theme_foreground=$value ;;
    theme_muted) theme_muted=$value ;;
    theme_accent) theme_accent=$value ;;
    esac
  done <"$share/theme.conf"
}
read_theme
backdrop="$theme_background"

# X's default cursor is 16 px, which is nearly invisible on a scaled-down view
# of the display and in a recording. Exporting these before anything starts is
# what makes Chromium, openbox, the dock and the file manager agree on one big
# themed cursor.
cursor_theme=Adwaita
cursor_size=48
export XCURSOR_THEME="$cursor_theme" XCURSOR_SIZE="$cursor_size"

devtools() { curl -sf --max-time 1 "http://127.0.0.1:9222/json/version" >/dev/null; }
listening() { ss -ltn "sport = :$1" | grep -q LISTEN; }
display_answering() { xdpyinfo -display "$DISPLAY" >/dev/null 2>&1; }
wait_for() {
  i=0
  until "$@"; do
    i=$((i + 1))
    [ "$i" -lt 150 ] || return 1
    sleep 0.1
  done
}

# write_config writes its input to $1, unless the file there has been changed
# since this script last wrote it: a newer browser.sh replaces its own config,
# an agent that edits one keeps it. The checksum beside the file tells the two
# apart; a config older than the checksum (one an earlier browser.sh wrote) is
# replaced too, with a .bak of it left behind.
#
# It sets wrote_config to 1 when the file's contents actually changed, which is
# what tells apply_theme whether anything on the display has to be told. A
# config the agent has taken over never changes, so following the host's theme
# can't undo an agent's own edit: the new colours wait in the checksum-less
# file the agent is now responsible for.
write_config() {
  target=$1
  sum="$target.agentbox"
  wrote_config=0
  mkdir -p "$(dirname "$target")"
  if [ -e "$target" ] && [ -f "$sum" ] && [ "$(cksum <"$target")" != "$(cat "$sum")" ]; then
    cat >/dev/null # the agent has made this config its own
    return 0
  fi
  if [ -e "$target" ] && [ ! -f "$sum" ]; then
    cp "$target" "$target.bak"
  fi
  before=""
  [ -f "$target" ] && before=$(cksum <"$target")
  cat >"$target"
  cksum <"$target" >"$sum"
  [ "$(cat "$sum")" = "$before" ] || wrote_config=1
}

# set_background paints the solid colour first, so a display the viewer has
# resized past the picture keeps a dark edge rather than X's grey weave.
set_background() {
  xsetroot -solid "$backdrop" || true
  [ -f "$wallpaper" ] || return 0
  if command -v xwallpaper >/dev/null 2>&1; then
    xwallpaper --zoom "$wallpaper" || true
  elif command -v hsetroot >/dev/null 2>&1; then
    hsetroot -fill "$wallpaper" || true
  fi
}

# set_cursor sizes the pointer. The Xresources are for the toolkits that read
# them; xsetroot -cursor_name goes through libXcursor, which honours
# XCURSOR_THEME and XCURSOR_SIZE, so the root window's own pointer grows too.
set_cursor() {
  printf 'Xcursor.theme: %s\nXcursor.size: %s\n' "$cursor_theme" "$cursor_size" | xrdb -merge || true
  xsetroot -cursor_name left_ptr || true
}

# GTK's own settings, for what doesn't read the X resources: Chromium takes its
# cursor theme and size from here, not from XCURSOR_SIZE, and without this its
# pointer stays the small default over every page. Written only if the agent
# has no settings of its own. No dark theme here on purpose: Chromium maps
# GTK's dark preference onto prefers-color-scheme, which would quietly render
# every page an agent looks at in its dark mode.
write_gtk_config() {
  if [ -e "$config/gtk-3.0/settings.ini" ]; then return 0; fi
  write_config "$config/gtk-3.0/settings.ini" <<EOF
[Settings]
gtk-icon-theme-name = Adwaita
gtk-cursor-theme-name = $cursor_theme
gtk-cursor-theme-size = $cursor_size
EOF
}

# The window decorations. openbox takes its colours from a theme directory
# rather than from rc.xml, so following a palette means writing one: a themerc
# under ~/.themes, which is plain key: value text and goes through write_config
# like every other config here. An agent that edits it keeps it, and openbox
# falls back to its own defaults for any key a themerc leaves out, so a broken
# one costs the colours and not the window manager.
write_openbox_theme() {
  write_config "$HOME/.themes/AgentBox/openbox-3/themerc" <<EOF
border.width: 1
border.color: $theme_accent
padding.width: 6
padding.height: 4
window.handle.width: 0
window.client.padding.width: 0
window.label.text.justify: center

window.active.border.color: $theme_accent
window.active.title.bg: flat solid
window.active.title.bg.color: $theme_surface
window.active.label.bg: parentrelative
window.active.label.text.color: $theme_foreground
window.active.handle.bg: flat solid
window.active.handle.bg.color: $theme_surface
window.active.button.unpressed.image.color: $theme_foreground
window.active.button.hover.image.color: $theme_accent
window.active.button.pressed.image.color: $theme_accent

window.inactive.border.color: $theme_surface
window.inactive.title.bg: flat solid
window.inactive.title.bg.color: $theme_background
window.inactive.label.bg: parentrelative
window.inactive.label.text.color: $theme_muted
window.inactive.handle.bg: flat solid
window.inactive.handle.bg.color: $theme_background
window.inactive.button.unpressed.image.color: $theme_muted
window.inactive.button.hover.image.color: $theme_foreground

menu.border.color: $theme_accent
menu.title.bg: flat solid
menu.title.bg.color: $theme_background
menu.title.text.color: $theme_foreground
menu.items.bg: flat solid
menu.items.bg.color: $theme_surface
menu.items.text.color: $theme_foreground
menu.items.disabled.text.color: $theme_muted
menu.items.active.bg: flat solid
menu.items.active.bg.color: $theme_accent
menu.items.active.text.color: $theme_background

osd.border.color: $theme_accent
osd.bg: flat solid
osd.bg.color: $theme_surface
osd.label.bg: parentrelative
osd.label.text.color: $theme_foreground
EOF
}

write_openbox_config() {
  write_config "$config/openbox/rc.xml" <<'EOF'
<?xml version="1.0" encoding="UTF-8"?>
<openbox_config xmlns="http://openbox.org/3.4/rc">
<theme>
  <name>AgentBox</name>
  <titleLayout>NLIMC</titleLayout>
  <keepBorder>yes</keepBorder>
</theme>
<desktops><number>1</number></desktops>
<mouse>
  <context name="Titlebar">
    <mousebind button="Left" action="Drag"><action name="Move"/></mousebind>
    <mousebind button="Left" action="DoubleClick"><action name="ToggleMaximize"/></mousebind>
  </context>
  <context name="Maximize">
    <mousebind button="Left" action="Click"><action name="ToggleMaximize"/></mousebind>
  </context>
</mouse>
</openbox_config>
EOF
}

# A dock rather than a bar: panel_shrink makes tint2 only as wide as what it
# holds, and bottom center floats it over the wallpaper. Its height and
# bottom margin add up to panelHeight in media.go, which places the recording's
# key overlay just above it — change them together.
write_tint2_config() {
  write_config "$config/tint2/tint2rc" <<EOF
panel_items = LTSC
panel_size = 100% 44
panel_shrink = 1
panel_margin = 0 8
panel_padding = 8 4 8
panel_background_id = 1
panel_dock = 0
panel_position = bottom center horizontal
panel_layer = normal
panel_monitor = all

# 1: the dock itself. 2: what the pointer is over. 3: the active window.
rounded = 16
border_width = 1
border_sides = TBLR
background_color = $theme_surface 82
border_color = $theme_accent 28

rounded = 10
border_width = 0
background_color = $theme_foreground 12
border_color = $theme_foreground 0

rounded = 10
border_width = 1
border_sides = TBLR
background_color = $theme_accent 45
border_color = $theme_accent 90

launcher_padding = 2 0 8
launcher_icon_size = 32
launcher_icon_theme = Adwaita
launcher_tooltip = 1
launcher_item_app = chromium.desktop
launcher_item_app = pcmanfm.desktop
launcher_item_app = xfce4-terminal.desktop

taskbar_mode = single_desktop
taskbar_padding = 4 0 6
taskbar_background_id = 0
task_text = 1
task_icon = 1
task_centered = 1
task_padding = 6 2 6
task_maximum_size = 150 32
task_font = sans 9
task_font_color = $theme_foreground 100
task_background_id = 0
task_active_background_id = 3
task_mouse_over_background_id = 2

time1_format = %H:%M
time1_font = sans bold 10
clock_font_color = $theme_foreground 100
clock_padding = 8 0
clock_background_id = 0

systray_padding = 4 0 4
systray_background_id = 0

mouse_middle = none
mouse_right = close
EOF
}

# apply_theme writes every config the desktop's colours reach, and leaves
# theme_changed at 1 when any of them is new — which it is after a theme
# change on the host, and after a browser.sh that writes them differently.
apply_theme() {
  theme_changed=0
  for writer in write_openbox_theme write_openbox_config write_tint2_config; do
    "$writer"
    [ "$wrote_config" = 0 ] || theme_changed=1
  done
}

# reload_desktop tells what is already on the display about the new configs.
# openbox rereads its theme on --reconfigure; tint2 has no such thing, so it is
# restarted, which costs the dock a blink and nothing else.
reload_desktop() {
  [ "$theme_changed" = 1 ] || return 0
  if pgrep -u "$uid" -f "openbox --config-file $config/openbox/rc.xml" >/dev/null; then
    openbox --reconfigure || true
  fi
  if pgrep -u "$uid" -f "tint2 -c $config/tint2/tint2rc" >/dev/null; then
    pkill -u "$uid" -f "tint2 -c $config/tint2/tint2rc" || true
    setsid tint2 -c "$config/tint2/tint2rc" >"$state/panel.log" 2>&1 </dev/null &
  fi
}

case "${1:-}" in
start)
  # Match this display's processes only: the Android emulator has a display too.
  # pgrep can miss a live Xvnc started with a different argument order or by
  # something other than this script, so also ask the display itself before
  # deciding it's dead and clearing its files out from under it.
  if ! pgrep -u "$uid" -f "Xvnc $DISPLAY " >/dev/null && ! display_answering; then
    # A snapshot taken while the display ran keeps its lock files.
    rm -f "/tmp/.X$display-lock" "/tmp/.X11-unix/X$display"
    setsid Xvnc "$DISPLAY" -geometry 1440x900 -depth 24 -desktop agentbox \
      -rfbport 5900 -localhost -SecurityTypes None -AlwaysShared >"$state/display.log" 2>&1 </dev/null &
    wait_for test -e "/tmp/.X11-unix/X$display" || { echo "the display didn't start: see $state/display.log" >&2; exit 1; }
    wait_for listening 5900 || { echo "the VNC server didn't start: see $state/display.log" >&2; exit 1; }
  fi
  # Not only on a fresh display: a start after the binary shipped a new
  # wallpaper is what puts it up.
  set_cursor
  set_background
  write_gtk_config
  # Written on every start, not only a fresh one: this is what carries a new
  # palette, or a newer browser.sh's own configs, into an agent whose desktop
  # has been up since before either.
  apply_theme
  if ! pgrep -u "$uid" -f "openbox --config-file $config/openbox/rc.xml" >/dev/null; then
    setsid openbox --config-file "$config/openbox/rc.xml" >"$state/window-manager.log" 2>&1 </dev/null &
  fi
  if ! pgrep -u "$uid" -f "tint2 -c $config/tint2/tint2rc" >/dev/null; then
    setsid tint2 -c "$config/tint2/tint2rc" >"$state/panel.log" 2>&1 </dev/null &
  else
    reload_desktop
  fi
  if ! devtools; then
    # Same for the lock of a browser that was running.
    rm -f "$profile"/Singleton*
    setsid chromium --no-first-run --no-default-browser-check --disable-dev-shm-usage --password-store=basic \
      --user-data-dir="$profile" --remote-debugging-port=9222 --start-maximized about:blank >"$state/chromium.log" 2>&1 </dev/null &
    wait_for devtools || { echo "the browser didn't start: see $state/chromium.log" >&2; tail -n 5 "$state/chromium.log" >&2; exit 1; }
  fi
  ;;
theme)
  # Repaint a desktop that is already up, after the host's theme changed. On
  # an agent with no display it writes the configs for the next start and says
  # nothing: a theme change shouldn't start anything that wasn't running.
  apply_theme
  display_answering || exit 0
  set_background
  reload_desktop
  ;;
stop)
  pkill -u "$uid" -x chromium || true
  pkill -u "$uid" -x pcmanfm || true
  pkill -u "$uid" -x xfce4-terminal || true
  pkill -u "$uid" -f "tint2 -c $config/tint2/tint2rc" || true
  pkill -u "$uid" -f "openbox --config-file $config/openbox/rc.xml" || true
  pkill -u "$uid" -f "Xvnc $DISPLAY " || true
  ;;
*)
  echo "usage: browser.sh start|stop|theme" >&2
  exit 2
  ;;
esac
