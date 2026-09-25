#!/bin/sh
# The packaged app runs the packaged command-line tool. Without this it would
# keep its own copy in ~/.local/share/agentbox/bin, because electron-builder
# ships one in the app's resources.
export AGENTBOX_BIN=/usr/bin/agentbox
exec /usr/lib/agentbox-desktop/agentbox-desktop "$@"
