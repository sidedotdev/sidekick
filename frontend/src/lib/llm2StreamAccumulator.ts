import type { Llm2ContentBlock, Llm2StreamEvent } from './models'

type Llm2StreamEventPayload = Pick<Llm2StreamEvent, 'type' | 'index' | 'contentBlock' | 'delta'>

/**
 * Rebuilds ordered llm2 content blocks from streamed events, mirroring the Go
 * llm2.AccumulateEventsToMessage semantics so the live view matches the
 * final assembled message block-for-block.
 */
export class Llm2StreamAccumulator {
  private readonly blocksByIndex = new Map<number, Llm2ContentBlock>()
  private maxIndex = -1

  applyEvent(event: Llm2StreamEventPayload): void {
    if (event.index > this.maxIndex) {
      this.maxIndex = event.index
    }

    switch (event.type) {
      case 'block_started':
        if (event.contentBlock) {
          this.blocksByIndex.set(event.index, cloneBlock(event.contentBlock))
        }
        break

      case 'text_delta': {
        const block = this.blocksByIndex.get(event.index)
        const delta = event.delta ?? ''
        if (!block) break
        if (block.type === 'text') {
          // Go omits empty text on the wire, so a freshly started block has no text field.
          block.text = (block.text ?? '') + delta
        } else if (block.type === 'reasoning' && block.reasoning) {
          block.reasoning.text = (block.reasoning.text ?? '') + delta
        } else if (block.type === 'tool_use' && block.toolUse) {
          block.toolUse.arguments += delta
        }
        break
      }

      case 'summary_text_delta': {
        const block = this.blocksByIndex.get(event.index)
        const delta = event.delta ?? ''
        if (!block) break
        if (block.type === 'text') {
          block.text = (block.text ?? '') + delta
        } else if (block.type === 'reasoning' && block.reasoning) {
          block.reasoning.summary = (block.reasoning.summary ?? '') + delta
        }
        break
      }

      case 'signature_delta': {
        const block = this.blocksByIndex.get(event.index)
        if (!block || block.type !== 'reasoning') break
        // Despite the event name, the delta carries the full encrypted content.
        block.reasoning = { ...(block.reasoning ?? {}), encryptedContent: event.delta ?? '' }
        break
      }

      case 'block_done': {
        const block = this.blocksByIndex.get(event.index)
        const done = event.contentBlock
        if (!block || !done) break
        if (block.type === 'builtin_tool_use' && done.type === 'builtin_tool_use' && done.builtinToolUse) {
          block.builtinToolUse = { ...done.builtinToolUse }
        }
        if (block.type === 'reasoning' && done.type === 'reasoning' && done.reasoning) {
          const reasoning = { ...(block.reasoning ?? {}) }
          if (done.reasoning.text) reasoning.text = done.reasoning.text
          if (done.reasoning.summary) reasoning.summary = done.reasoning.summary
          if (done.reasoning.encryptedContent) reasoning.encryptedContent = done.reasoning.encryptedContent
          block.reasoning = reasoning
        }
        break
      }
    }
  }

  /** Blocks ordered by stream index, skipping indexes that never started. */
  get blocks(): Llm2ContentBlock[] {
    const ordered: Llm2ContentBlock[] = []
    for (let i = 0; i <= this.maxIndex; i++) {
      const block = this.blocksByIndex.get(i)
      if (block) ordered.push(block)
    }
    return ordered
  }

  blockAt(streamIndex: number): Llm2ContentBlock | undefined {
    return this.blocksByIndex.get(streamIndex)
  }

  /** Position of the given stream index within `blocks`, or -1 if it never started. */
  positionOf(streamIndex: number): number {
    if (!this.blocksByIndex.has(streamIndex)) return -1
    let position = 0
    for (let i = 0; i < streamIndex; i++) {
      if (this.blocksByIndex.has(i)) position++
    }
    return position
  }
}

export function accumulateLlm2StreamEvents(events: Llm2StreamEventPayload[]): Llm2ContentBlock[] {
  const accumulator = new Llm2StreamAccumulator()
  for (const event of events) {
    accumulator.applyEvent(event)
  }
  return accumulator.blocks
}

// Block payloads arrive from JSON so a shallow copy of the block plus its
// mutable nested payload is enough to avoid aliasing the incoming event.
function cloneBlock(block: Llm2ContentBlock): Llm2ContentBlock {
  switch (block.type) {
    case 'tool_use':
      return { ...block, toolUse: { ...block.toolUse } }
    case 'reasoning':
      return { ...block, reasoning: { ...block.reasoning } }
    case 'builtin_tool_use':
      return { ...block, builtinToolUse: { ...block.builtinToolUse } }
    default:
      return { ...block }
  }
}