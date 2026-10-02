package probe_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"testing"
	"time"

	"tgws/internal/mtproto"
	"tgws/internal/probe"
	"tgws/internal/ws/wstest"
)

// fakeTelegram играет DC: читает init и незашифрованный req_pq_multi, как
// настоящий сервер, и отвечает resPQ с тем же nonce — или нарочно портит ответ.
func fakeTelegram(t *testing.T, mutate func(reply []byte) []byte, silent bool) *wstest.Server {
	return wstest.New(t, func(p *wstest.Peer) {
		first, err := p.ReadBinary()
		if err != nil {
			return
		}
		init, err := mtproto.ParseInit(first)
		if err != nil || init.Proto != mtproto.ProtoIntermediate {
			t.Errorf("unexpected init: %+v, %v", init, err)
			return
		}
		pkt, err := p.ReadBinary()
		if err != nil {
			return
		}
		init.Pair.Fwd.XORKeyStream(pkt, pkt)
		// intermediate: 4 байта длины, затем auth_key_id(8) message_id(8)
		// длина(4) конструктор(4) nonce(16).
		if len(pkt) != 44 || binary.LittleEndian.Uint32(pkt[24:28]) != 0xbe7e8ef1 {
			t.Errorf("not a req_pq_multi: % x", pkt)
			return
		}
		if silent {
			p.ReadBinary() // молчим до закрытия
			return
		}
		nonce := pkt[28:44]
		payload := make([]byte, 0, 40)
		payload = append(payload, make([]byte, 16)...) // auth_key_id и message_id
		payload = binary.LittleEndian.AppendUint32(payload, 20)
		payload = binary.LittleEndian.AppendUint32(payload, 0x05162463) // resPQ
		payload = append(payload, nonce...)
		reply := binary.LittleEndian.AppendUint32(nil, uint32(len(payload)))
		reply = append(reply, payload...)
		if mutate != nil {
			reply = mutate(reply)
		}
		init.Pair.Rev.XORKeyStream(reply, reply)
		p.WriteFrame(2, true, reply)
		p.ReadBinary()
	})
}

func TestCheckAcceptsARealLookingAnswer(t *testing.T) {
	srv := fakeTelegram(t, nil, false)
	if err := probe.Check(context.Background(), srv.Dialer("/apiws"), 2, false, 2*time.Second); err != nil {
		t.Fatalf("Check: %v", err)
	}
}

func TestCheckRejectsABrokenAnswer(t *testing.T) {
	cases := map[string]func([]byte) []byte{
		"nonce of someone else": func(r []byte) []byte { r[30] ^= 0xff; return r },
		"wrong constructor":     func(r []byte) []byte { r[24] ^= 0xff; return r },
		"too short":             func(r []byte) []byte { return r[:20] },
	}
	for name, mutate := range cases {
		srv := fakeTelegram(t, mutate, false)
		if err := probe.Check(context.Background(), srv.Dialer("/apiws"), 2, false, 2*time.Second); err == nil {
			t.Errorf("%s: Check must fail", name)
		}
	}
}

// Сервер принял init, но не ответил: это отдельный и важный случай (WebSocket
// открылся, а Telegram молчит), и он не должен висеть дольше срока.
func TestCheckTimesOutOnSilence(t *testing.T) {
	srv := fakeTelegram(t, nil, true)
	start := time.Now()
	err := probe.Check(context.Background(), srv.Dialer("/apiws"), 2, false, 400*time.Millisecond)
	if err == nil {
		t.Fatal("silence must be an error")
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("Check took %v, the timeout was ignored", time.Since(start))
	}
}

func TestCheckReportsAnUnreachableServer(t *testing.T) {
	srv := fakeTelegram(t, nil, false)
	d := srv.Dialer("/apiws")
	d.Addr = "127.0.0.1:1" // порт, на котором никто не слушает
	if err := probe.Check(context.Background(), d, 2, false, time.Second); err == nil {
		t.Fatal("an unreachable server must be an error")
	}
}

func TestResultJSONHasStableFields(t *testing.T) {
	b, err := probe.Marshal([]probe.Result{{Domain: "kws2.web.telegram.org", Target: "149.154.167.220", OK: true, Ms: 123}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"domain":"kws2.web.telegram.org"`, `"target":"149.154.167.220"`, `"ok":true`, `"ms":123`, `"error":""`} {
		if !bytes.Contains(b, []byte(want)) {
			t.Errorf("JSON %s lacks %s", b, want)
		}
	}
}
