# ssh2socks — Android

Native Android app that turns an SSH connection (incl. jump-host chains) into a
device-wide or per-app SOCKS proxy, delivered through `VpnService` + tun2socks.

- **UI:** Flutter (`app/`)
- **Core:** Go (`core/`) — SSH chaining, SOCKS5, reconnect, connectivity probe
- **Bridge:** gomobile `.aar` (`mobile/`) — wraps the core + tun2socks for Kotlin

## Architecture

```
Flutter UI ──MethodChannel/EventChannel──▶ MainActivity (Kotlin)
                                              │ start/stop, listHosts
                                              ▼
                                        SshVpnService (Kotlin, foreground)
                                          │  builds tun (global | per-app)
                                          │  implements Callback + Protector
                                          ▼
                                        mobile.Tunnel  (gomobile .aar)
                                          ├─ core.Engine
                                          │    ├─ SSH chain (hop → hop → target, private key)
                                          │    ├─ local SOCKS5 server
                                          │    ├─ auto-reconnect (exp. backoff)
                                          │    └─ HTTP connectivity probe
                                          └─ tun2socks: tun fd ──▶ SOCKS5
```

The **first SSH hop's socket is `protect()`-ed** so it escapes the tun and does
not loop back through tun2socks. Subsequent hops ride inside that connection as
`direct-tcpip` channels.

### ProxyCommand / ProxyJump support

`ProxyJump a,b,c` and `ProxyCommand ssh -W %h:%p <jump>` are resolved natively
into an ordered hop chain and dialed in-process — **no external `ssh` binary on
the device.** Other `ProxyCommand` forms (corkscrew, ncat, …) are rejected with
a clear error rather than silently ignored.

## Build status

| Component | State |
|-----------|-------|
| `core/` Go engine | **Implemented & unit + integration tested** (`go test ./...`, incl. a real 2-hop sshd E2E) |
| `mobile/` gomobile bridge | **Builds in CI** → `mobile.aar` (gomobile bind, Go 1.26 + tun2socks v2.7.0) |
| `app/` Flutter + Kotlin | **Builds in CI** → signed-with-debug-key release APK artifact |

The GitHub Actions workflow (`.github/workflows/android-apk.yml`) builds the
whole thing end-to-end on every push and uploads the APK as the `ssh2socks-apk`
artifact. Everything under `core/` is additionally verified offline on any
machine with Go via `go test ./...`.

### CI build notes (hard-won constraints)

- **Go 1.26 ⇒ tun2socks v2.7.0.** Older tun2socks (≤ v2.6.x) pin a gvisor whose
  `pkg/sync` uses `//go:linkname` into Go runtime internals (`goready`,
  `gopark`, `semacquire`, …) that Go 1.26 no longer exposes → "undefined"
  link errors. v2.7.0 pins a gvisor built for Go 1.26.3. Its `engine` API
  (`Key`/`Insert`/`Start`/`Stop`) is unchanged, so `mobile/mobile.go` is untouched.
- **gomobile bind needs `golang.org/x/mobile` in the module graph.** A plain
  `go get golang.org/x/mobile/bind` is stripped again by `go mod tidy`; use a
  **tool directive** (`go get -tool golang.org/x/mobile/cmd/gobind`) which
  survives tidy. `scripts/build_aar.sh` does this.
- **`device_apps` (abandoned) vs AGP 8.** It declares no `namespace` and pins an
  old `compileSdk`. `scripts/ci_assemble.sh` injects a `subprojects` hook into
  the generated root Gradle that back-fills `namespace` from the manifest
  `package` and forces `compileSdk 34` (fixes `android:attr/lStar not found`).
  The hook is injected **before** Flutter's own `subprojects{}` blocks so its
  `afterEvaluate` registers before early evaluation. If `device_apps` breaks
  again on a future AGP bump, replace it with a MethodChannel-based app list.

### Download the built APK

- Web: repo → **Actions** → latest green run → **Artifacts** → `ssh2socks-apk`.
- CLI: `gh run download <run-id> --repo <owner>/<repo> -n ssh2socks-apk`

The artifact is a `.zip` containing `app-release.apk`. It is signed with the
**debug key** (no release keystore configured), so it installs for testing but
is not suitable for store distribution — see release signing below.

## Prerequisites (build machine)

- Go ≥ 1.26
- Android SDK + **NDK** (`ANDROID_HOME`, `ANDROID_NDK_HOME` set)
- Flutter ≥ 3.4
- JDK 17

## Build & run

```bash
# 1. Create the Flutter host project ONCE, then drop these sources in.
cd android
flutter create --org com.ssh2socks --project-name ssh2socks app_tmp
#   move the generated gradle/wrapper/res into app/ (keep our lib/ + kotlin/),
#   OR run flutter create directly over app/ and re-apply our files.
#   Merge app/android/app/build.gradle.snippet into the generated build.gradle.

# 2. Build the Go core into an .aar (fetches tun2socks, runs gomobile bind).
ANDROID_HOME=$HOME/Android/Sdk \
ANDROID_NDK_HOME=$HOME/Android/Sdk/ndk/<version> \
  scripts/build_aar.sh
#   → app/android/app/libs/mobile.aar

# 3. Fetch Dart deps and run on a connected device.
cd app
flutter pub get
flutter run          # or: flutter build apk --release
```

## Verify the Go core locally (no Android needed)

```bash
cd android/core
go test ./...        # config/chain unit tests + real-sshd 2-hop E2E
```

The E2E test spins up a throwaway `sshd` in a temp dir with its own host/client
keys — it never touches your real `~/.ssh`.

## Integration point to double-check

`mobile/mobile.go` calls the tun2socks engine API (`engine.Key` /
`engine.Insert` / `engine.Start` / `engine.Stop`), pinned to
`github.com/xjasonlyu/tun2socks/v2 v2.5.2` in `mobile/go.mod`. If you bump that
version and the engine API has shifted, adjust `StartTun`/`StopTun` accordingly.

## Security notes (v1)

- Host keys use `InsecureIgnoreHostKey` (TOFU/`known_hosts` is a planned
  follow-up). Fine for trusted jump hosts you control; audit before wider use.
- Private keys + passphrases live in `flutter_secure_storage` (Android
  Keystore-backed); only a key handle is kept in plain profile storage.
