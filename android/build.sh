#!/usr/bin/env bash
set -euo pipefail
cd -- "$(dirname -- "$(readlink -f -- "$0")")/.."
: "${ANDROID_HOME:?Set ANDROID_HOME to an SDK with platform 35 and build-tools 35.0.0}"
: "${ANDROID_NDK_HOME:?Set ANDROID_NDK_HOME to NDK 29.0.14206865}"
: "${JAVA_HOME:?Set JAVA_HOME to JDK 17}"
mkdir -p android/app/libs android/app/src/main/assets
gomobile bind -ldflags=-checklinkname=0 -target=android/arm64 -androidapi=26 \
  -javapkg=org.minecraftlan.nativecore -o android/app/libs/connector.aar ./mobile
cp LICENSE THIRD_PARTY_NOTICES.txt android/app/src/main/assets/
printf 'sdk.dir=%s\n' "$ANDROID_HOME" > android/local.properties
cd android
"${GRADLE:-gradle}" :app:assembleDebug --console=plain
