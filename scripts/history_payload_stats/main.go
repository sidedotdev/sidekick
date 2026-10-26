// history_payload_stats summarizes where the bytes in a Temporal workflow
// history live, using a protobuf snapshot written by `worker/replay
// -history-cache`. It answers "what makes this history heavy" without dumping
// events: wire size per event type, per activity type (inputs vs results), and
// how much payload is inline versus offloaded through the sidekick codec.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"sidekick/temporal"
	"sort"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/history/v1"
	"google.golang.org/protobuf/proto"
)

const codecMetadataKey = "sidekick-payload-codec-key"

type sizeCount struct {
	Count int
	Bytes int
}

type activityStats struct {
	Scheduled   sizeCount
	Completed   sizeCount
	InputBytes  int
	ResultBytes int
}

func main() {
	top := flag.Int("top", 15, "number of rows to print per table")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: %s [-top N] <history-snapshot.pb>\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}

	workflowId, hist, wireBytes, err := readSnapshot(flag.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("workflowId=%s\n", workflowId)
	summarize(hist, wireBytes, *top, os.Stdout)
}

// readSnapshot loads a snapshot written by `worker/replay -history-cache`,
// returning the workflow it was captured from and the file's size in bytes.
func readSnapshot(path string) (string, *history.History, int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", nil, 0, err
	}
	workflowId, hist, err := temporal.DecodeHistorySnapshot(data)
	if err != nil {
		return "", nil, 0, fmt.Errorf("%s: %w", path, err)
	}
	return workflowId, hist, len(data), nil
}

// summarize prints the payload/size breakdown of hist. wireBytes is the size
// of the serialized snapshot the events were read from.
func summarize(hist *history.History, wireBytes int, top int, out io.Writer) {
	byEventType := map[string]*sizeCount{}
	byActivity := map[string]*activityStats{}
	activityTypeBySchedule := map[int64]string{}
	var inline, offloaded sizeCount
	var largest []struct {
		EventId int64
		Type    string
		Bytes   int
	}

	for _, e := range hist.Events {
		size := proto.Size(e)
		et := e.GetEventType().String()
		sc := byEventType[et]
		if sc == nil {
			sc = &sizeCount{}
			byEventType[et] = sc
		}
		sc.Count++
		sc.Bytes += size

		label := et
		switch attrs := e.Attributes.(type) {
		case *history.HistoryEvent_ActivityTaskScheduledEventAttributes:
			name := attrs.ActivityTaskScheduledEventAttributes.GetActivityType().GetName()
			activityTypeBySchedule[e.GetEventId()] = name
			label = "ActivityTaskScheduled " + name
			as := activityFor(byActivity, name)
			as.Scheduled.Count++
			as.Scheduled.Bytes += size
			as.InputBytes += payloadsSize(attrs.ActivityTaskScheduledEventAttributes.GetInput(), &inline, &offloaded)
		case *history.HistoryEvent_ActivityTaskCompletedEventAttributes:
			name := activityTypeBySchedule[attrs.ActivityTaskCompletedEventAttributes.GetScheduledEventId()]
			label = "ActivityTaskCompleted " + name
			as := activityFor(byActivity, name)
			as.Completed.Count++
			as.Completed.Bytes += size
			as.ResultBytes += payloadsSize(attrs.ActivityTaskCompletedEventAttributes.GetResult(), &inline, &offloaded)
		case *history.HistoryEvent_MarkerRecordedEventAttributes:
			label = "MarkerRecorded " + attrs.MarkerRecordedEventAttributes.GetMarkerName()
			for _, p := range attrs.MarkerRecordedEventAttributes.GetDetails() {
				payloadsSize(p, &inline, &offloaded)
			}
		case *history.HistoryEvent_WorkflowExecutionSignaledEventAttributes:
			label = "WorkflowExecutionSignaled " + attrs.WorkflowExecutionSignaledEventAttributes.GetSignalName()
			payloadsSize(attrs.WorkflowExecutionSignaledEventAttributes.GetInput(), &inline, &offloaded)
		case *history.HistoryEvent_WorkflowExecutionStartedEventAttributes:
			payloadsSize(attrs.WorkflowExecutionStartedEventAttributes.GetInput(), &inline, &offloaded)
		}
		largest = append(largest, struct {
			EventId int64
			Type    string
			Bytes   int
		}{e.GetEventId(), label, size})
	}

	fmt.Fprintf(out, "events=%d wireBytes=%d\n", len(hist.Events), wireBytes)
	fmt.Fprintf(out, "payloads (activity input/result, markers, signals, start input only): inline count=%d bytes=%d | codec-offloaded count=%d bytes=%d\n\n",
		inline.Count, inline.Bytes, offloaded.Count, offloaded.Bytes)

	fmt.Fprintln(out, "Bytes by event type:")
	printSizeCounts(out, byEventType, top)

	fmt.Fprintln(out, "\nBytes by activity type (scheduled+completed events):")
	byActivityTotal := map[string]*sizeCount{}
	for name, as := range byActivity {
		byActivityTotal[name] = &sizeCount{Count: as.Scheduled.Count, Bytes: as.Scheduled.Bytes + as.Completed.Bytes}
	}
	printSizeCounts(out, byActivityTotal, top)

	fmt.Fprintln(out, "\nActivity payload split (input vs result bytes):")
	names := make([]string, 0, len(byActivity))
	for name := range byActivity {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		a, b := byActivity[names[i]], byActivity[names[j]]
		return a.InputBytes+a.ResultBytes > b.InputBytes+b.ResultBytes
	})
	for i, name := range names {
		if i >= top {
			break
		}
		as := byActivity[name]
		fmt.Fprintf(out, "  %-45s count=%-5d input=%-10d result=%-10d\n", name, as.Scheduled.Count, as.InputBytes, as.ResultBytes)
	}

	fmt.Fprintln(out, "\nLargest individual events:")
	sort.Slice(largest, func(i, j int) bool { return largest[i].Bytes > largest[j].Bytes })
	for i, l := range largest {
		if i >= top {
			break
		}
		fmt.Fprintf(out, "  %-8d %-60s %d\n", l.EventId, l.Type, l.Bytes)
	}
}

func activityFor(m map[string]*activityStats, name string) *activityStats {
	as := m[name]
	if as == nil {
		as = &activityStats{}
		m[name] = as
	}
	return as
}

// payloadsSize returns the wire size of the payloads and tallies each payload
// as inline or codec-offloaded.
func payloadsSize(p *commonpb.Payloads, inline, offloaded *sizeCount) int {
	total := 0
	for _, payload := range p.GetPayloads() {
		size := proto.Size(payload)
		total += size
		if _, ok := payload.GetMetadata()[codecMetadataKey]; ok {
			offloaded.Count++
			offloaded.Bytes += size
		} else {
			inline.Count++
			inline.Bytes += size
		}
	}
	return total
}

func printSizeCounts(out io.Writer, m map[string]*sizeCount, top int) {
	type row struct {
		Name string
		sizeCount
	}
	rows := make([]row, 0, len(m))
	for name, sc := range m {
		rows = append(rows, row{name, *sc})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Bytes > rows[j].Bytes })
	for i, r := range rows {
		if i >= top {
			break
		}
		fmt.Fprintf(out, "  %-45s count=%-6d bytes=%d\n", r.Name, r.Count, r.Bytes)
	}
}
