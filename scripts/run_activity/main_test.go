package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/sdk/converter"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func exampleActivity(context.Context, map[string]interface{}, int) error {
	return nil
}

func malformedInputActivity(context.Context, map[string]interface{}) error {
	return nil
}

func noInputActivity(context.Context) error {
	return nil
}

func largeIntegerActivity(context.Context, int64) error {
	return nil
}

func protobufActivity(context.Context, *wrapperspb.Int64Value) error {
	return nil
}

func testActivityRegistry() map[string]interface{} {
	return map[string]interface{}{
		"ExampleActivity":      exampleActivity,
		"LargeIntegerActivity": largeIntegerActivity,
		"ProtobufActivity":     protobufActivity,
	}
}

type testHistoryIterator struct {
	events []*historypb.HistoryEvent
	index  int
	err    error
}

func (i *testHistoryIterator) HasNext() bool {
	return i.index < len(i.events) || i.err != nil
}

func (i *testHistoryIterator) Next() (*historypb.HistoryEvent, error) {
	if i.err != nil {
		err := i.err
		i.err = nil
		return nil, err
	}
	event := i.events[i.index]
	i.index++
	return event, nil
}

func TestFindActivityInvocation(t *testing.T) {
	t.Parallel()

	dataConverter := converter.GetDefaultDataConverter()
	input, err := dataConverter.ToPayloads(map[string]interface{}{"message": "hello"}, 3)
	require.NoError(t, err)

	events := []*historypb.HistoryEvent{
		{
			EventId:   10,
			EventType: enumspb.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED,
			Attributes: &historypb.HistoryEvent_ActivityTaskScheduledEventAttributes{
				ActivityTaskScheduledEventAttributes: &historypb.ActivityTaskScheduledEventAttributes{
					ActivityId:   "activity-abc",
					ActivityType: &commonpb.ActivityType{Name: "ExampleActivity"},
					Input:        input,
				},
			},
		},
		{
			EventId:   11,
			EventType: enumspb.EVENT_TYPE_ACTIVITY_TASK_STARTED,
			Attributes: &historypb.HistoryEvent_ActivityTaskStartedEventAttributes{
				ActivityTaskStartedEventAttributes: &historypb.ActivityTaskStartedEventAttributes{
					ScheduledEventId: 10,
				},
			},
		},
		{
			EventId:   12,
			EventType: enumspb.EVENT_TYPE_ACTIVITY_TASK_COMPLETED,
			Attributes: &historypb.HistoryEvent_ActivityTaskCompletedEventAttributes{
				ActivityTaskCompletedEventAttributes: &historypb.ActivityTaskCompletedEventAttributes{
					ScheduledEventId: 10,
				},
			},
		},
	}

	tests := []struct {
		name       string
		identifier string
	}{
		{name: "activity ID", identifier: "activity-abc"},
		{name: "scheduled event ID", identifier: "10"},
		{name: "started event ID", identifier: "11"},
		{name: "completed event ID", identifier: "12"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			invocation, err := findActivityInvocation(
				&testHistoryIterator{events: events},
				dataConverter,
				testActivityRegistry(),
				tt.identifier,
			)
			require.NoError(t, err)
			assert.Equal(t, "ExampleActivity", invocation.Name)
			require.Len(t, invocation.Args, 2)
			assert.JSONEq(t, `{"message":"hello"}`, string(invocation.Args[0]))
			assert.JSONEq(t, `3`, string(invocation.Args[1]))
		})
	}
}

