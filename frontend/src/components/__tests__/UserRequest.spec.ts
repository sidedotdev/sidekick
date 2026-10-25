import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { mount, VueWrapper } from '@vue/test-utils'
import UserRequest from '../UserRequest.vue'
import type { FlowAction } from '../../lib/models'
import { TOOLTIP_OPEN_DELAY_MS } from '../../composables/useTooltipTrigger'

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

  it.each([
    ['Restart Docker', true],
    ['Retry Without Restart', false],
  ])('submits %s without requiring feedback', async (label, approved) => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      json: () => Promise.resolve({ success: true }),
    })
    global.fetch = fetchMock
    mountComponent(createMockFlowAction({
      actionParams: {
        requestKind: 'approval',
        requestContent: 'Docker is unavailable.',
        approveTag: 'restart_docker',
        rejectTag: 'retry_without_restart',
        rejectReasonRequired: false,
      },
    }))

    const button = wrapper.findAll('button').find(button => button.text().startsWith(label as string))
    expect(button).toBeDefined()
    expect(button!.attributes('disabled')).toBeUndefined()
    await button!.trigger('click')

    expect(fetchMock).toHaveBeenCalledWith(
      '/api/v1/workspaces/test-workspace-id/flow_actions/test-action-id/complete',
      expect.objectContaining({
        body: JSON.stringify({ userResponse: { content: '', approved } }),
      }),
    )
  })

  it('requires rejection feedback by default', () => {
    mountComponent(createMockFlowAction())
    expect(wrapper.find('button.secondary').attributes('disabled')).toBeDefined()
  })

  describe('copy request content', () => {
    it.each([
      ['pending', '# Requirements\n\n- Preserve `markdown`.\n', undefined],
      ['complete', '# Plan\n\n1. Implement the feature.\n', undefined],
      ['pending', '# Requirements\n\n- Preserve `markdown`.\n', 'Separate copy payload'],
      ['complete', '# Plan\n\n1. Implement the feature.\n', 'Separate copy payload'],
    ])('copies requestContent verbatim for %s requests', async (actionStatus, requestContent, copyContent) => {
      const writeText = vi.fn().mockResolvedValue(undefined)
      const originalClipboard = Object.getOwnPropertyDescriptor(navigator, 'clipboard')
      Object.defineProperty(navigator, 'clipboard', {
        configurable: true,
        value: { writeText },
      })
      try {
        mountComponent(createMockFlowAction({
          actionStatus,
          actionParams: {
            requestKind: 'approval',
            requestContent,
            copyContent,
          },
        }))

        expect(wrapper.find('.markdown h1').text()).toBe(actionStatus === 'pending' ? 'Requirements' : 'Plan')
        await wrapper.find('button.copy-button').trigger('click')

        expect(writeText).toHaveBeenCalledExactlyOnceWith(requestContent)
      } finally {
        wrapper.unmount()
        if (originalClipboard) {
          Object.defineProperty(navigator, 'clipboard', originalClipboard)
        } else {
          Reflect.deleteProperty(navigator, 'clipboard')
        }
      }
    })
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

  describe('provider auth', () => {
    const providerAuthParams = {
      requestKind: 'provider_auth',
      requestContent: 'No API key found for openai.',
      provider: 'openai',
      profileId: 'default',
      reason: 'missing_api_key',
      methods: [
        { method: 'oauth_subscription', label: 'ChatGPT/Codex' },
        { method: 'api_key', label: 'Enter API key' },
      ],
    }

    it('submits the completed method as the choice', async () => {
      const fetchMock = vi.fn().mockImplementation((url: string) => Promise.resolve({
        ok: true,
        json: () => Promise.resolve(url.startsWith('/api/v1/provider_auth/api_key') ? { exists: true } : {}),
      }))
      global.fetch = fetchMock

      mountComponent(createMockFlowAction({ actionParams: providerAuthParams }))
      expect(wrapper.findAll('.markdown-container')).toHaveLength(0)

      const apiKeyButton = wrapper.findAll('button').find(b => b.text() === 'Enter API key')
      await apiKeyButton!.trigger('click')
      await new Promise(resolve => setTimeout(resolve, 0))

      const completeCall = fetchMock.mock.calls.find(([url]) => url.endsWith('/complete'))
      expect(completeCall).toBeTruthy()
      expect(JSON.parse(completeCall![1].body).userResponse.choice).toBe('api_key')
    })

    const completeCalls = (fetchMock: ReturnType<typeof vi.fn>) =>
      fetchMock.mock.calls.filter(([url]) => url.endsWith('/flow_actions/test-action-id/complete'))

    it('submits oauth_subscription as the choice once the OAuth login finishes', async () => {
      vi.useFakeTimers()
      try {
        vi.spyOn(window, 'open').mockReturnValue(null)
        const fetchMock = vi.fn().mockImplementation((url: string) => {
          let body: unknown = {}
          if (url === '/api/v1/provider_auth/oauth/start') body = { sessionId: 'poa_ui', authUrl: 'https://auth.openai.com/oauth/authorize' }
          else if (url === '/api/v1/provider_auth/oauth/poa_ui') body = { status: 'succeeded' }
          return Promise.resolve({ ok: true, json: () => Promise.resolve(body) })
        })
        global.fetch = fetchMock

        mountComponent(createMockFlowAction({ actionParams: providerAuthParams }))
        await wrapper.findAll('button').find(b => b.text() === 'ChatGPT/Codex')!.trigger('click')
        await vi.advanceTimersByTimeAsync(0)
        expect(completeCalls(fetchMock)).toHaveLength(0)

        await vi.advanceTimersByTimeAsync(1000)

        const calls = completeCalls(fetchMock)
        expect(calls).toHaveLength(1)
        expect(JSON.parse(calls[0][1].body).userResponse.choice).toBe('oauth_subscription')
      } finally {
        vi.useRealTimers()
      }
    })

    it('lets a rejected completion be retried without repeating the key check', async () => {
      let completeAttempts = 0
      const fetchMock = vi.fn().mockImplementation((url: string) => {
        if (url.startsWith('/api/v1/provider_auth/api_key')) {
          return Promise.resolve({ ok: true, json: () => Promise.resolve({ exists: true }) })
        }
        completeAttempts++
        if (completeAttempts === 1) {
          return Promise.resolve({ ok: false, json: () => Promise.resolve({ error: 'Flow action is busy' }) })
        }
        return Promise.resolve({ ok: true, json: () => Promise.resolve({}) })
      })
      global.fetch = fetchMock

      mountComponent(createMockFlowAction({ actionParams: providerAuthParams }))
      await wrapper.findAll('button').find(b => b.text() === 'Enter API key')!.trigger('click')
      await new Promise(resolve => setTimeout(resolve, 0))

      expect(wrapper.find('.provider-auth-error').text()).toContain('Flow action is busy')

      await wrapper.find('button.provider-auth-retry').trigger('click')
      await new Promise(resolve => setTimeout(resolve, 0))

      const calls = completeCalls(fetchMock)
      expect(calls).toHaveLength(2)
      expect(JSON.parse(calls[1][1].body).userResponse.choice).toBe('api_key')
      expect(fetchMock.mock.calls.filter(([url]) => url.startsWith('/api/v1/provider_auth/api_key'))).toHaveLength(1)
      expect(wrapper.find('.provider-auth-error').exists()).toBe(false)
    })

    it.each([
      [{ method: 'oauth_subscription', authType: 'subscription', resolvedExternally: false }, 'Authenticated with: ChatGPT/Codex'],
      [{ method: 'api_key', authType: 'api', resolvedExternally: true }, 'Resolved externally'],
    ])('shows the resolution of a completed request', (result, expected) => {
      mountComponent(createMockFlowAction({
        actionStatus: 'complete',
        actionParams: providerAuthParams,
        actionResult: JSON.stringify(result),
      }))

      expect(wrapper.find('.provider-auth-result').text()).toBe(expected)
    })
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

    it('explains a flagged item in a tooltip after the hover delay', async () => {
      vi.useFakeTimers()
      try {
        mountComponent(createCommandApprovalFlowAction())

        await wrapper.find('.permission-evaluation .perm-flagged').trigger('mouseover')
        expect(document.body.querySelector('.perm-tooltip')).toBeNull()
        await vi.advanceTimersByTimeAsync(TOOLTIP_OPEN_DELAY_MS)

        const tooltip = document.body.querySelector('.perm-tooltip')
        expect(tooltip?.textContent).toContain('Rule ^rm\\b (repo config) requires approval')
        expect(tooltip?.textContent).toContain('rm is risky')
      } finally {
        wrapper.unmount()
        vi.runOnlyPendingTimers()
        vi.useRealTimers()
      }
    })

    it.each([
      ['leaving the script', (w: VueWrapper) => w.find('.permission-evaluation .perm-script').trigger('mouseleave')],
      ['moving onto a non-target segment', (w: VueWrapper) => w.find('.permission-evaluation .perm-line-code span:not([data-target-ids])').trigger('mouseover')],
    ])('does not show the tooltip after %s just before the hover delay elapses', async (_, leaveTarget) => {
      vi.useFakeTimers()
      try {
        mountComponent(createCommandApprovalFlowAction())

        await wrapper.find('.permission-evaluation .perm-flagged').trigger('mouseover')
        await vi.advanceTimersByTimeAsync(TOOLTIP_OPEN_DELAY_MS - 50)
        await leaveTarget(wrapper)
        await vi.advanceTimersByTimeAsync(TOOLTIP_OPEN_DELAY_MS * 2)

        expect(document.body.querySelector('.perm-tooltip')).toBeNull()
      } finally {
        wrapper.unmount()
        vi.runOnlyPendingTimers()
        vi.useRealTimers()
      }
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