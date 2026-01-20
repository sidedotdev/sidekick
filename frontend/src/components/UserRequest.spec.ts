import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { config, mount } from '@vue/test-utils'
import PrimeVue from 'primevue/config'
import UserRequest from './UserRequest.vue'
import type { FlowAction } from '../lib/models'

config.global.plugins.push(PrimeVue)

describe('UserRequest', () => {
  const createMergeApprovalFlowAction = (diff: string = 'diff --git a/file.txt b/file.txt\n--- a/file.txt\n+++ b/file.txt\n@@ -1 +1 @@\n-old\n+new'): FlowAction => ({
    id: 'test-id',
    flowId: 'flow-id',
    workspaceId: 'workspace-id',
    created: new Date(),
    updated: new Date(),
    actionType: 'user_request',
    actionStatus: 'pending',
    actionParams: {
      requestKind: 'merge_approval',
      mergeApprovalInfo: {
        diff,
        defaultTargetBranch: 'main',
      },
    },
    actionResult: '',
    subflow: 'test',
    isHumanAction: true,
  })

  let fetchMock: ReturnType<typeof vi.fn>

  beforeEach(() => {
    fetchMock = vi.fn(() => Promise.resolve({
      ok: true,
      json: () => Promise.resolve({}),
    }))
    vi.stubGlobal('fetch', fetchMock)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  describe('focus behavior with diff viewer', () => {
    it('form has tabindex=-1 to allow focus restoration', () => {
      const wrapper = mount(UserRequest, {
        props: {
          flowAction: createMergeApprovalFlowAction(),
          expand: true,
          level: 0,
        },
      })

      const form = wrapper.find('form')
      expect(form.exists()).toBe(true)
      expect(form.attributes('tabindex')).toBe('-1')
    })

    it('diff container is inside the form element', () => {
      const wrapper = mount(UserRequest, {
        props: {
          flowAction: createMergeApprovalFlowAction(),
          expand: true,
          level: 0,
        },
      })

      const form = wrapper.find('form')
      const diffContainer = form.find('.diff-container')
      expect(diffContainer.exists()).toBe(true)
    })

    it('keydown Ctrl+Enter on form triggers submit API call', async () => {
      const wrapper = mount(UserRequest, {
        props: {
          flowAction: createMergeApprovalFlowAction(),
          expand: true,
          level: 0,
        },
      })

      const form = wrapper.find('form')
      
      // Ctrl+Enter should trigger approval when no rejection text
      // (jsdom doesn't report as Mac, so ctrlKey is used)
      await form.trigger('keydown', { key: 'Enter', ctrlKey: true })
      
      // Check that the API call was made to complete the flow action
      await vi.waitFor(() => {
        const completeCall = fetchMock.mock.calls.find(
          (call: unknown[]) => typeof call[0] === 'string' && call[0].includes('/flow_actions/') && call[0].includes('/complete')
        )
        expect(completeCall).toBeTruthy()
        const opts = completeCall![1] as RequestInit
        expect(opts.method).toBe('POST')
        expect(opts.body).toContain('"approved":true')
      })
    })

    it('clicking on diff container restores focus to form', async () => {
      const wrapper = mount(UserRequest, {
        props: {
          flowAction: createMergeApprovalFlowAction(),
          expand: true,
          level: 0,
        },
        attachTo: document.body,
      })

      const form = wrapper.find('form')
      const diffContainer = wrapper.find('.diff-container')
      
      // Focus something else first
      const textarea = wrapper.find('textarea')
      await textarea.element.focus()
      
      // Click on diff container
      await diffContainer.trigger('click')
      
      // Wait for restoreFocus's setTimeout(0) to complete
      await vi.waitFor(() => {
        expect(document.activeElement).toBe(form.element)
      })
      
      wrapper.unmount()
    })

    it('Ctrl+Enter triggers approval API call when form is focused', async () => {
      const wrapper = mount(UserRequest, {
        props: {
          flowAction: createMergeApprovalFlowAction(),
          expand: true,
          level: 0,
        },
        attachTo: document.body,
      })

      const form = wrapper.find('form')
      
      // Focus the form directly (simulating focus restoration after clicking diff)
      form.element.focus()
      
      // Trigger Ctrl+Enter on the form
      await form.trigger('keydown', { key: 'Enter', ctrlKey: true })
      
      await vi.waitFor(() => {
        const completeCall = fetchMock.mock.calls.find(
          (call: unknown[]) => typeof call[0] === 'string' && call[0].includes('/flow_actions/') && call[0].includes('/complete')
        )
        expect(completeCall).toBeTruthy()
        const opts = completeCall![1] as RequestInit
        expect(opts.method).toBe('POST')
        expect(opts.body).toContain('"approved":true')
      })
      
      wrapper.unmount()
    })

    it('Ctrl+Enter triggers rejection API call when rejection text is present', async () => {
      const wrapper = mount(UserRequest, {
        props: {
          flowAction: createMergeApprovalFlowAction(),
          expand: true,
          level: 0,
        },
        attachTo: document.body,
      })

      // Enter rejection text
      const textarea = wrapper.find('textarea')
      await textarea.setValue('This needs changes')
      
      const form = wrapper.find('form')
      form.element.focus()
      
      // Trigger Ctrl+Enter
      await form.trigger('keydown', { key: 'Enter', ctrlKey: true })
      
      await vi.waitFor(() => {
        const completeCall = fetchMock.mock.calls.find(
          (call: unknown[]) => typeof call[0] === 'string' && call[0].includes('/flow_actions/') && call[0].includes('/complete')
        )
        expect(completeCall).toBeTruthy()
        const opts = completeCall![1] as RequestInit
        expect(opts.method).toBe('POST')
        expect(opts.body).toContain('"approved":false')
        expect(opts.body).toContain('This needs changes')
      })
      
      wrapper.unmount()
    })

    it('keydown events bubble up from elements inside the form', async () => {
      const wrapper = mount(UserRequest, {
        props: {
          flowAction: createMergeApprovalFlowAction(),
          expand: true,
          level: 0,
        },
        attachTo: document.body,
      })

      // Find an element inside the diff container
      const diffContainer = wrapper.find('.diff-container')
      
      // Trigger keydown on the diff container - should bubble to form
      await diffContainer.trigger('keydown', { key: 'Enter', ctrlKey: true })
      
      await vi.waitFor(() => {
        const completeCall = fetchMock.mock.calls.find(
          (call: unknown[]) => typeof call[0] === 'string' && call[0].includes('/flow_actions/') && call[0].includes('/complete')
        )
        expect(completeCall).toBeTruthy()
        const opts = completeCall![1] as RequestInit
        expect(opts.method).toBe('POST')
      })
      
      wrapper.unmount()
    })
  })
})