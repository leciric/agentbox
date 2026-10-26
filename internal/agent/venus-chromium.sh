# Written by AgentBox: "GPU for agents" on a Mac (internal/agent/vgpudriver.go).
# Debian's chromium wrapper reads this before every start, so the desktop's
# browser, Playwright's and a headless one all run WebGL on the Mac's GPU:
# ANGLE's Vulkan backend on the VM's Venus driver, which Chromium neither
# picks nor allows by itself. Only a driver with venus/0004 runs it, and only
# one with 0005 survives WebGL's timer queries, so 0005's message is looked
# for in the driver: one built before keeps Chromium's default.
agentbox_venus=$(awk '$5 ~ /\/libvulkan_virtio\.so$/ { print $5; exit }' "${mountinfo:-/proc/self/mountinfo}" 2>/dev/null)
if [ -n "$agentbox_venus" ] && grep -qa 'query feedback off: the renderer has no dma-buf' "$agentbox_venus"; then
  export CHROMIUM_FLAGS="$CHROMIUM_FLAGS --use-angle=vulkan --ignore-gpu-blocklist"
fi
unset agentbox_venus
