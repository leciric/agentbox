#!/usr/bin/env bash
# Sets up AgentBox on a new machine from the app, covering every item on its
# Setup page. The machine is a fresh Debian 13 VM (Incus) with a desktop's
# libraries, and a user, dev, who has never used AgentBox. dev opens the
# AppImage; Playwright drives it on the VM's own X display
# (setup-app/driver.mjs), and the steps the Setup page sends to a terminal run
# in a terminal. It ends with a first agent (Claude Code, its browser, a preview
# URL) and an Android emulator nested in the VM.
#
#   npm --prefix desktop run dist
#   scripts/demo/setup-app.sh > .demo-runs/setup-app/demo.log 2>&1
#
# The Claude Code login is AgentBox's own token on this machine
# (~/.config/agentbox/credentials/claude-oauth-token, or CLAUDE_TOKEN_FILE). It
# goes into the VM, which is deleted at the end, and isn't printed. Codex gets a
# placeholder API key: its check turns ready, but no Codex request is made.
set -uo pipefail
exec </dev/null

root=$(cd "$(dirname "$0")/../.." && pwd)
version=$(node -p "require('$root/desktop/package.json').version")
appimage=AgentBox-$version-x86_64.AppImage
vm=abx-setup-app
media=$root/.demo-runs/setup-app/media
token_file=${CLAUDE_TOKEN_FILE:-$HOME/.config/agentbox/credentials/claude-oauth-token}
work=$(mktemp -d)
results=()
started=$SECONDS

step() { printf '\n######## [%4ss] %s\n' "$((SECONDS - started))" "$*"; }
check() { # check <name> <command that succeeds when it holds>
  if eval "$2" >/dev/null 2>&1; then results+=("PASS  $1"); echo "PASS $1"; else results+=("FAIL  $1"); echo "FAIL $1"; fi
}
collect() { # the driver's PASS and FAIL lines, from the last output
  while IFS= read -r line; do results+=("$line"); done < <(grep -E '^(PASS|FAIL) ' "$work/last" | sed -E 's/^(PASS|FAIL) /\1  /')
}
# as_dev runs a command in a new login shell of dev, as a terminal would, and prints it.
as_dev() {
  printf '\n$ %s\n' "$*"
  incus exec "$vm" -- su - dev -c "$*" 2>&1 | tee "$work/last"
  return "${PIPESTATUS[0]}"
}
driver_env="DISPLAY=:99 APPIMAGE=/home/dev/Downloads/$appimage PLAYWRIGHT_BROWSERS_PATH=/opt/driver/ms-playwright"
# app runs a phase of the driver in a new login of dev: dev opening the app after logging in.
app() {
  printf '\n[the app, new login] %s\n' "$1"
  incus exec "$vm" -- su - dev -c "$driver_env /opt/driver/node /opt/driver/driver.mjs $1" 2>&1 | tee "$work/last"
  collect
}
# app_old_session runs a phase with only dev's own group, like a session that
# started before host setup: no incus-admin. Since host setup's ACL on the
# Incus socket goes by UID, this session can use Incus all the same.
app_old_session() {
  printf '\n[the app, in the session from before host setup] %s\n' "$1"
  incus exec "$vm" --user 1000 --group 1000 --cwd /home/dev \
    --env HOME=/home/dev --env USER=dev --env LOGNAME=dev --env SHELL=/bin/bash \
    --env DISPLAY=:99 --env APPIMAGE="/home/dev/Downloads/$appimage" --env PLAYWRIGHT_BROWSERS_PATH=/opt/driver/ms-playwright \
    -- /opt/driver/node /opt/driver/driver.mjs "$1" 2>&1 | tee "$work/last"
  collect
}

[ -f "$root/desktop/dist/$appimage" ] || { echo "build the AppImage first: npm --prefix desktop run dist" >&2; exit 1; }
[ -s "$token_file" ] || { echo "no Claude Code token in $token_file (set CLAUDE_TOKEN_FILE)" >&2; exit 1; }

step "A fresh Debian 13 VM: 6 CPUs, 12 GiB of memory, nested virtualization for Android"
incus delete --force "$vm" >/dev/null 2>&1
incus launch images:debian/13 "$vm" --vm -c limits.cpu=6 -c limits.memory=12GiB -d root,size=100GiB
for _ in $(seq 120); do incus exec "$vm" -- getent hosts deb.debian.org >/dev/null 2>&1 && break; sleep 2; done
incus exec "$vm" -- sh -c '. /etc/os-release; echo "$PRETTY_NAME, kernel $(uname -r), $(nproc) CPUs, $(free -h | awk "/Mem/{print \$2}") of memory"; ls -l /dev/kvm; command -v incus || echo "no incus installed"'

