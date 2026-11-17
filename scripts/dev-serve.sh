#!/bin/sh
#
#   Runs the daemon against the fake system and the fake sshd, for the pages' development and the
#   Playwright tests: the fake system on 127.0.0.1:18082, the fake sshd on 127.0.0.1:18022 (root's
#   password "root-pass"; its SFTP serves a scratch directory seeded like a small system), the
#   pages on http://127.0.0.1:18290/addons/shell/ - without the system's session check and over
#   plain HTTP, which the daemon offers for development only. The watch runs every second and a
#   closed tab's grace is five seconds, so a test does not wait. A fresh configuration directory
#   each run unless SHELL_CONF_DIR names one. The pages are served from ui/dist (npm run build
#   first).
#
set -e
cd "$(dirname "$0")/.."
DIR=${SHELL_CONF_DIR:-$(mktemp -d)}
mkdir -p build "$DIR/conf"
go build -o build/fakesystem ./cmd/fakesystem
go build -o build/shell-dev ./cmd/lite-shell
echo dev > "$DIR/token"
build/fakesystem -addr 127.0.0.1:18082 -sshd 127.0.0.1:18022 -host-key "$DIR/ssh_host_ed25519_key.pub" -files "$DIR/root" &
FAKE=$!
sleep 0.5
build/shell-dev -system http://127.0.0.1:18082 -notices http://127.0.0.1:18082 -control-token "$DIR/token" \
    -no-session-check -insecure-http -port 18290 -conf-dir "$DIR/conf" -addon-dir "$DIR" -www ui/dist \
    -ssh 127.0.0.1:18022 -host-keys "$DIR/ssh_host_*_key.pub" -watch-every 1s -grace 5s &
DAEMON=$!
trap 'kill $DAEMON $FAKE 2>/dev/null' EXIT INT TERM
wait $DAEMON
