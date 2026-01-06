import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, VueWrapper } from '@vue/test-utils'
import UserRequest from '../UserRequest.vue'
import type { FlowAction } from '../../lib/models'

// Mock PrimeVue Select component
vi.mock('primevue/select', () => ({
  default: {
    name: 'Select',
    template: '<select :value="modelValue" @change="$emit(\'update:modelValue\', $event.target.value)"><slot /></select>',
    props: ['modelValue', 'options', 'optionLabel', 'optionValue']
  }
}))

// Mock BranchSelector to avoid PrimeVue issues
vi.mock('../BranchSelector.vue', () => ({
  default: {
    name: 'BranchSelector',
    template: '<select :value="modelValue" @change="$emit(\'update:modelValue\', $event.target.value)"><slot /></select>',
    props: ['modelValue', 'workspaceId', 'id']
  }
}))

// Mock DevRunControls
vi.mock('../DevRunControls.vue', () => ({
  default: {
    name: 'DevRunControls',
    template: '<div class="dev-run-controls-mock"></div>',
    props: ['workspaceId', 'flowId', 'disabled'],
    emits: ['start', 'stop']
  }
}))

describe('UserRequest', () => {
  let wrapper: VueWrapper

  beforeEach(() => {
    vi.clearAllMocks()
    localStorage.clear()
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

  describe('merge approval', () => {
    const createMergeApprovalFlowAction = (overrides = {}): FlowAction => ({
      id: 'test-action-id',
      flowId: 'test-flow-id',
      workspaceId: 'test-workspace-id',
      created: new Date(),
      updated: new Date(),
      subflow: 'test-subflow',
      actionType: 'user_request',
      actionStatus: 'pending',
      actionParams: {
        requestKind: 'merge_approval',
        requestContent: 'Review and approve merge',
        mergeApprovalInfo: {
          defaultTargetBranch: 'main',
          sourceBranch: 'feature-branch',
          diff: 'diff content here',
        }
      },
      actionResult: '',
      isHumanAction: true,
      ...overrides
    })

    it('includes mergeStrategy in approval payload with default squash', async () => {
      const fetchMock = vi.fn().mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({ success: true })
      })
      global.fetch = fetchMock

      const flowAction = createMergeApprovalFlowAction()
      mountComponent(flowAction)

      const approveButton = wrapper.find('button.cta-button-color')
      await approveButton.trigger('click')

      expect(fetchMock).toHaveBeenCalledWith(
        '/api/v1/workspaces/test-workspace-id/flow_actions/test-action-id/complete',
        expect.objectContaining({
          method: 'POST',
          body: expect.stringContaining('"mergeStrategy":"squash"')
        })
      )
    })

    it('persists mergeStrategy to localStorage', async () => {
      const flowAction = createMergeApprovalFlowAction()
      mountComponent(flowAction)

      // The component should have default squash strategy
      // Trigger a change by accessing the component's internal state
      const vm = wrapper.vm as any
      vm.mergeStrategy = 'merge'
      await wrapper.vm.$nextTick()

      expect(localStorage.getItem('mergeApproval.mergeStrategy')).toBe('merge')
    })

    it('loads persisted mergeStrategy from localStorage', async () => {
      localStorage.setItem('mergeApproval.mergeStrategy', 'merge')

      const flowAction = createMergeApprovalFlowAction()
      mountComponent(flowAction)

      await wrapper.vm.$nextTick()

      const vm = wrapper.vm as any
      expect(vm.mergeStrategy).toBe('merge')
    })

    it('sends devRunAction start via user action API', async () => {
      const fetchMock = vi.fn().mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({ success: true })
      })
      global.fetch = fetchMock

      const flowAction = createMergeApprovalFlowAction({
        actionParams: {
          requestKind: 'merge_approval',
          requestContent: 'Review and approve merge',
          mergeApprovalInfo: {
            defaultTargetBranch: 'main',
            sourceBranch: 'feature-branch',
            diff: 'diff content here',
            devRunContext: {
              worktreeDir: '/tmp/worktree',
              sourceBranch: 'feature-branch',
              baseBranch: 'main',
            }
          }
        }
      })
      mountComponent(flowAction)

      // Trigger dev run start via the component's handler
      const vm = wrapper.vm as any
      await vm.handleDevRunStart()

      expect(fetchMock).toHaveBeenCalledWith(
        '/api/v1/workspaces/test-workspace-id/flows/test-flow-id/user_action',
        expect.objectContaining({
          method: 'POST',
          body: JSON.stringify({ actionType: 'dev_run_start' })
        })
      )
    })

    it('sends devRunAction stop via user action API', async () => {
      const fetchMock = vi.fn().mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({ success: true })
      })
      global.fetch = fetchMock

      const flowAction = createMergeApprovalFlowAction({
        actionParams: {
          requestKind: 'merge_approval',
          requestContent: 'Review and approve merge',
          mergeApprovalInfo: {
            defaultTargetBranch: 'main',
            sourceBranch: 'feature-branch',
            diff: 'diff content here',
            devRunContext: {
              worktreeDir: '/tmp/worktree',
              sourceBranch: 'feature-branch',
              baseBranch: 'main',
            }
          }
        }
      })
      mountComponent(flowAction)

      // Trigger dev run stop via the component's handler
      const vm = wrapper.vm as any
      await vm.handleDevRunStop()

      expect(fetchMock).toHaveBeenCalledWith(
        '/api/v1/workspaces/test-workspace-id/flows/test-flow-id/user_action',
        expect.objectContaining({
          method: 'POST',
          body: JSON.stringify({ actionType: 'dev_run_stop' })
        })
      )
    })
  })
})