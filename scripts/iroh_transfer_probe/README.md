# Iroh transfer diagnostic

This probe isolates Go/Rust iroh transfers from the database, REST handlers,
Android loopback proxy, and JSON decoding. It serves a deterministic,
uncompressed payload using an ephemeral endpoint identity. It neither reads nor
changes Sidekick's data directory.

## Server

Run from the repository root, substituting the server's current reachable LAN IP:

```sh
go run ./scripts/iroh_transfer_probe \
  -bind '[::]:0' -advertise 192.168.0.35 -relay \
  -bytes 52428800 -duration 5m
```

Stdout contains one JSON object with `ticket`, `nodeId`, `bytes`, and `sha256`.
Stderr contains connection and write timings. Payloads up to 64 MiB are accepted.
Omit `-relay` for a direct-only control.

A completed server write is not proof of delivery: the client must verify the
byte count and checksum. Server write timings can include buffering.

## Android client

Build the debug app and instrumentation APK with Gradle, then install with
`adb install -r` to preserve app data. Avoid `connectedDebugAndroidTest` on a
personally paired phone: its installation lifecycle can remove the app.

Pass the values printed by the probe:

```sh
adb shell am instrument -w \
  -e class com.example.app.IrohTransferInstrumentedTest \
  -e sidekickProbeTicket "$TICKET" \
  -e sidekickProbeBytes "$BYTES" \
  -e sidekickProbeSha256 "$SHA256" \
  -e sidekickProbeRounds 3 \
  com.example.app.test/androidx.test.runner.AndroidJUnitRunner
```

The test verifies status, exact body length, and SHA-256 incrementally. Each
round opens a stream on the same connection and has a 25-second limit.
Restart the server and instrumentation to exercise fresh connections. Check
for `OK (1 test)` and no failures; adb's exit status alone is insufficient.
`SidekickPhoneE2E` logcat entries report byte progress and selected paths.

For a Go client control:

```sh
go run ./scripts/iroh_transfer_probe \
  -ticket "$TICKET" -bytes "$BYTES" -rounds 3
```

## Interpreting comparisons

Keep the phone library, network, bind address, relay setting, payload, and
deadline unchanged when comparing server versions. Alternate old and new
binaries: intermittent success on the old version is expected.

In the investigation motivating this probe, the old Go snapshot
`d5b13786f0ee` stalled with relay support enabled even while Android reported
a selected direct IP path. With the same Android library, `go-iroh v0.2.2`
passed 35 transfers at 2 MiB, followed by nine transfers each at 10 MiB and
50 MiB in an expanded run. The old version also failed the 10 MiB control.
These observations support the upgrade, not attribution to a specific
upstream commit or a guarantee against all network failures.

Use `RemoteWorkspaceIrohInstrumentedTest` separately to validate the production
API with a temporary pairing from the normally running server. Do not start
another server against live databases to run this diagnostic.
