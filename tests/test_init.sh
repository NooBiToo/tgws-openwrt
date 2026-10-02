#!/bin/sh
# Init-скрипт tgws на заглушках: procd, uci, nft и демона на машине
# разработчика нет, судить приходится по журналу вызовов.
. "$(dirname "$0")/lib.sh"

INIT="packages/luci-app-tgws/root/etc/init.d/tgws"
GEN_DIR="packages/luci-app-tgws/root/usr/libexec/tgws"

sandbox="$TT_TEST_TMP/sandbox"
bin="$sandbox/bin"
run="$sandbox/run"
mkdir -p "$bin" "$run"

CALLS="$sandbox/calls"
export CALLS
: > "$CALLS"

# Заглушка nft: журналирует вызов и сохраняет файл правил.
cat > "$bin/nft" <<'EOF'
#!/bin/sh
echo "nft $*" >> "$CALLS"
if [ "$1" = "-f" ]; then cp "$2" "$CALLS.ruleset"; fi
exit "${NFT_RC:-0}"
EOF
chmod +x "$bin/nft"
# Заглушка демона. Проверка по -x, а на Windows (Git Bash) пустой файл её не
# проходит, поэтому нужен shebang. Код возврата задаёт TGWS_CHECK_RC: так
# имитируется отказ `tgws -check` на недопустимых целях WebSocket.
printf '#!/bin/sh\nexit "${TGWS_CHECK_RC:-0}"\n' > "$bin/tgws"; chmod +x "$bin/tgws"

PATH="$bin:$PATH"; export PATH

# shellcheck disable=SC1090
. "$INIT"

BIN="$bin/tgws"
NFT="$bin/nft"
RUN="$run"
LIBDIR="$GEN_DIR"
STATS="$run/tgws.json"
RULESET="$run/tgws.nft"

# Заглушки библиотек OpenWrt. Значения UCI задаются переменными U_*.
config_load() { :; }
config_get_bool() { eval "$1=\${U_ENABLED:-0}"; }
config_get() {
	case "$3" in
		port) eval "$1=\${U_PORT:-$4}" ;;
		dc_ip) eval "$1=\${U_DC_IP:-$4}" ;;
		lan_devices) eval "$1=\${U_LAN:-$4}" ;;
		*) eval "$1=\"$4\"" ;;
	esac
}
procd_open_instance() { echo "procd_open_instance $*" >> "$CALLS"; }
procd_set_param() { echo "procd_set_param $*" >> "$CALLS"; }
procd_close_instance() { echo "procd_close_instance" >> "$CALLS"; }
procd_add_reload_trigger() { echo "procd_add_reload_trigger $*" >> "$CALLS"; }
logger() { echo "logger $*" >> "$CALLS"; }
uci() { return 1; }
# Ожидание готовности не должно тормозить тесты.
sleep() { :; }

# Демон «слушает», когда есть его файл счётчиков: он пишет его сразу после
# bind. Сам wait_listening не подменяется — проверяется настоящий.
daemon_up() { echo '{}' > "$STATS"; }

# reset возвращает заглушки в исходное состояние. Настройки задаются обычными
# присваиваниями, а не префиксом перед вызовом функции: у функций поведение
# такого префикса зависит от оболочки, и значения протекали бы в следующий случай.
reset() {
	: > "$CALLS"
	rm -f "$CALLS.ruleset" "$STATS"
	U_ENABLED=0; U_PORT=; U_DC_IP=; U_LAN=
	NFT_RC=0; export NFT_RC
	TGWS_CHECK_RC=0; export TGWS_CHECK_RC
}
calls() { cat "$CALLS"; }

# --- выключено ------------------------------------------------------------
reset
start_service; rc=$?
assert_eq "0" "$rc" "disabled: start_service succeeds"
assert_eq "0" "$(calls | grep -c procd_open_instance)" "disabled: no instance"
# procd убивает работающий экземпляр, когда start не открыл ни одного, а
# загруженная таблица осталась бы и перенаправляла в закрытый порт.
assert_contains "$(calls)" "nft delete table inet tgws" "disabled: a loaded table is removed"

# --- демона нет -------------------------------------------------------------
reset
BIN="$sandbox/missing"
U_ENABLED=1
start_service; rc=$?
assert_eq "1" "$rc" "missing binary: start_service fails"
assert_contains "$(calls)" "run install.sh" "missing binary: says what to do"
assert_contains "$(calls)" "nft delete table inet tgws" "missing binary: a loaded table is removed"
BIN="$bin/tgws"

# --- обычный запуск ---------------------------------------------------------
reset
U_ENABLED=1
start_service; rc=$?
assert_eq "0" "$rc" "enabled: start_service succeeds"
assert_contains "$(calls)" "procd_open_instance tgws" "enabled: opens the instance"
assert_contains "$(calls)" "-listen :5454" "enabled: default port"
assert_contains "$(calls)" "-dc-ip 2:149.154.167.220,4:149.154.167.220" "enabled: default DC targets"
assert_contains "$(calls)" "-mark 0x7467" "enabled: socket mark"
assert_contains "$(calls)" "-stats $STATS" "enabled: stats file"
assert_eq "0" "$(calls | grep -c '^nft ')" "enabled: nft is NOT loaded before the daemon listens"

