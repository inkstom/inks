#!/bin/sh
# Wrap build/inks.app in a compressed disk image with an Applications
# shortcut: dist/inks_<version>_<arch>.dmg.
set -e
cd "$(dirname "$0")/.."
APP=build/inks.app
[ -d "$APP" ] || { echo "No $APP — run make app first" >&2; exit 1; }
VERSION=$(cat VERSION)
OUT=dist/inks_${VERSION}_$(uname -m).dmg
mkdir -p dist
STAGE=$(mktemp -d)
cp -R "$APP" "$STAGE/"
ln -s /Applications "$STAGE/Applications"
hdiutil create -quiet -volname inks -srcfolder "$STAGE" -ov -format UDZO "$OUT"
rm -rf "$STAGE"
echo "Disk image: $OUT"
