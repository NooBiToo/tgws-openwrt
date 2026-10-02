// Package proxy — ядро демона: принимает перехваченные соединения, определяет
// дата-центр, ведёт трафик через WebSocket или, если это не вышло, прямым TCP.
package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"sync"
	"time"

	"tgws/internal/dcmap"
	"tgws/internal/mtproto"
	"tgws/internal/ws"
)

const wsPath = "/apiws"

// WSConn — то, что ядру нужно от WebSocket; *ws.Conn ему удовлетворяет.
type WSConn interface {
	Send(parts ...[]byte) error
	Recv() ([]byte, error)
	Close() error
}

// Config собирает зависимости; всё внешнее (сеть, адреса, часы) подменяемо.
type Config struct {
	Targets      map[int]string // DC → IP для WebSocket
	OrigDst      func(*net.TCPConn) (netip.AddrPort, error)
	DialWS       func(ctx context.Context, target, domain, path string) (WSConn, error)
	DialTCP      func(ctx context.Context, addr string) (net.Conn, error)
	Stats        *Stats
	Logf         func(format string, args ...any) // события, о которых стоит знать
	Debugf       func(format string, args ...any) // ход каждого соединения
	Now          func() time.Time
	HelloTimeout time.Duration
	Cooldown     time.Duration
}

// Server обслуживает перехваченные соединения.
type Server struct {
	cfg   Config
	fails *failTracker
}

// New подставляет значения по умолчанию.
func New(cfg Config) *Server {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.HelloTimeout == 0 {
		cfg.HelloTimeout = 10 * time.Second
	}
	if cfg.Cooldown == 0 {
		cfg.Cooldown = time.Minute
	}
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	if cfg.Debugf == nil {
		cfg.Debugf = func(string, ...any) {}
	}
	if cfg.Stats == nil {
		cfg.Stats = NewStats()
	}
	return &Server{cfg: cfg, fails: newFailTracker(cfg.Now, cfg.Cooldown)}
}

// Stats отдаёт счётчики.
func (s *Server) Stats() *Stats { return s.cfg.Stats }

// Serve принимает соединения, пока слушатель не закрыт.
func (s *Server) Serve(l net.Listener) error {
	for {
		c, err := l.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			// Нехватка дескрипторов и подобное: не падаем, а ждём.
			s.cfg.Logf("accept: %v", err)
			time.Sleep(50 * time.Millisecond)
			continue
		}
		go s.handle(c)
	}
}

func failKey(dc int, media bool) string {
	if media {
		return fmt.Sprintf("%dm", dc)
	}
	return fmt.Sprintf("%d", dc)
}

// pickDC предпочитает DC из init клиента; открытому клиенту DC подсказывает
// только адрес назначения.
func pickDC(h hello, ip netip.Addr) (dc int, media, ok bool) {
	if h.hasDC {
		dc = int(h.dcIdx)
		if dc < 0 {
			dc, media = -dc, true
		}
		if dcmap.Valid(dc) {
			return dc, media, true
		}
		// Тестовые DC (10000+) и прочее нестандартное — не наше дело.
	}
	dc, ok = dcmap.ByIP(ip)
	return dc, false, ok
}

func isTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

func (s *Server) handle(c net.Conn) {
	defer c.Close()
	st := s.cfg.Stats
	st.Active.Add(1)
	st.Total.Add(1)
	defer st.Active.Add(-1)

	tc, ok := c.(*net.TCPConn)
	if !ok {
		return
	}
	dst, err := s.cfg.OrigDst(tc)
	if err != nil {
		s.cfg.Logf("original destination: %v", err)
		return
	}
	label := c.RemoteAddr().String() + " -> " + dst.String()

	// Соединение, которое не проходило через redirect, отвечает на
	// SO_ORIGINAL_DST собственным адресом сокета. Откат дозвонился бы тогда до
	// самого демона, тот принял бы это соединение и снова откатился на себя:
	// цепочка до исчерпания дескрипторов, а вместе с ней перестаёт работать
	// Telegram во всей сети. Достаточно одного любопытного клиента на порту.
	if local, ok := c.LocalAddr().(*net.TCPAddr); ok {
		lp := local.AddrPort()
		if dst.Addr().Unmap() == lp.Addr().Unmap() && dst.Port() == lp.Port() {
			s.cfg.Debugf("[%s] not redirected, dropping", label)
			return
		}
	}
	// Правило nft перенаправляет только эти порты; остальное дошло до демона
	// мимо перехвата, и обслуживать это не наше дело.
	switch dst.Port() {
	case 80, 443, 5222:
	default:
		s.cfg.Debugf("[%s] unexpected destination port, dropping", label)
		return
	}

	_ = c.SetReadDeadline(time.Now().Add(s.cfg.HelloTimeout))
	h := readHello(c)
	_ = c.SetReadDeadline(time.Time{})
	if h.err != nil {
		if len(h.consumed) == 0 {
			// Клиент ничего не сказал: откатываться не из-за чего.
			return
		}
		// Первые байты нужны, чтобы понять, что за клиент пришёл: без них
		// «не MTProto» не отличить от TLS, обфускации с secret или нового
		// транспорта.
		head := h.consumed
		if len(head) > 24 {
			head = head[:24]
		}
		s.fallback(c, h.consumed, dst, label, fmt.Sprintf("not MTProto (%v), first bytes % x", h.err, head))
		return
	}

	dc, media, ok := pickDC(h, dst.Addr())
	if !ok {
		if st.NoteUnknown(dst.Addr().String()) {
			s.cfg.Logf("no DC known for %s: extend dcmap if clients keep using it", dst.Addr())
		}
		s.fallback(c, h.consumed, dst, label, "unknown DC")
		return
	}
	target, has := s.cfg.Targets[dc]
	key := failKey(dc, media)
	if !has {
		s.fallback(c, h.consumed, dst, label, fmt.Sprintf("no WebSocket target for DC%d", dc))
		return
	}
	if s.fails.blocked(key) {
		s.fallback(c, h.consumed, dst, label, fmt.Sprintf("DC%d WebSocket is paused", dc))
		return
	}
	conn := s.connectWS(dc, media, target, key, label)
	if conn == nil {
		s.fallback(c, h.consumed, dst, label, fmt.Sprintf("DC%d WebSocket failed", dc))
		return
	}

	idx := int16(dc)
	if media {
		idx = -idx
	}
	relayInit, relay := mtproto.NewRelayInit(h.proto, idx)
	if err := conn.Send(relayInit); err != nil {
		_ = conn.Close()
		s.fallback(c, h.consumed, dst, label, fmt.Sprintf("DC%d WebSocket write: %v", dc, err))
		return
	}
	st.WS.Add(1)
	s.cfg.Debugf("[%s] DC%d media=%v via WebSocket", label, dc, media)
	bridge(c, conn, h.pair, relay, mtproto.NewSplitter(relayInit, h.proto), st)
}

