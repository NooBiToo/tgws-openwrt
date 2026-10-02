#!/bin/sh
# Кросс-сборка tgws под архитектуры роутеров OpenWrt.
#   sh scripts/build.sh [каталог]     TGWS_VERSION=1.2.3 — версия в бинарнике
#
# Имена файлов (tgws-linux-<arch>) совпадают с тем, что выбирает install.sh
# по DISTRIB_ARCH. softfloat для mips обязателен: у многих mips-роутеров нет
# FPU, и бинарник с аппаратной плавающей точкой падает на первой же операции.
set -eu
cd "$(dirname "$0")/.."

out="${1:-dist}"
version="${TGWS_VERSION:-dev}"
max_kb="${TGWS_MAX_KB:-8192}"
mkdir -p "$out"

fail=0
build() {
	name="$1"; shift
	env CGO_ENABLED=0 GOOS=linux "$@" \
		go build -trimpath -ldflags "-s -w -X main.version=$version" \
		-o "$out/tgws-linux-$name" ./cmd/tgws
	kb=$(( $(wc -c < "$out/tgws-linux-$name") / 1024 ))
	echo "tgws-linux-$name: ${kb} KB"
	if [ "$kb" -gt "$max_kb" ]; then
		echo "error: tgws-linux-$name is ${kb} KB, the limit is ${max_kb} KB" >&2
		fail=1
	fi
}

build x86_64  GOARCH=amd64
build aarch64 GOARCH=arm64
build armv7   GOARCH=arm GOARM=7
build mips    GOARCH=mips GOMIPS=softfloat
build mipsel  GOARCH=mipsle GOMIPS=softfloat
exit "$fail"
