#!/usr/bin/env bash
# Build Teams Status Scheduler (Go) for all four targets, on this Mac:
#
#   dist/TeamsStatusScheduler-<ver>-macos-arm64.zip    (Apple silicon .app)
#   dist/TeamsStatusScheduler-<ver>-macos-x86_64.zip   (Intel .app)
#   dist/TeamsStatusScheduler-<ver>-macos-arm64        (plain executable, for
#   dist/TeamsStatusScheduler-<ver>-macos-x86_64        running from a terminal)
#   dist/TeamsStatusScheduler-<ver>-windows-x64.exe
#   dist/TeamsStatusScheduler-<ver>-windows-arm64.exe
#
# Requirements: Go 1.25+, and for the macOS builds the Xcode command line
# tools (clang builds both Mac architectures). The Windows builds are pure
# Go and need no extra tools.
#
# Usage:
#   ./makebin.sh                        # all targets
#   ./makebin.sh macos-arm64 windows-x64
#   SKIP_TESTS=1 ./makebin.sh
#   CODESIGN_ID="Developer ID Application: …" ./makebin.sh   # sign Mac apps for distribution
set -euo pipefail
cd "$(dirname "$0")"

APP_NAME="Teams Status Scheduler"
EXE_NAME="TeamsStatusScheduler"
BUNDLE_ID="com.teamsstatusscheduler.app"
MACOS_MIN="11.0"
VERSION=$(tr -d '[:space:]' < VERSION)
ALL=(macos-arm64 macos-x86_64 windows-x64 windows-arm64)
TARGETS=("$@")
[ ${#TARGETS[@]} -eq 0 ] && TARGETS=("${ALL[@]}")

say() { printf '\n\033[1m==> %s\033[0m\n' "$*"; }
die() { printf '\033[31mError: %s\033[0m\n' "$*" >&2; exit 1; }

command -v go >/dev/null || die "Go is not installed (https://go.dev/dl/)"
for t in "${TARGETS[@]}"; do
  case "$t" in macos-arm64|macos-x86_64|windows-x64|windows-arm64) ;; *) die "unknown target '$t' (valid: ${ALL[*]})";; esac
  if [[ $t == macos-* ]]; then
    [ "$(uname -s)" = Darwin ] || die "$t must be built on a Mac (needs Apple's SDK)"
    command -v clang >/dev/null || die "clang not found: run 'xcode-select --install'"
  fi
done

LDFLAGS="-s -w -X main.version=$VERSION"
mkdir -p dist

if [ -z "${SKIP_TESTS:-}" ]; then
  say "Running tests"
  go test ./...
fi

build_macos() { # $1 = arm64 | x86_64
  local arch=$1 goarch=$1
  [ "$arch" = x86_64 ] && goarch=amd64
  local stage="dist/macos-$arch"
  local app="$stage/$APP_NAME.app"
  say "Building macos-$arch"
  rm -rf "$stage" && mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
  CGO_ENABLED=1 GOOS=darwin GOARCH=$goarch \
    CC="clang -arch $arch" MACOSX_DEPLOYMENT_TARGET=$MACOS_MIN \
    CGO_CFLAGS="-mmacosx-version-min=$MACOS_MIN" CGO_LDFLAGS="-mmacosx-version-min=$MACOS_MIN" \
    go build -trimpath -ldflags "$LDFLAGS" -o "$app/Contents/MacOS/$EXE_NAME" .

  # App icon (.icns) from assets/icon.png.
  local iconset; iconset=$(mktemp -d)/AppIcon.iconset
  mkdir -p "$iconset"
  for s in 16 32 128 256 512; do
    sips -z $s $s assets/icon.png --out "$iconset/icon_${s}x${s}.png" >/dev/null
    sips -z $((s*2)) $((s*2)) assets/icon.png --out "$iconset/icon_${s}x${s}@2x.png" >/dev/null
  done
  iconutil -c icns "$iconset" -o "$app/Contents/Resources/AppIcon.icns"

  cat > "$app/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key><string>$APP_NAME</string>
  <key>CFBundleDisplayName</key><string>$APP_NAME</string>
  <key>CFBundleIdentifier</key><string>$BUNDLE_ID</string>
  <key>CFBundleExecutable</key><string>$EXE_NAME</string>
  <key>CFBundleIconFile</key><string>AppIcon</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>$VERSION</string>
  <key>CFBundleVersion</key><string>$VERSION</string>
  <key>LSMinimumSystemVersion</key><string>$MACOS_MIN</string>
  <key>NSHighResolutionCapable</key><true/>
  <key>NSAccessibilityUsageDescription</key><string>Used to change your status in Microsoft Teams by operating its status menu.</string>
</dict>
</plist>
PLIST

  # Sign: Developer ID if provided, otherwise ad-hoc (required on Apple silicon).
  codesign --force --options runtime --timestamp=none --sign "${CODESIGN_ID:--}" "$app" >/dev/null
  local zip="dist/$EXE_NAME-$VERSION-macos-$arch.zip"
  rm -f "$zip"
  ditto -c -k --keepParent "$app" "$zip"
  echo "   $zip"

  # Plain executable: runs from a terminal without installing, and uses the
  # terminal's Accessibility permission (handy for debugging on managed Macs).
  local exe="dist/$EXE_NAME-$VERSION-macos-$arch"
  cp "$app/Contents/MacOS/$EXE_NAME" "$exe"
  codesign --force --sign "${CODESIGN_ID:--}" --identifier "$BUNDLE_ID" "$exe" >/dev/null 2>&1
  echo "   $exe"
}

build_windows() { # $1 = x64 | arm64
  local arch=$1 goarch=$1
  [ "$arch" = x64 ] && goarch=amd64
  local out="dist/$EXE_NAME-$VERSION-windows-$arch.exe"
  say "Building windows-$arch"
  # Icon, version info and manifest (DPI-aware, modern controls) as a .syso resource.
  local v4="$VERSION.0"
  go run github.com/tc-hib/go-winres@v0.3.3 simply \
    --arch "$goarch" --out rsrc --icon assets/icon.png --manifest gui \
    --product-name "$APP_NAME" --file-description "$APP_NAME" \
    --product-version "$v4" --file-version "$v4" >/dev/null
  CGO_ENABLED=0 GOOS=windows GOARCH=$goarch \
    go build -trimpath -ldflags "$LDFLAGS -H windowsgui" -o "$out" .
  rm -f rsrc_windows_*.syso
  echo "   $out"
}

for t in "${TARGETS[@]}"; do
  case "$t" in
    macos-arm64) build_macos arm64 ;;
    macos-x86_64) build_macos x86_64 ;;
    windows-x64) build_windows x64 ;;
    windows-arm64) build_windows arm64 ;;
  esac
done

say "Done"
for t in "${TARGETS[@]}"; do
  ls -lh dist/"$EXE_NAME-$VERSION-$t".* 2>/dev/null | awk '{print "  " $5 "\t" $NF}'
done
