#!/usr/bin/env bash
# Build the Go core into an Android .aar via gomobile, then stage it for the
# Flutter app. Run from a machine with the Android SDK/NDK + Go installed.
#
#   android/scripts/build_aar.sh
#
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"   # android/
mobile_dir="$here/mobile"
app_libs="$here/app/android/app/libs"

: "${ANDROID_HOME:?set ANDROID_HOME to your Android SDK path}"
: "${ANDROID_NDK_HOME:?set ANDROID_NDK_HOME to your NDK path (e.g. \$ANDROID_HOME/ndk/<ver>)}"

echo "==> Ensuring gomobile is installed"
go install golang.org/x/mobile/cmd/gomobile@latest
go install golang.org/x/mobile/cmd/gobind@latest
export PATH="$PATH:$(go env GOPATH)/bin"

echo "==> Resolving module deps (fetches tun2socks etc.)"
( cd "$mobile_dir" && go mod tidy )

echo "==> gomobile init"
gomobile init

echo "==> gomobile bind → mobile.aar"
mkdir -p "$app_libs"
# No -javapkg: the bound Java package stays `mobile`, matching the Kotlin
# imports (mobile.Mobile, mobile.Tunnel, mobile.Callback, mobile.Protector).
( cd "$mobile_dir" && gomobile bind \
    -target=android \
    -androidapi 24 \
    -o "$app_libs/mobile.aar" \
    . )

echo "==> Done: $app_libs/mobile.aar"
