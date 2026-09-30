import { parse } from 'jsonriver'

export type StreamingJsonSnapshot = (value: unknown) => void

/**
 * Incrementally parses one JSON document that arrives as a growing string,
 * such as tool-call arguments streamed token by token. A progressively
 * complete value is published after every chunk jsonriver manages to consume.
 *
 * jsonriver defers judgement on unfinished input until the stream ends, so the
 * owner must call `end()` once no more text can arrive: that is what turns a
 * truncated or garbage document into a failure with the `{ raw }` fallback.
 *
 * jsonriver also mutates the values it yields in place, so each published
 * snapshot is a deep copy and consumers can rely on a fresh reference per update.
 */
export class StreamingJsonParser {
  private consumed = ''
  private pending: string[] = []
  private wake: (() => void) | null = null
  private ended = false
  private disposed = false
  private hasFailed = false

  constructor(private readonly publish: StreamingJsonSnapshot) {
    void this.consume()
  }

  get failed(): boolean {
    return this.hasFailed
  }

  /** The full text fed so far. */
  get text(): string {
    return this.consumed
  }

  /**
   * Feeds the full text seen so far and queues only the new suffix. Returns
   * false when the text is not accepted: either it does not extend what was
   * already fed (the caller then needs a fresh parser) or the stream has
   * already ended, failed or been disposed.
   */
  feed(text: string): boolean {
    if (this.ended || this.hasFailed || !text.startsWith(this.consumed)) return false
    const delta = text.slice(this.consumed.length)
    if (delta.length === 0) return true
    this.consumed = text
    this.pending.push(delta)
    this.wake?.()
    return true
  }

  /** Signals that no more text will arrive, so incomplete JSON is reported as a failure. */
  end(): void {
    this.ended = true
    this.wake?.()
  }

  /** Ends the stream and suppresses any further snapshots. */
  dispose(): void {
    this.disposed = true
    this.end()
  }

  private async *chunks(): AsyncGenerator<string> {
    while (true) {
      const chunk = this.pending.shift()
      if (chunk !== undefined) {
        yield chunk
        continue
      }
      if (this.ended) return
      await new Promise<void>((resolve) => {
        this.wake = () => {
          this.wake = null
          resolve()
        }
      })
    }
  }

  private async consume(): Promise<void> {
    try {
      for await (const value of parse(this.chunks())) {
        if (this.disposed) return
        this.publish(JSON.parse(JSON.stringify(value)))
      }
    } catch {
      this.hasFailed = true
      if (!this.disposed) this.publish({ raw: this.consumed })
    }
  }
}