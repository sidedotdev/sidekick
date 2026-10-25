import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { flushPromises, mount, VueWrapper } from '@vue/test-utils'
import ProviderAuthRequest from '../ProviderAuthRequest.vue'
import type { FlowAction } from '../../lib/models'
import {
  getProviderOAuthStatus,
  providerAPIKeyExists,
  saveProviderAPIKey,
  startProviderOAuth,
  submitProviderOAuthCode,
} from '../../lib/providerAuth'

vi.mock('../../lib/providerAuth', async (importOriginal) => {
  const actual: object = await importOriginal()
  return {
    ...actual,
    startProviderOAuth: vi.fn(),
    getProviderOAuthStatus: vi.fn(),
    submitProviderOAuthCode: vi.fn(),
    providerAPIKeyExists: vi.fn(),
    saveProviderAPIKey: vi.fn(),
  }
})

const AUTH_URL = 'https://claude.ai/oauth/authorize?state=abc'

const createFlowAction = (params: Record<string, any> = {}): FlowAction => ({
  id: 'auth-action-id',
  flowId: 'flow-id',
  workspaceId: 'workspace-id',
  created: new Date(),
  updated: new Date(),
  subflow: '',
  actionType: 'user_request.provider_auth',
  actionStatus: 'pending',
  actionParams: {
    requestKind: 'provider_auth',
    requestContent: 'The OAuth credential for anthropic is invalid or expired.',
    note: 'Applies to this flow only. To change the default, edit the provider config.',
    provider: 'anthropic',
    profileId: 'default',
    reason: 'invalid_oauth',
    authType: 'subscription',
    methods: [
      { method: 'oauth_subscription', label: 'Claude Pro/Max' },
      { method: 'oauth_create_key', label: 'Create an API Key (OAuth)' },
      { method: 'api_key', label: 'Enter API key' },
    ],
    ...params,
  },
  actionResult: '',
  isHumanAction: true,
})

