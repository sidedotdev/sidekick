import { describe, it, expect } from 'vitest'
import { midString } from '../rank'

describe('midString', () => {
  it('returns a key strictly between the given bounds', () => {
    const cases: Array<[string, string]> = [
      ['', ''],
      ['', 'n'],
      ['n', ''],
      ['ab', 'b'],
      ['abc', 'abd'],
      ['az', 'b'],
      ['n', 'q'],
      ['nz', 'o'],
      ['z', ''],
      ['', 'ab'],
    ]
    for (const [prev, next] of cases) {
      const mid = midString(prev, next)
      if (prev) {
        expect(mid > prev, `expected ${mid} > ${prev}`).toBe(true)
      }
      if (next) {
        expect(mid < next, `expected ${mid} < ${next}`).toBe(true)
      }
      expect(mid.endsWith('a'), `expected ${mid} to not end in 'a'`).toBe(false)
    }
  })

  it('supports repeated insertion at the front', () => {
    let next = midString('', '')
    for (let i = 0; i < 100; i++) {
      const mid = midString('', next)
      expect(mid < next).toBe(true)
      next = mid
    }
  })

  it('supports repeated insertion at the back', () => {
    let prev = midString('', '')
    for (let i = 0; i < 100; i++) {
      const mid = midString(prev, '')
      expect(mid > prev).toBe(true)
      prev = mid
    }
  })

  it('supports repeated insertion between two keys', () => {
    let prev = midString('', '')
    let next = midString(prev, '')
    for (let i = 0; i < 100; i++) {
      const mid = midString(prev, next)
      expect(mid > prev, `expected ${mid} > ${prev}`).toBe(true)
      expect(mid < next, `expected ${mid} < ${next}`).toBe(true)
      if (i % 2 === 0) {
        prev = mid
      } else {
        next = mid
      }
    }
  })
})