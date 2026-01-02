export interface OffHoursWindow {
  days?: string[]
  start: string
  end: string
}

export interface OffHoursConfig {
  enabled: boolean
  blocked: boolean
  unblockAt?: string
  message?: string
  windows?: OffHoursWindow[]
}

let cachedConfig: OffHoursConfig | null = null
let configFetchPromise: Promise<OffHoursConfig> | null = null

const DEFAULT_MESSAGE = 'Time to rest!'

export const fetchOffHoursConfig = async (): Promise<OffHoursConfig> => {
  if (cachedConfig !== null) {
    return cachedConfig
  }

  if (configFetchPromise !== null) {
    return configFetchPromise
  }

  configFetchPromise = fetch('/api/v1/off_hours')
    .then(async (response) => {
      if (!response.ok) {
        return { enabled: false, blocked: false }
      }
      const data = await response.json()
      cachedConfig = data
      return data
    })
    .catch(() => {
      return { enabled: false, blocked: false }
    })
    .finally(() => {
      configFetchPromise = null
    })

  return configFetchPromise
}

export const clearOffHoursCache = (): void => {
  cachedConfig = null
}

const parseTimeOfDay = (s: string): { hour: number; minute: number } | null => {
  const parts = s.split(':')
  if (parts.length !== 2) return null

  const hour = parseInt(parts[0], 10)
  const minute = parseInt(parts[1], 10)

  if (isNaN(hour) || isNaN(minute)) return null
  if (hour < 0 || hour > 23) return null
  if (minute < 0 || minute > 59) return null

  return { hour, minute }
}

const timeOfDayMinutes = (hour: number, minute: number): number => {
  return hour * 60 + minute
}

const dayNames = ['sunday', 'monday', 'tuesday', 'wednesday', 'thursday', 'friday', 'saturday']

const containsDay = (days: string[] | undefined, day: string): boolean => {
  if (!days || days.length === 0) return true
  return days.some((d) => d.toLowerCase() === day.toLowerCase())
}

export const isTimeInWindow = (
  t: Date,
  window: OffHoursWindow
): { blocked: boolean; unblockAt: Date | null } => {
  const startParsed = parseTimeOfDay(window.start)
  const endParsed = parseTimeOfDay(window.end)

  if (!startParsed || !endParsed) {
    return { blocked: false, unblockAt: null }
  }

  const dayName = dayNames[t.getDay()]
  const currentMinutes = timeOfDayMinutes(t.getHours(), t.getMinutes())
  const startMinutes = timeOfDayMinutes(startParsed.hour, startParsed.minute)
  const endMinutes = timeOfDayMinutes(endParsed.hour, endParsed.minute)

  const crossesMidnight = endMinutes < startMinutes

  if (crossesMidnight) {
    // Window crosses midnight, e.g., 23:00 to 07:00
    // Check if we're in the "before midnight" part (on start day)
    // or the "after midnight" part (on end day, which is start day + 1)
    const previousDayName = dayNames[(t.getDay() + 6) % 7]

    if (containsDay(window.days, dayName) && currentMinutes >= startMinutes) {
      // We're in the before-midnight portion on a matching day
      const unblockAt = new Date(t)
      unblockAt.setDate(unblockAt.getDate() + 1)
      unblockAt.setHours(endParsed.hour, endParsed.minute, 0, 0)
      return { blocked: true, unblockAt }
    }

    if (containsDay(window.days, previousDayName) && currentMinutes < endMinutes) {
      // We're in the after-midnight portion, and the window started yesterday
      const unblockAt = new Date(t)
      unblockAt.setHours(endParsed.hour, endParsed.minute, 0, 0)
      return { blocked: true, unblockAt }
    }
  } else {
    // Same-day window
    if (containsDay(window.days, dayName) && currentMinutes >= startMinutes && currentMinutes < endMinutes) {
      const unblockAt = new Date(t)
      unblockAt.setHours(endParsed.hour, endParsed.minute, 0, 0)
      return { blocked: true, unblockAt }
    }
  }

  return { blocked: false, unblockAt: null }
}

export interface OffHoursStatus {
  blocked: boolean
  unblockAt: Date | null
  message: string
}

export const evaluateOffHours = (t: Date, config: OffHoursConfig): OffHoursStatus => {
  if (!config.enabled || !config.windows || config.windows.length === 0) {
    return { blocked: false, unblockAt: null, message: '' }
  }

  let earliestUnblock: Date | null = null

  for (const window of config.windows) {
    const result = isTimeInWindow(t, window)
    if (result.blocked) {
      if (result.unblockAt !== null) {
        if (earliestUnblock === null || result.unblockAt < earliestUnblock) {
          earliestUnblock = result.unblockAt
        }
      }
    }
  }

  if (earliestUnblock !== null) {
    return {
      blocked: true,
      unblockAt: earliestUnblock,
      message: config.message || DEFAULT_MESSAGE,
    }
  }

  return { blocked: false, unblockAt: null, message: '' }
}

export const isBlockedNow = async (): Promise<OffHoursStatus> => {
  const config = await fetchOffHoursConfig()
  return evaluateOffHours(new Date(), config)
}

export const getOffHoursMessage = (config: OffHoursConfig): string => {
  return config.message || DEFAULT_MESSAGE
}