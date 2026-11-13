# Minecraft LAN Broadcaster

A privacy-focused LAN helper for Bedrock consoles and Java computers. Includes an **Android app** and a **Linux desktop GUI and CLI**. No ads, analytics, helper account sign-in, or root access are required. The destination server still performs its normal authentication.

## Destinations

Set your own Bedrock and Java server addresses before broadcasting. Bundled addresses are reserved documentation examples, not usable servers. Home network and Public domain buttons currently contain example presets; replace them for your deployment. Local user settings are not stored in Git.

The helper and players must share a LAN without client isolation. Keep only one Bedrock helper active per host.

## Linux

Requirements: Go 1.27.3 or compatible newer version; Python 3, PyGObject, GTK 4, and a systemd user session for the GUI.

```sh
mkdir -p dist
go build -trimpath -ldflags=-checklinkname=0 -o dist/minecraft-lan-broadcaster .
./start-gui.sh
```

The desktop window provides Bedrock/Java toggles, editable destinations, presets, Start/Stop, status, and connection logs. It saves settings under the user's configuration directory. It attaches to an existing broadcaster service; closing the window keeps broadcasting. Stop broadcast ends it.

For terminal operation:

```sh
./start.sh -target YOUR_LAN_IP:19132
./dist/minecraft-lan-broadcaster -bedrock=true -java=true -target YOUR_LAN_IP:19132 -java-target YOUR_LAN_IP:25565
./stop.sh
```

Java uses a local TCP relay on 25566 by default to avoid colliding with a Java server on 25565. Keep the broadcaster running while Java relay clients play. Change with `-java-port`; use `-bedrock=false` for Java only.

A desktop-entry template is in `desktop/`. Replace its install paths before installing it in your user applications directory.

## Android

The source is under `android/`; shared networking code is exposed through `mobile/` using gomobile. Current packaging targets ARM64, Android API 26+, compile/target API 35. Root is not needed. This is a debug/test build, not a signed store release.

Install Go, gomobile/gobind, JDK 17, Android SDK platform/build tools 35, NDK 29.0.14206865, and Gradle 8.11.1. Put Go and gomobile tools on PATH. Set `JAVA_HOME`, `ANDROID_HOME`, `ANDROID_NDK_HOME`, and optionally `GRADLE` and `GRADLE_USER_HOME` for your environment. Then:

```sh
gomobile init
./android/build.sh
```

The generated APK is in `android/app/build/outputs/apk/debug/`. Generated AARs, APKs, SDK paths, caches, and keys are excluded from Git. Set both destination fields in the installed app. Start a broadcast on the same Wi-Fi as the console; stop before changing settings.

## Network behavior

- Bedrock discovery: UDP 7551.
- Temporary LAN WebRTC lobby: UDP 7552.
- Local second-stage HTTP signaling: TCP 7551.
- Java discovery: multicast UDP 4445; local relay TCP 25566 on Linux, 25565 on Android.

Allow helper ports only on trusted local networks. Do not router-forward them. Destination game-server ports are separate.

Bedrock uses a temporary lobby, then transfers to the helper's local HTTP signaling proxy. The proxy forwards the console's authenticated join to the destination. Gameplay then connects directly to the destination. Some consoles never acknowledge initialization of the empty lobby; a two-second fallback sends the transfer after the loading request. Loading/authentication after transfer can take longer. If the LAN entry is stale, fully reopen Minecraft.

The helper does not bypass Minecraft/Microsoft authentication. A signed-out console may fail with an empty display-name rejection. Runtime errors can include client addresses and usernames, so keep logs private. Raw authentication tokens and packet contents are not deliberately logged.

## Verification

```sh
go test ./...
CONNECTOR_INTEGRATION=1 go test ./... -timeout 60s
GDK_BACKEND=x11 xvfb-run -a python3 desktop/broadcaster.py --smoke-test
```

Integration tests use temporary LAN ports. Optional Android tests use `CONNECTOR_PHONE`, supplied locally. The GUI smoke test needs ImageMagick's `import` command and writes a screenshot to the ignored `dist/` directory.

Switch gameplay was confirmed with Android and Linux; PS5 gameplay was confirmed in user testing. Xbox testing, iOS packaging, release signing, and broad network/version compatibility remain unverified. Synthetic tests do not replace console testing. Current shared libraries target Bedrock protocol 2225; new protocol versions may require updates.

Experimental direct-bridge code and a reference probe remain in the source for development; production entry points use the two-stage flow.

## License

MIT for this project; dependency attribution and licenses are in `THIRD_PARTY_NOTICES.txt`.
