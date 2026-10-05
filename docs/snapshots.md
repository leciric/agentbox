# SnapShots

A SnapShot sends whatever window you are looking at to a project's chat, or to one of its agents,
as a bug report: the picture, the window's app and title, a summary of its accessibility tree when
it has one, and a note you write.

## Taking one

- **In the desktop app**, press <kbd>Ctrl</kbd>+<kbd>Alt</kbd>+<kbd>S</kbd> (<kbd>Cmd</kbd>+<kbd>Alt</kbd>+<kbd>S</kbd>
  on a Mac) anywhere, with the app running. On Wayland the compositor may ask once whether to allow
  the shortcut.
- **From a key bind of your desktop**, run `agentbox snap`. On Hyprland (Omarchy included), in
  `~/.config/hypr/hyprland.conf` (Omarchy: `~/.config/hypr/bindings.conf`):

  ```
  bind = SUPER SHIFT, S, exec, agentbox snap
  ```

  Sway: `bindsym $mod+Shift+s exec agentbox snap`. GNOME and KDE: add a custom shortcut running
  `agentbox snap` in the keyboard settings.

The AgentBox window then comes forward with a composer: pick the project and where it goes (the
project's chat, or an agent), say what's wrong, choose whether the accessibility tree goes along,
and send. <kbd>Ctrl</kbd>+<kbd>Enter</kbd> sends. Discard drops it. A capture nobody sends is
forgotten after an hour, and is never written to disk until it is sent.

`agentbox snap --full` captures the whole screen, `--project <name>` starts the composer on a
project, `--no-accessibility` leaves the tree out, and `--save shot.png` only saves the picture.

## What it captures where

| Desktop | Window | Tools it needs |
| --- | --- | --- |
| Hyprland | the active window (`hyprctl activewindow`) | `grim` |
| Sway | the focused window (`swaymsg -t get_tree`) | `grim` |
| KDE Plasma | the active window | `spectacle` |
| GNOME | the active window where GNOME still allows it, else the screen | `gnome-screenshot` |
| X11 | the active window (`xdotool`) | `import` (ImageMagick) or `maim` |
| Mac | the screen, with the frontmost app and window's names | built in |

Anywhere else, or when the window can't be had, it captures the whole screen with the first of
`grim`, `gnome-screenshot`, `spectacle`, `import` or `maim` it finds.

## The accessibility tree

On Linux, SnapShots read the window's tree over AT-SPI: its buttons, fields, labels and their text,
summarized to a few hundred lines, which tells an agent what the window says without guessing from
pixels. GTK and Qt apps expose it; Chromium and Electron apps do once accessibility is on, which
starting them with `--force-renderer-accessibility` does. When the window exposes nothing, only the
picture and its title go. Look at the tree in the composer before sending it: it carries the
window's text.
