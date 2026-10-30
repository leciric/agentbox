package state

// SettingTailnet serves the daemon to the tailnet ("1"): a loopback listener
// that `tailscale serve` publishes as an https address on this machine's
// MagicDNS name. Off by default.
const SettingTailnet = "tailnet"

// SettingTailnetAllow is who may use the tailnet listener: the Tailscale logins
// it accepts, comma separated. Empty refuses everyone, because a listener that
// takes anyone the tailnet carries is not what turning it on should mean.
const SettingTailnetAllow = "tailnet_allow"
