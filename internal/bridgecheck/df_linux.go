package bridgecheck

import "golang.org/x/sys/unix"

func setDontFragment(fd uintptr) error {
	return unix.SetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_MTU_DISCOVER, unix.IP_PMTUDISC_PROBE)
}
