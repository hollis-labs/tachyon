#!/bin/sh
# Starts the next/ binary for the browser suite (playwright.config.ts). Everything arrives through the
# environment, never spliced into a command line:
#   E2E_BIN   the binary;  E2E_DIR  a directory to create inside the marked run directory;
#   E2E_ADDR  host:port to listen on;  E2E_OWNER  "1" to create the owner offline first;
#   E2E_TETHER  optional URL of the suite's local Tether fake (e2e/tether-fake.ts), with chat sync on.
# It only ever creates E2E_DIR (which must not exist yet) and deletes nothing.
set -eu
: "${E2E_BIN:?}" "${E2E_DIR:?}" "${E2E_ADDR:?}"
case "$E2E_DIR" in
  /*) ;;
  *) echo "serve.sh: E2E_DIR must be absolute: $E2E_DIR" >&2; exit 2 ;;
esac
run=$(dirname "$E2E_DIR")
if [ ! -f "$run/.tachyon-e2e-run" ]; then
  echo "serve.sh: $run is not a run directory this suite created" >&2
  exit 2
fi
mkdir "$E2E_DIR"
mkdir "$E2E_DIR/data" "$E2E_DIR/profile"
printf 'httphost:\n  addr: "%s"\n' "$E2E_ADDR" > "$E2E_DIR/substrate.yaml"
if [ -n "${E2E_TETHER:-}" ]; then
  case "$E2E_TETHER" in
    http://127.0.0.1:*) ;;
    *) echo "serve.sh: E2E_TETHER must be a loopback URL: $E2E_TETHER" >&2; exit 2 ;;
  esac
  printf 'tachyon:\n  chat_sync_interval: 1s\n  upstreams:\n    tether: { url: "%s" }\n  features:\n    chat_sync: true\n' "$E2E_TETHER" >> "$E2E_DIR/substrate.yaml"
fi

export HOME="$E2E_DIR/profile"
export XDG_DATA_HOME="$E2E_DIR/profile/data" XDG_CONFIG_HOME="$E2E_DIR/profile/config" XDG_STATE_HOME="$E2E_DIR/profile/state"
export TACHYON_DATA_DIR="$E2E_DIR/data" SUBSTRATE_CONFIG="$E2E_DIR/substrate.yaml"

if [ "${E2E_OWNER:-}" = "1" ]; then
  # The owner is created on the host, as in production (setup over HTTP is refused), with the
  # password on stdin from a file only the suite reads.
  head -c 18 /dev/urandom | base64 | tr '/+' '-_' | tr -d '=\n' > "$E2E_DIR/owner-password"
  chmod 600 "$E2E_DIR/owner-password"
  "$E2E_BIN" identity setup --username owner --display-name "E2E Owner" < "$E2E_DIR/owner-password"
fi
exec "$E2E_BIN" serve
