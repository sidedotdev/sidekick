import { describe, it, expect, beforeEach } from 'vitest'
import { evaluateOffHours, isTimeInWindow, type OffHoursConfig, type OffHoursWindow } from '../offHours'

describe('isTimeInWindow', () => {
  it('should return not blocked for invalid time format', () => {
    const window: OffHoursWindow = { start: 'invalid', end: '07:00' }
    const t = new Date('2024-01-15T04:00:00')
    const result = isTimeInWindow(t, window)
    expect(result.blocked).toBe(false)
    expect(result.unblockAt).toBe(null)
  })

  it('should block during same-day window', () => {
    const window: OffHoursWindow = { start: '09:00', end: '17:00' }
    const t = new Date('2024-01-15T12:00:00') // Monday at noon
    const result = isTimeInWindow(t, window)
    expect(result.blocked).toBe(true)
    expect(result.unblockAt).not.toBe(null)
    expect(result.unblockAt?.getHours()).toBe(17)
    expect(result.unblockAt?.getMinutes()).toBe(0)
  })

  it('should not block outside same-day window', () => {
    const window: OffHoursWindow = { start: '09:00', end: '17:00' }
    const t = new Date('2024-01-15T08:00:00') // Monday at 8am
    const result = isTimeInWindow(t, window)
    expect(result.blocked).toBe(false)
  })

  it('should block during overnight window (before midnight portion)', () => {
    const window: OffHoursWindow = { start: '23:00', end: '07:00' }
    const t = new Date('2024-01-15T23:30:00') // Monday at 11:30pm
    const result = isTimeInWindow(t, window)
    expect(result.blocked).toBe(true)
    expect(result.unblockAt).not.toBe(null)
    expect(result.unblockAt?.getDate()).toBe(16) // Next day
    expect(result.unblockAt?.getHours()).toBe(7)
  })

  it('should block during overnight window (after midnight portion)', () => {
    const window: OffHoursWindow = { start: '23:00', end: '07:00' }
    const t = new Date('2024-01-16T03:00:00') // Tuesday at 3am
    const result = isTimeInWindow(t, window)
    expect(result.blocked).toBe(true)
    expect(result.unblockAt).not.toBe(null)
    expect(result.unblockAt?.getDate()).toBe(16) // Same day
    expect(result.unblockAt?.getHours()).toBe(7)
  })

  it('should not block outside overnight window', () => {
    const window: OffHoursWindow = { start: '23:00', end: '07:00' }
    const t = new Date('2024-01-15T12:00:00') // Monday at noon
    const result = isTimeInWindow(t, window)
    expect(result.blocked).toBe(false)
  })

  it('should respect day-specific windows', () => {
    const window: OffHoursWindow = { days: ['monday'], start: '09:00', end: '17:00' }
    
    // Monday - should block
    const monday = new Date('2024-01-15T12:00:00')
    expect(isTimeInWindow(monday, window).blocked).toBe(true)
    
    // Tuesday - should not block
    const tuesday = new Date('2024-01-16T12:00:00')
    expect(isTimeInWindow(tuesday, window).blocked).toBe(false)
  })

  it('should handle overnight window with day-specific config', () => {
    // Window starts on Monday night, ends Tuesday morning
    const window: OffHoursWindow = { days: ['monday'], start: '23:00', end: '07:00' }
    
    // Monday at 11:30pm - should block (start day matches)
    const mondayNight = new Date('2024-01-15T23:30:00')
    expect(isTimeInWindow(mondayNight, window).blocked).toBe(true)
    
    // Tuesday at 3am - should block (previous day was Monday)
    const tuesdayMorning = new Date('2024-01-16T03:00:00')
    expect(isTimeInWindow(tuesdayMorning, window).blocked).toBe(true)
    
    // Tuesday at 11:30pm - should not block (Tuesday not in days list)
    const tuesdayNight = new Date('2024-01-16T23:30:00')
    expect(isTimeInWindow(tuesdayNight, window).blocked).toBe(false)
    
    // Wednesday at 3am - should not block (previous day was Tuesday)
    const wednesdayMorning = new Date('2024-01-17T03:00:00')
    expect(isTimeInWindow(wednesdayMorning, window).blocked).toBe(false)
  })

  it('should handle empty days array as all days', () => {
    const window: OffHoursWindow = { days: [], start: '09:00', end: '17:00' }
    const t = new Date('2024-01-15T12:00:00')
    expect(isTimeInWindow(t, window).blocked).toBe(true)
  })

  it('should handle undefined days as all days', () => {
    const window: OffHoursWindow = { start: '09:00', end: '17:00' }
    const t = new Date('2024-01-15T12:00:00')
    expect(isTimeInWindow(t, window).blocked).toBe(true)
  })
})

describe('evaluateOffHours', () => {
  it('should return not blocked when not enabled', () => {
    const config: OffHoursConfig = { enabled: false, blocked: false }
    const result = evaluateOffHours(new Date(), config)
    expect(result.blocked).toBe(false)
    expect(result.message).toBe('')
  })

  it('should return not blocked when no windows configured', () => {
    const config: OffHoursConfig = { enabled: true, blocked: false, windows: [] }
    const result = evaluateOffHours(new Date(), config)
    expect(result.blocked).toBe(false)
  })

  it('should return blocked with default message when in window', () => {
    const config: OffHoursConfig = {
      enabled: true,
      blocked: false,
      windows: [{ start: '00:00', end: '23:59' }],
    }
    const result = evaluateOffHours(new Date('2024-01-15T12:00:00'), config)
    expect(result.blocked).toBe(true)
    expect(result.message).toBe('Time to rest!')
  })

  it('should return blocked with custom message when in window', () => {
    const config: OffHoursConfig = {
      enabled: true,
      blocked: false,
      message: 'Go to sleep!',
      windows: [{ start: '00:00', end: '23:59' }],
    }
    const result = evaluateOffHours(new Date('2024-01-15T12:00:00'), config)
    expect(result.blocked).toBe(true)
    expect(result.message).toBe('Go to sleep!')
  })

  it('should find earliest unblock time across multiple windows', () => {
    const config: OffHoursConfig = {
      enabled: true,
      blocked: false,
      windows: [
        { start: '03:00', end: '08:00' },
        { start: '03:00', end: '06:00' },
      ],
    }
    const t = new Date('2024-01-15T04:00:00')
    const result = evaluateOffHours(t, config)
    expect(result.blocked).toBe(true)
    expect(result.unblockAt?.getHours()).toBe(6) // Earlier of the two
  })

  it('should not block when outside all windows', () => {
    const config: OffHoursConfig = {
      enabled: true,
      blocked: false,
      windows: [
        { start: '03:00', end: '07:00' },
        { start: '22:00', end: '23:00' },
      ],
    }
    const t = new Date('2024-01-15T12:00:00')
    const result = evaluateOffHours(t, config)
    expect(result.blocked).toBe(false)
  })
})