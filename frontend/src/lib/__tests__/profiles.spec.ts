import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import type { Profile } from '../models'
import { store } from '../store'
import { useProfiles, profileName } from '../profiles'

const workProfiles: Profile[] = [
  { id: 'default', name: 'Default' },
  { id: 'work', name: 'Work' },
]

const createMockFetch = (profiles: Profile[] = workProfiles) =>
  vi.fn((url: string) => {
    if (url === '/api/v1/profiles') {
      return Promise.resolve({ ok: true, json: () => Promise.resolve({ profiles }) })
    }
    return Promise.resolve({ ok: false, json: () => Promise.resolve({}) })
  })

const cacheProfiles = (profiles: Profile[], ageMs: number) => {
  sessionStorage.setItem(
    'profiles_cache',
    JSON.stringify({ data: profiles, timestamp: Date.now() - ageMs }),
  )
}

describe('profiles', () => {
  beforeEach(() => {
    sessionStorage.clear()
    vi.stubGlobal('fetch', createMockFetch())
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('fetches profiles when nothing is cached', async () => {
    const fetchMock = createMockFetch()
    vi.stubGlobal('fetch', fetchMock)

    const { profiles } = useProfiles()
    expect(profiles.value).toEqual([{ id: 'default', name: 'Default' }])

    await flushPromises()

    expect(fetchMock).toHaveBeenCalledWith('/api/v1/profiles')
    expect(profiles.value).toEqual(workProfiles)
    expect(store.getProfilesCache()?.data).toEqual(workProfiles)
  })

  it('shows cached names immediately and refreshes stale names in the background', async () => {
    cacheProfiles([{ id: 'work', name: 'Old Work Name' }], 6 * 60 * 1000)
    const fetchMock = createMockFetch()
    vi.stubGlobal('fetch', fetchMock)

    const { profileName: name } = useProfiles()
    expect(name('work')).toBe('Old Work Name')

    await flushPromises()

    expect(fetchMock).toHaveBeenCalledWith('/api/v1/profiles')
    expect(name('work')).toBe('Work')
  })

  it('does not fetch profiles while the cache is fresh', async () => {
    cacheProfiles([{ id: 'work', name: 'Cached Work' }], 60 * 1000)
    const fetchMock = createMockFetch()
    vi.stubGlobal('fetch', fetchMock)

    const { profileName: name } = useProfiles()
    await flushPromises()

    expect(fetchMock).not.toHaveBeenCalled()
    expect(name('work')).toBe('Cached Work')
  })

  it('refreshes profile names only once the five minute cache ttl elapses', async () => {
    vi.useFakeTimers()
    try {
      store.setProfilesCache([{ id: 'work', name: 'Cached Work' }])
      const fetchMock = createMockFetch()
      vi.stubGlobal('fetch', fetchMock)

      vi.advanceTimersByTime(5 * 60 * 1000 - 1)
      const { profileName: name } = useProfiles()
      await flushPromises()

      expect(fetchMock).not.toHaveBeenCalled()
      expect(name('work')).toBe('Cached Work')

      vi.advanceTimersByTime(2)
      useProfiles()
      await flushPromises()

      expect(fetchMock).toHaveBeenCalledTimes(1)
      expect(name('work')).toBe('Work')
    } finally {
      vi.useRealTimers()
    }
  })

  it('keeps showing cached names when discovery fails', async () => {
    cacheProfiles([{ id: 'work', name: 'Cached Work' }], 6 * 60 * 1000)
    vi.stubGlobal('fetch', vi.fn(() => Promise.reject(new Error('offline'))))

    const { profileName: name } = useProfiles()
    await flushPromises()

    expect(name('work')).toBe('Cached Work')
  })

  it('falls back to the profile id when no name is known', async () => {
    const { profileName: name } = useProfiles()
    await flushPromises()

    expect(name('unknown_profile')).toBe('unknown_profile')
  })

  it('treats a missing profile id as the default profile', async () => {
    useProfiles()
    await flushPromises()

    expect(profileName()).toBe('Default')
    expect(profileName('DEFAULT')).toBe('Default')
  })
})