# Isolated native JetStream compression comparison

From the repository root:

```sh
go run ./scripts/jetstream_latency -runs=3 > .side/tmp/jetstream-latency-results.jsonl
go run ./scripts/jetstream_latency -summarize=.side/tmp/jetstream-latency-results.jsonl
```

No running Sidekick services are contacted. Every case starts a fresh subprocess,
a loopback NATS server on a random port, and a private storage directory under
`.side/tmp`. Successful and ordinary failed trials remove their storage
directories; interrupted subprocesses may leave private directories there.
The probe has no option to connect to an external server.

## Workload

Each repetition compares uncompressed FileStorage and native S2 FileStorage
under both steady and burst traffic. Mode order alternates between repetitions.
Both modes use the same subjects and 100 MiB/DiscardOld/no-age-limit configuration
as Sidekick. NATS server defaults control block sizes and flushing.

Defaults:

- `-messages=12000`: measured JSON messages per case.
- `-warmup=256`: additional unmeasured messages before timing.
- `-body-bytes=4096`: repeated synthetic log text per message, plus JSON overhead.
- `-interval=1ms`: steady publish schedule, one synchronous publisher.
- `-workers=32`: concurrent synchronous publishers during bursts.
- `-burst-size=256`, `-burst-pause=20ms`: burst size and inter-burst pause.
- `-runs=3`, `-timeout=3m`: repetitions and per-case timeout.
- `-temp-root=.side/tmp`: parent directory for private storage directories.

All published and delivered payloads remain plain JSON. This is intentionally a
highly compressible synthetic workload, not a replay of production traffic.
Vary body size, rate, concurrency, and burst size for sensitivity checks. Do not
generalize the compression ratio to arbitrary payload entropy or real workloads.

Steady traffic is closed-loop: an outstanding publish acknowledgement can delay
subsequent sends. Bursts have bounded concurrency and finish publishing before
the next pause. Neither mode models an unbounded open-loop queue. Results exclude
publisher scheduling/JSON serialization time before the publish call; they are
not a measurement of user-request latency under arbitrary saturation.

## Measurements and validity

- `publishAck`: time from immediately before `js.Publish` until its acknowledgement.
- `delivery`: time from that same start until the ordered JetStream consumer
  callback begins, independent of publish-ack completion. Every delivered payload
  is decoded and checked; missing messages, duplicates, and consumer errors fail
  the trial. This includes loopback delivery but not frontend/WebSocket latency.
- Latencies use a monotonic clock and nearest-rank p50/p95/p99 plus the maximum.
  Warmup is excluded. Durations in the workload config are nanoseconds; latency
  fields ending in `Ms` are milliseconds.
- `logicalBytes`: NATS stream accounting, not physical disk space.
- `disk.fileBytes`: sum of regular file lengths in the stopped server's storage
  directory, including metadata; not filesystem allocated sectors.
- `disk.blockBytes`: stored message block lengths, including the active block.
- `disk.expandedBlockBytes`: those same blocks after native decompression.
- `disk.blocks` / `compressedBlocks`: retained block counts. The probe parses
  NATS's compression metadata and actually decompresses each S2 block using the
  pinned server library, verifying its expanded length. It rejects an S2 trial
  with zero compressed sealed blocks, an uncompressed control with compressed
  blocks, corrupt blocks, or any workload that never rolls over a block.

In NATS 2.10.27, sealed blocks compress at rollover; the active last block remains
uncompressed. Default traffic writes roughly 50 MiB, spanning multiple natural
blocks. The probe does not change block sizes or manually compress stored data.
Disk inspection runs after graceful shutdown, outside the latency measurements.

Memory is sampled every 10 ms from the idle server/consumer before warmup until
delivery completes, before shutdown and diagnostic decompression. Reports include
baseline, end, sampled peak, and end-minus-baseline deltas for Go HeapInuse and
Linux RSS (`/proc/self/statm`, omitted if unavailable). Fresh subprocesses prevent
allocator state from earlier modes contaminating later modes. **RSS and heap cover
both the embedded server and probe client**, including arrays, JSON handling, and
consumer bookkeeping. Sampled peaks can miss short-lived spikes; OS file cache
outside process RSS is not measured. Sampling also adds some overhead to both modes.

**Compressed disk size is not RAM savings.** NATS uses caches and decompresses
blocks. Heap allocation, RSS, and disk usage must be interpreted separately.

## Summary mode

