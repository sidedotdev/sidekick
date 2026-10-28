# Ephemeral JetStream storage

`SIDEKICK_EPHEMERAL_V2` uses FileStorage with NATS's native S2 block compression.
Subjects, message JSON, publishers, consumers, and frontend behavior are unchanged.
Retention remains LimitsPolicy, DiscardOld, 100 MiB of logical message storage,
and no age limit. Compression does not increase the logical retention limit.

The embedded server stores files under `<Sidekick data home>/nats-jetstream`.
Keep this directory on writable, persistent storage. Startup reuses an existing
file stream, preserving retained messages and sequence numbers, and reapplies
the retention settings. An existing uncompressed file stream is updated without
deleting messages. Compression works on sealed blocks; the active write block
stays uncompressed. Do not expect immediate compression of all existing bytes.

Retained messages now survive server restarts, until size-based eviction.
There is still no age-based expiry. Disk usage also includes metadata, consumers,
the active block, and temporary block rewrites; 100 MiB is not a filesystem quota.
FileStorage and publish acknowledgements do not imply per-message fsync or
protection against every power-loss scenario.

## Coordinated upgrade

Upgrade **all** Sidekick binaries together: API, workers, CLI, and scripts.
This is not a mixed-version rolling upgrade. Old binaries try to configure
MemoryStorage and fail against the new file stream with NATS `err_code=10052`
(`stream configuration update can not change storage type`). Downgrading has
the same incompatibility; preserve the file stream before any intentional reset.

### Normal standalone embedded server

1. Stop all Sidekick processes, including the process hosting embedded NATS.
2. Install the new binaries.
3. Restart Sidekick.

A standalone embedded NATS server does not preserve MemoryStorage streams across
a server restart. The new binary creates the file stream fresh. Previously
retained ephemeral history is lost just as it was on any old server restart.
If that history matters, export it **before** stopping the old server.

Subsequent restarts preserve the new file-backed history. No manual directory
deletion is needed or recommended.

### NATS server outlives the Sidekick processes

An external NATS server, or a long-lived embedded host that was not restarted,
can still have the legacy memory stream. NATS cannot change its storage type
in place. Startup deliberately returns `requires explicit migration` rather than
deleting or purging it.

1. Stop every Sidekick publisher and consumer, including workers and scripts,
   while leaving the NATS server running if its memory history is needed.
2. Inspect `SIDEKICK_EPHEMERAL_V2` using your NATS administration tooling.
   Export any needed messages, headers, subjects, and sequence metadata, and
   verify the export before proceeding. Startup never copies or deletes them.
3. Only after accepting the loss of the retained ephemeral replay history,
   explicitly delete **that stream**, using the NATS stream management API or
   your administration tooling. Do not delete the NATS storage directory or
   unrelated streams.
4. Start the upgraded Sidekick processes. Startup creates the new file stream.

This reset does not preserve replay cursors: fresh sequence numbers start over.
Reconnect clients and reload authoritative task/flow state rather than reuse
pre-migration cursors. If lossless in-stream history/cursor preservation is
required, leave the legacy stream intact and arrange a separately validated,
offline migration before upgrading; this startup path does not implement one.

Empty legacy memory streams also require explicit action. Checking message
count and deleting are separate NATS operations: a publisher can add a message
between them. Refusing even an empty stream avoids silently deleting that
message when startup races with an old binary.

## Verification and performance

Tests use loopback servers on random ports and private temporary directories;
they never connect to the running Sidekick environment.

See [`scripts/jetstream_latency`](../../scripts/jetstream_latency/README.md)
for the isolated FileStorage versus S2 FileStorage comparison. Disk compression
is not evidence of equivalent RAM savings: NATS caches and decompresses blocks,
and the OS may cache file pages.