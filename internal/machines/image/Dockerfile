# The desktop machine `agentbox machines` gives an AI tool on the user's own
# machine: a display with Chromium on it (the agents' browser.sh), ffmpeg,
# xdotool and Playwright's MCP server. No agent CLI and nothing of the user's
# is in it, so it can be published. The build context is assembled by
# `agentbox machines build` (browser.sh and the wallpaper come from
# internal/agent); `agentbox machines build --context <dir>` writes it out.
FROM debian:trixie-slim

ARG PLAYWRIGHT_MCP

# The desktop is what system.sh installs for the agents' base image; then
# what a machine adds: node for Playwright's MCP server, git, sudo, and Docker
# for a project that runs one inside (docker_inside).
RUN apt-get update -q && DEBIAN_FRONTEND=noninteractive apt-get install -y -q --no-install-recommends \
      tigervnc-standalone-server chromium openbox tint2 pcmanfm xfce4-terminal xdotool x11-utils \
      x11-xserver-utils xwallpaper adwaita-icon-theme librsvg2-common fonts-liberation \
      fonts-dejavu-core fonts-noto-color-emoji ffmpeg \
      ca-certificates curl git procps iproute2 sudo nodejs npm \
      docker.io docker-cli iptables \
    && npm install -g --no-fund --no-audit "$PLAYWRIGHT_MCP" \
    && rm -rf /var/lib/apt/lists/* /root/.npm

COPY browser.sh /usr/local/bin/agentbox-browser
COPY wallpaper.png /usr/local/share/agentbox/wallpaper.png
COPY entrypoint.sh /usr/local/bin/agentbox-machine
COPY chromium.conf /etc/chromium.d/agentbox-machine
RUN chmod 0755 /usr/local/bin/agentbox-browser /usr/local/bin/agentbox-machine \
    && install -d -m 1777 /home/machine

ENTRYPOINT ["/usr/local/bin/agentbox-machine"]
