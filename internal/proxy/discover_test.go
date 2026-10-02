package proxy_test

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"tgws/internal/mtproto"
	"tgws/internal/proxy"
	"tgws/internal/ws/wstest"
)

// Android в прямом соединении не пишет в init настоящий индекс DC, а адреса,
// которые он выбирает из списка, присланного Telegram, зашитая таблица знать не
// может. Демон определяет DC перебором: отправляет первый пакет клиента на
// кандидата, и транспортная ошибка -404 означает «не тот DC».

// abridgedError — транспортная ошибка Telegram в формате abridged: один
// «пакет» из четырёх байт, -404 (auth key not found), little-endian.
var abridgedError404 = []byte{0x01, 0x6c, 0xfe, 0xff, 0xff}

// dcServer — поддельный WebSocket-сервер дата-центра. right=false отвечает на
// первый пакет ошибкой -404, как DC, которому неизвестен ключ клиента.
func dcServer(t *testing.T, right bool, hits *atomic.Int32) *wstest.Server {
	return wstest.New(t, func(p *wstest.Peer) {
		first, err := p.ReadBinary()
		if err != nil {
			return
		}
		init, err := mtproto.ParseInit(first)
		if err != nil {
			return
		}
		if _, err := p.ReadBinary(); err != nil { // первый пакет клиента
			return
		}
		hits.Add(1)
		reply := append([]byte(nil), abridgedError404...)
		if right {
			reply = []byte{0x01, 'p', 'o', 'n', 'g'}
		}
		init.Pair.Rev.XORKeyStream(reply, reply)
		p.WriteFrame(2, true, reply)
		p.ReadBinary() // ждём закрытия
	})
}

type discoverHarness struct {
	addr         string
	dials        []string // домены, на которые открывался WebSocket, по порядку
	mu           sync.Mutex
	srv          *proxy.Server
	directDialed atomic.Int32
}

func newDiscoverHarness(t *testing.T, ip string, byDomain map[string]*wstest.Server, dc *fakeDC) *discoverHarness {
	t.Helper()
	h := &discoverHarness{}
	h.srv = proxy.New(proxy.Config{
		Targets: map[int]string{2: "149.154.167.220", 4: "149.154.167.220"},
		OrigDst: func(*net.TCPConn) (netip.AddrPort, error) {
			return netip.AddrPortFrom(netip.MustParseAddr(ip), 443), nil
		},
		DialWS: func(ctx context.Context, target, domain, path string) (proxy.WSConn, error) {
			h.mu.Lock()
			h.dials = append(h.dials, domain)
			h.mu.Unlock()
			for prefix, s := range byDomain {
				if strings.HasPrefix(domain, prefix) {
					c, err := s.Dialer(path).Dial(ctx)
					if err != nil {
						return nil, err
					}
					return c, nil
				}
			}
			return nil, errors.New("no such domain")
		},
		DialTCP: func(ctx context.Context, addr string) (net.Conn, error) {
			h.directDialed.Add(1)
			if dc == nil {
				return nil, errors.New("no fallback expected")
			}
			return net.Dial("tcp", dc.ln.Addr().String())
		},
		HelloTimeout: 300 * time.Millisecond,
		DiscoverWait: 300 * time.Millisecond,
		ReplyWait:    500 * time.Millisecond,
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go h.srv.Serve(ln)
	h.addr = ln.Addr().String()
	return h
}

func (h *discoverHarness) domains() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.dials...)
}

// androidClient подключается как Android: обфусцированный init с чужим
// индексом DC и сразу первый пакет.
func (h *discoverHarness) androidClient(t *testing.T) (net.Conn, mtproto.Pair, []byte) {
	t.Helper()
	c, err := net.Dial("tcp", h.addr)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	t.Cleanup(func() { c.Close() })
	raw, cl := mtproto.NewRelayInit(mtproto.ProtoAbridged, 4735)
	pkt := append([]byte{2}, 1, 2, 3, 4, 5, 6, 7, 8) // abridged, 2 слова
	enc := append([]byte(nil), pkt...)
	cl.Fwd.XORKeyStream(enc, enc)
	c.Write(append(append([]byte(nil), raw...), enc...))
	return c, cl, append(append([]byte(nil), raw...), enc...)
}