func TestFindActivityInvocationPrefersActivityIDOverEventID(t *testing.T) {
	t.Parallel()

	dataConverter := converter.GetDefaultDataConverter()
	firstInput, err := dataConverter.ToPayloads("first")
	require.NoError(t, err)
	secondInput, err := dataConverter.ToPayloads("second")
	require.NoError(t, err)

	events := []*historypb.HistoryEvent{
		{
			EventId:   10,
			EventType: enumspb.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED,
			Attributes: &historypb.HistoryEvent_ActivityTaskScheduledEventAttributes{
				ActivityTaskScheduledEventAttributes: &historypb.ActivityTaskScheduledEventAttributes{
					ActivityId:   "12",
					ActivityType: &commonpb.ActivityType{Name: "FirstActivity"},
					Input:        firstInput,
				},
			},
		},
		{
			EventId:   11,
			EventType: enumspb.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED,
			Attributes: &historypb.HistoryEvent_ActivityTaskScheduledEventAttributes{
				ActivityTaskScheduledEventAttributes: &historypb.ActivityTaskScheduledEventAttributes{
					ActivityId:   "other",
					ActivityType: &commonpb.ActivityType{Name: "SecondActivity"},
					Input:        secondInput,
				},
			},
		},
		{
			EventId:   12,
			EventType: enumspb.EVENT_TYPE_ACTIVITY_TASK_COMPLETED,
			Attributes: &historypb.HistoryEvent_ActivityTaskCompletedEventAttributes{
				ActivityTaskCompletedEventAttributes: &historypb.ActivityTaskCompletedEventAttributes{
					ScheduledEventId: 11,
				},
			},
		},
	}

	invocation, err := findActivityInvocation(
		&testHistoryIterator{events: events},
		dataConverter,
		map[string]interface{}{
			"FirstActivity":  func(context.Context, string) error { return nil },
			"SecondActivity": func(context.Context, string) error { return nil },
		},
		"12",
	)

	require.NoError(t, err)
	assert.Equal(t, "FirstActivity", invocation.Name)
	require.Len(t, invocation.Args, 1)
	assert.JSONEq(t, `"first"`, string(invocation.Args[0]))
}

func TestFindActivityInvocationNotFound(t *testing.T) {
	t.Parallel()

	_, err := findActivityInvocation(
		&testHistoryIterator{},
		converter.GetDefaultDataConverter(),
		testActivityRegistry(),
		"missing",
	)

	require.EqualError(t, err, `activity "missing" not found in workflow history`)
}

func TestFindActivityInvocationHistoryError(t *testing.T) {
	t.Parallel()

	_, err := findActivityInvocation(
		&testHistoryIterator{err: errors.New("history unavailable")},
		converter.GetDefaultDataConverter(),
		testActivityRegistry(),
		"activity-abc",
	)

	require.EqualError(t, err, "error fetching workflow history: history unavailable")
}

func TestFindActivityInvocationRejectsMalformedInput(t *testing.T) {
	t.Parallel()

	events := []*historypb.HistoryEvent{
		{
			EventId:   10,
			EventType: enumspb.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED,
			Attributes: &historypb.HistoryEvent_ActivityTaskScheduledEventAttributes{
				ActivityTaskScheduledEventAttributes: &historypb.ActivityTaskScheduledEventAttributes{
					ActivityId:   "activity-abc",
					ActivityType: &commonpb.ActivityType{Name: "ExampleActivity"},
					Input: &commonpb.Payloads{Payloads: []*commonpb.Payload{
						{
							Metadata: map[string][]byte{
								"encoding": []byte("json/plain"),
							},
							Data: []byte(`{invalid`),
						},
					}},
				},
			},
		},
	}

	_, err := findActivityInvocation(
		&testHistoryIterator{events: events},
		converter.GetDefaultDataConverter(),
		map[string]interface{}{"ExampleActivity": malformedInputActivity},
		"activity-abc",
	)

	require.Error(t, err)
	assert.Contains(t, err.Error(), `decode activity "activity-abc" at scheduled event 10`)
	assert.Contains(t, err.Error(), "decode argument 0")
}

