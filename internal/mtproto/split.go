package mtproto

import (
	"crypto/cipher"
	"encoding/binary"
)

// Splitter режет поток, идущий в Telegram, на транспортные пакеты MTProto,
// чтобы каждый уходил в WebSocket отдельным кадром. Границы пакетов видны
// только в открытом тексте, поэтому разбиватель держит собственную копию
// потока шифрования стороны Telegram и расшифровывает ею то же, что уходит
// на провод.
type Splitter struct {
	dec      cipher.Stream
	proto    Proto
	cbuf     []byte // шифртекст, ещё не отданный наружу
	pbuf     []byte // тот же участок в открытом виде
	disabled bool
}

// NewSplitter строит разбиватель по init, отправленному в Telegram.
func NewSplitter(relayInit []byte, proto Proto) *Splitter {
	p := pairFrom(relayInit)
	// Пропустить 64 байта самого init: данные начинаются после него.
	skip := make([]byte, InitLen)
	p.Fwd.XORKeyStream(skip, skip)
	return &Splitter{dec: p.Fwd, proto: proto}
}

// Split принимает очередной кусок шифртекста и возвращает готовые пакеты.
// Недособранный хвост остаётся внутри до следующего вызова.
func (s *Splitter) Split(chunk []byte) [][]byte {
	if len(chunk) == 0 {
		return nil
	}
	if s.disabled {
		return [][]byte{append([]byte(nil), chunk...)}
	}
	s.cbuf = append(s.cbuf, chunk...)
	plain := make([]byte, len(chunk))
	s.dec.XORKeyStream(plain, chunk)
	s.pbuf = append(s.pbuf, plain...)

	var parts [][]byte
	off := 0
	for off < len(s.cbuf) {
		n, more := s.nextLen(off)
		if more {
			break
		}
		if n <= 0 {
			// Длина нечитаема: дальше резать нельзя, отдаём остаток целиком
			// и больше не пытаемся.
			parts = append(parts, append([]byte(nil), s.cbuf[off:]...))
			off = len(s.cbuf)
			s.disabled = true
			break
		}
		parts = append(parts, append([]byte(nil), s.cbuf[off:off+n]...))
		off += n
	}
	// Остаток копируется вниз, чтобы буфер не рос вместе с потоком.
	s.cbuf = append(s.cbuf[:0], s.cbuf[off:]...)
	s.pbuf = append(s.pbuf[:0], s.pbuf[off:]...)
	return parts
}

// Flush отдаёт накопленный хвост, когда клиент закрыл соединение.
func (s *Splitter) Flush() []byte {
	if len(s.cbuf) == 0 {
		return nil
	}
	tail := append([]byte(nil), s.cbuf...)
	s.cbuf = s.cbuf[:0]
	s.pbuf = s.pbuf[:0]
	return tail
}

// nextLen возвращает длину пакета, начинающегося в off. more=true — данных
// пока мало. n<=0 при more=false — длину прочитать нельзя.
func (s *Splitter) nextLen(off int) (n int, more bool) {
	avail := len(s.cbuf) - off
	switch s.proto {
	case ProtoAbridged:
		first := s.pbuf[off]
		var payload, header int
		if first == 0x7f || first == 0xff {
			if avail < 4 {
				return 0, true
			}
			payload = int(uint32(s.pbuf[off+1])|uint32(s.pbuf[off+2])<<8|uint32(s.pbuf[off+3])<<16) * 4
			header = 4
		} else {
			payload = int(first&0x7f) * 4
			header = 1
		}
		if payload <= 0 {
			return 0, false
		}
		if avail < header+payload {
			return 0, true
		}
		return header + payload, false
	case ProtoIntermediate, ProtoPadded:
		if avail < 4 {
			return 0, true
		}
		payload := int(binary.LittleEndian.Uint32(s.pbuf[off:]) & 0x7fffffff)
		if payload <= 0 {
			return 0, false
		}
		if avail < 4+payload {
			return 0, true
		}
		return 4 + payload, false
	}
	return 0, false
}
