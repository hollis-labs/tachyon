#!/bin/sh
# Starts the next/ binary for the browser suite (playwright.config.ts). Everything arrives through the
# environment, never spliced into a command line:
#   E2E_BIN   the binary;  E2E_DIR  a directory to create inside the marked run directory;
#   E2E_ADDR  host:port to listen on;  E2E_OWNER  "1" to create the owner offline first;
#   E2E_TETHER  optional URL of the suite's local Tether fake (e2e/tether-fake.ts), with chat sync on;
#   E2E_APPROVALS_SEED  optional test-only SQL (synthetic approval rows), applied with sqlite3 to this run's
#     database once the owner setup has created the schema, before serving.
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
tachyon=""
if [ -n "${E2E_TETHER:-}" ]; then
  case "$E2E_TETHER" in
    http://127.0.0.1:*) ;;
    *) echo "serve.sh: E2E_TETHER must be a loopback URL: $E2E_TETHER" >&2; exit 2 ;;
  esac
  tachyon="$tachyon  chat_sync_interval: 1s
  upstreams:
    tether: { url: \"$E2E_TETHER\" }
  features:
    chat_sync: true
"
fi
if [ -n "${E2E_APPROVALS_SEED:-}" ]; then
  # A throwaway key for approval secrets, made for this run only.
  (umask 077 && head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n' > "$E2E_DIR/approvals-secret.key")
  tachyon="$tachyon  files:
    secret_key: \"file://$E2E_DIR/approvals-secret.key\"
"
fi
if [ -n "$tachyon" ]; then
  printf 'tachyon:\n%s' "$tachyon" >> "$E2E_DIR/substrate.yaml"
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
if [ -n "${E2E_APPROVALS_SEED:-}" ]; then
  sqlite3 "$E2E_DIR/data/tachyon.db" < "$E2E_APPROVALS_SEED"
fi
exec "$E2E_BIN" serve
