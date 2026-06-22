import { describe, it, expect } from 'vitest'
import { diffAddedRanges } from '../intent_diff'

const extractAdded = (previous: string, current: string): string[] =>
  diffAddedRanges(previous, current).map((r) => current.slice(r.from, r.to))

describe('diffAddedRanges', () => {
  it('treats the whole text as added when there is no committed baseline', () => {
    expect(diffAddedRanges('', '# Hello')).toEqual([{ from: 0, to: 7 }])
  })

  it('returns nothing when texts are identical', () => {
    expect(diffAddedRanges('# Hello', '# Hello')).toEqual([])
  })

  it('returns nothing when only whitespace runs changed', () => {
    expect(diffAddedRanges('# Hello world', '#  Hello\nworld')).toEqual([])
  })

  it('flags a single inserted word inside otherwise identical prose', () => {
    const previous = 'Build a great product'
    const current = 'Build a really great product'
    expect(extractAdded(previous, current)).toEqual(['really '])
  })

  it('finds word-level changes across multi-line markdown with shifted newlines', () => {
    const previous = [
      '# Mission',
      '',
      'We help authors capture intent.',
      '',
      'Then implement from it.',
    ].join('\n')
    const current = [
      '# Mission',
      '',
      'We help authors capture and refine intent.',
      'Then implement from it carefully.',
    ].join('\n')

    expect(extractAdded(previous, current)).toEqual(['and refine ', ' carefully'])
  })

  it('merges adjacent added tokens into a single range', () => {
    const previous = 'alpha gamma'
    const current = 'alpha beta delta gamma'
    expect(extractAdded(previous, current)).toEqual(['beta delta '])
  })

  it('produces ranges that map back to the current text', () => {
    const previous = 'one two three'
    const current = 'one two and a half three'
    const ranges = diffAddedRanges(previous, current)
    for (const r of ranges) {
      expect(r.from).toBeGreaterThanOrEqual(0)
      expect(r.to).toBeLessThanOrEqual(current.length)
      expect(r.to).toBeGreaterThan(r.from)
    }
    expect(ranges.map((r) => current.slice(r.from, r.to)).join('')).toContain('and a half')
  })
})