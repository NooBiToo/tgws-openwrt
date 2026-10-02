package origdst

import (
	"net"
	"testing"
)

// Без правила redirect у соединения нет «исходного» адреса, отличного от его
// собственного. Что вернёт ядро, зависит от того, загружен ли conntrack: без
// него — ошибка, с ним (как на роутере и на раннере GitHub) — собственный адрес
// сокета. Обе картины допустимы; недопустим чужой адрес. Именно эту особенность
// закрывает проверка «соединение пришло не через перенаправление» в ядре прокси.
func TestGetNeverInventsAForeignAddressWithoutRedirect(t *testing.T) {
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
	ap, err := Get(srv)
	if err != nil {
		return // ядро без conntrack: отказ — допустимый ответ
	}
	local := srv.LocalAddr().(*net.TCPAddr).AddrPort()
	if ap.Addr().Unmap() != local.Addr().Unmap() || ap.Port() != local.Port() {
		t.Fatalf("a connection that was not redirected must report its own address %v, got %v", local, ap)
	}
}
