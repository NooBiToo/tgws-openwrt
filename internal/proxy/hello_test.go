package proxy

import (
	"bytes"
	"io"
	"testing"
	"time"

	"tgws/internal/mtproto"
)

func TestHelloPlainAbridged(t *testing.T) {
	r := bytes.NewReader([]byte{0xef, 1, 2, 3})
	h := readHello(r)
	if h.err != nil || h.proto != mtproto.ProtoAbridged || h.hasDC {
		t.Fatalf("hello = %+v", h)
	}
	if !bytes.Equal(h.consumed, []byte{0xef}) {
		t.Fatalf("consumed = % x, want only the tag byte", h.consumed)
	}
	if r.Len() != 3 {
		t.Fatalf("the rest of the stream must stay unread, %d left", r.Len())
	}
}

func TestHelloPlainIntermediateAndPadded(t *testing.T) {
	for _, tc := range []struct {
		tag   []byte
		proto mtproto.Proto
	}{
		{[]byte{0xee, 0xee, 0xee, 0xee}, mtproto.ProtoIntermediate},
		{[]byte{0xdd, 0xdd, 0xdd, 0xdd}, mtproto.ProtoPadded},
	} {
		h := readHello(bytes.NewReader(append(append([]byte(nil), tc.tag...), 9, 9)))
		if h.err != nil || h.proto != tc.proto || !bytes.Equal(h.consumed, tc.tag) {
			t.Fatalf("hello = %+v", h)
		}
	}
}

func TestHelloObfuscated(t *testing.T) {
	raw, _ := mtproto.NewRelayInit(mtproto.ProtoIntermediate, -2)
	h := readHello(bytes.NewReader(append(append([]byte(nil), raw...), 0x55)))
	if h.err != nil || !h.hasDC || h.dcIdx != -2 || h.proto != mtproto.ProtoIntermediate {
		t.Fatalf("hello = %+v", h)
	}
	if !bytes.Equal(h.consumed, raw) {
		t.Fatal("the whole init must be remembered for a verbatim replay")
	}
}

func TestHelloGarbageKeepsEverythingRead(t *testing.T) {
	// Не HTTP и не тег транспорта: HTTP опознаётся раньше (см. тест ниже).
	req := bytes.Repeat([]byte("ABCDEFGHIJKLMNOP"), 5) // 80 байт
	h := readHello(bytes.NewReader(req))
	if h.err == nil {
		t.Fatal("non-MTProto input must be an error")
	}
	if !bytes.Equal(h.consumed, req[:64]) {
		t.Fatalf("consumed %d bytes, want the first 64 for replay", len(h.consumed))
	}
}

func TestHelloShortInputKeepsPartialBytes(t *testing.T) {
	h := readHello(bytes.NewReader([]byte("ABCDE")))
	if h.err == nil || string(h.consumed) != "ABCDE" {
		t.Fatalf("hello = %+v", h)
	}
}

// Редирект подсетей Telegram цепляет и сайт telegram.org: короткий HTTP-запрос
// короче 64 байт, клиент после него ждёт ответа, и чтение «ещё 60 байт»
// держало бы его до HelloTimeout. Метод HTTP узнаётся по первым четырём байтам.
func TestHelloRecognisesShortHTTPWithoutWaitingForMore(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	req := []byte("GET / HTTP/1.1\r\nHost: telegram.org\r\n\r\n") // 40 байт, дальше тишина
	go pw.Write(req)

	done := make(chan hello, 1)
	go func() { done <- readHello(pr) }()
	select {
	case h := <-done:
		if h.err == nil {
			t.Fatal("HTTP is not MTProto")
		}
		if !bytes.Equal(h.consumed, req[:4]) {
			t.Fatalf("consumed = %q, want only the first 4 bytes, the rest stays in the socket", h.consumed)
		}
	case <-time.After(time.Second):
		t.Fatal("readHello kept waiting for 64 bytes after a recognisable HTTP method")
	}
}

func TestHelloEmptyInput(t *testing.T) {
	h := readHello(bytes.NewReader(nil))
	if h.err == nil || len(h.consumed) != 0 {
		t.Fatalf("hello = %+v", h)
	}
}
