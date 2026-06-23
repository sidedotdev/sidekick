import { describe, it, expect } from 'vitest'
import { formatMarkdown } from '../markdown_format'

describe('formatMarkdown', () => {
  it('wraps long plain paragraph lines at the wrap column', () => {
    const input =
      'This is a very long paragraph that should definitely be wrapped because it exceeds the configured column limit by a comfortable margin.\n'
    const out = formatMarkdown(input, 40)
    const lines = out.split('\n').filter((l) => l.length > 0)
    expect(lines.length).toBeGreaterThan(1)
    for (const l of lines) {
      expect(l.length).toBeLessThanOrEqual(40)
    }
  })

  it('merges soft-broken paragraph lines before wrapping', () => {
    const input = 'one two three\nfour five six\n'
    const out = formatMarkdown(input, 80)
    expect(out).toBe('one two three four five six\n')
  })

  it('collapses multiple blank lines into a single blank line', () => {
    const input = 'first\n\n\n\nsecond\n'
    const out = formatMarkdown(input, 80)
    expect(out).toBe('first\n\nsecond\n')
  })

  it('preserves YAML frontmatter verbatim', () => {
    const input = `---\nintent_links:\n  - intent: "#foo"\n    code:\n      - main.go:Bar\n---\nbody text here\n`
    const out = formatMarkdown(input, 80)
    expect(out.startsWith('---\nintent_links:\n  - intent: "#foo"\n    code:\n      - main.go:Bar\n---\n')).toBe(true)
  })

  it('does not modify fenced code blocks', () => {
    const input =
      'paragraph text\n\n```js\nconst x = 1\n\n\nconst y = 2\n```\n\nafter\n'
    const out = formatMarkdown(input, 80)
    expect(out).toContain('```js\nconst x = 1\n\n\nconst y = 2\n```')
  })

  it('leaves headings untouched even if long', () => {
    const input =
      '# This is a heading that is much longer than the wrap column would normally permit\n'
    const out = formatMarkdown(input, 40)
    expect(out).toBe(input)
  })

  it('leaves list items untouched', () => {
    const input = '- item one\n- item two\n- item three\n'
    const out = formatMarkdown(input, 80)
    expect(out).toBe(input)
  })

  it('returns the same string when already formatted', () => {
    const input = 'hello world\n\nsecond paragraph here\n'
    const out = formatMarkdown(input, 80)
    expect(out).toBe(input)
  })

  it('preserves absence of trailing newline', () => {
    const input = 'no trailing newline'
    const out = formatMarkdown(input, 80)
    expect(out).toBe('no trailing newline')
  })
})