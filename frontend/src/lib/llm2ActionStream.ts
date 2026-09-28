import type { Llm2ContentBlock, Llm2StreamEvent } from './models'
import { Llm2StreamAccumulator } from './llm2StreamAccumulator'
import { StreamingJsonParser } from './streamingJson'

type Llm2StreamEventPayload = Pick<Llm2StreamEvent, 'type' | 'index' | 'contentBlock' | 'delta'>

export interface Llm2StreamSnapshot {
  blocks: Llm2ContentBlock[]
  /** Progressively complete tool_use arguments keyed by position in `blocks`. */
  toolArguments: Record<number, object>
}

/**
 * Streaming state for one flow action: accumulates llm2 content blocks and
 * parses each tool_use block's arguments incrementally so renderers always get
 * a valid object. Block snapshots are reported synchronously per event, while
 * argument snapshots arrive asynchronously as jsonriver consumes the deltas.
 */
export class Llm2ActionStream {
  private readonly accumulator = new Llm2StreamAccumulator()
  // Parsers and parsed results are keyed by stream index, since a block's
  // position among rendered blocks can shift when a lower index starts later.
  private readonly parsers = new Map<number, StreamingJsonParser>()
  private readonly parsedByIndex = new Map<number, object>()
  private disposed = false

  constructor(private readonly onChange: (snapshot: Llm2StreamSnapshot) => void) {}

  applyEvent(event: Llm2StreamEventPayload): void {
    if (this.disposed) return
    this.accumulator.applyEvent(event)
    const block = this.accumulator.blockAt(event.index)
    if (block?.type === 'tool_use') {
      switch (event.type) {
        case 'block_started':
          this.startParser(event.index, block.toolUse.arguments)
          break
        case 'text_delta':
          this.feedParser(event.index, block.toolUse.arguments)
          break
        case 'block_done':
          this.parsers.get(event.index)?.end()
          break
      }
    }
    this.emit()
  }

  dispose(): void {
    this.disposed = true
    for (const parser of this.parsers.values()) {
      parser.dispose()
    }
    this.parsers.clear()
  }

  private startParser(index: number, args: string): void {
    this.parsers.get(index)?.dispose()
    this.parsedByIndex.delete(index)
    const parser = new StreamingJsonParser((value) => this.publishArguments(index, value))
    this.parsers.set(index, parser)
    parser.feed(args)
  }

  private feedParser(index: number, args: string): void {
    const parser = this.parsers.get(index)
    if (!parser) {
      this.startParser(index, args)
      return
    }
    if (parser.feed(args)) return
    // A parser that already gave up on the document stops consuming, so keep
    // the raw fallback in step with the text that keeps arriving.
    if (parser.failed) {
      this.parsedByIndex.set(index, { raw: args })
    }
  }

  private publishArguments(index: number, value: unknown): void {
    if (this.disposed) return
    const parsed = value !== null && typeof value === 'object'
      ? (value as object)
      : { raw: this.parsers.get(index)?.text ?? '' }
    this.parsedByIndex.set(index, parsed)
    this.emit()
  }

  private emit(): void {
    const toolArguments: Record<number, object> = {}
    for (const [index, parsed] of this.parsedByIndex) {
      const position = this.accumulator.positionOf(index)
      if (position !== -1) toolArguments[position] = parsed
    }
    this.onChange({ blocks: this.accumulator.blocks, toolArguments })
  }
}