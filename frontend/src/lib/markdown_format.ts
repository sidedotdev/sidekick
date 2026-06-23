// Lightweight markdown auto-formatter used by the intent editor's
// format-on-idle behavior. It rewrites plain prose paragraphs by collapsing
// internal whitespace and wrapping them at `wrapColumn`. Structural blocks
// that should preserve byte-for-byte fidelity -- YAML frontmatter, fenced
// code blocks, tables, headings, list markers, blockquotes, horizontal rules
// -- are left untouched. Multiple consecutive blank lines collapse to one so
// the document doesn't accumulate vertical drift over time.

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
      for (const bl of block) pushLine(bl)
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