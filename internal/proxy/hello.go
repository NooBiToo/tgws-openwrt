package proxy

import (
	"encoding/binary"
	"errors"
	"io"

	"tgws/internal/mtproto"
)

// hello — что клиент сказал первыми байтами соединения.
//
// consumed хранит ВСЕ прочитанные байты, даже когда разбор не удался: при
// откате на прямой TCP они повторяются дословно, и клиент не замечает, что его
// слушал посредник.
type hello struct {
	consumed []byte
	proto    mtproto.Proto
	pair     mtproto.Pair
	dcIdx    int16
	hasDC    bool
	err      error
}

var errNotMTProto = errors.New("proxy: not MTProto")

// looksLikeHTTP сообщает, что четыре байта — начало метода HTTP.
func looksLikeHTTP(b []byte) bool {
	switch string(b) {
	case "GET ", "POST", "HEAD", "PUT ", "DELE", "OPTI", "PATC", "CONN":
		return true
	}
	return false
}

// readHello читает ровно столько, сколько нужно для определения транспорта:
// байт 0xef (abridged без обфускации), тег из четырёх байт (intermediate или
// padded) либо 64 байта obfuscated2. Больше ни байта: остальное остаётся в
// сокете для моста или отката.
func readHello(r io.Reader) hello {
	var h hello
	buf := make([]byte, mtproto.InitLen)

	if _, err := io.ReadFull(r, buf[:1]); err != nil {
		h.err = err
		return h
	}
	h.consumed = buf[:1]
	// 0xef недопустим как первый байт obfuscated2-заголовка, поэтому он
	// однозначно означает открытый abridged.
	if buf[0] == 0xef {
		h.proto, h.pair = mtproto.ProtoAbridged, mtproto.PlainPair()
		return h
	}

	n, err := io.ReadFull(r, buf[1:4])
	h.consumed = buf[:1+n]
	if err != nil {
		h.err = err
		return h
	}
	// Короткий HTTP-запрос (редирект цепляет и сайт telegram.org) не дотянет
	// до 64 байт, а клиент ждёт ответа: читать дальше значило бы держать его
	// до HelloTimeout. Метод узнаётся по четырём байтам, остальное остаётся в
	// сокете для отката. Obfuscated2-заголовок так начаться может лишь с
	// вероятностью 2^-32, и такое соединение просто уйдёт напрямую.
	if looksLikeHTTP(buf[:4]) {
		h.err = errNotMTProto
		return h
	}
	switch binary.BigEndian.Uint32(buf[:4]) {
	case 0xeeeeeeee:
		h.proto, h.pair = mtproto.ProtoIntermediate, mtproto.PlainPair()
		return h
	case 0xdddddddd:
		h.proto, h.pair = mtproto.ProtoPadded, mtproto.PlainPair()
		return h
	}

	n, err = io.ReadFull(r, buf[4:])
	h.consumed = buf[:4+n]
	if err != nil {
		h.err = err
		return h
	}
	init, perr := mtproto.ParseInit(buf)
	if perr != nil {
		h.err = perr
		return h
	}
	h.proto, h.pair, h.dcIdx, h.hasDC = init.Proto, init.Pair, init.DCIdx, true
	return h
}
