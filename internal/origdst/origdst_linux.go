//go:build linux

// Package origdst возвращает исходный адрес назначения соединения, которое
// netfilter перенаправил (redirect) на локальный порт.
package origdst

import (
	"encoding/binary"
	"net"
	"net/netip"
	"syscall"
)

const soOriginalDst = 80 // SO_ORIGINAL_DST из linux/netfilter_ipv4.h

// Get читает SO_ORIGINAL_DST. Ядро кладёт в буфер sockaddr_in; стандартный
// пакет syscall не имеет для него отдельной обёртки, поэтому используется
// GetsockoptIPv6Mreq: её 16-байтное поле Multiaddr как раз вмещает sockaddr_in
// (семейство, порт в сетевом порядке, IPv4-адрес).
func Get(c *net.TCPConn) (netip.AddrPort, error) {
	rc, err := c.SyscallConn()
	if err != nil {
		return netip.AddrPort{}, err
	}
	var res netip.AddrPort
	var serr error
	if err := rc.Control(func(fd uintptr) {
		m, e := syscall.GetsockoptIPv6Mreq(int(fd), syscall.IPPROTO_IP, soOriginalDst)
		if e != nil {
			serr = e
			return
		}
		port := binary.BigEndian.Uint16(m.Multiaddr[2:4])
		ip := netip.AddrFrom4([4]byte(m.Multiaddr[4:8]))
		res = netip.AddrPortFrom(ip, port)
	}); err != nil {
		return netip.AddrPort{}, err
	}
	return res, serr
}
