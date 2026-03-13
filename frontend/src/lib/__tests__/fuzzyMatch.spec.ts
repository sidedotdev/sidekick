import { describe, it, expect } from 'vitest'
import { fuzzyWordPrefixRank } from '../fuzzyMatch'

describe('fuzzyWordPrefixRank', () => {
  it('returns 0 for exact prefix match', () => {
    expect(fuzzyWordPrefixRank('sidekick', 'sid')).toBe(0)
    expect(fuzzyWordPrefixRank('sidekick', 'sidekick')).toBe(0)
    expect(fuzzyWordPrefixRank('sidekick', 's')).toBe(0)
  })

  it('returns 0 for empty query', () => {
    expect(fuzzyWordPrefixRank('anything', '')).toBe(0)
  })

  it('ranks word-prefix matches starting at first word as 1', () => {
    expect(fuzzyWordPrefixRank('sidekick-alpha', 'sa')).toBe(1)
    expect(fuzzyWordPrefixRank('super-awesome', 'sa')).toBe(1)
  })

  it('ranks word-prefix matches starting at later words higher', () => {
    expect(fuzzyWordPrefixRank('intellij-sidekick', 'sid')).toBe(2)
    expect(fuzzyWordPrefixRank('my-intellij-sidekick', 'sid')).toBe(3)
  })

  it('prefers full prefix over word-prefix match', () => {
    const prefixRank = fuzzyWordPrefixRank('sidekick', 'sid')
    const wordPrefixRank = fuzzyWordPrefixRank('intellij-sidekick', 'sid')
    expect(prefixRank).toBeLessThan(wordPrefixRank)
  })

  it('matches across multiple word prefixes', () => {
    expect(fuzzyWordPrefixRank('sidekick-alpha-beta', 'sab')).toBe(1)
    expect(fuzzyWordPrefixRank('sidekick-alpha-beta', 'siab')).toBe(1)
    expect(fuzzyWordPrefixRank('sidekick-alpha-beta', 'sidalb')).toBe(1)
  })

  it('returns -1 for no match', () => {
    expect(fuzzyWordPrefixRank('sidekick', 'xyz')).toBe(-1)
    expect(fuzzyWordPrefixRank('sidekick', 'dx')).toBe(-1)
  })

  it('falls back to substring match with lowest priority', () => {
    const rank = fuzzyWordPrefixRank('sidekick', 'ick')
    expect(rank).toBeGreaterThan(0)
    expect(rank).toBe(2) // 1 word + 1
  })

  it('is case insensitive', () => {
    expect(fuzzyWordPrefixRank('Sidekick', 'sid')).toBe(0)
    expect(fuzzyWordPrefixRank('sidekick', 'SID')).toBe(0)
    expect(fuzzyWordPrefixRank('Intellij-Sidekick', 'is')).toBe(1)
  })

  it('handles underscore and space delimiters', () => {
    expect(fuzzyWordPrefixRank('my_workspace', 'mw')).toBe(1)
    expect(fuzzyWordPrefixRank('my workspace', 'mw')).toBe(1)
  })

  it('handles single character words', () => {
    expect(fuzzyWordPrefixRank('a-b-c', 'ac')).toBe(1)
    expect(fuzzyWordPrefixRank('a-b-c', 'bc')).toBe(2)
  })

  it('handles camelCase word boundaries', () => {
    expect(fuzzyWordPrefixRank('myWorkspace', 'mw')).toBe(1)
    expect(fuzzyWordPrefixRank('myWorkspace', 'my')).toBe(0)
    expect(fuzzyWordPrefixRank('someGreatProject', 'sgp')).toBe(1)
    expect(fuzzyWordPrefixRank('someGreatProject', 'gp')).toBe(2)
  })

  it('handles acronym camelCase boundaries', () => {
    expect(fuzzyWordPrefixRank('XMLParser', 'xp')).toBe(1)
    expect(fuzzyWordPrefixRank('parseXMLDocument', 'pxd')).toBe(1)
  })

  it('handles slash delimiters', () => {
    expect(fuzzyWordPrefixRank('path/to/file', 'ptf')).toBe(1)
    expect(fuzzyWordPrefixRank('path/to/file', 'tf')).toBe(2)
    expect(fuzzyWordPrefixRank('org/repo/project', 'orp')).toBe(1)
  })

  it('handles mixed delimiters', () => {
    expect(fuzzyWordPrefixRank('my-app/someGreat_thing', 'mst')).toBe(1)
    expect(fuzzyWordPrefixRank('my-app/someGreat_thing', 'st')).toBe(3)
  })

  it('ranks sidekick before intellij-sidekick for query "sid"', () => {
    const sidekickRank = fuzzyWordPrefixRank('sidekick', 'sid')
    const intellijSidekickRank = fuzzyWordPrefixRank('intellij-sidekick', 'sid')
    expect(sidekickRank).toBeLessThan(intellijSidekickRank)
  })
})