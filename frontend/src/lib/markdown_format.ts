// Lightweight markdown auto-formatter used by the intent editor's
// format-on-idle behavior. It rewrites plain prose paragraphs and list items
// by collapsing internal whitespace and wrapping them at `wrapColumn`. List
// continuation lines are hung-indented to align after the marker. Structural
// blocks that should preserve byte-for-byte fidelity -- YAML frontmatter,
// fenced code blocks, tables, headings, blockquotes, horizontal rules -- are
// left untouched. Multiple consecutive blank lines collapse to one so the
// document doesn't accumulate vertical drift over time.

const FENCE_RE = /^(\s*)(```+|~~~+)(.*)$/
const HEADING_RE = /^\s{0,3}#{1,6}\s/
const HR_RE = /^\s{0,3}(?:-{3,}|\*{3,}|_{3,})\s*$/
const UNORDERED_LIST_RE = /^\s*[-*+]\s+/
const ORDERED_LIST_RE = /^\s*\d+[.)]\s+/
const BLOCKQUOTE_RE = /^\s*>/
const TABLE_RE = /\|/
const HTML_BLOCK_RE = /^\s*<\/?[a-zA-Z]/
const INDENTED_CODE_RE = /^(?: {4}|\t)/

const isPlainParagraphLine = (line: string): boolean => {
  if (line.trim() === '') return false
  if (HEADING_RE.test(line)) return false
  if (HR_RE.test(line)) return false
  if (UNORDERED_LIST_RE.test(line)) return false
  if (ORDERED_LIST_RE.test(line)) return false
  if (BLOCKQUOTE_RE.test(line)) return false
  if (TABLE_RE.test(line)) return false
  if (HTML_BLOCK_RE.test(line)) return false
  if (INDENTED_CODE_RE.test(line)) return false
  return true
}

const wrapParagraph = (text: string, wrapColumn: number): string => {
  const words = text.split(/\s+/).filter((w) => w.length > 0)
  if (words.length === 0) return ''
  const lines: string[] = []
  let current = ''
  for (const word of words) {
    if (current.length === 0) {
      current = word
      continue
    }
    if (current.length + 1 + word.length > wrapColumn) {
      lines.push(current)
      current = word
    } else {
      current += ' ' + word
    }
  }
  if (current.length > 0) lines.push(current)
  return lines.join('\n')
}

// LIST_ITEM_RE captures the leading indentation and full marker (including
// trailing whitespace) of a list item so we can preserve the hanging indent
// when reflowing the item's content.
const LIST_ITEM_RE = /^(\s*)([-*+]|\d+[.)])(\s+)(.*)$/

type ListItem = { indent: string; marker: string; content: string }

// parseListBlock walks a contiguous non-blank block and, if it begins with a
// list marker, returns the parsed items. Continuation lines must be indented
// at least to the marker's content column and must not themselves introduce
// another structural block (heading, fence, blockquote, table). Returns null
// when the block is not a (clean) list so the caller can fall back to the
// passthrough behavior used for ambiguous structures.
const parseListBlock = (block: string[]): ListItem[] | null => {
  if (block.length === 0) return null
  const first = block[0].match(LIST_ITEM_RE)
  if (!first) return null

  const items: ListItem[] = []
  let current: ListItem | null = null
  let currentContentCol = 0

  for (const line of block) {
    const m = line.match(LIST_ITEM_RE)
    if (m) {
      if (current) items.push(current)
      const [, indent, marker, gap, content] = m
      current = { indent, marker, content }
      currentContentCol = indent.length + marker.length + gap.length
      continue
    }
    if (!current) return null
    // Continuation lines must be indented at least to the item's content
    // column; otherwise the block is mixing list items with non-list content
    // and we leave it alone to avoid corrupting unusual structures.
    const leading = line.match(/^(\s*)/)![1].length
    if (leading < currentContentCol) return null
    if (HEADING_RE.test(line) || FENCE_RE.test(line) || HR_RE.test(line)) {
      return null
    }
    current.content += ' ' + line.trim()
  }
  if (current) items.push(current)
  return items
}

const formatListItem = (item: ListItem, wrapColumn: number): string[] => {
  const prefix = item.indent + item.marker + ' '
  const hangingIndent = ' '.repeat(prefix.length)
  // Reserve room for the marker prefix when wrapping so the produced lines
  // (including the indent) fit within wrapColumn where possible.
  const effective = Math.max(wrapColumn - prefix.length, 1)
  const wrapped = wrapParagraph(item.content, effective).split('\n')
  if (wrapped.length === 0 || wrapped[0] === '') return [prefix.trimEnd()]
  return wrapped.map((l, idx) => (idx === 0 ? prefix + l : hangingIndent + l))
}

// extractFrontmatter returns the frontmatter block (including delimiters and
// trailing newline) if the document opens with one, and the remaining body.
const extractFrontmatter = (lines: string[]): { frontmatter: string[]; body: string[] } => {
  if (lines.length === 0 || lines[0] !== '---') {
    return { frontmatter: [], body: lines }
  }
  for (let i = 1; i < lines.length; i++) {
    if (lines[i] === '---') {
      return { frontmatter: lines.slice(0, i + 1), body: lines.slice(i + 1) }
    }
  }
  return { frontmatter: [], body: lines }
}

export const formatMarkdown = (input: string, wrapColumn = 80): string => {
  if (input.length === 0) return input

  // Preserve the document's final-newline status: most editors emit one and
  // we don't want formatting to flicker it on/off.
  const hadTrailingNewline = input.endsWith('\n')
  const rawLines = input.split('\n')
  if (hadTrailingNewline) rawLines.pop()

  const { frontmatter, body } = extractFrontmatter(rawLines)

  const out: string[] = []
  let i = 0
  let prevBlank = false
  const pushLine = (line: string) => {
    const blank = line.trim() === ''
    if (blank && prevBlank) return
    out.push(line)
    prevBlank = blank
  }

  while (i < body.length) {
    const line = body[i]

    // Fenced code block: copy through verbatim until the matching closing fence.
    const fenceMatch = line.match(FENCE_RE)
    if (fenceMatch) {
      const fenceMarker = fenceMatch[2][0]
      const fenceLen = fenceMatch[2].length
      pushLine(line)
      i++
      while (i < body.length) {
        const inner = body[i]
        out.push(inner)
        prevBlank = inner.trim() === ''
        i++
        const closeMatch = inner.match(FENCE_RE)
        if (
          closeMatch &&
          closeMatch[2][0] === fenceMarker &&
          closeMatch[2].length >= fenceLen &&
          closeMatch[3].trim() === ''
        ) {
          break
        }
      }
      continue
    }

    if (line.trim() === '') {
      pushLine(line)
      i++
      continue
    }

    // Gather a contiguous block of non-blank lines.
    const blockStart = i
    while (i < body.length && body[i].trim() !== '') {
      // Stop the block if a fenced code block begins mid-stream.
      if (i > blockStart && FENCE_RE.test(body[i])) break
      i++
    }
    const block = body.slice(blockStart, i)

    const allPlain = block.every(isPlainParagraphLine)
    if (allPlain && block.length > 0) {
      const merged = block.map((l) => l.trim()).join(' ')
      const wrapped = wrapParagraph(merged, wrapColumn)
      for (const wl of wrapped.split('\n')) pushLine(wl)
    } else {
      const listItems = parseListBlock(block)
      if (listItems) {
        for (const item of listItems) {
          for (const wl of formatListItem(item, wrapColumn)) pushLine(wl)
        }
      } else {
        for (const bl of block) pushLine(bl)
      }
    }
  }

  // Trim trailing blank lines from the body so collapsing doesn't leave a
  // run of empty lines at the end of the document.
  while (out.length > 0 && out[out.length - 1].trim() === '') out.pop()

  const result =
    frontmatter.length > 0
      ? [...frontmatter, ...out].join('\n')
      : out.join('\n')
  return hadTrailingNewline ? result + '\n' : result
}