package persisted_ai

import "sidekick/llm2"

const ContextTypeDeliverable = "deliverable"

// DeliverableMetadata describes an operation or its report, not both.
// A request snapshot is validated only by a subsequent success report.
type DeliverableMetadata struct {
	ID        string `json:"id"`
	Operation string `json:"operation,omitempty"`
	Outcome   string `json:"outcome,omitempty"`
	Snapshot  bool   `json:"snapshot,omitempty"`
}

func GetDeliverable(block llm2.ContentBlock) *DeliverableMetadata {
	meta := getSidekickBlockMetadata(block)
	if meta == nil || meta.ContextType != ContextTypeDeliverable {
		return nil
	}
	return meta.Deliverable
}

func SetDeliverable(block *llm2.ContentBlock, deliverable DeliverableMetadata) {
	SetContextType(block, ContextTypeDeliverable)
	meta := *getSidekickBlockMetadata(*block)
	meta.Deliverable = &deliverable
	block.Metadata[MetadataNamespaceSidekick] = &meta
}

type historyBlockPosition struct {
	message int
	block   int
}

type deliverableAttempt struct {
	id        string
	operation historyBlockPosition
	report    historyBlockPosition
	outcome   string
}

// deliverableAttempts uses content order rather than tool protocol identities,
// so non-tool producers and interleaved deliverables follow the same rules.
func deliverableAttempts(messages []llm2.Message) []deliverableAttempt {
	pending := make(map[string]historyBlockPosition)
	var attempts []deliverableAttempt
	for i, msg := range messages {
		for j, block := range msg.Content {
			meta := GetDeliverable(block)
			if meta == nil || meta.ID == "" {
				continue
			}
			position := historyBlockPosition{message: i, block: j}
			if meta.Operation != "" && meta.Outcome == "" {
				pending[meta.ID] = position
				continue
			}
			if meta.Operation != "" || (meta.Outcome != "success" && meta.Outcome != "failure") {
				continue
			}
			operation, ok := pending[meta.ID]
			if !ok {
				continue
			}
			attempts = append(attempts, deliverableAttempt{
				id: meta.ID, operation: operation, report: position, outcome: meta.Outcome,
			})
			delete(pending, meta.ID)
		}
	}
	return attempts
}

func protectedDeliverableBlocks(messages []llm2.Message) [][]bool {
	protected := make([][]bool, len(messages))
	for i, msg := range messages {
		protected[i] = make([]bool, len(msg.Content))
	}
	successes := make(map[string]deliverableAttempt)
	failures := make(map[string]deliverableAttempt)
	for _, attempt := range deliverableAttempts(messages) {
		if attempt.outcome == "success" {
			successes[attempt.id] = attempt
			delete(failures, attempt.id)
		} else {
			failures[attempt.id] = attempt
		}
	}
	protect := func(attempt deliverableAttempt) {
		protected[attempt.operation.message][attempt.operation.block] = true
		protected[attempt.report.message][attempt.report.block] = true
	}
	for _, attempt := range successes {
		protect(attempt)
	}
	for _, attempt := range failures {
		protect(attempt)
	}
	return protected
}
