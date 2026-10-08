package common

import (
	"context"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/workflow"
)

// replayAwareDataConverter offloads large payloads through the codec except
// when workflow code is replaying. The SDK re-runs workflow code on replay and
// encodes every command input (activity, child workflow, signal) before it
// discovers the matching event already exists in history and discards the
// command, so a plain codec would write a fresh copy of every large input to
// KV on each replay of a long-lived workflow. Those copies are never
// referenced by history. Encoding inline during replay yields a
// self-contained payload: it is discarded with its command, and stays
// decodable if the SDK reads it back internally.
//
// Decoding always goes through the codec, and encoding outside a workflow
// context (clients, activities) always offloads.
type replayAwareDataConverter struct {
	converter.DataConverter
	inline converter.DataConverter
}

var _ workflow.ContextAware = (*replayAwareDataConverter)(nil)

// NewReplayAwareDataConverter wraps parent with codec, skipping the codec for
// values encoded by replaying workflow code.
func NewReplayAwareDataConverter(parent converter.DataConverter, codec converter.PayloadCodec) converter.DataConverter {
	return &replayAwareDataConverter{
		DataConverter: converter.NewCodecDataConverter(parent, codec),
		inline:        parent,
	}
}

func (d *replayAwareDataConverter) WithWorkflowContext(ctx workflow.Context) converter.DataConverter {
	return &workflowContextDataConverter{replayAwareDataConverter: d, ctx: ctx}
}

func (d *replayAwareDataConverter) WithContext(context.Context) converter.DataConverter {
	return d
}

type workflowContextDataConverter struct {
	*replayAwareDataConverter
	ctx workflow.Context
}

// encoder is chosen per call: a single workflow task can start in replay and
// transition to live execution once it passes the last recorded event.
func (d *workflowContextDataConverter) encoder() converter.DataConverter {
	if workflow.IsReplaying(d.ctx) {
		return d.inline
	}
	return d.DataConverter
}

func (d *workflowContextDataConverter) ToPayload(value interface{}) (*commonpb.Payload, error) {
	return d.encoder().ToPayload(value)
}

func (d *workflowContextDataConverter) ToPayloads(values ...interface{}) (*commonpb.Payloads, error) {
	return d.encoder().ToPayloads(values...)
}
