#!/usr/bin/env bash
set -euo pipefail

usage() {
  printf '%s\n' \
    'Usage: scripts/tests/host-integration-probe.sh --host HOST --surface SURFACE --allow-authenticated [--json]' \
    'The canonical host/surface matrix and probe contract are owned by reconc hook verify.'
}

host=''
surface=''
allow_authenticated=false
json_output=false
while (($# > 0)); do
  case "$1" in
    --host)
      (($# >= 2)) || { usage >&2; exit 2; }
      host="$2"
      shift 2
      ;;
    --surface)
      (($# >= 2)) || { usage >&2; exit 2; }
      surface="$2"
      shift 2
      ;;
    --allow-authenticated)
      allow_authenticated=true
      shift
      ;;
    --json)
      json_output=true
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      printf 'Unknown flag: %s\n' "$1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if [[ -z "$host" || -z "$surface" ]]; then
  usage >&2
  exit 2
fi

case "$host:$surface" in
  cursor:desktop-agent|cursor:desktop-cmd-k|cursor:tab|cursor:cli-interactive|cursor:cli-print|cursor:cloud)
    surface="cursor-$surface"
    ;;
esac

verify_args=(hook verify --live --host "$host" --surface "$surface")
if [[ "$allow_authenticated" == true ]]; then
  verify_args+=(--allow-authenticated)
fi
if [[ "$json_output" == true ]]; then
  verify_args+=(--json)
fi

if [[ -n "${RECONC_BIN:-}" ]]; then
  exec "$RECONC_BIN" "${verify_args[@]}"
fi
if [[ -x .build/bin/reconc ]]; then
  exec .build/bin/reconc "${verify_args[@]}"
fi
if command -v reconc >/dev/null 2>&1; then
  exec reconc "${verify_args[@]}"
fi
exec go run ./cmd/reconc "${verify_args[@]}"
