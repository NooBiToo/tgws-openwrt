// Package mtproto разбирает и строит транспортный заголовок MTProto
// (obfuscated2) и режет поток на транспортные пакеты.
package mtproto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
)

// InitLen — длина обфусцированного заголовка соединения.
const InitLen = 64

const (
	keyStart = 8  // ключ AES-256: init[8:40]
	keyEnd   = 40 // iv: init[40:56]
	ivEnd    = 56
	protoPos = 56 // тег протокола и индекс DC лежат в зашифрованном хвосте
	dcPos    = 60
)

// Proto — транспорт MTProto, объявленный в init.
type Proto uint32

const (
	ProtoAbridged     Proto = 0xefefefef
	ProtoIntermediate Proto = 0xeeeeeeee
	ProtoPadded       Proto = 0xdddddddd
)

// ErrBadInit означает, что 64 байта не похожи на obfuscated2-заголовок.
var ErrBadInit = errors.New("mtproto: not an obfuscated2 init")

// Pair — два потока AES-CTR одного соединения. Fwd идёт от инициатора к
// ответчику, Rev — обратно. Кто из сторон «мы» — зависит от того, читаем мы
// чужой init (ParseInit: мы ответчик) или строим свой (NewRelayInit: мы
// инициатор). Ключи прямого направления берутся из init как есть, обратного —
// из тех же 48 байт, прочитанных задом наперёд.
type Pair struct {
	Fwd cipher.Stream
	Rev cipher.Stream
}

// Init — результат разбора заголовка клиента.
type Init struct {
	Proto Proto
	// DCIdx со знаком: отрицательный — media-соединение.
	DCIdx int16
	Pair  Pair
}

type identity struct{}

// XORKeyStream копирует src в dst: клиент без обфускации шлёт открытый поток.
func (identity) XORKeyStream(dst, src []byte) {
	if len(src) == 0 {
		return
	}
	if &dst[0] != &src[0] {
		copy(dst, src)
	}
}

// PlainPair — потоки для клиента, который не обфусцирует соединение.
func PlainPair() Pair { return Pair{Fwd: identity{}, Rev: identity{}} }

func newCTR(key, iv []byte) cipher.Stream {
	block, err := aes.NewCipher(key)
	if err != nil {
		// Длина ключа здесь всегда 32 байта, ошибка невозможна.
		panic(err)
	}
	return cipher.NewCTR(block, iv)
}

func pairFrom(raw []byte) Pair {
	fwd := newCTR(raw[keyStart:keyEnd], raw[keyEnd:ivEnd])
	var rev [ivEnd - keyStart]byte
	for i := range rev {
		rev[i] = raw[ivEnd-1-i]
	}
	return Pair{Fwd: fwd, Rev: newCTR(rev[:32], rev[32:48])}
}

// reserved сообщает, что начало init совпадает с сигнатурой другого протокола.
func reserved(b []byte) bool {
	if b[0] == 0xef {
		return true
	}
	switch binary.BigEndian.Uint32(b[:4]) {
	case 0x48454144, // HEAD
		0x504f5354, // POST
		0x47455420, // GET
		0xeeeeeeee,
		0xdddddddd,
		0x16030102: // TLS ClientHello
		return true
	}
	return b[4]|b[5]|b[6]|b[7] == 0
}

// ParseInit разбирает заголовок клиента, пришедшего без secret (прямое
// соединение с дата-центром). Поток Fwd возвращается уже продвинутым на
// 64 байта: дальше им расшифровываются данные после init.
func ParseInit(raw []byte) (Init, error) {
	if len(raw) != InitLen {
		return Init{}, ErrBadInit
	}
	p := pairFrom(raw)
	plain := make([]byte, InitLen)
	p.Fwd.XORKeyStream(plain, raw)
	proto := Proto(binary.LittleEndian.Uint32(plain[protoPos:]))
	switch proto {
	case ProtoAbridged, ProtoIntermediate, ProtoPadded:
	default:
		return Init{}, ErrBadInit
	}
	return Init{
		Proto: proto,
		DCIdx: int16(binary.LittleEndian.Uint16(plain[dcPos:])),
		Pair:  p,
	}, nil
}

// NewRelayInit строит заголовок для стороны Telegram: мы — инициатор.
// Возвращает 64 байта для отправки и потоки; Fwd уже продвинут на 64 байта.
func NewRelayInit(proto Proto, dcIdx int16) ([]byte, Pair) {
	var raw [InitLen]byte
	for {
		if _, err := rand.Read(raw[:]); err != nil {
			panic(err)
		}
		if !reserved(raw[:]) {
			break
		}
	}
	p := pairFrom(raw[:])

	// Открытым на проводе остаются первые 56 байт; хвост (тег протокола,
	// индекс DC, два случайных байта) идёт зашифрованным ТЕМ ЖЕ потоком на
	// позиции 56. Гамма хвоста получается шифрованием всего блока.
	enc := make([]byte, InitLen)
	p.Fwd.XORKeyStream(enc, raw[:])

	var tail [8]byte
	binary.LittleEndian.PutUint32(tail[0:4], uint32(proto))
	binary.LittleEndian.PutUint16(tail[4:6], uint16(dcIdx))
	if _, err := rand.Read(tail[6:8]); err != nil {
		panic(err)
	}
	for i := 0; i < 8; i++ {
		gamma := enc[protoPos+i] ^ raw[protoPos+i]
		raw[protoPos+i] = tail[i] ^ gamma
	}
	return append([]byte(nil), raw[:]...), p
}
