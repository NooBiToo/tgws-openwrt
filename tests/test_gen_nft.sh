#!/bin/sh
. "$(dirname "$0")/lib.sh"

GEN=packages/luci-app-tgws/root/usr/libexec/tgws/gen-nft

out="$(sh "$GEN" 5454 "br-lan")"
two="$(sh "$GEN" 5454 "br-lan br-guest")"

assert_contains "$out" "table inet tgws { }" "declares the table first, so delete is idempotent"
assert_contains "$out" "delete table inet tgws" "recreates the table"
assert_contains "$out" "set tg_v4 {" "has the telegram set"
assert_contains "$out" "auto-merge" "adjacent subnets merge instead of failing the whole file"
assert_contains "$out" "chain intercept {" "chain name is not an nft keyword"
assert_eq "0" "$(printf '%s\n' "$out" | grep -c 'chain redirect')" "redirect is a reserved word and cannot name a chain"
assert_contains "$out" "type nat hook prerouting priority dstnat" "nat chain at dstnat"
assert_contains "$out" 'iifname != { "br-lan" } return' "only LAN interfaces are intercepted"
assert_contains "$two" 'iifname != { "br-lan", "br-guest" } return' "several interfaces"
assert_contains "$out" "ip daddr @tg_v4 tcp dport { 80, 443, 5222 } redirect to :5454" "redirect rule"

for net in 91.105.192.0/23 91.108.4.0/22 91.108.8.0/22 91.108.12.0/22 \
		91.108.16.0/22 91.108.20.0/22 91.108.56.0/22 149.154.160.0/20 185.76.151.0/24; do
	assert_contains "$out" "$net" "set contains $net"
done

# Блок собран последовательными printf, и закрывающие скобки обязаны стоять
# на своих строках: nftables отвергает файл целиком от одной склеенной скобки.
open=$(printf '%s\n' "$out" | tr -cd '{' | wc -c)
close=$(printf '%s\n' "$out" | tr -cd '}' | wc -c)
assert_eq "$open" "$close" "braces are balanced"
assert_eq "0" "$(printf '%s\n' "$out" | grep -c ' ip6 ')" "ipv6 is not touched"

# Список подсетей можно обновлять (скрипт update-subnets кладёт его в файл):
# если файл есть и в нём есть годные строки, берётся он, иначе зашитый.
cat > "$TT_TEST_TMP/subnets.txt" <<'EOF'
# comment
149.154.160.0/20
203.0.113.0/24
not-a-subnet
999.1.1.0/24
10.0.0.0/8
EOF
upd="$(TGWS_SUBNETS="$TT_TEST_TMP/subnets.txt" sh "$GEN" 5454 br-lan)"
assert_contains "$upd" "203.0.113.0/24" "a subnet from the updated file is used"
assert_contains "$upd" "149.154.160.0/20" "the main range stays"
assert_eq "0" "$(printf '%s\n' "$upd" | grep -c '91.108.4.0/22')" "the built-in list is replaced, not merged"
assert_eq "0" "$(printf '%s\n' "$upd" | grep -c 'not-a-subnet\|999.1.1.0\|10.0.0.0/8')" "invalid and private lines are ignored"
open=$(printf '%s\n' "$upd" | tr -cd '{' | wc -c); close=$(printf '%s\n' "$upd" | tr -cd '}' | wc -c)
assert_eq "$open" "$close" "braces stay balanced with an updated list"

# Файл без единой годной строки (например, страница с ошибкой) не должен
# оставить таблицу без подсетей: берётся зашитый список.
printf '<html>error</html>\n' > "$TT_TEST_TMP/garbage.txt"
assert_contains "$(TGWS_SUBNETS="$TT_TEST_TMP/garbage.txt" sh "$GEN" 5454 br-lan)" "91.108.4.0/22" "a file with no valid subnet falls back to the built-in list"
assert_contains "$(TGWS_SUBNETS="$TT_TEST_TMP/missing.txt" sh "$GEN" 5454 br-lan)" "91.108.4.0/22" "a missing file falls back to the built-in list"

# --subnets печатает ту подсеть-за-строкой, которую gen-nft реально использует:
# интерфейс показывает по ней, зашитый список или обновлённый.
builtin_list="$(sh "$GEN" --subnets)"
assert_eq "9" "$(printf '%s\n' "$builtin_list" | wc -l | tr -d ' ')" "--subnets lists the built-in subnets, one per line"
assert_contains "$builtin_list" "149.154.160.0/20" "--subnets includes the main range"
assert_eq "2" "$(TGWS_SUBNETS="$TT_TEST_TMP/subnets.txt" sh "$GEN" --subnets | wc -l | tr -d ' ')" "--subnets reflects an updated file"
assert_eq "0" "$(sh "$GEN" --subnets | grep -c 'table\|redirect')" "--subnets prints no ruleset"

# Порт.
assert_exit 2 "port 0 is rejected" sh "$GEN" 0 br-lan
assert_exit 2 "port 70000 is rejected" sh "$GEN" 70000 br-lan
assert_exit 2 "a non-numeric port is rejected" sh "$GEN" abc br-lan
assert_exit 2 "a missing port is rejected" sh "$GEN"

# Имена интерфейсов попадают в nft в кавычках, поэтому кавычка или `;`
# в значении из UCI позволила бы вставить в ruleset чужое правило.
assert_exit 2 "a quote in an interface name is rejected" sh "$GEN" 5454 'br-lan" ; drop'
assert_exit 2 "a semicolon in an interface name is rejected" sh "$GEN" 5454 'br-lan;'
assert_exit 0 "dots, dashes and digits are fine" sh "$GEN" 5454 'br-lan.10 eth0-1'

# Без аргумента интерфейс по умолчанию — br-lan.
assert_contains "$(sh "$GEN" 5454)" 'iifname != { "br-lan" } return' "default interface"

tt_test_summary
