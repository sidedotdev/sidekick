import { describe, it, expect } from 'vitest'
import { StreamingJsonParser } from './streamingJson'

async function settle() {
  // jsonriver consumes chunks over several microtask hops; a macrotask boundary lets it drain fully.
  await new Promise((resolve) => setTimeout(resolve, 0))
}

function recording() {
  const snapshots: unknown[] = []
  const parser = new StreamingJsonParser((value) => snapshots.push(value))
  return { parser, snapshots, latest: () => snapshots.at(-1) }
}

describe('StreamingJsonParser', () => {
  it('publishes progressively complete values as the text grows', async () => {
    const { parser, latest } = recording()

    expect(parser.feed('{"query":"hel')).toBe(true)
    await settle()
    expect(latest()).toEqual({ query: 'hel' })

    expect(parser.feed('{"query":"hello","paths":["a"')).toBe(true)
    await settle()
    expect(latest()).toEqual({ query: 'hello', paths: ['a'] })

    expect(parser.feed('{"query":"hello","paths":["a","b"]}')).toBe(true)
    await settle()
    expect(latest()).toEqual({ query: 'hello', paths: ['a', 'b'] })
    expect(parser.failed).toBe(false)
  })

  it('publishes independent snapshots that later chunks do not mutate', async () => {
    const { parser, snapshots, latest } = recording()

    parser.feed('{"a":["x"')
    await settle()
    const first = latest()
    parser.feed('{"a":["x","y"],"b":true}')
    await settle()

    expect(first).toEqual({ a: ['x'] })
    expect(latest()).toEqual({ a: ['x', 'y'], b: true })
    expect(snapshots.at(-1)).not.toBe(first)
  })

  it('rejects text that does not extend what was already fed', () => {
    const { parser } = recording()
    parser.feed('{"a":1')
    expect(parser.feed('{"b":2')).toBe(false)
    expect(parser.feed('{"a":1')).toBe(true)
    expect(parser.text).toBe('{"a":1')
  })

  it('completes without failure when ended on valid JSON', async () => {
    const { parser, latest } = recording()
    parser.feed('{"a":1}')
    parser.end()
    await settle()
    expect(parser.failed).toBe(false)
    expect(latest()).toEqual({ a: 1 })
  })

  it('falls back to the raw text when ended on truncated JSON', async () => {
    const { parser, latest } = recording()
    parser.feed('{"query":"hello","paths":["a"')
    await settle()
    expect(latest()).toEqual({ query: 'hello', paths: ['a'] })

    parser.end()
    await settle()
    expect(parser.failed).toBe(true)
    expect(latest()).toEqual({ raw: '{"query":"hello","paths":["a"' })
  })

  it('falls back to the raw text when ended on garbage', async () => {
    const { parser, latest } = recording()
    parser.feed('not json at all')
    parser.end()
    await settle()
    expect(parser.failed).toBe(true)
    expect(latest()).toEqual({ raw: 'not json at all' })
  })

  it('fails as soon as the text can no longer become valid JSON', async () => {
    const { parser, latest } = recording()
    parser.feed('{"a":1}}')
    await settle()
    expect(parser.failed).toBe(true)
    expect(latest()).toEqual({ raw: '{"a":1}}' })
    expect(parser.feed('{"a":1}} more')).toBe(false)
  })

  it('refuses text after end', () => {
    const { parser } = recording()
    parser.feed('{"a":')
    parser.end()
    expect(parser.feed('{"a":1}')).toBe(false)
  })

  it('stops publishing once disposed', async () => {
    const { parser, snapshots } = recording()
    parser.feed('{"a":')
    parser.dispose()
    await settle()
    expect(snapshots).toEqual([])
    expect(parser.feed('{"a":1}')).toBe(false)
  })
})