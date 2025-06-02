import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, VueWrapper } from '@vue/test-utils'
import UserRequest from '../UserRequest.vue'
import type { FlowAction } from '../../lib/models'

describe('UserRequest', () => {
  let wrapper: VueWrapper

  beforeEach(() => {
    vi.clearAllMocks()
  })

  const createMockFlowAction = (overrides = {}): FlowAction => ({
    id: 'test-action-id',
    flowId: 'test-flow-id',
    workspaceId: 'test-workspace-id',
    created: new Date(),
    updated: new Date(),
    subflow: 'test-subflow',
    actionType: 'user_request',
    actionStatus: 'pending',
    actionParams: {
      requestKind: 'approval',
      requestContent: 'Test request content',
      approveTag: 'approve_plan',
      rejectTag: 'reject_plan'
    },
    actionResult: '',
    isHumanAction: true,
    ...overrides
  })

  const mountComponent = (flowAction: FlowAction, expand = true) => {
    wrapper = mount(UserRequest, {
      props: { flowAction, expand }
    })
  }

  it('renders without errors', () => {
    const flowAction = createMockFlowAction()
    mountComponent(flowAction)
    expect(wrapper.exists()).toBe(true)
  })

  it('submits user response successfully and clears error message', async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      json: () => Promise.resolve({ success: true })
    })
    global.fetch = fetchMock

    const flowAction = createMockFlowAction()
    mountComponent(flowAction)

    // Click approve button
    const approveButton = wrapper.find('button.cta-button-color')
    await approveButton.trigger('click')

    expect(fetchMock).toHaveBeenCalledWith(
      '/api/v1/workspaces/test-workspace-id/flow_actions/test-action-id/complete',
      expect.objectContaining({
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          userResponse: {
            content: '',
            approved: true
          }
        })
      })
    )

    // Verify no error message is displayed
    const errorElement = wrapper.find('.error-message')
    expect(errorElement.exists()).toBe(false)
  })

  it('displays error message when API returns error response', async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: false,
      json: () => Promise.resolve({ error: 'API error message' })
    })
    global.fetch = fetchMock

    const flowAction = createMockFlowAction()
    mountComponent(flowAction)

    // Click approve button
    const approveButton = wrapper.find('button.cta-button-color')
    await approveButton.trigger('click')

    // Wait for the async operation to complete
    await new Promise(resolve => setTimeout(resolve, 0))
    await wrapper.vm.$nextTick()

    // Verify error message is displayed
    const errorElement = wrapper.find('.error-message')
    expect(errorElement.exists()).toBe(true)
    expect(errorElement.text()).toBe('API error message')
  })

  it('displays generic error message when API returns non-JSON error', async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: false,
      json: () => Promise.reject(new Error('Invalid JSON'))
    })
    global.fetch = fetchMock

    const flowAction = createMockFlowAction()
    mountComponent(flowAction)

    // Click approve button
    const approveButton = wrapper.find('button.cta-button-color')
    await approveButton.trigger('click')

    // Wait for the async operation to complete
    await new Promise(resolve => setTimeout(resolve, 0))
    await wrapper.vm.$nextTick()

    // Verify generic error message is displayed
    const errorElement = wrapper.find('.error-message')
    expect(errorElement.exists()).toBe(true)
    expect(errorElement.text()).toBe('Failed to complete flow action')
  })

  it('displays network error message when fetch fails', async () => {
    const fetchMock = vi.fn().mockRejectedValue(new Error('Network error'))
    global.fetch = fetchMock

    const flowAction = createMockFlowAction()
    mountComponent(flowAction)

    // Click approve button
    const approveButton = wrapper.find('button.cta-button-color')
    await approveButton.trigger('click')

    // Wait for the async operation to complete
    await new Promise(resolve => setTimeout(resolve, 0))
    await wrapper.vm.$nextTick()

    // Verify network error message is displayed
    const errorElement = wrapper.find('.error-message')
    expect(errorElement.exists()).toBe(true)
    expect(errorElement.text()).toBe('Network error: Failed to submit response')
  })
})