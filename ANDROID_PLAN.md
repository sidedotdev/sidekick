Scaffold a minimal-but-well-architected Kotlin + Jetpack Compose (Material 3) Android app
in THIS headless Linux dev container. Priorities, in order: fast dev loop, long-term
maintainability, fast + reliable automated testability. Everything must build and test from
the CLI (no Android Studio GUI, no @Preview/Live Edit at runtime, likely no emulator). Use
latest stable versions, pinned in a Gradle version catalog; if any pinned version fails to
resolve, look up the current stable and use that instead. Implement PART A now. Do NOT
implement PART B — write it as a "Deferred / add later" section in README.md using the
notes below.

================= PART A — BUILD NOW =================

1. TOOLCHAIN (skip anything already present)
- JDK 17 active (java -version → 17); install Temurin 17 if missing.
- If sdkmanager is absent, install the Android SDK headlessly: download the latest
  commandline-tools, unzip to $ANDROID_HOME/cmdline-tools/latest; yes | sdkmanager --licenses;
  sdkmanager "platform-tools" "platforms;android-36" "build-tools;36.0.0" — if 36 is no
  longer the latest stable, use the current stable platform/build-tools instead.
  Export ANDROID_HOME and ANDROID_SDK_ROOT, add to PATH, persist to the shell profile,
  and write local.properties with sdk.dir=$ANDROID_HOME.

2. PROJECT — single :app module, package com.example.app.
- settings.gradle.kts: pluginManagement + dependencyResolutionManagement with google() and
  mavenCentral(); rootProject.name="app"; include(":app").
- gradle/libs.versions.toml — pin and reference everything from here:
  plugins:
    com.android.application (latest stable AGP),
    org.jetbrains.kotlin.android (latest stable Kotlin 2.x — 2.0+ is required for the
      compose plugin),
    org.jetbrains.kotlin.plugin.compose (same Kotlin version).
  libraries:
    UI  → androidx.compose:compose-bom (latest stable), activity-compose,
          lifecycle-runtime-ktx, lifecycle-viewmodel-compose, compose ui, ui-graphics,
          ui-tooling-preview, material3.
    test (JVM, src/test) → junit 4.13.2, app.cash.turbine:turbine (latest),
          org.jetbrains.kotlinx:kotlinx-coroutines-test,
          org.robolectric:robolectric (4.14+), androidx.compose.ui:ui-test-junit4.
    androidTest → androidx.test.ext:junit, androidx.compose.ui:ui-test-junit4.
    debug only → androidx.compose.ui:ui-tooling, ui-test-manifest.
  Do NOT add mocking, assertion, static-analysis, or debug-tooling libraries beyond this
  list — those are deliberately deferred (see PART B).
- Root build.gradle.kts: the three plugins with `apply false`.
- app/build.gradle.kts:
    plugins { application; kotlin-android; kotlin-compose }
    android { namespace="com.example.app"; compileSdk = latest stable (36 or newer)
      defaultConfig { applicationId; minSdk=24; targetSdk=compileSdk; versionCode=1;
        versionName="1.0"; testInstrumentationRunner="androidx.test.runner.AndroidJUnitRunner" }
      buildFeatures { compose=true }
      compileOptions { source/target 17 }; kotlin { jvmToolchain(17) }
      testOptions { unitTests { isIncludeAndroidResources = true } } }  // required by Robolectric
    dependencies wired from the catalog exactly as grouped above
      (implementation(platform(bom)); testImplementation(platform(bom));
       androidTestImplementation(platform(bom))).
- gradle.properties: org.gradle.caching=true; org.gradle.configuration-cache=true;
  org.gradle.parallel=true; org.gradle.jvmargs=-Xmx2g; android.useAndroidX=true.
- Wrapper: gradle wrapper --gradle-version <latest Gradle compatible with the AGP chosen>
  (create wrapper files manually if no gradle is on PATH).

3. SOURCE — package-by-feature + layered, so it splits into modules cleanly later. Use
   src/main/kotlin, src/test/kotlin, src/androidTest/kotlin.
   com.example.app/
     MainActivity.kt                       // ComponentActivity → setContent { AppTheme { CounterRoute() } }
     core/ui/theme/…                        // minimal Material 3 theme
     core/coroutine/DispatcherProvider.kt   // interface { val default/io/main: CoroutineDispatcher } + default impl
     feature/counter/
       CounterUiState.kt                    // immutable data class (UDF state)
       CounterViewModel.kt                  // ViewModel; exposes StateFlow<CounterUiState>;
                                            //   takes DispatcherProvider via constructor (testable coroutines)
       CounterScreen.kt                     // STATELESS: CounterScreen(state, onIncrement) + a @Preview;
                                            //   plus stateful CounterRoute(vm = viewModel()) that
                                            //   collectAsStateWithLifecycle() and forwards callbacks
   Apply state hoisting + unidirectional data flow throughout: composables take state +
   event lambdas and never touch the ViewModel directly except in the Route wrapper. Use
   testTag on the count Text ("counter") and the increment Button ("increment").
