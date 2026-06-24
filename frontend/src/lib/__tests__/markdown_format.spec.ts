import { describe, it, expect } from 'vitest'
import {
  formatMarkdown,
  formatMarkdownPreservingSelection,
  mapCursorAfterFormat,
} from '../markdown_format'

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

  it('leaves short list items unchanged when already formatted', () => {
    const input = '- item one\n- item two\n- item three\n'
    const out = formatMarkdown(input, 80)
    expect(out).toBe(input)
  })

  it('wraps long list items with a hanging indent that aligns after the marker', () => {
    const input =
      '- this list item is intentionally long so that it needs to wrap onto multiple lines when formatted\n'
    const out = formatMarkdown(input, 40)
    const lines = out.split('\n').filter((l) => l.length > 0)
    expect(lines.length).toBeGreaterThan(1)
    expect(lines[0].startsWith('- ')).toBe(true)
    for (const line of lines.slice(1)) {
      expect(line.startsWith('  ')).toBe(true)
      expect(line.startsWith('- ')).toBe(false)
    }
  })

  it('merges soft-broken list item continuation lines before wrapping', () => {
    const input = '- one two three\n  four five six\n'
    const out = formatMarkdown(input, 80)
    expect(out).toBe('- one two three four five six\n')
  })

  it('wraps ordered list items preserving the numeric marker', () => {
    const input =
      '1. an ordered list item that is sufficiently long to require wrapping at a narrow column\n'
    const out = formatMarkdown(input, 40)
    const lines = out.split('\n').filter((l) => l.length > 0)
    expect(lines.length).toBeGreaterThan(1)
    expect(lines[0].startsWith('1. ')).toBe(true)
    for (const line of lines.slice(1)) {
      expect(line.startsWith('   ')).toBe(true)
    }
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

  it('removes trailing whitespace from prose and passthrough lines', () => {
    const input = 'hello world   \n\n> a quote\t \n'
    const out = formatMarkdown(input, 80)
    expect(out).toBe('hello world\n\n> a quote\n')
  })

  it('preserves trailing whitespace inside fenced code blocks', () => {
    const input = '```\ncode line   \nmore\t\n```\n'
    const out = formatMarkdown(input, 80)
    expect(out).toBe('```\ncode line   \nmore\t\n```\n')
  })

  it('leaves empty list items alone', () => {
    const input = '- one\n- \n- three\n'
    const out = formatMarkdown(input, 80)
    expect(out).toBe('- one\n- \n- three\n')
  })

  it('reflows the active line from the caret, stripping its trailing whitespace, and keeps left-of-caret content exact', () => {
    const input = 'aaa   \n\n\n\nlong line one\nlong line two   \n'
    const caret = input.indexOf('line two') + 'line'.length
    const res = formatMarkdownPreservingSelection(input, caret, caret, 80)
    // The trailing whitespace right of the caret is removed; surrounding content
    // (the blank-padded first paragraph) is also formatted.
    expect(res.text).toBe('aaa\n\nlong line one\nlong line two\n')
    // Every character to the left of the caret on the active line is byte-identical.
    expect(res.text.slice(0, res.head)).toBe('aaa\n\nlong line one\nlong line')
  })

  it('keeps a space the caret sits before from being collapsed on the active line', () => {
    const input = 'one two   three\n'
    // caret sits just after "two", before the run of spaces.
    const caret = 'one two'.length
    const res = formatMarkdownPreservingSelection(input, caret, caret, 80)
    expect(res.text.slice(0, res.head)).toBe('one two')
    // The collapsible whitespace right of the caret is normalized to one space.
    expect(res.text).toBe('one two three\n')
  })

  it('wraps the active list item from the caret with a hanging indent, keeping left-of-caret content', () => {
    const input =
      '- alpha\n- bravo charlie delta echo foxtrot golf hotel india juliet kilo\n- lima\n'
    const caret = input.indexOf('charlie')
    const res = formatMarkdownPreservingSelection(input, caret, caret, 40)
    const lineStart = res.text.lastIndexOf('\n', res.head - 1) + 1
    expect(res.text.slice(lineStart, res.head)).toBe('- bravo ')
    const activeLines = res.text.slice(lineStart).split('\n')
    expect(activeLines.length).toBeGreaterThan(1)
    expect(activeLines[1].startsWith('  ')).toBe(true)
  })

  it('preserves the entire fenced code block when the caret is inside it', () => {
    const input = 'intro text\n\n```js\nconst x = 1   \n\n\nconst y = 2\n```\n\noutro text\n'
    const caret = input.indexOf('const x') + 'const'.length
    const res = formatMarkdownPreservingSelection(input, caret, caret, 80)
    // The fenced block keeps its trailing whitespace and blank lines verbatim,
    // and the closing fence is not misread as a new opening fence.
    expect(res.text).toContain('```js\nconst x = 1   \n\n\nconst y = 2\n```')
    // The caret still sits right after 'const' inside the preserved block.
    expect(res.text.slice(0, res.head)).toMatch(/const$/)
  })

  it('preserves a fenced code block when a selection extends into it', () => {
    const input = 'a   \n\n```\nfoo bar baz   \nqux\n```\n'
    const anchor = input.indexOf('foo')
    const head = input.indexOf('qux') + 'qux'.length
    const res = formatMarkdownPreservingSelection(input, anchor, head, 40)
    expect(res.text).toContain('```\nfoo bar baz   \nqux\n```')
    expect(res.text.slice(res.anchor, res.head)).toBe(input.slice(anchor, head))
  })

  it('collapses and wraps lines above the caret within the same paragraph', () => {
    const input = 'one    two\nthree    four\nfive\n'
    const caret = input.indexOf('five') + 2
    const res = formatMarkdownPreservingSelection(input, caret, caret, 80)
    expect(res.text).toBe('one two three four\nfive\n')
    expect(res.text.slice(0, res.head)).toBe('one two three four\nfi')
  })

  it('does not duplicate the last character of the active block', () => {
    const input = 'foo bar\nbaz qux\n'
    const res = formatMarkdownPreservingSelection(input, 1, 1, 80)
    expect(res.text).toBe('foo bar baz qux\n')
    expect(res.text.slice(0, res.head)).toBe('f')
  })

  it('preserves the absence of a trailing newline', () => {
    const input = 'alpha beta\n\n\n\ngamma delta'
    const caret = input.indexOf('gamma') + 2
    const res = formatMarkdownPreservingSelection(input, caret, caret, 80)
    expect(res.text.endsWith('\n')).toBe(false)
    expect(res.text).toBe('alpha beta\n\ngamma delta')
    expect(res.text.slice(0, res.head)).toBe('alpha beta\n\nga')
  })

  it('keeps the caret on its blank line instead of jumping to the previous paragraph', () => {
    const input = 'para one\n\n\n\npara two\n'
    const caret = 10
    const res = formatMarkdownPreservingSelection(input, caret, caret, 80)
    expect(res.text.slice(0, res.head)).toBe('para one\n\n')
    expect(/\S/.test(res.text.slice(res.head))).toBe(true)
  })

  it('keeps a caret inside frontmatter exactly in place while formatting the body', () => {
    const input = '---\ntitle: hi\n---\n\nbody    text   here\n'
    const caret = input.indexOf('title') + 2
    const res = formatMarkdownPreservingSelection(input, caret, caret, 80)
    expect(res.text.slice(0, res.head)).toBe(input.slice(0, caret))
    expect(res.text).toContain('body text here\n')
  })

  it('maps the cursor exactly when text before it is unchanged', () => {
    const before = '# H\n\n\n\nbody\n'
    const after = '# H\n\nbody\n'
    expect(mapCursorAfterFormat(before, after, 3)).toBe(3)
  })

  it('shifts the cursor by the length delta when it follows the change', () => {
    const before = '# H\n\n\n\nbody\n'
    const after = '# H\n\nbody\n'
    expect(mapCursorAfterFormat(before, after, before.length)).toBe(after.length)
  })

  it('keeps the active-line text left of the caret unchanged when surrounded by changes', () => {
    const input = 'aaa   \n\n\n\nbbb ccc ddd\n\n\n\neee   \n'
    const caret = input.indexOf('ccc')
    const res = formatMarkdownPreservingSelection(input, caret, caret, 80)
    const lineStart = res.text.lastIndexOf('\n', res.head - 1) + 1
    expect(res.text.slice(lineStart, res.head)).toBe('bbb ')
  })

  it('preserves a single-line selection exactly when surrounded by changes', () => {
    const input = 'aaa   \n\n\n\nbbb ccc ddd\n\n\n\neee   \n'
    const anchor = input.indexOf('bbb')
    const head = input.indexOf('ddd') + 'ddd'.length
    const res = formatMarkdownPreservingSelection(input, anchor, head, 80)
    expect(res.text.slice(res.anchor, res.head)).toBe(input.slice(anchor, head))
  })

  it('preserves a multi-line selection (including interior blanks) exactly', () => {
    const input = 'x   \n\n\n\npara one here\n\npara two here\n\n\n\ny   \n'
    const anchor = input.indexOf('para one here')
    const head = input.indexOf('para two here') + 'para two here'.length
    const res = formatMarkdownPreservingSelection(input, anchor, head, 80)
    expect(res.text.slice(res.anchor, res.head)).toBe(input.slice(anchor, head))
  })
})