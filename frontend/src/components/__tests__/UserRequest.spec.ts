import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
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

  afterEach(() => {
    vi.restoreAllMocks()
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
    vi.spyOn(console, 'error').mockImplementation(() => {})

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

  describe('permission evaluation details', () => {
    const permissionEvaluation = {
      outcome: 'require_approval',
      commands: [
        {
          command: 'go test ./...',
          outcome: 'auto_approve',
          matchedRules: [
            { action: 'auto_approve', pattern: '^go test\\b', source: 'base' }
          ],
          decidedBy: 'rule',
          decidedByIndex: 0
        },
        {
          command: 'rm -rf /data',
          outcome: 'require_approval',
          matchedRules: [
            { action: 'require_approval', pattern: '^rm\\b', source: 'repo_config', message: 'rm is risky' }
          ],
          factors: [
            { kind: 'absolute_path_escalation', outcome: 'require_approval', paths: ['/data'] }
          ],
          decidedBy: 'rule',
          decidedByIndex: 0
        }
      ],
      factors: [
        { kind: 'temp_path_advisory', message: 'Prefer .side/tmp over /tmp' }
      ]
    }

    const createCommandApprovalFlowAction = (overrides = {}): FlowAction => createMockFlowAction({
      actionParams: {
        requestKind: 'approval',
        requestContent: 'Allow running the following command?',
        command: 'go test ./... && rm -rf /data',
        workingDir: '/repo',
        permissionEvaluation
      },
      ...overrides
    })

    beforeEach(() => {
      vi.stubEnv('MODE', 'development')
    })

    afterEach(() => {
      vi.unstubAllEnvs()
    })

    it('shows the script once, highlighting only what needs approval', () => {
      mountComponent(createCommandApprovalFlowAction())

      const evalBlock = wrapper.find('.permission-evaluation')
      expect(evalBlock.exists()).toBe(true)
      expect(wrapper.find('pre').exists()).toBe(false)
      expect(evalBlock.find('.perm-script').text()).toContain('go test ./... && rm -rf /data')
      expect(evalBlock.find('.perm-headline').text()).toBe('1 item needs approval')
      expect(evalBlock.findAll('.perm-flagged').map((s) => s.element.textContent).join('')).toBe('rm -rf /data')
      expect(evalBlock.findAll('.perm-gutter-icon')).toHaveLength(1)
      expect(evalBlock.find('.perm-gutter-icon').attributes('aria-label')).toBe('Rule ^rm\\b (repo config) requires approval')
      expect(evalBlock.text()).not.toContain('rm is risky')
      expect(evalBlock.text()).not.toContain('Prefer .side/tmp over /tmp')
    })

    it('explains a flagged item in a tooltip on hover', async () => {
      mountComponent(createCommandApprovalFlowAction())

      await wrapper.find('.permission-evaluation .perm-flagged').trigger('mouseover')
      await new Promise((resolve) => setTimeout(resolve, 0))

      const tooltip = document.body.querySelector('.perm-tooltip')
      expect(tooltip?.textContent).toContain('Rule ^rm\\b (repo config) requires approval')
      expect(tooltip?.textContent).toContain('rm is risky')
      wrapper.unmount()
    })

    it('reveals full details on expand', async () => {
      mountComponent(createCommandApprovalFlowAction())

      await wrapper.find('.permission-evaluation .perm-toggle').trigger('click')

      const details = wrapper.find('.permission-evaluation .perm-details')
      const flaggedCard = details.find('.perm-card')
      expect(flaggedCard.text()).toContain('Needs approval · Rule')
      expect(flaggedCard.text()).toContain('rm -rf /data')
      expect(flaggedCard.text()).toContain('rm is risky')
      expect(flaggedCard.text()).toContain('Absolute path')
      expect(flaggedCard.text()).toContain('/data')
      expect(flaggedCard.find('.perm-check-decided').text()).toContain('^rm\\b')
      expect(details.find('.perm-approved summary').text()).toBe('Auto-approved (1)')
      expect(details.find('.perm-approved').text()).toContain('^go test\\b')
      expect(details.text()).toContain('Prefer .side/tmp over /tmp')
      expect(details.text()).not.toContain('absolute_path_escalation')
    })

    it('renders the summary in the expanded non-pending state in dev mode', () => {
      mountComponent(createCommandApprovalFlowAction({
        actionStatus: 'complete',
        actionResult: JSON.stringify({ Approved: false, Content: 'no thanks' })
      }))

      const evalBlock = wrapper.find('.permission-evaluation')
      expect(evalBlock.exists()).toBe(true)
      expect(evalBlock.text()).toContain('go test ./...')
    })

    it('falls back to the plain command when permissionEvaluation is absent', () => {
      const flowAction = createCommandApprovalFlowAction()
      delete flowAction.actionParams.permissionEvaluation
      mountComponent(flowAction)

      expect(wrapper.find('.permission-evaluation').exists()).toBe(false)
      expect(wrapper.find('pre').text()).toBe('go test ./... && rm -rf /data')
    })

    it('falls back to the plain command outside dev mode', () => {
      vi.stubEnv('MODE', 'production')
      mountComponent(createCommandApprovalFlowAction())

      expect(wrapper.find('.permission-evaluation').exists()).toBe(false)
      expect(wrapper.find('pre').text()).toBe('go test ./... && rm -rf /data')
    })
  })
})