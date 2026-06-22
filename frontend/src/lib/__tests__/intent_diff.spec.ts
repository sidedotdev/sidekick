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

describe('diffAddedRanges - insertions', () => {
  it('highlights a single word inserted at the start of a line', () => {
    expect(extractAdded('bar baz', 'foo bar baz')).toEqual(['foo '])
  })

  it('highlights a single word inserted at the end of a line', () => {
    expect(extractAdded('foo bar', 'foo bar baz')).toEqual([' baz'])
  })

  it('highlights a single word inserted in the middle of a line', () => {
    expect(extractAdded('foo bar baz', 'foo NEW bar baz')).toEqual(['NEW '])
  })

  it('highlights only the inserted word when surrounded by long context', () => {
    const previous = 'one two three four five six seven eight nine ten'
    const current = 'one two three four INSERTED five six seven eight nine ten'
    expect(extractAdded(previous, current)).toEqual(['INSERTED '])
  })

  it('highlights an inserted word when adjacent words are repeated elsewhere', () => {
    const previous = 'the cat sat on the mat'
    const current = 'the cat sat on a the mat'
    expect(extractAdded(previous, current)).toEqual(['a '])
  })

  it('highlights an inserted word when the same word exists earlier', () => {
    const previous = 'the cat'
    const current = 'the cat and the dog'
    expect(extractAdded(previous, current).join('')).toContain('and')
    expect(extractAdded(previous, current).join('')).toContain('dog')
  })

  it('highlights an inserted word that matches a different existing word', () => {
    const previous = 'I have a cat'
    const current = 'I really have a cat'
    expect(extractAdded(previous, current)).toEqual(['really '])
  })

  it('highlights multiple consecutive inserted words as one range', () => {
    expect(extractAdded('alpha gamma', 'alpha beta delta gamma')).toEqual(['beta delta '])
  })

  it('highlights two separate insertions as two ranges', () => {
    const previous = 'one two three four five'
    const current = 'one X two three Y four five'
    expect(extractAdded(previous, current)).toEqual(['X ', 'Y '])
  })

  it('highlights insertion at the boundary of repeated tokens', () => {
    const previous = 'foo foo bar'
    const current = 'foo X foo bar'
    expect(extractAdded(previous, current).join('')).toContain('X')
    expect(extractAdded(previous, current).join('').replace(/X/g, '').trim()).toBe('')
  })

  it('highlights a single character insertion in the middle of a word', () => {
    const result = extractAdded('hello world', 'helXlo world')
    expect(result.join('')).toContain('helXlo')
  })

  it('does not highlight subsequent words when adding a word in the middle of a line with trailing context', () => {
    const previous = 'The quick brown fox jumps over the lazy dog'
    const current = 'The quick brown sly fox jumps over the lazy dog'
    expect(extractAdded(previous, current)).toEqual(['sly '])
  })

  it('handles insertion after punctuation', () => {
    expect(extractAdded('hello, world', 'hello, my world')).toEqual(['my '])
  })

  it('handles insertion before punctuation', () => {
    const result = extractAdded('hello world.', 'hello brave world.')
    expect(result).toEqual(['brave '])
  })

  it('treats an inserted line as its own range', () => {
    const previous = 'line one\nline two'
    const current = 'line one\nline new\nline two'
    const joined = extractAdded(previous, current).join('')
    expect(joined).toContain('new')
    expect(joined).not.toContain('two')
  })
})

describe('diffAddedRanges - substitutions', () => {
  it('highlights only the substituted word', () => {
    expect(extractAdded('foo bar baz', 'foo XYZ baz')).toEqual(['XYZ'])
  })

  it('highlights substitution at the start of a line', () => {
    expect(extractAdded('foo bar baz', 'XYZ bar baz')).toEqual(['XYZ'])
  })

  it('highlights substitution at the end of a line', () => {
    expect(extractAdded('foo bar baz', 'foo bar XYZ')).toEqual(['XYZ'])
  })

  it('highlights multiple non-adjacent substitutions separately', () => {
    const previous = 'one two three four five'
    const current = 'one TWO three FOUR five'
    expect(extractAdded(previous, current)).toEqual(['TWO', 'FOUR'])
  })
})

describe('diffAddedRanges - deletions', () => {
  it('returns no ranges when a single word is removed from the middle', () => {
    expect(extractAdded('foo bar baz', 'foo baz')).toEqual([])
  })

  it('returns no ranges when a word is removed from the start', () => {
    expect(extractAdded('foo bar baz', 'bar baz')).toEqual([])
  })

  it('returns no ranges when a word is removed from the end', () => {
    expect(extractAdded('foo bar baz', 'foo bar')).toEqual([])
  })

  it('returns no ranges when the current text is empty', () => {
    expect(diffAddedRanges('something', '')).toEqual([])
  })

  it('returns no ranges when a line is removed', () => {
    expect(extractAdded('line one\nline two\nline three', 'line one\nline three')).toEqual([])
  })
})

