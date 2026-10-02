package ws_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"testing"

	"tgws/internal/ws"
	"tgws/internal/ws/wstest"
)

func dial(t *testing.T, s *wstest.Server) *ws.Conn {
	t.Helper()
	c, err := s.Dialer("/apiws").Dial(context.Background())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestEchoAndSeparateFrames(t *testing.T) {
	srv := wstest.New(t, func(p *wstest.Peer) {
		for {
			data, err := p.ReadBinary()
			if err != nil {
				return
			}
			p.WriteFrame(2, true, data)
		}
	})
	c := dial(t, srv)
	if err := c.Send([]byte("one"), []byte("two")); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"one", "two"} {
		got, err := c.Recv()
		if err != nil || string(got) != want {
			t.Fatalf("Recv = %q, %v; want %q", got, err, want)
		}
	}
}

func TestLargeFrameUsesExtendedLength(t *testing.T) {
	big := bytes.Repeat([]byte{0xab}, 70000)
	srv := wstest.New(t, func(p *wstest.Peer) {
		data, err := p.ReadBinary()
		if err != nil {
			return
		}
		p.WriteFrame(2, true, data)
	})
	c := dial(t, srv)
	if err := c.Send(big); err != nil {
		t.Fatal(err)
	}
	got, err := c.Recv()
	if err != nil || !bytes.Equal(got, big) {
		t.Fatalf("large frame round trip failed: len=%d err=%v", len(got), err)
	}
}

func TestPingIsAnsweredAndSkipped(t *testing.T) {
	pong := make(chan []byte, 1)
	srv := wstest.New(t, func(p *wstest.Peer) {
		p.WriteFrame(9, true, []byte("x"))
		p.WriteFrame(2, true, []byte("data"))
		op, payload, err := p.ReadFrame()
		if err == nil && op == 0xA {
			pong <- payload
		}
	})
	c := dial(t, srv)
	got, err := c.Recv()
	if err != nil || string(got) != "data" {
		t.Fatalf("Recv = %q, %v", got, err)
	}
	if p := <-pong; string(p) != "x" {
		t.Fatalf("pong payload = %q, want x", p)
	}
}

func TestFragmentedMessageIsAssembled(t *testing.T) {
	srv := wstest.New(t, func(p *wstest.Peer) {
		p.WriteFrame(2, false, []byte("ab"))
		p.WriteFrame(0, true, []byte("cd"))
		p.ReadBinary()
	})
	c := dial(t, srv)
	got, err := c.Recv()
	if err != nil || string(got) != "abcd" {
		t.Fatalf("Recv = %q, %v; want abcd", got, err)
	}
}

func TestCloseFrameEndsWithEOF(t *testing.T) {
	srv := wstest.New(t, func(p *wstest.Peer) {
		p.WriteFrame(8, true, []byte{0x03, 0xe8})
		p.ReadBinary()
	})
	c := dial(t, srv)
	if _, err := c.Recv(); err != io.EOF {
		t.Fatalf("Recv err = %v, want io.EOF", err)
	}
}

func TestRedirectIsReportedWithLocation(t *testing.T) {
	srv := wstest.NewHTTP(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://example.org/")
		w.WriteHeader(http.StatusFound)
	})
	_, err := srv.Dialer("/apiws").Dial(context.Background())
	var he *ws.HandshakeError
	if !errors.As(err, &he) {
		t.Fatalf("err = %v, want *HandshakeError", err)
	}
	if !he.Redirect() || he.Status != 302 || he.Location != "https://example.org/" {
		t.Fatalf("unexpected handshake error: %+v", he)
	}
}

func TestOversizedFrameIsRejected(t *testing.T) {
	srv := wstest.New(t, func(p *wstest.Peer) {
		// Заявленная длина 1 ТиБ при крошечном теле: клиент не должен пытаться
		// выделить память под неё.
		p.Conn.Write([]byte{0x82, 127, 0, 0, 1, 0, 0, 0, 0, 0})
		p.ReadBinary()
	})
	c := dial(t, srv)
	if _, err := c.Recv(); err == nil {
		t.Fatal("an oversized frame must be an error")
	}
}
