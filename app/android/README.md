# Sidekick Android App

A Kotlin + Jetpack Compose (Material 3) remote control for the sidekick server.
It pairs with a server by scanning a QR code, connects over iroh, discovers
workspaces, and displays their tasks. The app uses state hoisting,
unidirectional data flow, injected dispatchers, and package-by-feature.

## Toolchain / SDK setup

Everything builds and tests from the CLI — no Android Studio required, no
`@Preview`/Live Edit at runtime, no emulator required for the fast loop.

- **JDK**: run the Gradle daemon on JDK 21. `java -version` should report 21.
- **Android SDK**: either reuse Android Studio's SDK or install one headlessly
  (see below). In both cases, point `ANDROID_HOME` at the SDK root and persist
  it (plus the `PATH` additions) in your shell profile:

  ```sh
  export ANDROID_HOME="$HOME/Android/Sdk"  # see below for the actual path
  export ANDROID_SDK_ROOT="$ANDROID_HOME"
  export PATH="$ANDROID_HOME/cmdline-tools/latest/bin:$ANDROID_HOME/platform-tools:$PATH"
  ```

  Gradle finds the SDK via `ANDROID_HOME`, or via `local.properties` in
  `app/android` (which takes precedence — check it if the build picks up the
  wrong SDK):

  ```sh
  echo "sdk.dir=$ANDROID_HOME" > local.properties
  ```

### Option A: reuse Android Studio's SDK

If Android Studio is installed, it has already downloaded an SDK. Set
`ANDROID_HOME` to its location — `$HOME/Library/Android/sdk` on macOS,
`$HOME/Android/Sdk` on Linux (double-check under **Settings → Languages &
Frameworks → Android SDK**). From that same SDK Manager screen, under the
**SDK Tools** tab, install "Android SDK Command-line Tools (latest)" if
`sdkmanager` is missing and "Android SDK Platform-Tools" if `adb` is missing,
then use `sdkmanager` as below to add any missing platform/build-tools
versions.

### Option B: headless install (no Android Studio)

1. Pick an SDK location and export the variables above with that path, e.g.
   `export ANDROID_HOME="$HOME/Android/Sdk"`. Do not point it inside the repo.