describe('diffAddedRanges - mixed changes', () => {
  it('highlights only the inserted parts when mixed with deletions', () => {
    const previous = 'one two three four'
    const current = 'one INSERTED four'
    expect(extractAdded(previous, current)).toEqual(['INSERTED'])
  })

  it('highlights both inserted phrase and replacement in the same diff', () => {
    const previous = 'alpha beta gamma'
    const current = 'alpha NEW beta REPLACED'
    const added = extractAdded(previous, current).join('')
    expect(added).toContain('NEW')
    expect(added).toContain('REPLACED')
    expect(added).not.toContain('alpha')
    expect(added).not.toContain('beta')
  })
})

describe('diffAddedRanges - whitespace handling', () => {
  it('ignores changes in run length of a single space token', () => {
    expect(diffAddedRanges('a b c', 'a   b c')).toEqual([])
  })

  it('ignores newline reflows that do not change token sequence', () => {
    expect(diffAddedRanges('a b c', 'a\nb\nc')).toEqual([])
  })

  it('ignores additions of pure whitespace', () => {
    expect(diffAddedRanges('hello world', 'hello \n world')).toEqual([])
  })

  it('highlights an inserted word even when surrounding whitespace changes', () => {
    const result = extractAdded('hello world', 'hello\nnew\nworld')
    expect(result.join('').replace(/\s+/g, '')).toBe('new')
  })
})

describe('diffAddedRanges - multi-line markdown', () => {
  it('highlights a word added on an existing line within a paragraph', () => {
    const previous = ['# Heading', '', 'first second third', 'last line'].join('\n')
    const current = ['# Heading', '', 'first NEW second third', 'last line'].join('\n')
    expect(extractAdded(previous, current)).toEqual(['NEW '])
  })

  it('highlights a fully new line inserted between existing lines', () => {
    const previous = ['line one', 'line two'].join('\n')
    const current = ['line one', 'inserted line', 'line two'].join('\n')
    const added = extractAdded(previous, current).join('')
    expect(added).toContain('inserted')
    expect(added).toContain('line')
    expect(added).not.toContain('two')
  })

  it('highlights multiple isolated word insertions across lines', () => {
    const previous = ['alpha beta', 'gamma delta'].join('\n')
    const current = ['alpha NEW beta', 'gamma X delta'].join('\n')
    expect(extractAdded(previous, current)).toEqual(['NEW ', 'X '])
  })
})

describe('diffAddedRanges - edge cases', () => {
  it('returns empty for identical empty strings', () => {
    expect(diffAddedRanges('', '')).toEqual([])
  })

  it('handles current consisting only of whitespace', () => {
    expect(diffAddedRanges('', '   ')).toEqual([{ from: 0, to: 3 }])
  })

  it('handles current containing unicode word characters', () => {
    const previous = 'café noir'
    const current = 'café très noir'
    expect(extractAdded(previous, current)).toEqual(['très '])
  })

  it('handles current containing emoji as non-word token', () => {
    const previous = 'hello world'
    const current = 'hello 🎉 world'
    const added = extractAdded(previous, current).join('')
    expect(added).toContain('🎉')
    expect(added).not.toContain('hello')
    expect(added).not.toContain('world')
  })

  it('produces non-overlapping ranges sorted by from', () => {
    const previous = 'a b c d e f g h'
    const current = 'a X b c Y d e Z f g h'
    const ranges = diffAddedRanges(previous, current)
    for (let k = 1; k < ranges.length; k++) {
      expect(ranges[k].from).toBeGreaterThanOrEqual(ranges[k - 1].to)
    }
  })

  it('produces strictly positive-length ranges', () => {
    const previous = 'a b c'
    const current = 'a X b Y c'
    const ranges = diffAddedRanges(previous, current)
    for (const r of ranges) expect(r.to).toBeGreaterThan(r.from)
  })

  it('range slices reconstruct only the additions', () => {
    const previous = 'red green blue'
    const current = 'red NEW green YELLOW blue'
    const ranges = diffAddedRanges(previous, current)
    const joined = ranges.map((r) => current.slice(r.from, r.to)).join('')
    expect(joined).toContain('NEW')
    expect(joined).toContain('YELLOW')
    expect(joined).not.toContain('red')
    expect(joined).not.toContain('blue')
  })
})