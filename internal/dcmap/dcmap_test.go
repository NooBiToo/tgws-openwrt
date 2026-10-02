package dcmap

import (
	"net/netip"
	"reflect"
	"testing"
)

func TestByIP(t *testing.T) {
	cases := map[string]int{
		"149.154.175.50": 1,
		"149.154.167.51": 2,
		// Адреса из живой проверки: Desktop, который пишет в init настоящий
		// индекс DC, подключался к ним с dc=2 и работал. Android же индекс в
		// прямом соединении не заполняет (там мусор), и DC для него берётся
		// только отсюда.
		"149.154.167.41":  2,
		"149.154.167.50":  2,
		"149.154.175.100": 3,
		"149.154.167.91":  4,
		"149.154.171.5":   5,
		"91.105.192.100":  203,
	}
	for ip, want := range cases {
		got, ok := ByIP(netip.MustParseAddr(ip))
		if !ok || got != want {
			t.Errorf("ByIP(%s) = %d, %v; want %d", ip, got, ok, want)
		}
	}
	// Соседний адрес из той же подсети НЕ угадывается: неверный DC хуже отказа.
	if _, ok := ByIP(netip.MustParseAddr("149.154.167.52")); ok {
		t.Error("an address missing from the table must not be guessed")
	}
}

func TestValid(t *testing.T) {
	for _, dc := range []int{1, 2, 3, 4, 5, 203} {
		if !Valid(dc) {
			t.Errorf("Valid(%d) = false", dc)
		}
	}
	for _, dc := range []int{-1, 0, 6, 10002, 204} {
		if Valid(dc) {
			t.Errorf("Valid(%d) = true", dc)
		}
	}
}

func TestDomainsOrder(t *testing.T) {
	if got, want := Domains(2, false), []string{"kws2.web.telegram.org", "kws2-1.web.telegram.org"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Domains(2,false) = %v", got)
	}
	if got, want := Domains(2, true), []string{"kws2-1.web.telegram.org", "kws2.web.telegram.org"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Domains(2,true) = %v", got)
	}
	// DC 203 обслуживается теми же доменами, что и DC 2.
	if got := Domains(203, false)[0]; got != "kws2.web.telegram.org" {
		t.Errorf("Domains(203)[0] = %s", got)
	}
}

func TestParseTargets(t *testing.T) {
	got, err := ParseTargets("2:149.154.167.220, 4:149.154.167.220")
	if err != nil {
		t.Fatal(err)
	}
	want := map[int]string{2: "149.154.167.220", 4: "149.154.167.220"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v", got)
	}
	if m, err := ParseTargets(""); err != nil || len(m) != 0 {
		t.Errorf("empty input: %v, %v", m, err)
	}
	for _, bad := range []string{"2", "x:1.2.3.4", "9:1.2.3.4", "2:not-an-ip", "2:::1"} {
		if _, err := ParseTargets(bad); err == nil {
			t.Errorf("ParseTargets(%q) must fail", bad)
		}
	}
}

func TestDefaultTargets(t *testing.T) {
	want := map[int]string{2: "149.154.167.220", 4: "149.154.167.220"}
	if !reflect.DeepEqual(DefaultTargets(), want) {
		t.Errorf("DefaultTargets() = %v", DefaultTargets())
	}
}