describe('ProviderAuthRequest', () => {
  let wrapper: VueWrapper
  let openSpy: ReturnType<typeof vi.spyOn>
  let submitChoice: ReturnType<typeof vi.fn<(method: string) => Promise<void>>>

  beforeEach(() => {
    vi.useFakeTimers()
    submitChoice = vi.fn<(method: string) => Promise<void>>().mockResolvedValue(undefined)
    vi.mocked(startProviderOAuth).mockReset()
    vi.mocked(getProviderOAuthStatus).mockReset()
    vi.mocked(submitProviderOAuthCode).mockReset()
    vi.mocked(providerAPIKeyExists).mockReset()
    vi.mocked(saveProviderAPIKey).mockReset()
    openSpy = vi.spyOn(window, 'open').mockReturnValue(null)
  })

  afterEach(() => {
    wrapper?.unmount()
    vi.useRealTimers()
    vi.restoreAllMocks()
  })

  const mountComponent = (params: Record<string, any> = {}) => {
    wrapper = mount(ProviderAuthRequest, { props: { flowAction: createFlowAction(params), submitChoice } })
  }

  const clickMethod = async (label: string) => {
    const button = wrapper.findAll('button.provider-auth-method').find(b => b.text() === label)
    expect(button, `method button "${label}"`).toBeTruthy()
    await button!.trigger('click')
    await flushPromises()
  }

  it('renders the content, note, and one button per method with the first as primary', () => {
    mountComponent()

    expect(wrapper.text()).toContain('The OAuth credential for anthropic is invalid or expired.')
    expect(wrapper.text()).toContain('Applies to this flow only. To change the default, edit the provider config.')
    const buttons = wrapper.findAll('button.provider-auth-method')
    expect(buttons.map(b => b.text())).toEqual(['Claude Pro/Max', 'Create an API Key (OAuth)', 'Enter API key'])
    expect(buttons[0].classes()).toContain('cta-button-color')
    expect(buttons[1].classes()).toContain('secondary')
    expect(buttons[2].classes()).toContain('secondary')
  })

  describe('OAuth', () => {
    it('opens the auth URL and submits the method once the login succeeds', async () => {
      vi.mocked(startProviderOAuth).mockResolvedValue({ sessionId: 'poa_1', authUrl: AUTH_URL })
      vi.mocked(getProviderOAuthStatus)
        .mockResolvedValueOnce({ status: 'pending' })
        .mockResolvedValueOnce({ status: 'succeeded' })
      mountComponent()

      await clickMethod('Claude Pro/Max')

      expect(startProviderOAuth).toHaveBeenCalledWith('anthropic', 'default', 'oauth_subscription')
      expect(openSpy).toHaveBeenCalledWith(AUTH_URL, '_blank', 'noopener,noreferrer')
      expect(wrapper.find('a.provider-auth-link').attributes('href')).toBe(AUTH_URL)
      expect(submitChoice).not.toHaveBeenCalled()

      await vi.advanceTimersByTimeAsync(1000)
      expect(submitChoice).not.toHaveBeenCalled()

      await vi.advanceTimersByTimeAsync(1000)
      expect(getProviderOAuthStatus).toHaveBeenCalledWith('poa_1')
      expect(submitChoice.mock.calls).toEqual([['oauth_subscription']])

      await vi.advanceTimersByTimeAsync(5000)
      expect(getProviderOAuthStatus).toHaveBeenCalledTimes(2)
    })

    it('completes the login with a pasted authorization code', async () => {
      vi.mocked(startProviderOAuth).mockResolvedValue({ sessionId: 'poa_2', authUrl: AUTH_URL })
      vi.mocked(getProviderOAuthStatus).mockResolvedValue({ status: 'pending' })
      vi.mocked(submitProviderOAuthCode).mockResolvedValue({ status: 'succeeded' })
      mountComponent()

      await clickMethod('Create an API Key (OAuth)')
      await wrapper.find('input.provider-auth-manual-code').setValue('  code#state  ')
      await wrapper.findAll('button').find(b => b.text() === 'Submit code')!.trigger('click')
      await flushPromises()

      expect(submitProviderOAuthCode).toHaveBeenCalledWith('poa_2', 'code#state')
      expect(submitChoice.mock.calls).toEqual([['oauth_create_key']])
    })

    it('shows a failed login inline and restarts it on retry', async () => {
      vi.mocked(startProviderOAuth)
        .mockResolvedValueOnce({ sessionId: 'poa_3', authUrl: AUTH_URL })
        .mockResolvedValueOnce({ sessionId: 'poa_4', authUrl: AUTH_URL })
      vi.mocked(getProviderOAuthStatus)
        .mockResolvedValueOnce({ status: 'failed', error: 'token exchange failed' })
        .mockResolvedValueOnce({ status: 'succeeded' })
      mountComponent()

      await clickMethod('Claude Pro/Max')
      await vi.advanceTimersByTimeAsync(1000)

      expect(wrapper.find('.provider-auth-error').text()).toContain('token exchange failed')
      expect(wrapper.find('a.provider-auth-link').exists()).toBe(false)
      expect(submitChoice).not.toHaveBeenCalled()

      await wrapper.find('button.provider-auth-retry').trigger('click')
      await flushPromises()
      expect(startProviderOAuth).toHaveBeenCalledTimes(2)
      expect(wrapper.find('.provider-auth-error').exists()).toBe(false)

      await vi.advanceTimersByTimeAsync(1000)
      expect(getProviderOAuthStatus).toHaveBeenLastCalledWith('poa_4')
      expect(submitChoice.mock.calls).toEqual([['oauth_subscription']])
    })
  })

  describe('API key', () => {
    it('submits immediately when a key already exists', async () => {
      vi.mocked(providerAPIKeyExists).mockResolvedValue(true)
      mountComponent({ reason: 'missing_api_key' })

      await clickMethod('Enter API key')

      expect(providerAPIKeyExists).toHaveBeenCalledWith('anthropic', 'default')
      expect(saveProviderAPIKey).not.toHaveBeenCalled()
      expect(wrapper.find('input[type="password"]').exists()).toBe(false)
      expect(submitChoice.mock.calls).toEqual([['api_key']])
    })

    it('saves an entered key before submitting when none exists', async () => {
      vi.mocked(providerAPIKeyExists).mockResolvedValue(false)
      vi.mocked(saveProviderAPIKey).mockResolvedValue()
      mountComponent({ profileId: 'work', provider: 'google', methods: [{ method: 'api_key', label: 'Enter API key' }] })

      await clickMethod('Enter API key')
      expect(submitChoice).not.toHaveBeenCalled()

      await wrapper.find('input[type="password"]').setValue('sk-new')
      await wrapper.findAll('button').find(b => b.text() === 'Save')!.trigger('click')
      await flushPromises()

      expect(saveProviderAPIKey).toHaveBeenCalledWith('google', 'work', 'sk-new')
      expect(submitChoice.mock.calls).toEqual([['api_key']])
    })

    it('asks for a new key without reusing one that was rejected', async () => {
      vi.mocked(saveProviderAPIKey)
        .mockRejectedValueOnce(new Error('Failed to save API key'))
        .mockResolvedValueOnce()
      mountComponent({ reason: 'invalid_api_key' })

      await clickMethod('Enter API key')
      expect(providerAPIKeyExists).not.toHaveBeenCalled()

      await wrapper.find('input[type="password"]').setValue('sk-replacement')
      await wrapper.find('input[type="password"]').trigger('keydown', { key: 'Enter' })
      await flushPromises()

      expect(wrapper.find('.provider-auth-error').text()).toContain('Failed to save API key')
      expect(submitChoice).not.toHaveBeenCalled()

      await wrapper.find('button.provider-auth-retry').trigger('click')
      await flushPromises()

      expect(saveProviderAPIKey).toHaveBeenCalledTimes(2)
      expect(saveProviderAPIKey).toHaveBeenLastCalledWith('anthropic', 'default', 'sk-replacement')
      expect(submitChoice.mock.calls).toEqual([['api_key']])
    })
  })

  describe('completion failure', () => {
    it('retries only the submission after a saved key is not accepted', async () => {
      vi.mocked(providerAPIKeyExists).mockResolvedValue(false)
      vi.mocked(saveProviderAPIKey).mockResolvedValue()
      submitChoice
        .mockRejectedValueOnce(new Error('Failed to complete flow action'))
        .mockResolvedValueOnce(undefined)
      mountComponent({ reason: 'missing_api_key' })

      await clickMethod('Enter API key')
      await wrapper.find('input[type="password"]').setValue('sk-new')
      await wrapper.find('input[type="password"]').trigger('keydown', { key: 'Enter' })
      await flushPromises()

      expect(wrapper.find('.provider-auth-error').text()).toContain('Failed to complete flow action')
      expect(wrapper.find('button.provider-auth-method').attributes('disabled')).toBeUndefined()

      await wrapper.find('button.provider-auth-retry').trigger('click')
      await flushPromises()

      expect(saveProviderAPIKey).toHaveBeenCalledTimes(1)
      expect(submitChoice.mock.calls).toEqual([['api_key'], ['api_key']])
      expect(wrapper.find('.provider-auth-error').exists()).toBe(false)
      expect(wrapper.find('button.provider-auth-method').attributes('disabled')).toBeDefined()
    })

    it('retries only the submission after a finished OAuth login is not accepted', async () => {
      vi.mocked(startProviderOAuth).mockResolvedValue({ sessionId: 'poa_5', authUrl: AUTH_URL })
      vi.mocked(getProviderOAuthStatus).mockResolvedValue({ status: 'succeeded' })
      submitChoice
        .mockRejectedValueOnce(new Error('Network error: Failed to submit response'))
        .mockResolvedValueOnce(undefined)
      mountComponent()

      await clickMethod('Claude Pro/Max')
      await vi.advanceTimersByTimeAsync(1000)

      expect(wrapper.find('.provider-auth-error').text()).toContain('Network error: Failed to submit response')
      expect(wrapper.find('a.provider-auth-link').exists()).toBe(false)

      await wrapper.find('button.provider-auth-retry').trigger('click')
      await flushPromises()
      await vi.advanceTimersByTimeAsync(5000)

      expect(startProviderOAuth).toHaveBeenCalledTimes(1)
      expect(getProviderOAuthStatus).toHaveBeenCalledTimes(1)
      expect(submitChoice.mock.calls).toEqual([['oauth_subscription'], ['oauth_subscription']])
    })
  })
})