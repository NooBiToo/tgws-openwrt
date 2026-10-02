package origdst

import (
	"net"
	"testing"
)

// Без правила redirect у соединения нет исходного адреса: вызов обязан вернуть
// ошибку, а не выдать локальный адрес за настоящий.
func TestGetFailsWithoutRedirect(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	done := make(chan *net.TCPConn, 1)
	go func() {
		c, _ := ln.Accept()
		tc, _ := c.(*net.TCPConn)
		done <- tc
	}()
	cl, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	srv := <-done
	defer srv.Close()
	if ap, err := Get(srv); err == nil {
		t.Fatalf("expected an error, got %v", ap)
	}
}
