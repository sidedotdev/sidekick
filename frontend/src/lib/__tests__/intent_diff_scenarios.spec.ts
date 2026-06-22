import { describe, it, expect } from 'vitest'
import { diffAddedRanges } from '../intent_diff'

const lines = (...l: string[]): string => l.join('\n')

// A realistic markdown document exercising headings, paragraphs, list items,
// inline links and a fenced code block. Each scenario applies a small edit and
// asserts that only the genuinely added text is highlighted.
const BASE = lines(
  '# Project Title',
  '',
  'This is the intro paragraph describing the project clearly.',
  '',
  '## Features',
  '',
  '- First feature item',
  '- Second feature item',
  '- Third feature item',
  '',
  'See the [documentation](https://example.com) for details.',
  '',
  '```js',
  'const value = compute(input)',
  'return value + 1',
  '```',
  '',
  'Final closing paragraph here.',
)

const added = (previous: string, current: string): string[] =>
  diffAddedRanges(previous, current).map((r) => current.slice(r.from, r.to))

describe('diffAddedRanges single word insertion in markdown', () => {
  it('highlights only the inserted word in a heading', () => {
    const current = BASE.replace('# Project Title', '# Awesome Project Title')
    expect(added(BASE, current)).toEqual(['Awesome '])
  })

  it('highlights only the inserted word in the middle of a paragraph line', () => {
    const current = BASE.replace(
      'describing the project clearly',
      'describing the whole project clearly',
    )
    expect(added(BASE, current)).toEqual(['whole '])
  })

  it('does not highlight the words after a mid-line insertion', () => {
    const current = BASE.replace(
      'describing the project clearly',
      'describing the whole project clearly',
    )
    const result = added(BASE, current).join('')
    expect(result).not.toContain('project')
    expect(result).not.toContain('clearly')
  })

  it('highlights only the inserted word in a list item', () => {
    const current = BASE.replace('- Second feature item', '- Second favourite feature item')
    expect(added(BASE, current)).toEqual(['favourite '])
  })

  it('highlights only the inserted word in link text', () => {
    const current = BASE.replace('[documentation]', '[updated documentation]')
    expect(added(BASE, current)).toEqual(['updated '])
  })

  it('highlights only the inserted token in a code block', () => {
    const current = BASE.replace('compute(input)', 'compute(input, opts)')
    expect(added(BASE, current).join('')).toBe(', opts')
  })

  it('highlights a word appended at the end of a line', () => {
    const current = BASE.replace('- Third feature item', '- Third feature item now')
    expect(added(BASE, current).join('').trim()).toBe('now')
  })
})

describe('diffAddedRanges multiple edits in markdown', () => {
  it('highlights two separate insertions on the same line without the words between them', () => {
    const current = BASE.replace(
      'This is the intro paragraph describing the project clearly.',
      'This is the short intro paragraph describing the whole project clearly.',
    )
    expect(added(BASE, current)).toEqual(['short ', 'whole '])
  })

  it('highlights insertions spread across multiple components', () => {
    const current = BASE.replace('# Project Title', '# Awesome Project Title')
      .replace('- First feature item', '- First useful feature item')
      .replace('Final closing paragraph here.', 'Final short closing paragraph here.')
    const result = added(BASE, current).join('|')
    expect(result).toContain('Awesome ')
    expect(result).toContain('useful ')
    expect(result).toContain('short ')
    expect(result).not.toContain('feature')
    expect(result).not.toContain('closing')
  })

  it('highlights a newly inserted whole list item line', () => {
    const current = BASE.replace(
      '- Second feature item\n',
      '- Second feature item\n- Extra feature item\n',
    )
    expect(added(BASE, current).join('')).toContain('Extra')
    expect(added(BASE, current).join('')).not.toContain('Third')
  })
})

describe('diffAddedRanges replacements and deletions', () => {
  it('highlights only the replacement word', () => {
    const current = BASE.replace('Final closing paragraph', 'Final concluding paragraph')
    expect(added(BASE, current)).toEqual(['concluding'])
  })

  it('highlights nothing when a word is deleted', () => {
    const current = BASE.replace('the intro paragraph', 'the paragraph')
    expect(added(BASE, current)).toEqual([])
  })

  it('highlights nothing when only whitespace shifts', () => {
    const current = BASE.replace('## Features\n\n- First', '## Features\n\n\n- First')
    expect(added(BASE, current)).toEqual([])
  })

  it('highlights nothing for an identical document', () => {
    expect(added(BASE, BASE)).toEqual([])
  })
})