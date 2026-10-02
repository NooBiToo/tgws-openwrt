package sockmark

import "testing"

func TestControlIsNilWithoutMark(t *testing.T) {
	if Control(0) != nil {
		t.Fatal("mark 0 must not install a control function")
	}
	if Control(0x7467) == nil {
		t.Fatal("a non-zero mark must install a control function")
	}
}