// connectWS перебирает домены DC. Решение об отказе принимает здесь же:
// все домены ответили перенаправлением — DC закрыт для WebSocket насовсем,
// иначе (таймаут, ошибка) — пауза.
func (s *Server) connectWS(dc int, media bool, target, key, label string) WSConn {
	redirects, attempts := 0, 0
	for _, domain := range dcmap.Domains(dc, media) {
		attempts++
		conn, err := s.cfg.DialWS(context.Background(), target, domain, wsPath)
		if err == nil {
			s.fails.clear(key)
			return conn
		}
		s.cfg.Stats.WSErrors.Add(1)
		var he *ws.HandshakeError
		switch {
		case errors.As(err, &he) && he.Redirect():
			redirects++
			s.cfg.Debugf("[%s] %s answered %d -> %s", label, domain, he.Status, he.Location)
		case isTimeout(err):
			// Таймаут не прекращает перебор: на живом роутере первое
			// TCP-соединение к IP дата-центра обрывалось (троттлинг), а
			// повтор на соседний домен того же DC проходил. Пауза нужна,
			// только если не удался ни один домен.
			s.cfg.Debugf("[%s] %s timed out", label, domain)
		default:
			s.cfg.Debugf("[%s] %s: %v", label, domain, err)
		}
	}
	if redirects == attempts {
		s.fails.blacklist(key)
		s.cfg.Logf("DC%d: every WebSocket domain redirects, using direct TCP until restart", dc)
	} else {
		s.fails.cooldown(key)
	}
	return nil
}

// fallback ведёт соединение прямым TCP на исходный адрес. Прочитанные у
// клиента байты повторяются дословно, поэтому перешифровка не нужна.
func (s *Server) fallback(c net.Conn, consumed []byte, dst netip.AddrPort, label, why string) {
	s.cfg.Stats.Fallback.Add(1)
	s.cfg.Debugf("[%s] direct: %s", label, why)
	up, err := s.cfg.DialTCP(context.Background(), dst.String())
	if err != nil {
		s.cfg.Debugf("[%s] direct dial: %v", label, err)
		return
	}
	defer up.Close()
	if len(consumed) > 0 {
		if _, err := up.Write(consumed); err != nil {
			return
		}
	}
	var wg sync.WaitGroup
	wg.Add(2)
	pipe := func(to, from net.Conn) {
		defer wg.Done()
		_, _ = io.Copy(to, from)
		if cw, ok := to.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}
	}
	go pipe(up, c)
	go pipe(c, up)
	wg.Wait()
}

// bridge связывает клиента и WebSocket, перешифровывая поток в обе стороны:
// клиентский шифр снимается, шифр стороны Telegram накладывается.
func bridge(c net.Conn, w WSConn, client, relay mtproto.Pair, sp *mtproto.Splitter, st *Stats) {
	var once sync.Once
	shutdown := func() {
		once.Do(func() {
			// Сначала клиентский сокет: его закрытие не блокируется, а
			// закрытие WebSocket может ждать записи.
			_ = c.Close()
			_ = w.Close()
		})
	}
	var wg sync.WaitGroup
	wg.Add(2)

	go func() { // клиент → Telegram
		defer wg.Done()
		defer shutdown()
		buf := make([]byte, 64<<10)
		for {
			n, err := c.Read(buf)
			if n > 0 {
				b := buf[:n]
				client.Fwd.XORKeyStream(b, b)
				relay.Fwd.XORKeyStream(b, b)
				st.BytesUp.Add(int64(n))
				if parts := sp.Split(b); len(parts) > 0 {
					if w.Send(parts...) != nil {
						return
					}
				}
			}
			if err != nil {
				if tail := sp.Flush(); len(tail) > 0 {
					_ = w.Send(tail)
				}
				return
			}
		}
	}()

	go func() { // Telegram → клиент
		defer wg.Done()
		defer shutdown()
		for {
			msg, err := w.Recv()
			if err != nil {
				return
			}
			relay.Rev.XORKeyStream(msg, msg)
			client.Rev.XORKeyStream(msg, msg)
			st.BytesDown.Add(int64(len(msg)))
			if _, err := c.Write(msg); err != nil {
				return
			}
		}
	}()
	wg.Wait()
}
