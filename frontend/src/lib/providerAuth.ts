export type ProviderAuthMethod = 'oauth_subscription' | 'oauth_create_key' | 'api_key'

export interface ProviderAuthMethodInfo {
  method: ProviderAuthMethod
  label: string
}

export type ProviderOAuthStatusValue = 'pending' | 'succeeded' | 'failed'

export interface ProviderOAuthStatus {
  status: ProviderOAuthStatusValue
  error?: string
}

export interface ProviderOAuthSession {
  sessionId: string
  authUrl: string
}

export const isOAuthMethod = (method: string): boolean => method.startsWith('oauth_')

export const providerAuthMethodLabel = (methods: ProviderAuthMethodInfo[] | undefined, method: string): string =>
  methods?.find(info => info.method === method)?.label ?? method

// Provider auth errors are shown inline, so the server's message is surfaced
// whenever it provides one.
const requestJSON = async <T>(url: string, init?: RequestInit): Promise<T> => {
  const response = await fetch(url, init)
  if (!response.ok) {
    let message = `Request failed with status ${response.status}`
    try {
      const data = await response.json()
      if (data?.error) message = data.error
    } catch {
      // keep the status-based message when the body isn't JSON
    }
    throw new Error(message)
  }
  return response.json()
}

const postJSON = <T>(url: string, body: unknown): Promise<T> =>
  requestJSON<T>(url, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })

export const startProviderOAuth = (provider: string, profileId: string, method: ProviderAuthMethod): Promise<ProviderOAuthSession> =>
  postJSON('/api/v1/provider_auth/oauth/start', { provider, profileId, method })

export const getProviderOAuthStatus = (sessionId: string): Promise<ProviderOAuthStatus> =>
  requestJSON(`/api/v1/provider_auth/oauth/${encodeURIComponent(sessionId)}`)

// submitProviderOAuthCode hands a pasted authorization code or callback URL to
// a login whose browser redirect couldn't reach the server.
export const submitProviderOAuthCode = (sessionId: string, input: string): Promise<ProviderOAuthStatus> =>
  postJSON(`/api/v1/provider_auth/oauth/${encodeURIComponent(sessionId)}/code`, { input })

export const providerAPIKeyExists = async (provider: string, profileId: string): Promise<boolean> => {
  const query = new URLSearchParams({ provider, profileId })
  const data = await requestJSON<{ exists: boolean }>(`/api/v1/provider_auth/api_key?${query.toString()}`)
  return data.exists
}

export const saveProviderAPIKey = async (provider: string, profileId: string, apiKey: string): Promise<void> => {
  await postJSON('/api/v1/provider_auth/api_key', { provider, profileId, apiKey })
}