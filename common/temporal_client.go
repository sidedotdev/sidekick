package common

import (
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/contrib/opentelemetry"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/interceptor"
)

// NewTemporalClientOptions builds the base client.Options that all Temporal
// clients in the codebase should share. Callers may override or extend
// individual fields (Logger, ContextPropagators, etc.) before dialing.
func NewTemporalClientOptions(storage KeyValueStorage, hostPort string) (client.Options, error) {
	tracingInterceptor, err := opentelemetry.NewTracingInterceptor(opentelemetry.TracerOptions{})
	if err != nil {
		return client.Options{}, err
	}

	codec := NewPayloadCodec(storage, DefaultCodecThreshold)
	return client.Options{
		HostPort:      hostPort,
		Interceptors:  []interceptor.ClientInterceptor{tracingInterceptor},
		DataConverter: converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), codec),
	}, nil
}
