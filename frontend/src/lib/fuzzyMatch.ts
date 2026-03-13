/**
 * Ranks how well a query matches a name using word-prefix matching
 * (similar to Ctrl+P / Cmd+P in editors like Sublime Text or VS Code).
 *
 * Returns a non-negative rank (lower is better), or -1 if no match.
 *   0: query is a prefix of the full name
 *   1..N: query characters match prefixes of words starting at word index N-1
 *   N+1: query is a substring but not a word-prefix match
 */
export function fuzzyWordPrefixRank(name: string, query: string): number {
  const lowerName = name.toLowerCase()
  const lowerQuery = query.toLowerCase()

  if (!lowerQuery) return 0
  if (lowerName.startsWith(lowerQuery)) return 0

  const words = name
    .replace(/([a-z])([A-Z])/g, '$1\0$2')
    .replace(/([A-Z]+)([A-Z][a-z])/g, '$1\0$2')
    .split(/[-_\s/\0]+/)
    .filter(w => w.length > 0)
    .map(w => w.toLowerCase())

  for (let startWord = 0; startWord < words.length; startWord++) {
    if (words[startWord][0] !== lowerQuery[0]) continue

    let queryIdx = 0
    for (let wordIdx = startWord; wordIdx < words.length && queryIdx < lowerQuery.length; wordIdx++) {
      const word = words[wordIdx]
      let charIdx = 0
      while (charIdx < word.length && queryIdx < lowerQuery.length && word[charIdx] === lowerQuery[queryIdx]) {
        charIdx++
        queryIdx++
      }
    }
    if (queryIdx === lowerQuery.length) {
      return startWord + 1
    }
  }

  if (lowerName.includes(lowerQuery)) {
    return words.length + 1
  }

  return -1
}