step "A desktop user, dev, who can use sudo, with the libraries a desktop has (GTK, NSS, FUSE) and a display"
incus exec "$vm" -- sh -c 'apt-get update -qq >/dev/null && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends \
    sudo git ca-certificates curl python3 xvfb xauth fuse3 libfuse2t64 libgtk-3-0t64 libnss3 libasound2t64 libgbm1 libxss1 libxtst6 \
    libsecret-1-0 libnotify4 xdg-utils fonts-dejavu-core >/dev/null &&
  useradd -m -u 1000 -s /bin/bash -G sudo dev && echo "dev ALL=(ALL) NOPASSWD:ALL" >/etc/sudoers.d/dev && id dev'
incus file push "$root/desktop/dist/$appimage" "$vm/home/dev/Downloads/$appimage" --create-dirs --uid 1000 --gid 1000 --mode 0755
for fixture in hello-stack hello-android; do
  incus file push -r "$root/testdata/fixtures/$fixture" "$vm/home/dev/"
done
incus exec "$vm" -- chown -R dev:dev /home/dev
as_dev 'for d in hello-stack hello-android; do (cd ~/$d && git init -q -b main && git add -A && git -c user.name=dev -c user.email=dev@example.com commit -qm "first commit"); done; echo repositories ready'
as_dev 'setsid -f Xvfb :99 -screen 0 1440x900x24 -nolisten tcp >/dev/null 2>&1; sleep 1; ls /tmp/.X11-unix'

step "The test driver (not part of the setup): Node.js, Playwright and its ffmpeg, in /opt/driver"
incus exec "$vm" -- mkdir -p /opt/driver/node_modules /opt/driver/ms-playwright
incus file push "$(mise which node 2>/dev/null || command -v node)" "$vm/opt/driver/node" --mode 0755
incus file push -r "$root/desktop/node_modules/playwright" "$root/desktop/node_modules/playwright-core" "$vm/opt/driver/node_modules/"
incus file push -r "$HOME/.cache/ms-playwright/ffmpeg-1011" "$vm/opt/driver/ms-playwright/"
incus file push "$root/scripts/demo/setup-app/driver.mjs" "$vm/opt/driver/driver.mjs"
incus exec "$vm" -- chmod -R a+rX /opt/driver
incus exec "$vm" -- mkdir -p /tmp/setup-app
incus file push "$token_file" "$vm/tmp/setup-app/claude-token" --uid 1000 --gid 1000 --mode 0600
incus exec "$vm" -- chown dev:dev /tmp/setup-app

step "Setup 1-2, 5-8 in the app: a new user opens the AppImage, installs the command-line tool, saves the Claude Code login"
app first-open
incus exec "$vm" -- rm -f /tmp/setup-app/claude-token
host_setup=$(incus exec "$vm" -- cat /tmp/setup-app/host-setup-command)
as_dev "command -v agentbox && agentbox --version && readlink ~/.local/bin/agentbox"
check "a new login shell runs agentbox $version, linked to the app's copy" "grep -q 'agentbox version $version' $work/last && grep -q '.local/share/agentbox/bin/agentbox' $work/last"

step "Setup 3 in a terminal: the host setup command the Incus step gave"
# The app's own Set up host button runs the same thing through pkexec, which
# needs a desktop with a polkit agent this VM has not got: that half is covered
# by scripts/demo/host-setup.mjs and its manual checklist.
as_dev "$host_setup"
check "host setup, as the app gave it, finishes" "grep -q 'Done. dev ' $work/last"
check "and says no logout is needed" "grep -q 'no need to log out' $work/last"
app_old_session same-session-as-host-setup

step "Logging out and back in; Setup 4 in the app: Build base image"
app base-image
as_dev "agentbox host check"
check "agentbox host check agrees: Incus, the user mapping and the base image are ready" "grep -q '✓  Incus' $work/last && grep -q '✓  User mapping' $work/last && grep -q '✓  Base image' $work/last"