- AndroidManifest.xml: one exported MainActivity with LAUNCHER intent filter.

4. TESTS (all must run headless on the JVM)
   src/test:
   - CounterViewModelTest: kotlinx-coroutines-test (runTest + an injected
     StandardTestDispatcher via DispatcherProvider) and Turbine to assert StateFlow
     emissions (initial state → after increment). Plain JUnit/kotlin.test assertions.
     No mocking library — this ViewModel has no collaborators; if a future class needs a
     test double, prefer a hand-written fake first (see PART B for when MockK earns its place).
   - CounterScreenJvmTest: @RunWith(RobolectricTestRunner::class) @Config(sdk=[34]);
     @get:Rule createComposeRule(); setContent { CounterScreen(state, onIncrement) } with
     state held in the test and onIncrement updating it; assert "counter" reads 0,
     performClick "increment", assert it reads 1. This is the fast JVM UI test — no device.
     If a Compose/loader error appears, add @Config(instrumentedPackages=["androidx.loader.content"]).
   src/androidTest:
   - CounterScreenInstrumentedTest via createAndroidComposeRule<MainActivity>() — same
     assertions. Real on-device e2e; keep the file but do NOT run it in-container.

5. DEV LOOP + VERIFY (must pass headless, no device)
   - Primary fast loop (replaces @Preview/Live Edit here):
       ./gradlew :app:testDebugUnitTest -t     // continuous; re-runs unit + Robolectric UI on save
     Config cache + build cache keep it snappy.
   - One-shot: ./gradlew testDebugUnitTest → confirm green.
   - Do NOT run connectedDebugAndroidTest unless /dev/kvm and a device/emulator exist. If
     e2e is needed in CI, note it requires nested virtualization or a device farm
     (Firebase Test Lab or Gradle Managed Devices).
   - Report the final commands and the test-run output.

Constraints for PART A: dependencies strictly limited to the list above — no networking,
DI, navigation, persistence, mocking, static analysis, or extra assertion libraries yet.
Keep the app trivial but the architecture correct (hoisted state, injected dispatchers,
feature packages) so later additions are drop-in.

================= PART B — DOCUMENT ONLY (README "Deferred / add later") =================
For each item, note WHAT it replaces / why it's preferred, WHEN to add it, and any
container caveat. Do not implement any of these.
- Static analysis: detekt with the formatting (ktlint) ruleset — add once the build is
  stable, in its own commit. Plugin resolution can be flaky in-container, so wire it as
  non-blocking rather than letting it gate builds.
- Mocking: MockK — add when a class gains collaborators that hand-written fakes can't
  cover cleanly. Until then, fakes keep tests simpler, faster, and less brittle.
- Assertions: assertk (or kotest-assertions) — optional readability upgrade over plain
  JUnit assertions; not worth a dependency at this size.
- Leak detection: LeakCanary (debugImplementation) — runtime tool, only useful when the
  app runs on a device/emulator, so it does nothing in this container. Add when device
  testing starts.
- Networking: Retrofit + OkHttp + kotlinx.serialization (over HttpURLConnection/org.json);
  MockWebServer for tests. Add when the first API call appears.
- Images: Coil (Compose-native). Add on the first remote image.
- DI: Koin (lighter, incremental) or Hilt (mainstream). Add when wiring outgrows
  constructor injection.
- Navigation: type-safe Navigation Compose. Add on the 2nd screen.
- Persistence: Room + DataStore. Add when storage is needed.
- Modularization: split feature/* and core/* into Gradle modules as the app grows — the
  package layout above is already shaped for this.
- Screenshot tests: Roborazzi — runs on the JVM via Robolectric native graphics, so it IS
  container-compatible; a good early addition for UI regression.
- E2E flows: Maestro (YAML, low-flakiness, over Espresso/UIAutomator) — needs a running
  app on a device/emulator, so it's a CI-with-device concern,gg not in-container.
