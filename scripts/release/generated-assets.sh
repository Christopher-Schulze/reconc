#!/usr/bin/env sh

set -eu

root="$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)"
go_bin=${GO:-go}

fail() {
  printf 'error: %s\n' "$1" >&2
  exit 1
}

asset_specs() {
  cat <<'EOF'
completion bash reconc.bash
completion zsh _reconc
completion fish reconc.fish
manpage - reconc.1
manifest - release-manifest.json
sbom spdx reconc-{version}.spdx.json
sbom cyclonedx reconc-{version}.cdx.json
EOF
}

expand_name() {
  template=$1
  version=$2
  prefix=${template%%\{version\}*}
  if [ "$prefix" = "$template" ]; then
    printf '%s\n' "$template"
    return
  fi
  suffix=${template#*\{version\}}
  printf '%s%s%s\n' "$prefix" "$version" "$suffix"
}

validate_name() {
  case "$1" in
    ''|*/*|*'..'*|*[!A-Za-z0-9._-]*) fail "unsafe generated release asset name: $1" ;;
  esac
}

list_assets() {
  [ "$#" -ge 2 ] || fail "usage: ${0##*/} list BIN VERSION [OS/ARCH...]"
  bin=$1
  version=$2
  shift 2
  asset_specs | while read -r kind variant template extra; do
    [ -n "$kind" ] && [ -n "$variant" ] && [ -n "$template" ] && [ -z "${extra:-}" ] \
      || fail "malformed generated release asset specification"
    name=$(expand_name "$template" "$version")
    validate_name "$name"
    printf '%s\n' "$name"
  done
  for target in "$@"; do
    os=${target%/*}
    arch=${target##*/}
    [ -n "$os" ] && [ -n "$arch" ] && [ "$os/$arch" = "$target" ] \
      || fail "invalid release target: $target"
    extension=''
    [ "$os" = windows ] && extension=.exe
    name="$bin-$version-$os-$arch$extension"
    validate_name "$name"
    printf '%s\n' "$name"
  done
}

list_mode_assets() {
  [ "$#" -eq 2 ] || fail "usage: ${0##*/} list-mode MODE VERSION"
  mode=$1
  version=$2
  case "$mode" in
    all|completion|manpage|manifest|sbom) ;;
    *) fail "unknown generated release asset mode: $mode" ;;
  esac
  asset_specs | while read -r kind variant template extra; do
    [ -n "$kind" ] && [ -n "$variant" ] && [ -n "$template" ] && [ -z "${extra:-}" ] \
      || fail "malformed generated release asset specification"
    if [ "$mode" != all ] && [ "$kind" != "$mode" ]; then
      continue
    fi
    name=$(expand_name "$template" "$version")
    validate_name "$name"
    printf '%s\n' "$name"
  done
}

generate_assets() {
  [ "$#" -eq 5 ] || fail "usage: ${0##*/} generate MODE DIST VERSION COMMIT SOURCE_DATE_EPOCH"
  mode=$1
  dist=$2
  version=$3
  commit=$4
  epoch=$5
  case "$mode" in
    all|completion|manpage|sbom) ;;
    *) fail "unknown generated release asset mode: $mode" ;;
  esac
  [ -d "$dist" ] || fail "distribution directory does not exist: $dist"

  asset_specs | while read -r kind variant template extra; do
    [ -n "$kind" ] && [ -n "$variant" ] && [ -n "$template" ] && [ -z "${extra:-}" ] \
      || fail "malformed generated release asset specification"
    name=$(expand_name "$template" "$version")
    validate_name "$name"
    case "$kind" in
      completion)
        if [ "$mode" = all ] || [ "$mode" = completion ]; then
          "$go_bin" -C "$root" run ./cmd/reconc completion "$variant" > "$dist/$name"
        fi
        ;;
      manpage)
        if [ "$mode" = all ] || [ "$mode" = manpage ]; then
          SOURCE_DATE_EPOCH="$epoch" "$go_bin" -C "$root" run \
            -ldflags "-X main.Version=$version" ./cmd/reconc manpage > "$dist/$name"
        fi
        ;;
      manifest|sbom) ;;
      *) fail "unknown generated release asset kind: $kind" ;;
    esac
  done

  if [ "$mode" = all ] || [ "$mode" = sbom ]; then
    "$go_bin" -C "$root" run ./scripts/release/sbom generate \
      --root "$root" --output-dir "$dist" --version "$version" \
      --commit "$commit" --source-date-epoch "$epoch"
    asset_specs | while read -r kind _ template _; do
      [ "$kind" = sbom ] || continue
      name=$(expand_name "$template" "$version")
      [ -f "$dist/$name" ] || fail "SBOM generator omitted declared asset: $name"
    done
  fi
}

[ "$#" -ge 1 ] || fail "usage: ${0##*/} list|list-mode|generate ..."
command=$1
shift
case "$command" in
  list) list_assets "$@" ;;
  list-mode) list_mode_assets "$@" ;;
  generate) generate_assets "$@" ;;
  *) fail "unknown command: $command" ;;
esac
