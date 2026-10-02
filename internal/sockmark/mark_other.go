//go:build !linux

package sockmark

// Вне Linux метки сокетов нет: демон там только собирается и тестируется.
func setMark(uintptr, int) error { return nil }
