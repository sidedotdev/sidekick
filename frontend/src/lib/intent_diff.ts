// Word-level diff between a previous (committed) and current (working) text.
// The result is a list of ranges within the current text that have no
// counterpart in the previous text — i.e. uncommitted additions.
//
// Whitespace runs are compared loosely so that purely cosmetic newline
// shifts in multi-line markdown don't get flagged as edits.

export interface AddedRange {
  from: number
  to: number
}

interface Token {
  text: string
  key: string
}

const TOKEN_RE = /\s+|[\p{L}\p{N}_]+|[^\s\p{L}\p{N}_]+/gu
const WS_KEY = '\u0001ws'

const tokenize = (text: string): Token[] => {
  const out: Token[] = []
  for (const match of text.matchAll(TOKEN_RE)) {
    const t = match[0]
    const isWs = /^\s+$/.test(t)
    out.push({ text: t, key: isWs ? WS_KEY : t })
  }
  return out
}

const measure = (tokens: Token[], start: number, end: number): number => {
  let n = 0
  for (let i = start; i < end; i++) n += tokens[i].text.length
  return n
}

// computeAddedRanges produces the contiguous ranges within `current` whose
// token stream has no counterpart in `previous`. Adjacent additions are merged.
//
// The middle of the diff is computed with classical LCS. To keep memory and
// time bounded, common prefix and suffix tokens are trimmed first, and a
// generous size cap falls back to highlighting everything between the
// trimmed prefix and suffix.
const computeAddedRanges = (previous: string, current: string): AddedRange[] => {
  if (current.length === 0) return []
  if (previous.length === 0) return [{ from: 0, to: current.length }]

  const a = tokenize(previous)
  const b = tokenize(current)

  let prefix = 0
  while (prefix < a.length && prefix < b.length && a[prefix].key === b[prefix].key) prefix++
  let suffix = 0
  while (
    suffix < a.length - prefix &&
    suffix < b.length - prefix &&
    a[a.length - 1 - suffix].key === b[b.length - 1 - suffix].key
  ) {
    suffix++
  }
  // Keep a trailing whitespace token attached to a non-whitespace insertion
  // rather than letting the greedy suffix match swallow it. Without this,
  // typing "WORD " between matched context would highlight as " WORD" because
  // the suffix matcher pairs the new trailing space with the original space
  // before the next token.
  if (
    suffix > 0 &&
    b.length - suffix - 1 >= prefix &&
    b[b.length - suffix].key === WS_KEY &&
    b[b.length - suffix - 1].key !== WS_KEY
  ) {
    suffix--
  }

  const prefixLen = measure(b, 0, prefix)
  const midA = a.slice(prefix, a.length - suffix)
  const midB = b.slice(prefix, b.length - suffix)
  const n = midA.length
  const m = midB.length

  if (m === 0) return []
  if (n === 0) {
    const midLen = measure(midB, 0, m)
    return midLen > 0 ? [{ from: prefixLen, to: prefixLen + midLen }] : []
  }

  const MAX_CELLS = 4_000_000
  if (n * m > MAX_CELLS) {
    const midLen = measure(midB, 0, m)
    return midLen > 0 ? [{ from: prefixLen, to: prefixLen + midLen }] : []
  }

  const width = m + 1
  const dp = new Int32Array((n + 1) * width)
  for (let i = n - 1; i >= 0; i--) {
    for (let j = m - 1; j >= 0; j--) {
      const idx = i * width + j
      if (midA[i].key === midB[j].key) {
        dp[idx] = dp[idx + width + 1] + 1
      } else {
        const down = dp[idx + width]
        const right = dp[idx + 1]
        dp[idx] = down >= right ? down : right
      }
    }
  }

  const ranges: AddedRange[] = []
  let pos = prefixLen
  let runStart = -1
  const closeRun = (end: number) => {
    if (runStart !== -1 && end > runStart) ranges.push({ from: runStart, to: end })
    runStart = -1
  }

  let i = 0
  let j = 0
  while (i < n && j < m) {
    if (midA[i].key === midB[j].key) {
      closeRun(pos)
      pos += midB[j].text.length
      i++
      j++
      continue
    }
    if (dp[(i + 1) * width + j] >= dp[i * width + (j + 1)]) {
      i++
    } else {
      if (runStart === -1) runStart = pos
      pos += midB[j].text.length
      j++
    }
  }
  while (j < m) {
    if (runStart === -1) runStart = pos
    pos += midB[j].text.length
    j++
  }
  closeRun(pos)
  return ranges
}

const isWhitespace = (ch: string | undefined): boolean => ch !== undefined && /\s/.test(ch)

// rotateLeadingWhitespace shifts whitespace from the start of a range to its
// end whenever following whitespace is available. Whitespace is fungible for
// this word-level diff, so the common prefix/suffix trimming can leave an
// inserted word highlighted as " word" when the more intuitive result is
// "word ". Rotating keeps the highlighted length identical while preferring a
// trailing space, except where none exists (e.g. before punctuation or at the
// end of the text), which legitimately keeps the leading space.
const rotateLeadingWhitespace = (range: AddedRange, text: string): AddedRange => {
  let { from, to } = range
  while (from < to && isWhitespace(text[from]) && to < text.length && isWhitespace(text[to])) {
    from++
    to++
  }
  return { from, to }
}

// diffAddedRanges computes the uncommitted additions in `current` relative to
// `previous`, normalizing range boundaries so adjacent insertions are
// highlighted consistently.
export const diffAddedRanges = (previous: string, current: string): AddedRange[] =>
  computeAddedRanges(previous, current).map((range) => rotateLeadingWhitespace(range, current))