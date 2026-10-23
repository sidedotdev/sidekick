import type { FlowAction } from './models'

/** Human-readable count of failed attempts, or null when there were none. */
export function failedAttemptsSummary(flowAction: Pick<FlowAction, 'attempts'>): string | null {
  const count = flowAction.attempts?.length ?? 0
  if (count === 0) {
    return null
  }
  return `${count} failed attempt${count === 1 ? '' : 's'}`
}

/**
 * Formats a retry time relative to now: time of day when it's today, with
 * the date otherwise.
 */
export function formatRetryTime(retryAt: Date, now: Date = new Date()): string {
  const timeOptions: Intl.DateTimeFormatOptions = { hour: 'numeric', minute: '2-digit' }
  if (retryAt.toDateString() === now.toDateString()) {
    return retryAt.toLocaleTimeString(undefined, timeOptions)
  }
  return retryAt.toLocaleString(undefined, { month: 'short', day: 'numeric', ...timeOptions })
}

/** Whether the pending continue request was raised by a provider usage limit. */
export function isRateLimitRequest(flowAction: Pick<FlowAction, 'actionParams'>): boolean {
  return flowAction.actionParams?.requestKind === 'continue' && flowAction.actionParams?.rateLimited === true
}

const goDurationUnitMs: { [unit: string]: number } = {
  h: 3_600_000,
  m: 60_000,
  s: 1_000,
  ms: 1,
  'µs': 0.001,
  us: 0.001,
  ns: 0.000001,
}

/** Parses a Go time.Duration string such as "1h2m3.5s" into milliseconds. */
export function parseGoDuration(value: string): number | null {
  const matches = [...value.matchAll(/(\d+(?:\.\d+)?)(h|ms|m|s|µs|us|ns)/g)]
  if (matches.length === 0 || matches.map((m) => m[0]).join('') !== value) {
    return null
  }
  return matches.reduce((total, [, amount, unit]) => total + Number(amount) * goDurationUnitMs[unit], 0)
}

export function formatApproxDuration(ms: number): string {
  const plural = (n: number, unit: string) => `${n} ${unit}${n === 1 ? '' : 's'}`
  const minutes = Math.round(ms / 60_000)
  if (minutes < 1) {
    return 'less than a minute'
  }
  if (minutes < 60) {
    return plural(minutes, 'minute')
  }
  const hours = Math.round(ms / 3_600_000)
  if (hours < 24) {
    return plural(hours, 'hour')
  }
  return plural(Math.round(ms / 86_400_000), 'day')
}

/**
 * The message shown for a usage-limit request. The request offers an
 * automatic retry via retryAfter (and, when raised by a recent worker, an
 * absolute retryAt); without one the schedule is exhausted and only the user
 * can continue. We only claim to know when the limit resets if the provider
 * said so, otherwise the retry time is just our backoff guess.
 */
export function rateLimitMessage(flowAction: Pick<FlowAction, 'actionParams'>, now: Date = new Date()): string {
  const params = flowAction.actionParams ?? {}
  const retryAfter: string | undefined = params.retryAfter
  if (!retryAfter) {
    return "You've reached a usage limit and it hasn't reset after retrying for over a week. Try again once it resets."
  }
  const retryAt = params.retryAt ? new Date(params.retryAt) : null
  if (retryAt && params.resetHinted === true) {
    return `You've reached a usage limit until ${formatRetryTime(retryAt, now)}. We'll automatically continue then.`
  }
  const waitMs = retryAt ? Math.max(0, retryAt.getTime() - now.getTime()) : parseGoDuration(retryAfter)
  if (waitMs === null) {
    return "You've reached a usage limit. We'll automatically retry later."
  }
  return `You've reached a usage limit. We'll automatically retry in about ${formatApproxDuration(waitMs)}.`
}