2. Download the latest "command line tools only" package from
   [developer.android.com](https://developer.android.com/studio#command-line-tools-only)
   and unzip it so that `sdkmanager` ends up at
   `$ANDROID_HOME/cmdline-tools/latest/bin/sdkmanager` (the zip extracts a
   `cmdline-tools` directory — rename/move it to `latest` inside
   `$ANDROID_HOME/cmdline-tools/`).
3. Accept licenses and install the SDK components:

   ```sh
   yes | sdkmanager --licenses
   sdkmanager "platform-tools" "platforms;android-36" "build-tools;36.0.0"
   ```

   Use the current stable platform/build-tools if 36 is no longer latest.

If `sdkmanager: command not found`, verify that
`$ANDROID_HOME/cmdline-tools/latest/bin/sdkmanager` exists and that directory
is on your `PATH` (open a new shell after editing your profile).

If `adb: command not found`, verify that `$ANDROID_HOME/platform-tools/adb`
exists and that directory is on your `PATH`. If the file is missing, install
platform-tools with `sdkmanager "platform-tools"` (or via Android Studio's
SDK Manager under **SDK Tools → Android SDK Platform-Tools**).

## Remote access stack

- `computer.iroh:iroh-android` supplies the Android `libiroh_ffi.so` libraries
  used to open authenticated HTTP/1.1 streams to the sidekick server, plus the
  matching `computer.iroh:iroh` Kotlin bindings and JNA's Android AAR
  (`libjnidispatch.so`). The bindings jar's desktop natives are excluded from
  APK packaging.
- Retrofit, OkHttp, and kotlinx.serialization provide the typed REST client.
  MockWebServer covers the HTTP layer in JVM tests.
- ZXing Embedded scans the server-generated pairing QR code.
- Navigation Compose routes between pairing, workspace selection, and task
  screens.
- Preferences DataStore persists the iroh ticket and bearer token.

The app requests camera permission when pairing and requires network access for
iroh connectivity. Native libraries are packaged for `arm64-v8a`,
`armeabi-v7a`, `x86_64`, and `x86`; release builds should retain the ABI splits
needed by their target devices.

## Fast dev loop

Robolectric runs the Compose UI tests on the JVM, so the inner loop needs no
device:

- **Continuous** (re-runs unit + Robolectric UI tests on every save):

  ```sh
  ./gradlew :app:testDebugUnitTest -t
  ```

- **One-shot**:

  ```sh
  ./gradlew testDebugUnitTest
  ```

Build cache + configuration cache keep both snappy.

## Install on a device

To try the app out as a developer, install a debug build on an Android device
(Android 7.0 / API 24+):

1. Complete the [toolchain setup](#toolchain--sdk-setup) above. This includes
   `adb`, which comes from the SDK's platform-tools package — see the
   troubleshooting note in that section if `adb` is not found.
2. Enable **Developer options** and **USB debugging** on the device, connect it
   over USB (or use `adb pair` + `adb connect` for wireless debugging), and
   confirm it shows up in `adb devices`.
3. Build and install the debug APK from `app/android`:

   ```sh
   ./gradlew :app:installDebug
   ```

   Alternatively, `./gradlew :app:assembleDebug` produces
   `app/build/outputs/apk/debug/app-debug.apk`, which can be sideloaded with
   `adb install -r`.

The app installs as **Sidekick**. To pair it with a server:

1. Start the sidekick server (`side start`, or the development backend from
   [CONTRIBUTING.md](../../CONTRIBUTING.md)) and open the web UI.
2. Click the **Remote Control** icon (a basic smartphone) in the left sidebar,
   enter a device name, and generate a pairing code. The QR code is displayed
   right in the web app. It embeds a one-time token, so generate a fresh code
   if it is lost before scanning.
3. Open the app, tap **Scan pairing code**, grant camera access, and scan the
   QR code from the screen. The connection runs over iroh, so the device does
   not need to be on the same network as the server.

## On-device / e2e tests

`PairingEntryInstrumentedTest` (`src/androidTest`) verifies that the app launches
into the pairing entry screen on a real device/emulator:

```sh
./gradlew connectedDebugAndroidTest
```

Do **not** run this in the container — it requires `/dev/kvm` and a
device/emulator. For CI, use nested virtualization or a device farm such as
Firebase Test Lab or Gradle Managed Devices.

## Deferred / add later

These are intentionally not implemented yet. For each: what it replaces / why
it's preferred, when to add it, and any container caveat.

- **Static analysis — detekt + the formatting (ktlint) ruleset.** Replaces manual
  style review with automated linting. Add once the build is stable, in its own
  commit. Container caveat: plugin resolution can be flaky in-container, so wire
  it as non-blocking rather than letting it gate builds.
- **Mocking — MockK.** Replaces hand-written fakes for test doubles. Add when a
  class gains collaborators that fakes can't cover cleanly; until then fakes keep
  tests simpler, faster, and less brittle. No container caveat.
- **Assertions — assertk (or kotest-assertions).** A readability upgrade over
  plain JUnit assertions. Optional; add only when assertions get verbose enough
  to justify the dependency. No container caveat.
- **Leak detection — LeakCanary (`debugImplementation`).** Replaces manual memory
  inspection. Add when device testing starts. Container caveat: it's a runtime
  tool that only does anything on a device/emulator, so it's useless in the
  headless container.
- **Images — Coil (Compose-native).** Replaces manual bitmap loading. Add on the
  first remote image. No container caveat.
- **Dependency injection — Koin (lighter, incremental) or Hilt (mainstream).**
  Replaces manual constructor injection. Add when wiring outgrows constructor
  injection. No container caveat.
- **Room.** Add when structured local storage is needed beyond the connection
  credentials held in DataStore. No container caveat.
- **Modularization — split `feature/*` and `core/*` into Gradle modules.** Replaces
  the single `:app` module. Add as the app grows; the package layout is already
  shaped for this. No container caveat.
- **Screenshot tests — Roborazzi.** Adds UI regression coverage. A good early
  addition. Container-compatible: runs on the JVM via Robolectric native graphics.
- **E2E flows — Maestro (YAML, low-flakiness).** Replaces Espresso/UIAutomator for
  full-app flows. Add when device-based e2e is needed. Container caveat: needs a
  running app on a device/emulator, so it's a CI-with-device concern, not
  in-container.