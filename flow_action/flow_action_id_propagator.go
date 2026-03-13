package flow_action

import (
	"context"

	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/workflow"
)

type flowActionIdKeyType struct{}

var flowActionIdCtxKey = flowActionIdKeyType{}

const sidekickFlowActionIdHeaderKey = "sidekickFlowActionId"

type flowActionIdPropagator struct{}

func NewFlowActionIdPropagator() workflow.ContextPropagator {
	return &flowActionIdPropagator{}
}

func (f *flowActionIdPropagator) InjectFromWorkflow(ctx workflow.Context, writer workflow.HeaderWriter) error {
	val := ctx.Value(flowActionIdCtxKey)
	if id, ok := val.(string); ok && id != "" {
		payload, err := converter.GetDefaultDataConverter().ToPayload(id)
		if err != nil {
			return err
		}
		writer.Set(sidekickFlowActionIdHeaderKey, payload)
	}
	return nil
}

func (f *flowActionIdPropagator) ExtractToWorkflow(ctx workflow.Context, reader workflow.HeaderReader) (workflow.Context, error) {
	payload, ok := reader.Get(sidekickFlowActionIdHeaderKey)
	if !ok || payload == nil {
		return ctx, nil
	}
	var id string
	if err := converter.GetDefaultDataConverter().FromPayload(payload, &id); err != nil {
		return ctx, err
	}
	return workflow.WithValue(ctx, flowActionIdCtxKey, id), nil
}

func (f *flowActionIdPropagator) Inject(ctx context.Context, writer workflow.HeaderWriter) error {
	val := ctx.Value(flowActionIdCtxKey)
	if id, ok := val.(string); ok && id != "" {
		payload, err := converter.GetDefaultDataConverter().ToPayload(id)
		if err != nil {
			return err
		}
		writer.Set(sidekickFlowActionIdHeaderKey, payload)
	}
	return nil
}

func (f *flowActionIdPropagator) Extract(ctx context.Context, reader workflow.HeaderReader) (context.Context, error) {
	payload, ok := reader.Get(sidekickFlowActionIdHeaderKey)
	if !ok || payload == nil {
		return ctx, nil
	}
	var id string
	if err := converter.GetDefaultDataConverter().FromPayload(payload, &id); err != nil {
		return ctx, err
	}
	return context.WithValue(ctx, flowActionIdCtxKey, id), nil
}
