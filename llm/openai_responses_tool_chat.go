package llm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
	"github.com/rs/zerolog/log"
	"go.temporal.io/sdk/activity"
)

const OpenaiResponsesDefaultModel = "gpt-5-codex"

type OpenaiResponsesToolChat struct{}

func (o OpenaiResponsesToolChat) ChatStream(ctx context.Context, options ToolChatOptions, deltaChan chan<- ChatMessageDelta, progressChan chan<- ProgressInfo) (*ChatMessageResponse, error) {
	heartbeatCtx, cancelHeartbeat := context.WithCancel(context.Background())
	defer cancelHeartbeat()
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				{
					if activity.IsActivity(ctx) {
						activity.RecordHeartbeat(ctx, map[string]bool{"fake": true})
					}
					continue
				}
			}
		}
	}()

	providerNameNormalized := options.Params.ModelConfig.NormalizedProviderName()
	token, err := options.Secrets.SecretManager.GetSecret(fmt.Sprintf("%s_API_KEY", providerNameNormalized))
	if err != nil {
		return nil, err
	}

	client := openai.NewClient(option.WithAPIKey(token))

	var model string
	if options.Params.Model != "" {
		model = options.Params.Model
	} else {
		model = OpenaiResponsesDefaultModel
	}

	input := buildInputFromMessages(options.Params.Messages)

	params := responses.ResponseNewParams{
		Input: responses.ResponseNewParamsInputUnion{
			OfString: openai.String(input),
		},
		Model: openai.ChatModel(model),
	}

	if options.Params.Temperature != nil {
		params.Temperature = openai.Float(float64(*options.Params.Temperature))
	}

	stream := client.Responses.NewStreaming(ctx, params)

	var deltas []ChatMessageDelta
	var stopReason string
	var usage Usage

loop:
	for stream.Next() {
		data := stream.Current()

		switch data.AsAny().(type) {
		case responses.ResponseCompletedEvent:
			response := data.Response
			if response.IncompleteDetails.Reason != "" {
				stopReason = string(response.IncompleteDetails.Reason)
			} else {
				// TODO in step 4 map response.Status == "failed", "cancelled"
				// or "completed" to appropriate stop reason, other is passed as
				// `response_status=${ status }` go-equivalent
			}
			if response.Usage.InputTokens > 0 {
				usage.InputTokens = int(response.Usage.InputTokens)
			}
			if response.Usage.OutputTokens > 0 {
				usage.OutputTokens = int(response.Usage.OutputTokens)
			}
			break loop
		case responses.ResponseReasoningSummaryTextDeltaEvent:
			// TODO later task output to progressChan similar to google tool chat
			continue

		/* TODO: uncomment this in step 4 of the plan
		case responses.ResponseOutputItemAddedEvent:
			event := data.AsResponseOutputItemAdded()
			switch event.Item.AsAny().(type) {
			case responses.ResponseFunctionToolCall:
				item := event.Item.AsFunctionCall()
				delta := ChatMessageDelta{
					Role: ChatMessageRoleAssistant,
					ToolCalls: []ToolCall{
						{Id: item.CallID, Name: item.Name, Arguments: item.Arguments},
					},
				}
				delta = cleanupDelta(delta)
				deltaChan <- delta
				deltas = append(deltas, delta)
				//	case responses.ResponseFunctionWebSearch:
				//	case responses.ResponseComputerToolCall:
				//	case responses.ResponseReasoningItem:
				//	case responses.ResponseOutputItemImageGenerationCall:
				//	case responses.ResponseCodeInterpreterToolCall:
				//	case responses.ResponseOutputItemLocalShellCall:
				//	case responses.ResponseOutputItemMcpCall:
				//	case responses.ResponseOutputItemMcpListTools:
				//	case responses.ResponseOutputItemMcpApprovalRequest:
				//	case responses.ResponseCustomToolCall:
			}
		case responses.ResponseFunctionCallArgumentsDeltaEvent:
			event := data.AsResponseFunctionCallArgumentsDelta()
			delta := ChatMessageDelta{
				Role: ChatMessageRoleAssistant,
				ToolCalls: []ToolCall{
					{Arguments: event.Delta},
				},
			}
			delta = cleanupDelta(delta)
			deltaChan <- delta
			deltas = append(deltas, delta)
		END_TODO: uncomment this in step 4 of the plan
		*/
		case responses.ResponseReasoningTextDeltaEvent:
			delta := ChatMessageDelta{
				Role:    ChatMessageRoleAssistant,
				Content: data.Delta,
			}
			delta = cleanupDelta(delta)
			deltaChan <- delta
			deltas = append(deltas, delta)
		default:
			fmt.Printf("GOT: %s\n", data.Type)
		}
	}

	if err := stream.Err(); err != nil {
		return nil, err
	}

	message := stitchDeltasToMessage(deltas, false)
	if message.Role == "" && len(deltas) == 0 && usage.InputTokens == 0 && usage.OutputTokens == 0 {
		err := errors.New("chat message role not found")
		log.Error().Err(err).Interface("deltas", deltas)
		return nil, err
	}

	return &ChatMessageResponse{
		ChatMessage: message,
		StopReason:  stopReason,
		Usage:       usage,
		Model:       model,
		Provider:    options.Params.Provider,
	}, nil
}

func buildInputFromMessages(messages []ChatMessage) string {
	var builder strings.Builder
	for i, msg := range messages {
		if i > 0 {
			builder.WriteString("\n")
		}
		builder.WriteString(string(msg.Role))
		builder.WriteString(": ")
		builder.WriteString(msg.Content)
	}
	return builder.String()
}
