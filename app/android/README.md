# Sidekick Android App

A Kotlin + Jetpack Compose (Material 3) remote control for the sidekick server.
It pairs with a server by scanning a QR code, connects over iroh, discovers
workspaces, and displays their tasks. The app uses state hoisting,
unidirectional data flow, injected dispatchers, and package-by-feature.

## Toolchain / SDK setup (headless)

Everything builds and tests from the CLI — no Android Studio, no `@Preview`/Live
Edit at runtime, no emulator required for the fast loop.

- **JDK**: run the Gradle daemon on JDK 21. `java -version` should report 21.
- **Android SDK** (headless): download the latest `commandline-tools`, unzip to
  `$ANDROID_HOME/cmdline-tools/latest`, then:

  ```sh
  yes | sdkmanager --licenses
  sdkmanager "platform-tools" "platforms;android-36" "build-tools;36.0.0"
  ```

  Use the current stable platform/build-tools if 36 is no longer latest. Export
  `ANDROID_HOME` and `ANDROID_SDK_ROOT`, add them to `PATH`, persist to the shell
  profile, and write `local.properties` with `sdk.dir=$ANDROID_HOME`.

## Remote access stack

- `computer.iroh:iroh` supplies the Kotlin API and native `libiroh_ffi.so`
  libraries used to open authenticated HTTP/1.1 streams to the sidekick server.
  JNA uses its Android AAR variant so the required `libjnidispatch.so` libraries
  are packaged without conflicting with JNA's transitive JVM JAR.
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