func readReply(t *testing.T, c net.Conn, cl mtproto.Pair, n int) []byte {
	t.Helper()
	buf := make([]byte, n)
	got := 0
	for got < n {
		m, err := c.Read(buf[got:])
		if err != nil {
			t.Fatalf("client read after %d bytes: %v", got, err)
		}
		got += m
	}
	cl.Rev.XORKeyStream(buf, buf)
	return buf
}

func TestDiscoveryFindsTheRightDCByTheTransportError(t *testing.T) {
	var wrongHits, rightHits atomic.Int32
	h := newDiscoverHarness(t, "149.154.167.77", map[string]*wstest.Server{
		"kws2": dcServer(t, false, &wrongHits), // DC2 не знает ключ клиента
		"kws4": dcServer(t, true, &rightHits),  // DC4 знает
	}, nil)

	c, cl, _ := h.androidClient(t)
	// Клиент получает ответ правильного DC, а не ошибку первого кандидата.
	if got := readReply(t, c, cl, 5); string(got) != "\x01pong" {
		t.Fatalf("client saw % x, want the right DC's reply", got)
	}
	if wrongHits.Load() != 1 || rightHits.Load() != 1 {
		t.Fatalf("hits: wrong=%d right=%d, want 1 and 1", wrongHits.Load(), rightHits.Load())
	}
	if h.directDialed.Load() != 0 {
		t.Fatal("a discovered DC must not use the direct path")
	}
	c.Close()

	// Результат запомнен: второе соединение с этим адресом идёт сразу на DC4.
	before := len(h.domains())
	c2, cl2, _ := h.androidClient(t)
	if got := readReply(t, c2, cl2, 5); string(got) != "\x01pong" {
		t.Fatalf("second client saw % x", got)
	}
	for _, d := range h.domains()[before:] {
		if strings.HasPrefix(d, "kws2") {
			t.Fatalf("the learned DC was ignored: dialled %v after learning", h.domains()[before:])
		}
	}
}

func TestDiscoveryWithNoRightDCFallsBackVerbatim(t *testing.T) {
	var a, b atomic.Int32
	// Нужно знать длину отправляемого, чтобы fakeDC ждал ровно столько же.
	const sent = 64 + 9
	dc := newFakeDC(t, sent)
	h := newDiscoverHarness(t, "149.154.167.78", map[string]*wstest.Server{
		"kws2": dcServer(t, false, &a),
		"kws4": dcServer(t, false, &b),
	}, dc)

	c, _, raw := h.androidClient(t)
	_ = c
	got := recvBytes(t, dc.got)
	if string(got) != string(raw) {
		t.Fatal("when no candidate DC accepts the client, its bytes must reach the original destination verbatim")
	}
	if h.srv.Stats().Fallback.Load() != 1 {
		t.Fatalf("fallback = %d, want 1", h.srv.Stats().Fallback.Load())
	}
}

func TestDiscoveryWithoutAFirstPacketFallsBack(t *testing.T) {
	var a, b atomic.Int32
	dc := newFakeDC(t, 64)
	h := newDiscoverHarness(t, "149.154.167.79", map[string]*wstest.Server{
		"kws2": dcServer(t, true, &a),
		"kws4": dcServer(t, true, &b),
	}, dc)
	c, err := net.Dial("tcp", h.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	raw, _ := mtproto.NewRelayInit(mtproto.ProtoAbridged, 4735)
	c.Write(raw) // только init, ни одного пакета
	if got := recvBytes(t, dc.got); string(got) != string(raw) {
		t.Fatal("the init alone must be replayed verbatim")
	}
	if len(h.domains()) != 0 {
		t.Fatalf("nothing to test a DC with, yet WebSocket was dialled: %v", h.domains())
	}
}
