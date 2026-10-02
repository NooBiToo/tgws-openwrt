//go:build linux

package sockmark

import "syscall"

// SO_MARK равен 36 на x86, arm, arm64 и mips; пакет syscall его не объявляет
// для всех архитектур, поэтому значение зафиксировано здесь.
const soMark = 36

func setMark(fd uintptr, mark int) error {
	return syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, soMark, mark)
}
