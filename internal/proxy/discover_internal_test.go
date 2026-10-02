package proxy

import (
	"testing"

	"tgws/internal/mtproto"
)

func TestIsWrongDCError(t *testing.T) {
	cases := []struct {
		name  string
		proto mtproto.Proto
		plain []byte
		want  bool
	}{
		{"abridged -404", mtproto.ProtoAbridged, []byte{0x01, 0x6c, 0xfe, 0xff, 0xff}, true},
		{"intermediate -404", mtproto.ProtoIntermediate, []byte{4, 0, 0, 0, 0x6c, 0xfe, 0xff, 0xff}, true},
		{"padded -404", mtproto.ProtoPadded, []byte{4, 0, 0, 0, 0x6c, 0xfe, 0xff, 0xff}, true},
		// -444 («неверный DC») — тоже «не тот DC».
		{"abridged -444", mtproto.ProtoAbridged, []byte{0x01, 0x44, 0xfe, 0xff, 0xff}, true},
		// Другие транспортные ошибки (например, -429, слишком много запросов)
		// не говорят о неверном DC: перебор по ним продолжаться не должен.
		{"abridged -429", mtproto.ProtoAbridged, []byte{0x01, 0x5b, 0xfe, 0xff, 0xff}, false},
		{"ordinary reply", mtproto.ProtoAbridged, []byte{0x01, 'p', 'o', 'n', 'g'}, false},
		{"long reply", mtproto.ProtoIntermediate, make([]byte, 64), false},
		{"empty", mtproto.ProtoAbridged, nil, false},
	}
	for _, tc := range cases {
		if got := isWrongDCError(tc.proto, tc.plain); got != tc.want {
			t.Errorf("%s: isWrongDCError = %v, want %v", tc.name, got, tc.want)
		}
	}
}
