import { ref, type Ref } from 'vue'
import type { Profile } from './models'
import { store } from './store'

export const DEFAULT_PROFILE_ID = 'default'
export const DEFAULT_PROFILE_NAME = 'Default'

// The default profile exists conceptually whether or not it is declared, so it
// is always available before any discovery request completes.
const defaultProfile: Profile = { id: DEFAULT_PROFILE_ID, name: DEFAULT_PROFILE_NAME }

const profiles = ref<Profile[]>([defaultProfile])
let refreshInFlight: Promise<void> | null = null

const applyCachedProfiles = () => {
  const cache = store.getProfilesCache()
  profiles.value = cache?.data?.length ? cache.data : [defaultProfile]
}

// refreshProfiles fetches declared profiles in the background, leaving the
// currently displayed profiles untouched when discovery fails.
export const refreshProfiles = (): Promise<void> => {
  if (refreshInFlight) return refreshInFlight

  refreshInFlight = (async () => {
    try {
      const response = await fetch('/api/v1/profiles')
      if (!response.ok) return
      const fetched: Profile[] = (await response.json()).profiles ?? []
      if (fetched.length === 0) return
      profiles.value = fetched
      store.setProfilesCache(fetched)
    } catch {
      // stale or unresolved names are preferable to losing the profile list
    } finally {
      refreshInFlight = null
    }
  })()

  return refreshInFlight
}

// profileName derives a profile's display name from its id, falling back to the
// id itself while the name is unknown.
export const profileName = (profileId?: string): string => {
  const id = profileId || DEFAULT_PROFILE_ID
  const match = profiles.value.find(profile => profile.id.toLowerCase() === id.toLowerCase())
  return match?.name ?? id
}

// useProfiles exposes the shared profile list, immediately reflecting cached
// values while refreshing them in the background once the cache is stale.
export const useProfiles = (): { profiles: Ref<Profile[]>; profileName: (profileId?: string) => string } => {
  applyCachedProfiles()
  if (store.isProfilesCacheStale()) {
    void refreshProfiles()
  }
  return { profiles, profileName }
}