#!/usr/bin/env bash
# Builds the Flock menu bar app into build/macos/Flock.app (ad-hoc signed), with the flock CLI
# inside it (the app sets Flock up from it on first start), and build/macos/Flock.dmg to install it.
# SKIP_DMG=1 builds the app only.
set -euo pipefail

cd "$(dirname "$0")"
VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
OUT="../build/macos"

# Universal (Apple silicon and Intel), like the CLI below.
ARCHS=(--arch arm64 --arch x86_64)
swift build -c release --product FlockBar "${ARCHS[@]}"

APP="$OUT/Flock.app"
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
cp "$(swift build -c release "${ARCHS[@]}" --show-bin-path)/FlockBar" "$APP/Contents/MacOS/FlockBar"
# The app icon and the menu bar's logomark, rendered by design/icongen.
cp Resources/AppIcon.icns Resources/MenuBar*.png Resources/LogoMark*.png "$APP/Contents/Resources/"

# The CLI for both architectures, built like `make build`; `flock install` copies it to the data dir.
CLI="$OUT/cli"
mkdir -p "$CLI"
for arch in arm64 amd64; do
	(cd .. && CGO_ENABLED=0 GOOS=darwin GOARCH="$arch" go build -ldflags "-s -w -X flock/internal/cli.Version=$VERSION" \
		-o "build/macos/cli/flock-$arch" ./cmd/flock)
done
lipo -create -output "$APP/Contents/MacOS/flock" "$CLI/flock-arm64" "$CLI/flock-amd64"

cat > "$APP/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleIdentifier</key><string>dev.flock.bar</string>
	<key>CFBundleName</key><string>Flock</string>
	<key>CFBundleDisplayName</key><string>Flock</string>
	<key>CFBundleExecutable</key><string>FlockBar</string>
	<key>CFBundleIconFile</key><string>AppIcon</string>
	<key>CFBundlePackageType</key><string>APPL</string>
	<key>CFBundleShortVersionString</key><string>${VERSION}</string>
	<key>CFBundleVersion</key><string>${VERSION}</string>
	<key>LSMinimumSystemVersion</key><string>15.0</string>
	<key>LSUIElement</key><true/>
	<key>NSHighResolutionCapable</key><true/>
	<key>NSAppTransportSecurity</key>
	<dict><key>NSAllowsLocalNetworking</key><true/></dict>
	<!-- flock://install/<service>/<version> links from the website; the app asks before installing. -->
	<key>CFBundleURLTypes</key>
	<array>
		<dict>
			<key>CFBundleURLName</key><string>dev.flock.bar</string>
			<key>CFBundleURLSchemes</key><array><string>flock</string></array>
		</dict>
	</array>
</dict>
</plist>
PLIST

# Nested code first: the app's signature covers the CLI's.
codesign --force --sign - "$APP/Contents/MacOS/flock"
codesign --force --sign - "$APP"
echo "built $(cd "$OUT" && pwd)/Flock.app"

if [[ "${SKIP_DMG:-}" == 1 ]]; then
	exit 0
fi

# Unsigned disk image: the app next to a link to /Applications, to drag it there.
STAGE="$OUT/dmg"
DMG="$OUT/Flock.dmg"
rm -rf "$STAGE" "$DMG"
mkdir -p "$STAGE"
cp -R "$APP" "$STAGE/"
ln -s /Applications "$STAGE/Applications"
hdiutil create -quiet -volname "Flock" -srcfolder "$STAGE" -fs HFS+ -format UDZO -ov "$DMG"
rm -rf "$STAGE"
echo "built $(cd "$OUT" && pwd)/Flock.dmg"
