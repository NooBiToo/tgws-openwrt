// Package probe проверяет путь «клиент → WebSocket → Telegram» без настоящего
// клиента: проходит тем же путём, что и демон (obfuscated2-заголовок, AES-CTR,
// кадры WebSocket), отправляет незашифрованный req_pq_multi и ждёт resPQ с тем
// же nonce. Ответ означает, что Telegram принял и наш init, и перешифрованный
// поток; всё меньшее (открылся WebSocket, но молчит сервер) — отдельные отказы.
//
// Используется диагностикой в LuCI через `tgws -probe`.
package probe

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"

	"tgws/internal/dcmap"
	"tgws/internal/mtproto"
	"tgws/internal/ws"
)

// Result — итог проверки одного домена. Поля стабильны: их читает бэкенд LuCI.
type Result struct {
	Domain string `json:"domain"`
	Target string `json:"target"`
	OK     bool   `json:"ok"`
	Ms     int64  `json:"ms"`
	Error  string `json:"error"`
}

// Marshal сериализует итоги в JSON для бэкенда LuCI.
func Marshal(r []Result) ([]byte, error) { return json.Marshal(r) }

// Run проверяет все домены DC на указанном IP и возвращает итог по каждому.
func Run(ctx context.Context, target string, dc int, media bool, timeout time.Duration) []Result {
	var out []Result
	for _, domain := range dcmap.Domains(dc, media) {
		d := ws.Dialer{
			Addr:       net.JoinHostPort(target, "443"),
			ServerName: domain,
			Path:       "/apiws",
			Timeout:    timeout,
		}
		start := time.Now()
		err := Check(ctx, d, dc, media, timeout)
		r := Result{Domain: domain, Target: target, OK: err == nil, Ms: time.Since(start).Milliseconds()}
		if err != nil {
			r.Error = err.Error()
		}
		out = append(out, r)
	}
	return out
}

// Check открывает WebSocket через d и проверяет, что Telegram отвечает.
// timeout ограничивает ожидание ответа после открытия.
func Check(ctx context.Context, d ws.Dialer, dc int, media bool, timeout time.Duration) error {
	conn, err := d.Dial(ctx)
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
	body := binary.LittleEndian.AppendUint32(make([]byte, 0, 20), 0xbe7e8ef1)
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
		if c := binary.LittleEndian.Uint32(r.data[24:28]); c != 0x05162463 {
			return fmt.Errorf("unexpected constructor %#x", c)
		}
		if !bytes.Equal(r.data[28:44], nonce) {
			return errors.New("nonce mismatch")
		}
		return nil
	case <-time.After(timeout):
		// Закрытие соединения обрывает читающую горутину.
		_ = conn.Close()
		return errors.New("no answer from Telegram within the timeout")
	}
}
