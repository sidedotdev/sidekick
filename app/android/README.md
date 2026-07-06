# Sidekick Android App

A minimal Kotlin + Jetpack Compose (Material 3) shell that will grow into a
remote control for the sidekick server over iroh. Right now it ships a trivial
Counter feature that establishes the architecture (state hoisting, unidirectional
data flow, injected dispatchers, package-by-feature) so later features drop in
cleanly.

## Toolchain / SDK setup (headless)

Everything builds and tests from the CLI — no Android Studio, no `@Preview`/Live
Edit at runtime, no emulator required for the fast loop.

- **JDK**: run the Gradle daemon on a current LTS (Temurin 17). `java -version`
  should report 17.
- **Android SDK** (headless): download the latest `commandline-tools`, unzip to
  `$ANDROID_HOME/cmdline-tools/latest`, then:

  ```sh
  yes | sdkmanager --licenses
  sdkmanager "platform-tools" "platforms;android-36" "build-tools;36.0.0"
  ```

  Use the current stable platform/build-tools if 36 is no longer latest. Export
  `ANDROID_HOME` and `ANDROID_SDK_ROOT`, add them to `PATH`, persist to the shell
  profile, and write `local.properties` with `sdk.dir=$ANDROID_HOME`.

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

`CounterScreenInstrumentedTest` (`src/androidTest`) mirrors the JVM assertions
but runs on a real device/emulator:

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
- **Networking — Retrofit + OkHttp + kotlinx.serialization, with MockWebServer for
  tests.** Replaces raw `HttpURLConnection`/`org.json`. This is the layer that
  will implement the sidekick REST / iroh integration (task listing) described in
  the original requirements. Add when the first API call appears. No container
  caveat (MockWebServer runs on the JVM).
- **Images — Coil (Compose-native).** Replaces manual bitmap loading. Add on the
  first remote image. No container caveat.
- **Dependency injection — Koin (lighter, incremental) or Hilt (mainstream).**
  Replaces manual constructor injection. Add when wiring outgrows constructor
  injection. No container caveat.
- **Navigation — type-safe Navigation Compose.** Replaces ad-hoc screen switching.
  Add on the 2nd screen. No container caveat.
- **Persistence — Room + DataStore.** Replaces in-memory/no storage. Add when
  storage is needed. No container caveat.
- **Modularization — split `feature/*` and `core/*` into Gradle modules.** Replaces
  the single `:app` module. Add as the app grows; the package layout is already
  shaped for this. No container caveat.
- **Screenshot tests — Roborazzi.** Adds UI regression coverage. A good early
  addition. Container-compatible: runs on the JVM via Robolectric native graphics.
- **E2E flows — Maestro (YAML, low-flakiness).** Replaces Espresso/UIAutomator for
  full-app flows. Add when device-based e2e is needed. Container caveat: needs a
  running app on a device/emulator, so it's a CI-with-device concern, not
  in-container.