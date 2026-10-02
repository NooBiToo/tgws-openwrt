// Команда tgprobe проверяет путь «клиент → WebSocket → Telegram» без настоящего
// клиента Telegram: проходит тем же путём, что и демон (obfuscated2-заголовок,
// AES-CTR, кадры WebSocket), отправляет незашифрованный запрос req_pq_multi и
// ждёт resPQ с тем же nonce. Ответ означает, что Telegram принял и наш init,
// и перешифрованный поток.
//
// Запускать там, где есть прямой доступ к IP дата-центра, например на роутере.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"flag"
	"fmt"
	"os"
	"time"

	"tgws/internal/dcmap"
	"tgws/internal/mtproto"
	"tgws/internal/ws"
)

func main() {
	dc := flag.Int("dc", 2, "номер дата-центра")
	media := flag.Bool("media", false, "media-соединение (отрицательный индекс DC)")
	target := flag.String("target", "149.154.167.220", "IP, на который открывать WebSocket")
	flag.Parse()

	failed := false
	for _, domain := range dcmap.Domains(*dc, *media) {
		if err := probe(domain, *target, *dc, *media); err != nil {
			fmt.Printf("FAIL %s via %s: %v\n", domain, *target, err)
			failed = true
			continue
		}
		fmt.Printf("OK   %s via %s: resPQ received, nonce matches\n", domain, *target)
	}
	if failed {
		os.Exit(1)
	}
}

func probe(domain, target string, dc int, media bool) error {
	d := ws.Dialer{Addr: target + ":443", ServerName: domain, Path: "/apiws", Timeout: 8 * time.Second}
	conn, err := d.Dial(context.Background())
	if err != nil {
		return fmt.Errorf("websocket: %w", err)
	}
	defer conn.Close()

	idx := int16(dc)
	if media {
		idx = -idx
	}
	init, pair := mtproto.NewRelayInit(mtproto.ProtoIntermediate, idx)
	if err := conn.Send(init); err != nil {
		return fmt.Errorf("send init: %w", err)
	}

	// req_pq_multi без шифрования: auth_key_id=0, message_id, длина, конструктор, nonce.
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	body := make([]byte, 0, 20)
	body = binary.LittleEndian.AppendUint32(body, 0xbe7e8ef1)
	body = append(body, nonce...)
	msg := make([]byte, 0, 40)
	msg = append(msg, make([]byte, 8)...) // auth_key_id
	msg = binary.LittleEndian.AppendUint64(msg, uint64(time.Now().Unix())<<32)
	msg = binary.LittleEndian.AppendUint32(msg, uint32(len(body)))
	msg = append(msg, body...)
	pkt := binary.LittleEndian.AppendUint32(nil, uint32(len(msg))) // intermediate
	pkt = append(pkt, msg...)
	pair.Fwd.XORKeyStream(pkt, pkt)
	if err := conn.Send(pkt); err != nil {
		return fmt.Errorf("send req_pq_multi: %w", err)
	}

	type result struct {
		data []byte
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		var all []byte
		for len(all) < 4+8+8+4+4+16 {
			m, err := conn.Recv()
			if err != nil {
				ch <- result{err: err}
				return
			}
			pair.Rev.XORKeyStream(m, m)
			all = append(all, m...)
		}
		ch <- result{data: all}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			return fmt.Errorf("receive: %w", r.err)
		}
		// 4 (длина) + 8 (auth_key_id) + 8 (message_id) + 4 (длина) = 24, дальше конструктор.
		if len(r.data) < 24+4+16 {
			return fmt.Errorf("short answer: % x", r.data)
		}
		if c := binary.LittleEndian.Uint32(r.data[24:28]); c != 0x05162463 {
			return fmt.Errorf("unexpected constructor %#x, answer % x", c, r.data)
		}
		if !bytes.Equal(r.data[28:44], nonce) {
			return fmt.Errorf("nonce mismatch")
		}
		return nil
	case <-time.After(8 * time.Second):
		return fmt.Errorf("no answer within 8s (init accepted by the server, but nothing came back)")
	}
}
