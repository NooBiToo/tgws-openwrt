// Package sockmark ставит SO_MARK на исходящие сокеты демона.
//
// Зачем: пакет luci-app-trusttunnel (если он установлен и маршрутизирует
// трафик самого роутера) возвращает пакеты с этой отметкой без маркировки в
// туннель. Без метки соединения tgws к Telegram ушли бы в VPN, хотя смысл
// демона — идти напрямую.
package sockmark

import "syscall"

// Control подходит для net.Dialer.Control; при mark == 0 возвращает nil.
func Control(mark int) func(network, address string, c syscall.RawConn) error {
	if mark == 0 {
		return nil
	}
	return func(_, _ string, c syscall.RawConn) error {
		var serr error
		if err := c.Control(func(fd uintptr) { serr = setMark(fd, mark) }); err != nil {
			return err
		}
		return serr
	}
}
