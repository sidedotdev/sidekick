import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import FlowActionItem from '../FlowActionItem.vue'
import UserRequest from '../UserRequest.vue'
import type { FlowAction } from '../../lib/models'

const requestContent = 'The OAuth credential for openai is invalid or expired.'

const createFlowAction = (): FlowAction => ({
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
    requestContent,
    provider: 'openai',
    profileId: 'default',
    reason: 'invalid_oauth',
    methods: [
      { method: 'oauth_subscription', label: 'ChatGPT/Codex' },
      { method: 'api_key', label: 'Enter API key' },
    ],
  },
  actionResult: '',
  isHumanAction: true,
})

describe('provider auth completion', () => {
  beforeEach(() => {
    window.HTMLElement.prototype.scrollIntoView = vi.fn()
  })

  it.each([
    {
      result: { Choice: 'oauth_subscription' },
      resolution: 'Authenticated with: ChatGPT/Codex',
    },
    {
      result: { method: 'oauth_subscription', authType: 'subscription', resolvedExternally: false },
      resolution: 'Authenticated with: ChatGPT/Codex',
    },
    {
      result: { method: 'api_key', authType: 'api', resolvedExternally: true },
      resolution: 'Resolved externally',
    },
  ])('replaces the credential warning with the resolution for $result', async ({ result, resolution }) => {
    const pending = createFlowAction()
    const wrapper = mount(UserRequest, {
      props: { flowAction: pending, expand: true },
    })

    expect(wrapper.text()).toContain(requestContent)
    expect(wrapper.find('.provider-auth-methods').exists()).toBe(true)

    await wrapper.setProps({
      flowAction: {
        ...pending,
        actionStatus: 'complete',
        actionResult: JSON.stringify(result),
      },
    })

    expect(wrapper.text()).not.toContain(requestContent)
    expect(wrapper.find('.provider-auth-methods').exists()).toBe(false)
    expect(wrapper.find('.provider-auth-result').text()).toBe(resolution)
  })

  it.each([false, true])('collapses completed requests (already complete: %s)', async (alreadyComplete) => {
    const pending = createFlowAction()
    const completed: FlowAction = {
      ...pending,
      actionStatus: 'complete',
      actionResult: JSON.stringify({
        method: 'oauth_subscription',
        authType: 'subscription',
        resolvedExternally: false,
      }),
    }
    const wrapper = mount(FlowActionItem, {
      props: {
        flowAction: alreadyComplete ? completed : pending,
        defaultExpanded: true,
      },
    })

    if (!alreadyComplete) {
      expect(wrapper.classes()).toContain('expanded-action')
      expect(wrapper.text()).toContain(requestContent)

      await wrapper.find('.flow-action-header a').trigger('click')
      expect(wrapper.classes()).toContain('expanded-action')

      await wrapper.setProps({ flowAction: completed })
    }

    expect(wrapper.classes()).not.toContain('expanded-action')
    expect(wrapper.text()).not.toContain(requestContent)

    await wrapper.find('.flow-action-header a').trigger('click')

    expect(wrapper.classes()).toContain('expanded-action')
    expect(wrapper.text()).not.toContain(requestContent)
    expect(wrapper.find('.provider-auth-result').text()).toBe('Authenticated with: ChatGPT/Codex')
  })
})