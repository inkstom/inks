#!/bin/sh
# Build inks into build/inks.app (macOS), signed ad hoc.
set -e
cd "$(dirname "$0")/.."
VERSION=$(cat VERSION)
APP=build/inks.app
go build -trimpath -ldflags "-s -w -X main.version=$VERSION -extldflags=-Wl,-no_warn_duplicate_libraries" -o build/inks ./cmd/inks
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
cp build/inks "$APP/Contents/MacOS/inks"
cp build-resources/icon.icns "$APP/Contents/Resources/inks.icns"
mkdir -p "$APP/Contents/Resources/bin"
cp bin/inks "$APP/Contents/Resources/bin/inks"
cat > "$APP/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key><string>inks</string>
  <key>CFBundleDisplayName</key><string>inks</string>
  <key>CFBundleIdentifier</key><string>com.inks.editor</string>
  <key>CFBundleVersion</key><string>$VERSION</string>
  <key>CFBundleShortVersionString</key><string>$VERSION</string>
  <key>CFBundleExecutable</key><string>inks</string>
  <key>CFBundleIconFile</key><string>inks.icns</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>LSMinimumSystemVersion</key><string>12.0</string>
  <key>NSHighResolutionCapable</key><true/>
  <key>LSApplicationCategoryType</key><string>public.app-category.developer-tools</string>
  <key>NSHumanReadableCopyright</key><string>© 2026 Thomas Inks. MIT License.</string>
  <key>CFBundleDocumentTypes</key>
  <array>
    <dict>
      <key>CFBundleTypeName</key><string>Text Document</string>
      <key>CFBundleTypeRole</key><string>Editor</string>
      <key>LSHandlerRank</key><string>Alternate</string>
      <key>LSItemContentTypes</key><array><string>public.plain-text</string><string>public.source-code</string><string>public.data</string></array>
    </dict>
  </array>
</dict>
</plist>
PLIST
# Ad hoc signature: no developer account needed, but the bundle is sealed,
# so macOS reports "unidentified developer" rather than "damaged".
codesign --force --deep --sign - "$APP"
echo "Built $APP ($(du -sh "$APP" | cut -f1))"
if pgrep -qf "$(pwd)/$APP/Contents/MacOS/inks"; then
  echo "inks is already running: quit it (⌘Q) before opening, or the old build stays open"
fi
