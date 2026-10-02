package mtproto

import (
	"bytes"
	"testing"
)

func TestRelayInitParsesBack(t *testing.T) {
	cases := []struct {
		proto Proto
		dc    int16
	}{
		{ProtoAbridged, 2},
		{ProtoIntermediate, -4},
		{ProtoPadded, 203},
	}
	for _, tc := range cases {
		raw, relay := NewRelayInit(tc.proto, tc.dc)
		if len(raw) != InitLen {
			t.Fatalf("init length = %d, want %d", len(raw), InitLen)
		}
		got, err := ParseInit(raw)
		if err != nil {
			t.Fatalf("ParseInit: %v", err)
		}
		if got.Proto != tc.proto || got.DCIdx != tc.dc {
			t.Fatalf("got proto=%#x dc=%d, want proto=%#x dc=%d", got.Proto, got.DCIdx, tc.proto, tc.dc)
		}

		// Инициатор → ответчик: потоки обязаны быть согласованы ПОСЛЕ init,
		// то есть обе стороны продвинули Fwd на 64 байта.
		msg := []byte("hello telegram, this is a payload")
		wire := append([]byte(nil), msg...)
		relay.Fwd.XORKeyStream(wire, wire)
		if bytes.Equal(wire, msg) {
			t.Fatal("payload was not encrypted")
		}
		got.Pair.Fwd.XORKeyStream(wire, wire)
		if !bytes.Equal(wire, msg) {
			t.Fatalf("forward direction: got %q, want %q", wire, msg)
		}

		reply := []byte("answer from the other side")
		wire = append([]byte(nil), reply...)
		got.Pair.Rev.XORKeyStream(wire, wire)
		relay.Rev.XORKeyStream(wire, wire)
		if !bytes.Equal(wire, reply) {
			t.Fatalf("reverse direction: got %q, want %q", wire, reply)
		}
	}
}

func TestNewRelayInitNeverReserved(t *testing.T) {
	// Зарезервированные начала означают для сервера HTTP, TLS или другой
	// транспорт; init, начинающийся так, был бы принят за чужой протокол.
	for i := 0; i < 5000; i++ {
		raw, _ := NewRelayInit(ProtoIntermediate, 2)
		if reserved(raw) {
			t.Fatalf("reserved init generated: % x", raw[:8])
		}
	}
}

func TestParseInitRejectsGarbage(t *testing.T) {
	if _, err := ParseInit(bytes.Repeat([]byte{7}, InitLen)); err != ErrBadInit {
		t.Fatalf("garbage: err = %v, want ErrBadInit", err)
	}
	if _, err := ParseInit(make([]byte, 10)); err != ErrBadInit {
		t.Fatalf("short input: err = %v, want ErrBadInit", err)
	}
}

func TestPlainPairIsIdentity(t *testing.T) {
	p := PlainPair()
	data := []byte("untouched")
	buf := append([]byte(nil), data...)
	p.Fwd.XORKeyStream(buf, buf)
	p.Rev.XORKeyStream(buf, buf)
	if !bytes.Equal(buf, data) {
		t.Fatalf("plain pair altered data: %q", buf)
	}
	other := make([]byte, len(data))
	p.Fwd.XORKeyStream(other, data)
	if !bytes.Equal(other, data) {
		t.Fatalf("plain pair must copy src to dst, got %q", other)
	}
}
