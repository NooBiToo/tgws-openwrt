// Package wstest — поддельный WebSocket-сервер для тестов клиента и ядра.
package wstest

import (
	"bufio"
	"crypto/sha1"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"tgws/internal/ws"
)

const guid = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// Server — TLS-сервер, умеющий апгрейд до WebSocket.
type Server struct {
	*httptest.Server
}

// Peer — серверная сторона одной сессии.
type Peer struct {
	Conn net.Conn
	BR   *bufio.Reader
}

// NewHTTP поднимает TLS-сервер с произвольным обработчиком (например, 302).
func NewHTTP(t testing.TB, h http.HandlerFunc) *Server {
	t.Helper()
	s := httptest.NewTLSServer(h)
	t.Cleanup(s.Close)
	return &Server{s}
}

// New поднимает сервер, который апгрейдит любой запрос и отдаёт сессию session.
func New(t testing.TB, session func(*Peer)) *Server {
	return NewHTTP(t, func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "no hijack", http.StatusInternalServerError)
			return
		}
		conn, brw, err := hj.Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		sum := sha1.Sum([]byte(r.Header.Get("Sec-WebSocket-Key") + guid))
		brw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
			"Sec-WebSocket-Accept: " + base64.StdEncoding.EncodeToString(sum[:]) + "\r\n\r\n")
		brw.Flush()
		session(&Peer{Conn: conn, BR: brw.Reader})
	})
}

// Dialer возвращает клиента, настроенного на этот сервер.
func (s *Server) Dialer(path string) ws.Dialer {
	pool := x509.NewCertPool()
	pool.AddCert(s.Certificate())
	return ws.Dialer{
		Addr:       s.Listener.Addr().String(),
		ServerName: "example.com", // в сертификате httptest
		Path:       path,
		TLS:        &tls.Config{RootCAs: pool},
		Timeout:    3 * time.Second,
	}
}

// ReadFrame читает один кадр клиента и снимает маску.
func (p *Peer) ReadFrame() (byte, []byte, error) {
	var h [2]byte
	if _, err := io.ReadFull(p.BR, h[:]); err != nil {
		return 0, nil, err
	}
	n := uint64(h[1] & 0x7f)
	switch n {
	case 126:
		var e [2]byte
		if _, err := io.ReadFull(p.BR, e[:]); err != nil {
			return 0, nil, err
		}
		n = uint64(binary.BigEndian.Uint16(e[:]))
	case 127:
		var e [8]byte
		if _, err := io.ReadFull(p.BR, e[:]); err != nil {
			return 0, nil, err
		}
		n = binary.BigEndian.Uint64(e[:])
	}
	var mask [4]byte
	if h[1]&0x80 != 0 {
		if _, err := io.ReadFull(p.BR, mask[:]); err != nil {
			return 0, nil, err
		}
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(p.BR, payload); err != nil {
		return 0, nil, err
	}
	if h[1]&0x80 != 0 {
		for i := range payload {
			payload[i] ^= mask[i&3]
		}
	}
	return h[0] & 0x0f, payload, nil
}

// ReadBinary возвращает содержимое следующего кадра клиента.
func (p *Peer) ReadBinary() ([]byte, error) {
	_, payload, err := p.ReadFrame()
	return payload, err
}

// WriteFrame отправляет клиенту кадр без маски.
func (p *Peer) WriteFrame(op byte, fin bool, payload []byte) error {
	b0 := op
	if fin {
		b0 |= 0x80
	}
	hdr := []byte{b0}
	n := len(payload)
	switch {
	case n < 126:
		hdr = append(hdr, byte(n))
	case n < 65536:
		hdr = append(hdr, 126, byte(n>>8), byte(n))
	default:
		hdr = append(hdr, 127, 0, 0, 0, 0, byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
	}
	_, err := p.Conn.Write(append(hdr, payload...))
	return err
}
