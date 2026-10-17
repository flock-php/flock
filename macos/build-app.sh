#!/usr/bin/env bash
# Builds the Flock menu bar app into build/macos/Flock.app, with the flock CLI inside it (the app
# sets Flock up from it on first start), and build/macos/Flock.dmg to install it.
# SKIP_DMG=1 builds the app only.
#
# Ad-hoc signed by default. SIGN_IDENTITY="Developer ID Application: ..." (in the keychain) signs
# the app and disk image with it and the hardened runtime. With NOTARY_KEY_PATH (an App Store
# Connect API key, .p8), NOTARY_KEY_ID and NOTARY_ISSUER_ID set too, the disk image is notarized
# and stapled.
set -euo pipefail

cd "$(dirname "$0")"
VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
OUT="../build/macos"

# Apple silicon only: Flock doesn't support Intel Macs, and an arm64-only app won't open on one.
ARCHS=(--arch arm64)
swift build -c release --product FlockBar "${ARCHS[@]}"

APP="$OUT/Flock.app"
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
cp "$(swift build -c release "${ARCHS[@]}" --show-bin-path)/FlockBar" "$APP/Contents/MacOS/FlockBar"
# The app icon and the menu bar's logomark, rendered by design/icongen.
cp Resources/AppIcon.icns Resources/MenuBar*.png Resources/LogoMark*.png "$APP/Contents/Resources/"

# The CLI, built like `make build`; `flock install` copies it to the data dir.
(cd .. && CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags "-s -w -X flock/internal/cli.Version=$VERSION" \
	-o "build/macos/Flock.app/Contents/MacOS/flock" ./cmd/flock)

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
	<key>NSHumanReadableCopyright</key><string>Copyright © Guava s.r.o.</string>
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

SIGN_IDENTITY="${SIGN_IDENTITY:--}"
SIGN=(codesign --force --sign "$SIGN_IDENTITY")
if [[ "$SIGN_IDENTITY" != - ]]; then
	SIGN+=(--options runtime --timestamp)
fi

# Nested code first: the app's signature covers the CLI's.
"${SIGN[@]}" "$APP/Contents/MacOS/flock"
"${SIGN[@]}" "$APP"
codesign --verify --strict --deep "$APP"
echo "built $(cd "$OUT" && pwd)/Flock.app"

if [[ "${SKIP_DMG:-}" == 1 ]]; then
	exit 0
fi

# The disk image: the app next to a link to /Applications, to drag it there.
STAGE="$OUT/dmg"
DMG="$OUT/Flock.dmg"
rm -rf "$STAGE" "$DMG"
mkdir -p "$STAGE"
cp -R "$APP" "$STAGE/"
ln -s /Applications "$STAGE/Applications"
hdiutil create -quiet -volname "Flock" -srcfolder "$STAGE" -fs HFS+ -format UDZO -ov "$DMG"
rm -rf "$STAGE"

if [[ "$SIGN_IDENTITY" != - ]]; then
	codesign --force --sign "$SIGN_IDENTITY" --timestamp "$DMG"
fi

if [[ -n "${NOTARY_KEY_PATH:-}" ]]; then
	result=$(xcrun notarytool submit "$DMG" --key "$NOTARY_KEY_PATH" --key-id "$NOTARY_KEY_ID" \
		--issuer "$NOTARY_ISSUER_ID" --wait --output-format json)
	echo "$result"
	if [[ "$(plutil -extract status raw - <<<"$result")" != Accepted ]]; then
		xcrun notarytool log "$(plutil -extract id raw - <<<"$result")" --key "$NOTARY_KEY_PATH" \
			--key-id "$NOTARY_KEY_ID" --issuer "$NOTARY_ISSUER_ID" || true
		echo "notarization failed" >&2
		exit 1
	fi
	xcrun stapler staple "$DMG"
	spctl --assess --type open --context context:primary-signature --verbose "$DMG"
fi
echo "built $(cd "$OUT" && pwd)/Flock.dmg"