func TestResolveActivityInvocation(t *testing.T) {
	t.Parallel()

	jsonFile := func(string) (os.FileInfo, error) {
		return testFileInfo{name: "input.json", mode: 0}, nil
	}
	notAFile := func(string) (os.FileInfo, error) {
		return nil, os.ErrNotExist
	}
	readFile := func(string) ([]byte, error) {
		return []byte(`[{"message":"hello"}]`), nil
	}

	tests := []struct {
		name              string
		args              []string
		stat              func(string) (os.FileInfo, error)
		wantName          string
		wantHistoryLookup bool
	}{
		{
			name:     "explicit JSON subcommand",
			args:     []string{"json", "ExampleActivity", "input.json"},
			stat:     notAFile,
			wantName: "ExampleActivity",
		},
		{
			name:     "legacy JSON invocation",
			args:     []string{"ExampleActivity", "input.json"},
			stat:     jsonFile,
			wantName: "ExampleActivity",
		},
		{
			name:              "history invocation",
			args:              []string{"flow-123", "activity-456"},
			stat:              notAFile,
			wantName:          "HistoryActivity",
			wantHistoryLookup: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			historyLookupCalled := false
			loadFromHistory := func(_ context.Context, flowID string, activityID string) (activityInvocation, error) {
				historyLookupCalled = true
				assert.Equal(t, "flow-123", flowID)
				assert.Equal(t, "activity-456", activityID)
				return activityInvocation{Name: "HistoryActivity"}, nil
			}

			invocation, err := resolveActivityInvocation(
				context.Background(),
				tt.args,
				loadFromHistory,
				readFile,
				tt.stat,
			)

			require.NoError(t, err)
			assert.Equal(t, tt.wantName, invocation.Name)
			assert.Equal(t, tt.wantHistoryLookup, historyLookupCalled)
			if !tt.wantHistoryLookup {
				require.Len(t, invocation.Args, 1)
				assert.JSONEq(t, `{"message":"hello"}`, string(invocation.Args[0]))
			}
		})
	}
}

type testFileInfo struct {
	name string
	mode os.FileMode
}

func (i testFileInfo) Name() string       { return i.name }
func (i testFileInfo) Size() int64        { return 0 }
func (i testFileInfo) Mode() os.FileMode  { return i.mode }
func (i testFileInfo) ModTime() time.Time { return time.Time{} }
func (i testFileInfo) IsDir() bool        { return i.mode.IsDir() }
func (i testFileInfo) Sys() interface{}   { return nil }

func TestDecodeActivityInvocationPreservesConcreteTypes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		activityName string
		activityFn   interface{}
		input        interface{}
		wantJSON     string
	}{
		{
			name:         "large int64",
			activityName: "LargeIntegerActivity",
			activityFn:   largeIntegerActivity,
			input:        int64(9007199254740993),
			wantJSON:     `9007199254740993`,
		},
		{
			name:         "protobuf payload",
			activityName: "ProtobufActivity",
			activityFn:   protobufActivity,
			input:        wrapperspb.Int64(9007199254740993),
			wantJSON:     `{"value":9007199254740993}`,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			payloads, err := converter.GetDefaultDataConverter().ToPayloads(tt.input)
			require.NoError(t, err)

			invocation, err := decodeActivityInvocation(
				converter.GetDefaultDataConverter(),
				tt.activityName,
				payloads,
				tt.activityFn,
			)

			require.NoError(t, err)
			require.Len(t, invocation.Args, 1)
			assert.JSONEq(t, tt.wantJSON, string(invocation.Args[0]))
		})
	}
}

func TestDecodeActivityInvocationWithoutInput(t *testing.T) {
	t.Parallel()

	invocation, err := decodeActivityInvocation(
		converter.GetDefaultDataConverter(),
		"ExampleActivity",
		(*commonpb.Payloads)(nil),
		noInputActivity,
	)

	require.NoError(t, err)
	assert.Equal(t, "ExampleActivity", invocation.Name)
	assert.Empty(t, invocation.Args)

	encoded, err := json.Marshal(invocation.Args)
	require.NoError(t, err)
	assert.JSONEq(t, "null", string(encoded))
}
