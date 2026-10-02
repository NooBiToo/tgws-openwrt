//go:build !linux

package origdst

import (
	"errors"
	"net"
	"net/netip"
)

// Get вне Linux недоступен: перехват работает только на роутере.
func Get(*net.TCPConn) (netip.AddrPort, error) {
	return netip.AddrPort{}, errors.New("SO_ORIGINAL_DST is Linux-only")
}
