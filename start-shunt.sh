#!/bin/sh
# Build (when Go is available), initialize, and run the relay on any POSIX host.
set -eu

cd "$(dirname "$0")"
BIN=./shunt

if "$BIN" version >/dev/null 2>&1; then
	:
elif command -v go >/dev/null 2>&1; then
	echo "[shunt] building $BIN..."
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "$BIN" ./cmd/shunt
elif [ -x "$BIN" ]; then
	echo "[shunt] Go is not installed; using the existing $BIN binary."
else
	echo "[shunt] $BIN is missing and Go is not installed." >&2
	echo "Install Go 1.23+ from https://go.dev/dl/ and run this script again." >&2
	exit 1
fi

if ! "$BIN" config validate >/dev/null 2>&1; then
	echo "[shunt] first run: creating the default configuration..."
	"$BIN" init
fi

if "$BIN" ui >/dev/null 2>&1; then
	echo "[shunt] relay already running; opening the dashboard."
	"$BIN" ui --open || true
	exit 0
fi

echo "[shunt] starting the relay (Ctrl+C stops it)..."
if [ "${1:-}" = "--open-ui" ]; then
	(
		i=0
		while [ "$i" -lt 25 ]; do
			if "$BIN" ui --open >/dev/null 2>&1; then
				exit 0
			fi
			i=$((i + 1))
			sleep 0.2
		done
	) &
fi

exec "$BIN" serve
