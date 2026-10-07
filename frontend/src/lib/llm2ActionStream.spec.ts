import { describe, expect, it, vi } from 'vitest'
import { Llm2ActionStream, type Llm2StreamSnapshot } from './llm2ActionStream'
import type { Llm2ContentBlock, Llm2StreamEvent } from './models'

type Payload = Pick<Llm2StreamEvent, 'type' | 'index' | 'contentBlock' | 'delta'>

const started = (index: number, contentBlock: Llm2ContentBlock): Payload => ({ type: 'block_started', index, contentBlock })
const textDelta = (index: number, delta: string): Payload => ({ type: 'text_delta', index, delta })
const done = (index: number): Payload => ({ type: 'block_done', index })

const toolUse = (id = 'call_1'): Llm2ContentBlock => ({ type: 'tool_use', toolUse: { id, name: 'search', arguments: '' } })

// jsonriver parses on microtasks, so argument snapshots land after a short wait.
const settle = () => new Promise((resolve) => setTimeout(resolve, 0))

function newStream() {
  const snapshots: Llm2StreamSnapshot[] = []
  const stream = new Llm2ActionStream((snapshot) => snapshots.push(snapshot))
  const latest = () => snapshots[snapshots.length - 1]
  return { stream, snapshots, latest }
}

describe('Llm2ActionStream', () => {
  it('reports accumulated blocks synchronously on every event', () => {
    const { stream, latest } = newStream()
    stream.applyEvent(started(0, { type: 'text', text: '' }))
    stream.applyEvent(textDelta(0, 'Hel'))
    expect(latest().blocks).toEqual([{ type: 'text', text: 'Hel' }])
    stream.applyEvent(textDelta(0, 'lo'))
    expect(latest().blocks).toEqual([{ type: 'text', text: 'Hello' }])
  })

  it('parses tool_use arguments incrementally keyed by block position', async () => {
    const { stream, latest } = newStream()
    stream.applyEvent(started(0, { type: 'text', text: 'Calling' }))
    stream.applyEvent(started(1, toolUse()))
    stream.applyEvent(textDelta(1, '{"query":"te'))
    await settle()
    expect(latest().toolArguments).toEqual({ 1: { query: 'te' } })

    stream.applyEvent(textDelta(1, 'st","limit":2}'))
    await settle()
    expect(latest().toolArguments).toEqual({ 1: { query: 'test', limit: 2 } })
    expect(latest().blocks[1]).toEqual({ type: 'tool_use', toolUse: { id: 'call_1', name: 'search', arguments: '{"query":"test","limit":2}' } })
  })

  it('tracks several tool calls independently', async () => {
    const { stream, latest } = newStream()
    stream.applyEvent(started(0, toolUse('call_1')))
    stream.applyEvent(started(1, toolUse('call_2')))
    // A trailing space delimits the number; until then more digits could follow.
    stream.applyEvent(textDelta(0, '{"a":1 '))
    stream.applyEvent(textDelta(1, '{"b":'))
    await settle()
    expect(latest().toolArguments).toEqual({ 0: { a: 1 }, 1: {} })
    stream.applyEvent(textDelta(1, '"x"}'))
    stream.applyEvent(textDelta(0, '}'))
    await settle()
    expect(latest().toolArguments).toEqual({ 0: { a: 1 }, 1: { b: 'x' } })
  })

  it('falls back to raw arguments when a tool_use block ends on invalid JSON', async () => {
    const { stream, latest } = newStream()
    stream.applyEvent(started(0, toolUse()))
    stream.applyEvent(textDelta(0, '{"query":"unterminated'))
    stream.applyEvent(done(0))
    await settle()
    expect(latest().toolArguments).toEqual({ 0: { raw: '{"query":"unterminated' } })
  })

  it('keeps finalized arguments while later blocks stream', async () => {
    const { stream, latest } = newStream()
    stream.applyEvent(started(0, toolUse('call_1')))
    stream.applyEvent(textDelta(0, '{"a":"done"}'))
    stream.applyEvent(done(0))
    stream.applyEvent(started(1, toolUse('call_2')))
    stream.applyEvent(textDelta(1, 'not json'))
    stream.applyEvent(done(1))
    stream.applyEvent(started(2, { type: 'text', text: '' }))
    stream.applyEvent(textDelta(2, 'after'))
    await settle()
    expect(latest().toolArguments).toEqual({ 0: { a: 'done' }, 1: { raw: 'not json' } })
    expect(latest().blocks.map((b) => b.type)).toEqual(['tool_use', 'tool_use', 'text'])
  })

  it('keeps the raw fallback current once parsing has failed mid-stream', async () => {
    const { stream, latest } = newStream()
    stream.applyEvent(started(0, toolUse()))
    stream.applyEvent(textDelta(0, '{"a":1} trailing'))
    await settle()
    expect(latest().toolArguments).toEqual({ 0: { raw: '{"a":1} trailing' } })
    stream.applyEvent(textDelta(0, ' more'))
    await settle()
    expect(latest().toolArguments).toEqual({ 0: { raw: '{"a":1} trailing more' } })
  })

  it('keys arguments by rendered position even with sparse or late-starting indexes', async () => {
    const { stream, latest } = newStream()
    stream.applyEvent(started(3, toolUse('call_1')))
    stream.applyEvent(textDelta(3, '{"late":true}'))
    await settle()
    expect(latest().toolArguments).toEqual({ 0: { late: true } })

    stream.applyEvent(started(1, { type: 'text', text: 'inserted before' }))
    await settle()
    expect(latest().blocks.map((b) => b.type)).toEqual(['text', 'tool_use'])
    expect(latest().toolArguments).toEqual({ 1: { late: true } })
  })

  it('restarts parsing when a block is restarted at the same index', async () => {
    const { stream, latest } = newStream()
    stream.applyEvent(started(0, toolUse('call_1')))
    stream.applyEvent(textDelta(0, '{"first":'))
    await settle()
    stream.applyEvent(started(0, toolUse('call_2')))
    stream.applyEvent(textDelta(0, '{"second":true}'))
    await settle()
    expect(latest().toolArguments).toEqual({ 0: { second: true } })
  })

  it('stops reporting once disposed', async () => {
    const onChange = vi.fn()
    const stream = new Llm2ActionStream(onChange)
    stream.applyEvent(started(0, toolUse()))
    stream.applyEvent(textDelta(0, '{"query":"te'))
    stream.dispose()
    const callsAtDispose = onChange.mock.calls.length
    await settle()
    expect(onChange.mock.calls.length).toBe(callsAtDispose)
  })
})