`-summarize` reads reports without launching servers. It requires paired cases
with the same run number, workload, and server version. It rejects duplicate or
incomplete runs, invalid controls, and S2 rows without verified compressed blocks.

For each traffic/compression group it reports the median, minimum, and maximum
across runs of latency percentiles and per-run maxima, disk sizes, block counts,
throughput, and memory metrics. The median of run maxima is not the global worst
case: the `max` of the `publishMaxMs` or `deliveryMaxMs` metric is the worst
observed case. Compare between-mode differences with within-mode ranges before
claiming a speedup or regression. Three runs are exploratory evidence, not a
statistical significance test.

## Example measurements from this change

Linux/amd64 sandbox, NATS 2.10.27, three fresh subprocesses per case, default
parameters above. No other tests were run concurrently with the measurement.
The JSONL output was validated with `-summarize` before interpreting it.
These are exploratory local measurements, not a production latency guarantee.

All times below are milliseconds. p50, p99, and run-max columns are **medians
across three runs**; worst is the largest observation across all three runs.

| Traffic | Compression | Ack p50 | Ack p99 | Ack run-max | Ack worst | Delivery p50 | Delivery p99 | Delivery run-max | Delivery worst |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| steady | none | 0.346 | 0.658 | 15.806 | 149.042 | 0.374 | 0.731 | 15.916 | 149.080 |
| steady | S2 | 0.345 | 0.758 | 41.855 | 321.197 | 0.377 | 0.867 | 41.973 | 321.421 |
| burst | none | 1.516 | 5.090 | 9.276 | 10.698 | 1.945 | 5.728 | 9.960 | 10.802 |
| burst | S2 | 1.600 | 18.118 | 25.074 | 65.468 | 1.940 | 18.619 | 25.251 | 65.982 |

Steady ack p99 ranged from 0.649–1.141 ms without compression and
0.730–1.135 ms with S2; delivery p99 ranged from 0.689–1.197 and
0.829–1.231 ms respectively. Those ranges overlap, as do p50 ranges. The
between-mode differences do not establish a steady-traffic latency effect.
The large individual worst cases also show substantial run-to-run noise.

Burst ack p99 ranges were 4.562–6.412 ms without compression versus
15.060–18.314 ms with S2; delivery p99 ranges were 4.783–6.798 versus
15.369–18.631 ms. Those ranges do not overlap, and their between-mode median
differences exceed the within-mode p99 ranges. S2 had higher burst-tail latency
in this experiment. This does not by itself prove that every spike was caused
by compression or predict latency on a different disk, CPU, or workload.
Burst median throughput was 7,352 messages/s without compression versus 6,828
with S2; steady throughput was approximately 1,000 messages/s in both modes.

Every run retained all 12,256 messages (including warmup), with 51,868,538 logical
bytes. Median disk measurements:

| Traffic | Compression | Blocks | Compressed blocks | Block bytes | Expanded block bytes | All file bytes |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| steady | none | 7 | 0 | 51,868,538 | 51,868,538 | 51,869,290 |
| steady | S2 | 7 | 6 | 1,912,610 | 51,868,538 | 1,913,360 |
| burst | none | 7 | 0 | 51,868,538 | 51,868,538 | 51,869,290 |
| burst | S2 | 7 | 6 | 1,919,953 | 51,868,538 | 1,920,703 |

That is about **96.3% fewer stored block bytes** (49.47 MiB down to about
1.83 MiB). All six sealed blocks were decoded and their expanded sizes checked;
the seventh, active block remained uncompressed. The repeated synthetic log text
is unusually compressible, so this ratio is not an estimate for production data.

Median process memory measurements, in MiB:

| Traffic | Compression | RSS end-minus-baseline | HeapInuse end-minus-baseline | Sampled peak HeapInuse |
| --- | --- | ---: | ---: | ---: |
| steady | none | 123.7 | 99.0 | 102.8 |
| steady | S2 | 148.7 | 72.4 | 115.5 |
| burst | none | 133.8 | 77.1 | 100.3 |
| burst | S2 | 156.8 | 91.0 | 117.7 |

S2's RSS deltas were higher, not lower; their ranges did not overlap the
uncompressed ranges in either traffic mode. End HeapInuse was lower for steady
S2 but higher for burst S2, while sampled peak HeapInuse was higher for S2 in
both. These process-wide measurements include the client and depend on GC/cache
timing. **No general RAM savings were demonstrated. Compressed disk size is not
RAM savings.**