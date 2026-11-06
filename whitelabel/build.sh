#!/usr/bin/env bash
# whitelabel/build.sh — build a white-label release on this platform, pack
# it, and write the piece of update feed it needs.
#
#   CONFIG_URL=https://example.com/api/desktop/config.json \
#   PREFIX=yourdesk VERSION=1.6.1 ./whitelabel/build.sh
#
#   ./whitelabel/build.sh feed        # merge dist/whitelabel/*.asset.json
#
# Each platform builds on its own: the macOS app is a bundle, Windows
# cross-compiles without cgo (WebView2 needs none), and Linux links GTK and
# WebKitGTK natively. Run this once on each, then `feed` anywhere.
#
# PREFIX must be the same as updates.assetPrefix in the brand config, since
# the client asks the feed for exactly "<prefix>-<os>-<arch>.zip".
set -euo pipefail
cd "$(dirname "$0")/.."

MODE="${1:-build}"
OUT="${OUT:-dist/whitelabel}"
VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null | tr -cd 'A-Za-z0-9._+-')}"
VERSION="${VERSION:-dev}"
PREFIX="${PREFIX:-yourdesk}"
CONFIG_URL="${CONFIG_URL:-}"
# Where the uploaded files will be served from; the feed's asset URLs are
# ASSET_BASE_URL/<name> when set.
ASSET_BASE_URL="${ASSET_BASE_URL:-}"

os="$(go env GOOS)"
arch="$(go env GOARCH)"
mkdir -p "$OUT"

sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d' ' -f1
  else shasum -a 256 "$1" | cut -d' ' -f1; fi
}

asset_entry() { # asset_entry <file> <name>
  local file="$1" name="$2" sum size
  size=$(wc -c < "$file" | tr -d ' ')
  sum=$(sha256_of "$file")
  cat > "$OUT/$name.asset.json" <<EOF
{
  "name": "$name",
  "size": $size,
  "sha256": "$sum"
}
EOF
  echo "  $name  $size bytes  sha256 $sum"
}

build() {
  if [ -z "$CONFIG_URL" ]; then
    echo "set CONFIG_URL to the brand config's address, the URL the client reads" >&2
    exit 1
  fi
  local ldflags="-s -w -X main.version=$VERSION -X github.com/yetone/magpie/internal/brand.ConfigURL=$CONFIG_URL"

  case "$os" in
    darwin)
      echo "  darwin/$arch (app bundle)"
      # The Makefile's app target names the bundle; the make variables are
      # overridden, not the bundle's name, so the client's asset name is
      # the only place the brand shows here.
      make app VERSION="$VERSION" LDFLAGS="$ldflags" >/dev/null
      rm -f "$OUT/$PREFIX-darwin-$arch.zip"
      ditto -c -k --keepParent magpie.app "$OUT/$PREFIX-darwin-$arch.zip"
      ;;
    windows)
      echo "  windows/$arch (app)"
      # go-winres is run with `go run`, which builds for the host: with
      # GOOS=windows in the environment it builds a Windows tool and can't
      # run it here, so the host's are handed to make, whose own go builds
      # set the target explicitly.
      env -u GOOS -u GOARCH make release-windows VERSION="$VERSION" LDFLAGS="$ldflags" >/dev/null
      # The feed's asset is the bare exe: an update downloads it and
      # replaces the binary itself (update.BinaryAsset), so a zip there
      # would leave the client unable to update.
      rm -f "$OUT/$PREFIX-windows-$arch.exe"
      cp "dist/magpie-windows-$arch.exe" "$OUT/$PREFIX-windows-$arch.exe"
      # a zip beside it, for a download page to hand out
      rm -f "$OUT/$PREFIX-windows-$arch.zip"
      (cd dist && zip -q -9 "../$OUT/$PREFIX-windows-$arch.zip" "magpie-windows-$arch.exe")
      ;;
    linux)
      echo "  linux/$arch (app)"
      make release-linux VERSION="$VERSION" LDFLAGS="$ldflags" >/dev/null
      # the bare binary, as Windows' exe is (update.BinaryAsset)
      rm -f "$OUT/$PREFIX-linux-$arch"
      cp "dist/magpie-linux-$arch" "$OUT/$PREFIX-linux-$arch"
      rm -f "$OUT/$PREFIX-linux-$arch.zip"
      (cd dist && zip -q -9 "../$OUT/$PREFIX-linux-$arch.zip" "magpie-linux-$arch")
      ;;
    *)
      echo "unsupported GOOS $os" >&2
      exit 1
      ;;
  esac

  # The feed's name per platform: a zip of the .app on a Mac, the bare
  # binary elsewhere (update.AppAsset, update.BinaryAsset).
  local name
  case "$os" in
    darwin) name="$PREFIX-darwin-$arch.zip" ;;
    windows) name="$PREFIX-windows-$arch.exe" ;;
    *) name="$PREFIX-linux-$arch" ;;
  esac
  asset_entry "$OUT/$name" "$name"
}

feed() {
  local notes="${NOTES:-}"
  local url="${RELEASE_URL:-}"
  local files=("$OUT"/*.asset.json)
  [ -e "${files[0]}" ] || { echo "no $OUT/*.asset.json; run a build first" >&2; exit 1; }
  # JSON-escape the notes: backslash first, then quotes, then newlines.
  local esc
  esc=$(printf '%s' "$notes" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g' | awk '{printf "%s\\n", $0}')
  {
    printf '{\n  "version": "%s",\n  "notes": "%s",\n  "url": "%s",\n  "assets": {\n' "$VERSION" "$esc" "$url"
    local first=1
    for f in "${files[@]}"; do
      local name size sum file_url
      name=$(sed -n 's/.*"name": "\(.*\)".*/\1/p' "$f")
      size=$(sed -n 's/.*"size": \(.*\),.*/\1/p' "$f")
      sum=$(sed -n 's/.*"sha256": "\(.*\)".*/\1/p' "$f")
      if [ -n "$ASSET_BASE_URL" ]; then file_url="$ASSET_BASE_URL/$name"; else file_url="$name"; fi
      [ $first -eq 1 ] || printf ',\n'
      first=0
      printf '    "%s": { "url": "%s", "size": %s, "sha256": "%s" }' "$name" "$file_url" "$size" "$sum"
    done
    printf '\n  }\n}\n'
  } > "$OUT/latest.json"
  echo "  $OUT/latest.json"
}

case "$MODE" in
  build) build ;;
  feed) feed ;;
  *) echo "usage: $0 [build|feed]" >&2; exit 2 ;;
esac
