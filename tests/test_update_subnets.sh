#!/bin/sh
# update-subnets: скачивает официальный список подсетей Telegram, проверяет и
# кладёт в файл. Список уходит в правила файрвола, поэтому проверка строгая:
# что бы ни пришло по сети, в файл попадают только публичные IPv4-подсети.
. "$(dirname "$0")/lib.sh"

UPD=packages/luci-app-tgws/root/usr/libexec/tgws/update-subnets

sandbox="$TT_TEST_TMP/upd"
mkdir -p "$sandbox/bin"
OUT="$sandbox/etc/subnets.txt"

# Заглушка curl отдаёт содержимое файла $FAKE_BODY или завершается ошибкой.
cat > "$sandbox/bin/curl" <<'EOF'
#!/bin/sh
echo "curl $*" >> "$CALLS"
[ "${FAKE_CURL_RC:-0}" = "0" ] || exit "$FAKE_CURL_RC"
# Путь для записи — аргумент после -o.
out=""
while [ $# -gt 0 ]; do
	[ "$1" = "-o" ] && out="$2"
	shift
done
cp "$FAKE_BODY" "$out"
EOF
chmod +x "$sandbox/bin/curl"
CALLS="$sandbox/calls"; export CALLS

run() {
	: > "$CALLS"
	TGWS_CURL="$sandbox/bin/curl" TGWS_SUBNETS="$OUT" FAKE_BODY="$1" FAKE_CURL_RC="${2:-0}" \
		sh "$UPD" 2>"$sandbox/err"
}

good="$sandbox/good.txt"
cat > "$good" <<'EOF'
91.105.192.0/23
91.108.4.0/22
91.108.8.0/22
149.154.160.0/20
185.76.151.0/24
2001:67c:4e8::/48
2a0a:f280::/32
EOF

# --- нормальное обновление ---------------------------------------------------
rm -f "$OUT"
res=$(run "$good"); rc=$?
assert_eq "0" "$rc" "a valid list is accepted"
assert_contains "$res" "updated 5" "reports the number of IPv4 subnets"
assert_eq "5" "$(wc -l < "$OUT" | tr -d ' ')" "IPv4 subnets only: IPv6 lines are not written"
assert_contains "$(cat "$OUT")" "149.154.160.0/20" "the file has the subnets"
assert_eq "0" "$(grep -c ':' "$OUT")" "no IPv6 in the file"
assert_contains "$(cat "$sandbox/calls")" "-4" "curl is forced to IPv4"

# --- повтор без изменений -----------------------------------------------------
res=$(run "$good"); rc=$?
assert_eq "0" "$rc" "an unchanged list is not an error"
assert_contains "$res" "unchanged" "an unchanged list is reported as unchanged"

# --- отказы: файл не должен измениться ----------------------------------------
before=$(cat "$OUT")

res=$(run "$good" 22); rc=$?
assert_eq "1" "$rc" "a failed download fails"
assert_eq "$before" "$(cat "$OUT")" "a failed download keeps the previous list"

printf '<html><body>502 Bad Gateway</body></html>\n' > "$sandbox/html.txt"
res=$(run "$sandbox/html.txt"); rc=$?
assert_eq "1" "$rc" "an error page is rejected"
assert_eq "$before" "$(cat "$OUT")" "an error page keeps the previous list"

# Слишком короткий список: так выглядит усечённая или подменённая выдача.
printf '149.154.160.0/20\n' > "$sandbox/short.txt"
res=$(run "$sandbox/short.txt"); rc=$?
assert_eq "1" "$rc" "an implausibly short list is rejected"
assert_eq "$before" "$(cat "$OUT")" "a short list keeps the previous list"

# Опасное содержимое не должно попасть в правила файрвола ни при каких условиях.
cat > "$sandbox/evil.txt" <<'EOF'
91.108.4.0/22
91.108.8.0/22
149.154.160.0/20
0.0.0.0/0
10.0.0.0/8
192.168.0.0/16
172.16.0.0/12
127.0.0.0/8
169.254.0.0/16
100.64.0.0/10
224.0.0.0/4
8.0.0.0/8
300.1.1.0/24
1.2.3.4/33
1.2.3.0/24; drop
EOF
res=$(run "$sandbox/evil.txt"); rc=$?
assert_eq "0" "$rc" "the valid lines of a mixed list are still taken"
got=$(cat "$OUT")
for bad in 0.0.0.0/0 10.0.0.0 192.168 172.16 127.0.0 169.254 100.64 224.0.0 8.0.0.0/8 300.1 /33 drop; do
	assert_eq "0" "$(printf '%s\n' "$got" | grep -c -F "$bad")" "dangerous or invalid '$bad' never reaches the file"
done
assert_eq "3" "$(printf '%s\n' "$got" | wc -l | tr -d ' ')" "only the three valid public subnets remain"

# Слишком большая подсеть (короче /16) — не Telegram, а попытка перехватить
# половину интернета.
cat > "$sandbox/big.txt" <<'EOF'
91.108.4.0/22
91.108.8.0/22
149.154.160.0/20
64.0.0.0/2
EOF
res=$(run "$sandbox/big.txt")
assert_eq "0" "$(grep -c '^64.0.0.0' "$OUT")" "a huge subnet is never accepted"

tt_test_summary