# --- свой порт и недопустимые значения -------------------------------------
reset
U_ENABLED=1; U_PORT=6000
start_service
assert_contains "$(calls)" "-listen :6000" "custom port"

reset
U_ENABLED=1; U_PORT=abc
start_service
assert_contains "$(calls)" "-listen :5454" "bad port falls back to the default"

reset
U_ENABLED=1; U_PORT=70000
start_service
assert_contains "$(calls)" "-listen :5454" "out-of-range port falls back to the default"

# Ведущие нули: 06000 — это порт 6000, а не восьмеричное число и не отказ.
reset
U_ENABLED=1; U_PORT=06000
start_service
assert_contains "$(calls)" "-listen :6000" "leading zeros in the port are stripped"

reset
U_ENABLED=1; U_PORT=000
start_service
assert_contains "$(calls)" "-listen :5454" "an all-zero port falls back to the default"

reset
U_ENABLED=1; U_DC_IP='2:1.2.3.4;rm'
start_service; rc=$?
assert_eq "1" "$rc" "bad dc_ip: start_service fails"
assert_eq "0" "$(calls | grep -c procd_open_instance)" "bad dc_ip: no instance"
assert_contains "$(calls)" "nft delete table inet tgws" "bad dc_ip: a loaded table is removed"

# Допустимые символы, но не допустимый смысл (DC 7 не существует): без проверки
# демон завершался бы при запуске, а procd перезапускал бы его каждые 5 секунд.
# Разбор целей делает сам демон (tgws -check), чтобы правило было одно.
reset
U_ENABLED=1; U_DC_IP='7:1.2.3.4'; TGWS_CHECK_RC=2; export TGWS_CHECK_RC
start_service; rc=$?
assert_eq "1" "$rc" "dc_ip the daemon rejects: start_service fails"
assert_eq "0" "$(calls | grep -c procd_open_instance)" "dc_ip the daemon rejects: no instance, no respawn loop"
assert_contains "$(calls)" "nft delete table inet tgws" "dc_ip the daemon rejects: a loaded table is removed"

# LuCI удаляет опцию, когда поле очищено, а init подставляет значение по
# умолчанию, поэтому «только прямой TCP» выражается явным словом none.
reset
U_ENABLED=1; U_DC_IP=none
start_service; rc=$?
assert_eq "0" "$rc" "dc_ip none: start_service succeeds"
assert_contains "$(calls)" "-dc-ip  -mark 0x7467" "dc_ip none: the daemon gets an empty target list"

# --- устаревшие счётчики -----------------------------------------------------
# Файл прошлого экземпляра иначе сошёл бы за признак готовности нового и
# показывал бы в LuCI чужие цифры.
reset
U_ENABLED=1
daemon_up
start_service
assert_eq "no" "$([ -e "$STATS" ] && echo yes || echo no)" "start removes the previous instance's stats file"

# --- таблица грузится только когда демон слушает ----------------------------
reset
U_ENABLED=1; daemon_up
service_started
assert_contains "$(calls)" "nft -f" "listening: nft is loaded"
assert_contains "$(cat "$CALLS.ruleset")" "redirect to :5454" "listening: ruleset redirects to the port"
assert_contains "$(cat "$CALLS.ruleset")" 'iifname != { "br-lan" } return' "listening: default interface"

# Порт занят чужим процессом: демон упал на bind и файла счётчиков нет. Прежняя
# проверка по netstat приняла бы чужой процесс за демона и загрузила таблицу.
reset
U_ENABLED=1
service_started
assert_eq "0" "$(calls | grep -c '^nft ')" "not listening: nft is NOT loaded, Telegram stays untouched"
assert_contains "$(calls)" "not listening" "not listening: logged"

reset
service_started
assert_eq "0" "$(calls | grep -c '^nft ')" "disabled: service_started does nothing"

# --- интерфейсы из UCI -------------------------------------------------------
reset
U_ENABLED=1; U_LAN="br-lan br-guest"; daemon_up
service_started
assert_contains "$(cat "$CALLS.ruleset")" '"br-lan", "br-guest"' "several LAN interfaces"

reset
U_ENABLED=1; U_LAN='br-lan" ; drop'; daemon_up
service_started
assert_eq "0" "$(calls | grep -c '^nft ')" "a hostile interface name never reaches nft"
assert_contains "$(calls)" "cannot generate the ruleset" "a hostile interface name is reported"

# --- сбой nft ----------------------------------------------------------------
reset
U_ENABLED=1; NFT_RC=1; export NFT_RC; daemon_up
service_started
assert_contains "$(calls)" "nft rejected" "nft failure is logged"

# --- остановка ---------------------------------------------------------------
reset
daemon_up
stop_service
assert_contains "$(calls)" "nft delete table inet tgws" "stop removes the table"
# После остановки LuCI не должен показывать счётчики давно умершего процесса.
assert_eq "no" "$([ -e "$STATS" ] && echo yes || echo no)" "stop removes the stats file"

# --- триггер перезагрузки ----------------------------------------------------
reset
service_triggers
assert_contains "$(calls)" "procd_add_reload_trigger tgws" "reload on config change"

tt_test_summary
