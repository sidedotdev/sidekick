import { describe, it, expect } from 'vitest'
import { failedAttemptsSummary, formatApproxDuration, formatRetryTime, isRateLimitRequest, parseGoDuration, rateLimitMessage } from '../retryAttempts'

describe('failedAttemptsSummary', () => {
  it('is null without attempts', () => {
    expect(failedAttemptsSummary({})).toBeNull()
    expect(failedAttemptsSummary({ attempts: [] })).toBeNull()
  })

  it('pluralizes the count', () => {
    const attempt = { at: '2025-06-15T11:00:00Z', error: '429' }
    expect(failedAttemptsSummary({ attempts: [attempt] })).toBe('1 failed attempt')
    expect(failedAttemptsSummary({ attempts: [attempt, attempt, attempt] })).toBe('3 failed attempts')
  })
})

describe('formatRetryTime', () => {
  const now = new Date(2025, 5, 15, 10, 0, 0)

  it('shows only the time when the retry is today', () => {
    const formatted = formatRetryTime(new Date(2025, 5, 15, 14, 5, 0), now)
    expect(formatted).toMatch(/2:05|14:05/)
    expect(formatted).not.toMatch(/Jun|15/)
  })

  it('includes the date when the retry is on another day', () => {
    const formatted = formatRetryTime(new Date(2025, 5, 16, 14, 5, 0), now)
    expect(formatted).toMatch(/2:05|14:05/)
    expect(formatted).toMatch(/16/)
  })
})

describe('isRateLimitRequest', () => {
  it('matches only rate limited continue requests', () => {
    expect(isRateLimitRequest({ actionParams: { requestKind: 'continue', rateLimited: true } })).toBe(true)
    expect(isRateLimitRequest({ actionParams: { requestKind: 'continue', continueTag: 'try_again' } })).toBe(false)
    expect(isRateLimitRequest({ actionParams: { requestKind: 'approval', rateLimited: true } })).toBe(false)
  })
})

describe('parseGoDuration', () => {
  it('parses Go duration strings', () => {
    expect(parseGoDuration('4m0s')).toBe(4 * 60_000)
    expect(parseGoDuration('1h2m3.5s')).toBe(3_600_000 + 120_000 + 3_500)
    expect(parseGoDuration('500ms')).toBe(500)
    expect(parseGoDuration('bogus')).toBeNull()
  })
})

describe('formatApproxDuration', () => {
  it('rounds to the most natural unit', () => {
    expect(formatApproxDuration(20_000)).toBe('less than a minute')
    expect(formatApproxDuration(60_000)).toBe('1 minute')
    expect(formatApproxDuration(4 * 60_000 + 20_000)).toBe('4 minutes')
    expect(formatApproxDuration(2 * 3_600_000)).toBe('2 hours')
    expect(formatApproxDuration(3 * 86_400_000)).toBe('3 days')
  })
})

describe('rateLimitMessage', () => {
  const now = new Date(2025, 5, 15, 10, 0, 0)
  const retryAt = new Date(2025, 5, 15, 14, 5, 0).toISOString()

  it('promises to continue when the provider said the limit resets', () => {
    const message = rateLimitMessage({ actionParams: { requestKind: 'continue', rateLimited: true, retryAfter: '4h5m0s', retryAt, resetHinted: true } }, now)
    expect(message).toMatch(/^You've reached a usage limit until (2:05|14:05)/)
    expect(message).toContain("We'll automatically continue then.")
  })

  it('only promises a retry when the reset time is a guess', () => {
    const message = rateLimitMessage({ actionParams: { requestKind: 'continue', rateLimited: true, retryAfter: '4h5m0s', retryAt, resetHinted: false } }, now)
    expect(message).toBe("You've reached a usage limit. We'll automatically retry in about 4 hours.")
  })

  it('falls back to the offered wait for requests without an absolute retry time', () => {
    const message = rateLimitMessage({ actionParams: { requestKind: 'continue', rateLimited: true, retryAfter: '2m0s' } }, now)
    expect(message).toBe("You've reached a usage limit. We'll automatically retry in about 2 minutes.")
  })

  it('tells the user to retry manually once automatic retries are exhausted', () => {
    const message = rateLimitMessage({ actionParams: { requestKind: 'continue', rateLimited: true } }, now)
    expect(message).toContain('over a week')
    expect(message).toContain('Try again once it resets')
    expect(message).not.toContain('automatically')
  })
})