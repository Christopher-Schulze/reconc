#!/usr/bin/env bash
set -euo pipefail

root="$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
go_cmd="${GO:-go}"
profile=''
output_dir=''

fail() {
  printf 'benchmark-pgo: %s\n' "$1" >&2
  exit 64
}

while (( $# > 0 )); do
  case "$1" in
    --root)
      (( $# >= 2 )) || fail "--root requires a path"
      root="$2"
      shift 2
      ;;
    --go)
      (( $# >= 2 )) || fail "--go requires a command"
      go_cmd="$2"
      shift 2
      ;;
    --profile)
      (( $# >= 2 )) || fail "--profile requires a path"
      profile="$2"
      shift 2
      ;;
    --output-dir)
      (( $# >= 2 )) || fail "--output-dir requires a path"
      output_dir="$2"
      shift 2
      ;;
    *)
      fail "usage: ${0##*/} --profile PATH --output-dir PATH [--root PATH] [--go COMMAND]"
      ;;
  esac
done

[ -n "$profile" ] || fail "--profile is required"
[ -n "$output_dir" ] || fail "--output-dir is required"
root="$(CDPATH='' cd -- "$root" && pwd -P)"
version="$("$root/scripts/build/resolve-version.sh" "$root")"
if [[ "$profile" != /* ]]; then
  profile="$root/$profile"
fi
if [[ "$output_dir" != /* ]]; then
  output_dir="$root/$output_dir"
fi
[ -f "$profile" ] || fail "profile is not a regular file: $profile"
[ ! -e "$output_dir" ] || fail "output directory already exists: $output_dir"
mkdir -p "$output_dir"

goos="$($go_cmd -C "$root" env GOOS)"
goarch="$($go_cmd -C "$root" env GOARCH)"
goversion="$($go_cmd -C "$root" env GOVERSION)"
case "$goos" in
  darwin) cpu="$(sysctl -n machdep.cpu.brand_string | tr '\n' ' ' | awk '{$1=$1; print}')" ;;
  linux) cpu="$(awk -F: '/^(model name|Hardware|Processor)[[:space:]]*:/ { gsub(/^[[:space:]]+/, "", $2); print $2; exit }' /proc/cpuinfo)" ;;
  *) cpu='unavailable' ;;
esac
[ -n "$cpu" ] || cpu='unavailable'
commit="$(git -C "$root" rev-parse HEAD)"
dirty=false
if [ -n "$(git -C "$root" status --porcelain --untracked-files=normal)" ]; then
  dirty=true
fi

profile_bytes="$(wc -c < "$profile" | tr -d ' ')"
(( profile_bytes > 0 && profile_bytes <= 64 * 1024 * 1024 )) || fail "profile exceeds the 64 MiB input limit"
if command -v shasum >/dev/null 2>&1; then
  profile_sha256="$(shasum -a 256 "$profile" | awk '{print $1}')"
elif command -v sha256sum >/dev/null 2>&1; then
  profile_sha256="$(sha256sum "$profile" | awk '{print $1}')"
else
  fail "neither shasum nor sha256sum is available"
fi
marker="$(cd "$root" && "$go_cmd" run ./cmd/reconc-build-provenance --root "$root" --goos "$goos" --goarch "$goarch" --version "$version")"
ldflags="-X main.Version=$version -X reconc.dev/reconc/buildprovenance.BuildMarker=$marker -s -w"

build_candidate() {
  local name="$1"
  local pgo="$2"
  local output="$output_dir/reconc-$name"
  CGO_ENABLED=0 "$go_cmd" -C "$root" build -trimpath -pgo="$pgo" \
    -ldflags "$ldflags" -o "$output" ./cmd/reconc
  "$go_cmd" -C "$root" run ./cmd/reconc-build-provenance \
    --root "$root" --goos "$goos" --goarch "$goarch" --version "$version" \
    --verify-binary "$output"
  printf '%s\n' "$output"
}

off_binary="$(build_candidate off off)"
pgo_binary="$(build_candidate pgo "$profile")"

export PGO_ROOT="$root"
export PGO_PROFILE="$profile"
export PGO_OUTPUT_DIR="$output_dir"
export PGO_OFF_BINARY="$off_binary"
export PGO_BINARY="$pgo_binary"
export PGO_VERSION="$version"
export PGO_GO_VERSION="$goversion"
export PGO_GOOS="$goos"
export PGO_GOARCH="$goarch"
export PGO_CPU="$cpu"
export PGO_COMMIT="$commit"
export PGO_DIRTY="$dirty"
export PGO_PROFILE_BYTES="$profile_bytes"
export PGO_PROFILE_SHA256="$profile_sha256"
python3 - <<'PY'
import hashlib
import json
import os
import statistics
import subprocess
import time


def digest(path):
    hasher = hashlib.sha256()
    with open(path, "rb") as stream:
        for chunk in iter(lambda: stream.read(1 << 20), b""):
            hasher.update(chunk)
    return hasher.hexdigest()


def size(path):
    return os.stat(path).st_size


def cold_start(path):
    samples = []
    for _ in range(7):
        start = time.perf_counter_ns()
        subprocess.run([path, "--help"], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        samples.append(time.perf_counter_ns() - start)
    ordered = sorted(samples)
    return {
        "samples_ns": samples,
        "p50_ns": statistics.median(samples),
        "p95_ns": ordered[int(0.95 * (len(ordered) - 1))],
        "min_ns": ordered[0],
        "max_ns": ordered[-1],
        "range_ns": ordered[-1] - ordered[0],
    }


off_binary = os.environ["PGO_OFF_BINARY"]
pgo_binary = os.environ["PGO_BINARY"]
report = {
    "format_version": "reconc.benchmark-pgo/v1",
    "environment": {
        "go_version": os.environ["PGO_GO_VERSION"],
        "goos": os.environ["PGO_GOOS"],
        "goarch": os.environ["PGO_GOARCH"],
        "cpu": os.environ["PGO_CPU"],
        "commit": os.environ["PGO_COMMIT"],
        "dirty": os.environ["PGO_DIRTY"] == "true",
    },
    "version": os.environ["PGO_VERSION"],
    "profile": {
        "path": os.path.relpath(os.environ["PGO_PROFILE"], os.environ["PGO_ROOT"]),
        "bytes": int(os.environ["PGO_PROFILE_BYTES"]),
        "sha256": os.environ["PGO_PROFILE_SHA256"],
    },
    "builds": {
        "off": {"path": os.path.basename(off_binary), "bytes": size(off_binary), "sha256": digest(off_binary)},
        "pgo": {"path": os.path.basename(pgo_binary), "bytes": size(pgo_binary), "sha256": digest(pgo_binary)},
    },
    "cold_start": {"available": True, "off": cold_start(off_binary), "pgo": cold_start(pgo_binary)},
}
with open(os.path.join(os.environ["PGO_OUTPUT_DIR"], "report.json"), "w", encoding="utf-8") as stream:
    json.dump(report, stream, indent=2, sort_keys=True)
    stream.write("\n")
PY
printf 'benchmark-pgo: report=%s\n' "$output_dir/report.json"
