// Package dcmap знает, какому дата-центру Telegram принадлежит адрес, какие
// домены WebSocket ему соответствуют и по какому IP к ним ходить.
package dcmap

import (
	"fmt"
	"net/netip"
	"strconv"
	"strings"
)

// byIP — адреса, в чьей принадлежности мы уверены (список upstream
// Flowseal/tg-ws-proxy, DC_DEFAULT_IPS). Подсети не угадываются намеренно:
// DC1 и DC3 делят 149.154.175.0/24, DC2 и DC4 — 149.154.167.0/24, и ошибка в
// DC ломает вход в аккаунт сильнее, чем отказ от ускорения. Клиент, чей адрес
// здесь не найден, уходит прямым TCP, а адрес попадает в счётчики как
// неизвестный — по ним таблицу и пополняют.
var byIP = map[netip.Addr]int{
	netip.MustParseAddr("149.154.175.50"): 1,
	netip.MustParseAddr("149.154.167.51"): 2,
	// .41 и .50 добавлены по живой проверке (2026-10-03): Desktop называет в
	// init DC2 при подключении к ним, а Android в прямом соединении индекс DC
	// не заполняет и опирается только на эту таблицу.
	netip.MustParseAddr("149.154.167.41"):  2,
	netip.MustParseAddr("149.154.167.50"):  2,
	netip.MustParseAddr("149.154.175.100"): 3,
	netip.MustParseAddr("149.154.167.91"):  4,
	netip.MustParseAddr("149.154.171.5"):   5,
	netip.MustParseAddr("91.105.192.100"):  203,
}

// ByIP возвращает DC по адресу.
func ByIP(a netip.Addr) (int, bool) {
	dc, ok := byIP[a]
	return dc, ok
}

// Valid — поддерживаемый номер DC (203 — отдельный DC с общими доменами DC2).
func Valid(dc int) bool { return (dc >= 1 && dc <= 5) || dc == 203 }

// Domains — домены WebSocket в порядке попыток. Для media первым идёт
// вариант с суффиксом -1.
func Domains(dc int, media bool) []string {
	if dc == 203 {
		dc = 2
	}
	plain := fmt.Sprintf("kws%d.web.telegram.org", dc)
	alt := fmt.Sprintf("kws%d-1.web.telegram.org", dc)
	if media {
		return []string{alt, plain}
	}
	return []string{plain, alt}
}

// DefaultTargets — IP, на который открывается WebSocket, как в upstream:
// только DC2 и DC4; остальные уходят прямым TCP, пока не доказано обратное.
func DefaultTargets() map[int]string {
	return map[int]string{2: "149.154.167.220", 4: "149.154.167.220"}
}

// ParseTargets разбирает "2:149.154.167.220,4:149.154.167.220".
func ParseTargets(s string) (map[int]string, error) {
	out := map[int]string{}
	for _, item := range strings.Split(s, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		num, ip, ok := strings.Cut(item, ":")
		if !ok {
			return nil, fmt.Errorf("dc-ip %q: expected DC:IP", item)
		}
		dc, err := strconv.Atoi(strings.TrimSpace(num))
		if err != nil || !Valid(dc) {
			return nil, fmt.Errorf("dc-ip %q: unknown DC", item)
		}
		addr, err := netip.ParseAddr(strings.TrimSpace(ip))
		if err != nil || !addr.Is4() {
			return nil, fmt.Errorf("dc-ip %q: not an IPv4 address", item)
		}
		out[dc] = addr.String()
	}
	return out, nil
}
