import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { mount, VueWrapper } from '@vue/test-utils'
import UserRequest from '../UserRequest.vue'
import type { FlowAction } from '../../lib/models'

function rateLimitedRequest(params: { [key: string]: any }): FlowAction {
  return {
    id: 'fa_1',
    flowId: 'flow_1',
    workspaceId: 'ws_1',
    created: new Date(),
    updated: new Date(),
    subflow: 'coding',
    actionType: 'user_request.continue',
    actionParams: {
      requestKind: 'continue',
      continueTag: 'try_again',
      rateLimited: true,
      requestContent: "You've reached a usage limit.\n\n```\n429 Too Many Requests\n```",
      ...params,
    },
    actionStatus: 'pending',
    actionResult: '',
    isHumanAction: true,
  } as FlowAction
}

describe('UserRequest rate limit card', () => {
  let wrapper: VueWrapper
  let originalFetch: typeof global.fetch

  beforeEach(() => {
    originalFetch = global.fetch
    global.fetch = vi.fn().mockResolvedValue({ ok: true } as Response)
  })

  afterEach(() => {
    global.fetch = originalFetch
    wrapper?.unmount()
  })

  const mountRequest = (flowAction: FlowAction) => {
    wrapper = mount(UserRequest, {
      props: { flowAction, expand: true },
      global: { stubs: { VueMarkdown: { template: '<div class="markdown-stub" />' } } },
    })
  }

  it('says the limit resets at the hinted time and offers Try Again', async () => {
    const retryAt = new Date(Date.now() + 2 * 3_600_000).toISOString()
    mountRequest(rateLimitedRequest({ retryAfter: '2h0m0s', retryAt, resetHinted: true }))

    const notice = wrapper.find('.rate-limit-notice')
    expect(notice.exists()).toBe(true)
    expect(notice.text()).toContain("You've reached a usage limit until")
    expect(notice.text()).toContain("We'll automatically continue then.")
    expect(notice.text()).not.toContain('429')

    const button = wrapper.find('button.cta-button-color')
    expect(button.text()).toContain('Try Again')
    await button.trigger('click')

    expect(global.fetch).toHaveBeenCalledWith(
      '/api/v1/workspaces/ws_1/flow_actions/fa_1/complete',
      expect.objectContaining({ method: 'POST' }),
    )
  })

  it('only promises a retry without a provider hint', () => {
    const retryAt = new Date(Date.now() + 4 * 60_000).toISOString()
    mountRequest(rateLimitedRequest({ retryAfter: '4m0s', retryAt, resetHinted: false }))

    expect(wrapper.find('.rate-limit-notice').text()).toContain('automatically retry in about 4 minutes')
  })

  it('asks for a manual retry once automatic retries are exhausted', () => {
    mountRequest(rateLimitedRequest({}))

    const text = wrapper.find('.rate-limit-notice').text()
    expect(text).toContain('Try again once it resets')
    expect(text).not.toContain('automatically')
    expect(wrapper.find('button.cta-button-color').text()).toContain('Try Again')
  })

  it('keeps the plain continue prompt for other failures', () => {
    const flowAction = rateLimitedRequest({})
    delete flowAction.actionParams.rateLimited
    mountRequest(flowAction)

    expect(wrapper.find('.rate-limit-notice').exists()).toBe(false)
    expect(wrapper.find('.markdown-container').exists()).toBe(true)
  })
})