step "Your first agent, in the app: a project, Claude Code, its browser, a preview URL"
app first-agent
as_dev "agentbox list"
as_dev "agentbox exec hello-stack/agent-01 -- 'timeout 120 claude -p \"Reply with the single word READY\" --model haiku'"
check "Claude Code, logged in with the token saved in the app, answers inside the agent" "grep -q READY $work/last"
preview=$(incus exec "$vm" -- cat /tmp/setup-app/preview-url)
as_dev "agentbox exec hello-stack/agent-01 -- 'tmux new-session -d -s web \"node server.mjs\"; sleep 2' && curl -s --max-time 10 $preview | head -c 300"
check "the preview URL from the app opens the agent's dev server, from the VM's own shell" "grep -qi 'hello' $work/last"

step "Setup 6 in a terminal: the Codex login (needs the Codex CLI)"
as_dev "agentbox auth codex"
check "without the Codex CLI, agentbox auth codex says how to install it" "grep -q 'npm install -g @openai/codex' $work/last"
as_dev 'mkdir -p ~/.local/bin && curl -fsSL https://github.com/openai/codex/releases/latest/download/codex-x86_64-unknown-linux-musl.tar.gz | tar -xz -C /tmp && install -m 0755 /tmp/codex-x86_64-unknown-linux-musl ~/.local/bin/codex && codex --version'
as_dev "printf %s sk-placeholder-not-a-real-key | agentbox auth codex --api-key-stdin"
check "agentbox auth codex stores a login for agents" "grep -q 'Stored the Codex login for agents' $work/last"

step "Setup 7 in a terminal: an Android SDK with the command the Android step gave"
android=$(incus exec "$vm" -- cat /tmp/setup-app/android-command)
incus exec "$vm" -- sh -c 'DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends openjdk-21-jre-headless >/dev/null && echo "installed a Java runtime for sdkmanager"'
# The SDK command-line tools, as downloaded from developer.android.com.
incus file push -r "$HOME/Android/Sdk/cmdline-tools/latest/." "$vm/home/dev/Android/Sdk/cmdline-tools/latest/" --create-dirs
incus exec "$vm" -- chown -R dev:dev /home/dev/Android
as_dev "export PATH=\$HOME/Android/Sdk/cmdline-tools/latest/bin:\$PATH; yes | sdkmanager --licenses >/dev/null 2>&1; time $android 2>&1 | grep -vE '^\[|^\s*$' | tail -5; ls ~/Android/Sdk"
check "sdkmanager installed the emulator, the platform tools and the system image" "grep -q emulator $work/last && grep -q platform-tools $work/last && grep -q system-images $work/last"
app optional-items

step "Setup 8: preview URLs turn off when another program has their port, and back on"
# The daemon holds the port while it runs: stop it, then take the port, as another program would.
as_dev "agentbox daemon stop; setsid -f python3 -m http.server 7777 --bind 127.0.0.1 >/tmp/http-7777.log 2>&1; sleep 1.5; ss -ltnp 'sport = :7777' | tail -1"
check "another program listens on the preview port" "grep -q python3 $work/last"
app preview-off
as_dev "pkill -f '[h]ttp.server 7777'; agentbox daemon stop"
app preview-on

step "The screenshots and videos (off camera)"
incus file pull -r "$vm/tmp/setup-app" "$work/"
# The VM's memory is back before ffmpeg needs some.
incus delete --force "$vm"
mkdir -p "$media"
rm -f "$media"/setup-app-*.png
cp "$work"/setup-app/shots/*.png "$media/"
ls "$media"
videos=$(ls -d "$work"/setup-app/video-* 2>/dev/null | sort)
if [ -n "$videos" ]; then
  for d in $videos; do printf "file '%s'\n" "$(ls "$d"/*.webm)"; done >"$work/videos.txt"
  ffmpeg -y -loglevel error -f concat -safe 0 -i "$work/videos.txt" -c:v libx264 -pix_fmt yuv420p -crf 30 -preset veryfast -movflags +faststart "$media/setup-app.mp4"
  ffmpeg -y -loglevel error -i "$media/setup-app.mp4" \
    -vf 'setpts=PTS/10,fps=4,scale=800:-1:flags=lanczos,split[a][b];[a]palettegen=max_colors=96[p];[b][p]paletteuse=dither=bayer:bayer_scale=4' "$media/setup-app.gif"
  echo "saved setup-app.mp4 and a 10x GIF"
fi

step "Clean up"
rm -rf "$work"

printf '\n######## Summary (%ss)\n' "$((SECONDS - started))"
printf '%s\n' "${results[@]}"
passed=$(printf '%s\n' "${results[@]}" | grep -c '^PASS')
echo "$passed/${#results[@]} checks passed"
[ "$passed" -eq "${#results[@]}" ]
