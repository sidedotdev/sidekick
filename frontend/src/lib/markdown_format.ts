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

type ListItem = { indent: string; marker: string; gap: string; content: string }

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
      current = { indent, marker, gap, content }
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
  const pushLine = (line: string, preserveTrailing = false) => {
    const text = preserveTrailing ? line : line.replace(/[ \t]+$/, '')
    const blank = text.trim() === ''
    if (blank && prevBlank) return
    out.push(text)
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
          // Empty list items have no content to reflow; leave the marker and
          // its spacing exactly as authored rather than collapsing them away.
          if (item.content === '') {
            pushLine(item.indent + item.marker + item.gap, true)
            continue
          }
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

// frontmatterEndOffset returns the char offset just past the closing '---\n' of
// the document's opening YAML frontmatter, or 0 when there is none.
const frontmatterEndOffset = (input: string): number => {
  const lines = input.split('\n')
  if (lines[0] !== '---') return 0
  let off = lines[0].length + 1
  for (let i = 1; i < lines.length; i++) {
    if (lines[i] === '---') return off + lines[i].length + 1
    off += lines[i].length + 1
  }
  return 0
}

// isInsideFencedCode reports whether `pos` falls within a fenced code block
// (including the opening/closing fence lines). Such positions must not be
// reflowed via the left/right boundary split, which would lose the fence
// context and let the formatter mangle code as prose.
const isInsideFencedCode = (input: string, pos: number): boolean => {
  let off = 0
  let inFence = false
  let marker = ''
  let len = 0
  for (const line of input.split('\n')) {
    const start = off
    const end = off + line.length
    const here = pos >= start && pos <= end
    const fence = line.match(FENCE_RE)
    if (!inFence) {
      if (fence) {
        if (here) return true
        inFence = true
        marker = fence[2][0]
        len = fence[2].length
      } else if (here) {
        return false
      }
    } else {
      if (here) return true
      if (fence && fence[2][0] === marker && fence[2].length >= len && fence[3].trim() === '') {
        inFence = false
      }
    }
    off = end + 1
  }
  return false
}

// wrapFromPrefix reflows `words` onto lines that begin with a fixed, verbatim
// `prefix` (the unchanged text to the left of the caret on the active line).
// The prefix is never modified; wrapped continuation lines are hung-indented by
// `indent`. When `joinNoSpace` is set the first word abuts the prefix directly
// (the caret split a word), otherwise words are separated by single spaces,
// taking care not to double a space the prefix already ends with.
const wrapFromPrefix = (
  prefix: string,
  words: string[],
  wrapColumn: number,
  indent: number,
  joinNoSpace: boolean,
): string[] => {
  const lines: string[] = []
  const pad = ' '.repeat(indent)
  let current = prefix
  for (let i = 0; i < words.length; i++) {
    const word = words[i]
    if (i === 0 && joinNoSpace) {
      current += word
      continue
    }
    const sep =
      current.length === 0 || isWhitespace(current[current.length - 1]) ? '' : ' '
    if (current.length + sep.length + word.length > wrapColumn && current.trim() !== '') {
      lines.push(current)
      current = pad + word
    } else {
      current += sep + word
    }
  }
  lines.push(current)
  return lines
}

type RawListItem = {
  startLine: number
  lines: string[]
  indent: string
  marker: string
  gap: string
}

// splitListItems parses a contiguous block into list items preserving each
// item's raw lines (needed for exact offset mapping). Returns null when the
// block is not a clean list, mirroring parseListBlock's validation.
const splitListItems = (lines: string[]): RawListItem[] | null => {
  let current: RawListItem | null = null
  let contentCol = 0
  const items: RawListItem[] = []
  for (let idx = 0; idx < lines.length; idx++) {
    const line = lines[idx]
    const m = line.match(LIST_ITEM_RE)
    if (m) {
      if (current) items.push(current)
      current = { startLine: idx, lines: [line], indent: m[1], marker: m[2], gap: m[3] }
      contentCol = m[1].length + m[2].length + m[3].length
      continue
    }
    if (!current) return null
    const leading = line.match(/^(\s*)/)![1].length
    if (leading < contentCol) return null
    if (HEADING_RE.test(line) || FENCE_RE.test(line) || HR_RE.test(line)) return null
    current.lines.push(line)
  }
  if (current) items.push(current)
  return items
}

const rawListItemContent = (item: RawListItem): string => {
  const first = item.lines[0].match(LIST_ITEM_RE)
  let content = first ? first[4] : item.lines[0].trim()
  for (let i = 1; i < item.lines.length; i++) content += ' ' + item.lines[i].trim()
  return content.trim()
}

type BlockReflow = { text: string; fromOut: number; toOut: number }

// reflowParagraph reflows a plain-paragraph block, protecting only what must
// stay exact: lines strictly above the caret's line are collapsed and wrapped
// like normal prose, the caret line's left-of-caret text and the selection (up
// to `toOff`) are kept byte-identical, and the tail from the caret onward is
// collapsed and wrapped. Keeping just the caret line's prefix verbatim (rather
// than the whole block) keeps auto-formatting complete.
const reflowParagraph = (
  blockText: string,
  fromOff: number,
  toOff: number,
  wrapColumn: number,
): BlockReflow => {
  const fromLineStart = blockText.lastIndexOf('\n', fromOff - 1) + 1
  const aboveText = blockText.slice(0, fromLineStart)
  const protectedPrefix = blockText.slice(fromLineStart, toOff)
  const tail = blockText.slice(toOff)

  const vbLines = protectedPrefix.split('\n')
  const prefixLine = vbLines[vbLines.length - 1]
  const selectionMid = vbLines.slice(0, -1)
  const words = tail.split(/\s+/).filter((w) => w.length > 0)
  const joinNoSpace =
    tail.length > 0 &&
    !isWhitespace(tail[0]) &&
    prefixLine.length > 0 &&
    !isWhitespace(prefixLine[prefixLine.length - 1])
  const reflowed = wrapFromPrefix(prefixLine, words, wrapColumn, 0, joinNoSpace)

  const aboveCollapsed = aboveText.split(/\s+/).filter((w) => w.length > 0).join(' ')
  const aboveLines = aboveCollapsed === '' ? [] : wrapParagraph(aboveCollapsed, wrapColumn).split('\n')
  const base = aboveLines.reduce((n, l) => n + l.length + 1, 0)

  const text = [...aboveLines, ...selectionMid, ...reflowed].join('\n')
  return {
    text,
    fromOut: base + (fromOff - fromLineStart),
    toOut: base + (toOff - fromLineStart),
  }
}

// reflowListBlock reflows only the list item containing the caret/selection,
// formatting the other items normally. Returns null when the selection spans
// more than one item so the caller can fall back to verbatim protection.
const reflowListBlock = (
  lines: string[],
  fromOff: number,
  toOff: number,
  wrapColumn: number,
): BlockReflow | null => {
  const items = splitListItems(lines)
  if (!items) return null

  const lineOff: number[] = []
  let acc = 0
  for (const l of lines) {
    lineOff.push(acc)
    acc += l.length + 1
  }
  const ranges = items.map((it, idx) => {
    const start = lineOff[it.startLine]
    const lastLine = idx + 1 < items.length ? items[idx + 1].startLine - 1 : lines.length - 1
    return { start, end: lineOff[lastLine] + lines[lastLine].length }
  })

  let active = -1
  for (let i = 0; i < items.length; i++) {
    if (fromOff >= ranges[i].start && fromOff <= ranges[i].end) {
      active = i
      break
    }
  }
  if (active === -1 || toOff > ranges[active].end) return null

  const outLines: string[] = []
  let fromOut = 0
  let toOut = 0
  for (let i = 0; i < items.length; i++) {
    const it = items[i]
    if (i === active) {
      const startCharInOut = outLines.reduce((n, l) => n + l.length + 1, 0)
      const itemText = it.lines.join('\n')
      const innerFrom = fromOff - ranges[i].start
      const innerTo = toOff - ranges[i].start
      const verbatim = itemText.slice(0, innerTo)
      const tail = itemText.slice(innerTo)
      const vbLines = verbatim.split('\n')
      const prefixLine = vbLines[vbLines.length - 1]
      const above = vbLines.slice(0, -1)
      const contentCol = it.indent.length + it.marker.length + it.gap.length
      const words = tail.split(/\s+/).filter((w) => w.length > 0)
      const joinNoSpace =
        tail.length > 0 &&
        !isWhitespace(tail[0]) &&
        prefixLine.length > 0 &&
        !isWhitespace(prefixLine[prefixLine.length - 1])
      const reflowed = wrapFromPrefix(prefixLine, words, wrapColumn, contentCol, joinNoSpace)
      fromOut = startCharInOut + innerFrom
      toOut = startCharInOut + innerTo
      for (const l of [...above, ...reflowed]) outLines.push(l)
    } else {
      const content = rawListItemContent(it)
      if (content === '') {
        outLines.push(it.indent + it.marker + it.gap)
        continue
      }
      for (const wl of formatListItem(
        { indent: it.indent, marker: it.marker, gap: it.gap, content },
        wrapColumn,
      )) {
        outLines.push(wl)
      }
    }
  }
  return { text: outLines.join('\n'), fromOut, toOut }
}

// reflowProtectedBlock reflows a single block (paragraph or list) while keeping
// the caret's left-of-caret text and the whole selection byte-identical.
// Returns null for blocks we cannot safely reflow (fenced code, tables,
// headings, blockquotes, etc.), letting the caller protect them verbatim.
const reflowProtectedBlock = (
  blockText: string,
  fromOff: number,
  toOff: number,
  wrapColumn: number,
): BlockReflow | null => {
  const lines = blockText.split('\n')
  if (lines.some((l) => FENCE_RE.test(l))) return null
  if (LIST_ITEM_RE.test(lines[0])) {
    return reflowListBlock(lines, fromOff, toOff, wrapColumn)
  }
  if (!lines.every(isPlainParagraphLine)) return null
  return reflowParagraph(blockText, fromOff, toOff, wrapColumn)
}

// formatBoundaryLeft formats the document prefix that precedes the protected
// region. The prefix ends at a line boundary; its trailing blank lines collapse
// to at most one and a separating newline is re-appended so the protected
// region still starts on its own line.
const formatBoundaryLeft = (left: string, wrapColumn: number): string => {
  if (left === '') return ''
  let tn = 0
  while (tn < left.length && left[left.length - 1 - tn] === '\n') tn++
  const content = left.slice(0, left.length - tn)
  if (content === '') return '\n'.repeat(Math.min(tn, 2))
  return formatMarkdown(content, wrapColumn) + (tn >= 2 ? '\n\n' : '\n')
}

// formatBoundaryRight formats the document suffix that follows the protected
// region. The suffix opens with the protected line's terminating newline; any
// extra leading blank lines collapse to at most one.
const formatBoundaryRight = (right: string, wrapColumn: number): string => {
  if (right === '') return ''
  let ln = 0
  while (ln < right.length && right[ln] === '\n') ln++
  const rest = right.slice(ln)
  if (rest === '') return '\n'
  return (ln >= 2 ? '\n\n' : '\n') + formatMarkdown(rest, wrapColumn)
}

// formatMarkdownPreservingSelection formats the document while guaranteeing the
// caret/selection stays exactly put: the text to the left of the caret on the
// active line and the entire selection are kept byte-identical, while the rest
// of the document -- including the active line from the caret rightward (trailing
// whitespace removal, wrapping) -- is fully reflowed. The active block is
// reflowed with a fixed verbatim prefix; multi-block selections and structural
// active blocks (code fences, tables, headings, ...) protect the spanned lines
// verbatim. A caret in frontmatter or fenced code uses the whole-document
// formatter, which reproduces those regions byte-for-byte (so the active line's
// left-of-caret text is unchanged), and maps via content anchoring.
export const formatMarkdownPreservingSelection = (
  input: string,
  anchor: number,
  head: number,
  wrapColumn = 80,
): { text: string; anchor: number; head: number } => {
  if (input.length === 0) return { text: input, anchor, head }

  const from = Math.min(anchor, head)
  const to = Math.max(anchor, head)
  const feEnd = frontmatterEndOffset(input)

  const activeLineStart = input.lastIndexOf('\n', from - 1) + 1
  const firstNl = input.indexOf('\n', from)
  const activeLineEnd = firstNl === -1 ? input.length : firstNl

  // Frontmatter and fenced code are preserved verbatim by the whole-document
  // formatter, so content anchoring keeps the caret on its (unchanged) active
  // line there. Everything else is protected with the line-exact path below.
  if (from < feEnd || isInsideFencedCode(input, from) || isInsideFencedCode(input, to)) {
    const text = formatMarkdown(input, wrapColumn)
    return {
      text,
      anchor: mapCursorAfterFormat(input, text, anchor),
      head: mapCursorAfterFormat(input, text, head),
    }
  }

  // A caret on a blank line has no block to reflow; protect its line verbatim so
  // the caret cannot jump onto neighbouring (collapsed) content.
  const activeBlank = input.slice(activeLineStart, activeLineEnd).trim() === ''

  let blockStart = activeLineStart
  while (blockStart > feEnd) {
    const prevNl = blockStart - 1
    const prevStart = input.lastIndexOf('\n', prevNl - 1) + 1
    if (prevStart < feEnd || input.slice(prevStart, prevNl).trim() === '') break
    blockStart = prevStart
  }
  let blockEnd = activeLineEnd
  while (blockEnd + 1 <= input.length) {
    const nextStart = blockEnd + 1
    const nextNl = input.indexOf('\n', nextStart)
    const nextEnd = nextNl === -1 ? input.length : nextNl
    if (input.slice(nextStart, nextEnd).trim() === '') break
    blockEnd = nextEnd
  }

  const reflow =
    !activeBlank && to <= blockEnd
      ? reflowProtectedBlock(input.slice(blockStart, blockEnd), from - blockStart, to - blockStart, wrapColumn)
      : null

  let regionStart: number
  let mid: string
  let fromOut: number
  let toOut: number
  let tailStart: number
  if (reflow) {
    regionStart = blockStart
    mid = reflow.text
    fromOut = reflow.fromOut
    toOut = reflow.toOut
    tailStart = blockEnd
  } else {
    const toNl = input.indexOf('\n', to)
    const protEnd = toNl === -1 ? input.length : toNl
    regionStart = activeLineStart
    mid = input.slice(activeLineStart, protEnd)
    fromOut = from - activeLineStart
    toOut = to - activeLineStart
    tailStart = protEnd
  }

  const left = formatBoundaryLeft(input.slice(0, regionStart), wrapColumn)
  const right = formatBoundaryRight(input.slice(tailStart), wrapColumn)
  const text = left + mid + right
  const newFrom = left.length + fromOut
  const newTo = left.length + toOut
  const newAnchor = anchor <= head ? newFrom : newTo
  const newHead = anchor <= head ? newTo : newFrom
  return {
    text,
    anchor: Math.min(newAnchor, text.length),
    head: Math.min(newHead, text.length),
  }
}

const isWhitespace = (ch: string): boolean => ch === ' ' || ch === '\t' || ch === '\n' || ch === '\r'

// mapCursorAfterFormat translates a caret/selection offset from the pre-format
// document to the formatted one. Formatting only rewrites whitespace and line
// breaks, never the non-whitespace characters, so the cursor tracks the same
// content: we count the non-whitespace characters to its left and place it just
// after that same count in the formatted text. This guarantees the characters
// to the left of the cursor are never rewritten. Any whitespace the cursor sat
// within is re-traversed up to its original width (capped by what survives in
// the output) so a caret resting in a blank region or trailing newline keeps an
// equivalent spot.
export const mapCursorAfterFormat = (
  before: string,
  after: string,
  pos: number,
): number => {
  if (before === after) return pos
  const clamped = Math.max(0, Math.min(pos, before.length))

  let nonWs = 0
  let trailingWs = 0
  for (let i = 0; i < clamped; i++) {
    if (isWhitespace(before[i])) {
      trailingWs++
    } else {
      nonWs++
      trailingWs = 0
    }
  }

  let mapped = 0
  if (nonWs > 0) {
    let seen = 0
    let i = 0
    for (; i < after.length; i++) {
      if (!isWhitespace(after[i])) {
        seen++
        if (seen === nonWs) {
          i++
          break
        }
      }
    }
    mapped = i
  }

  let advanced = 0
  while (advanced < trailingWs && mapped < after.length && isWhitespace(after[mapped])) {
    mapped++
    advanced++
  }
  return mapped
}