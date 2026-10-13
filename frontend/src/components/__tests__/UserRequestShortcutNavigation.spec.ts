import { afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import UserRequest from '../UserRequest.vue'
import type { FlowAction } from '../../lib/models'
import { USER_REQUEST_SHORTCUT_SUBMITTED_KEY } from '../../lib/userRequestShortcut'

vi.mock('primevue/select', () => ({
  default: { name: 'Select', template: '<select></select>', props: ['modelValue', 'options', 'optionLabel', 'optionValue'] },
}))

const isMac = navigator.platform.toUpperCase().indexOf('MAC') >= 0

const flowAction = (): FlowAction => ({
  id: `action-${Math.random()}`,
  flowId: 'flow-1',
  workspaceId: 'ws-1',
  created: new Date(),
  updated: new Date(),
  subflow: '',
  actionType: 'user_request',
  actionStatus: 'pending',
  actionParams: { requestKind: 'approval', requestContent: 'Approve?' },
  actionResult: '',
  isHumanAction: true,
} as FlowAction)

describe('UserRequest shortcut submission notification', () => {
  let wrapper: VueWrapper | null = null

  const mountRequest = (ok = true) => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok, json: async () => ({ error: 'nope' }) }))
    const onSubmitted = vi.fn()
    const action = flowAction()
    wrapper = mount(UserRequest, {
      props: { flowAction: action, expand: true },
      attachTo: document.body,
      global: { provide: { [USER_REQUEST_SHORTCUT_SUBMITTED_KEY as symbol]: onSubmitted } },
    })
    return { onSubmitted, action }
  }

  const shortcut = (shiftKey = false) => ({ key: 'Enter', metaKey: isMac, ctrlKey: !isMac, shiftKey })

  afterEach(() => {
    wrapper?.unmount()
    wrapper = null
    vi.unstubAllGlobals()
  })

  it.each([
    ['form', false],
    ['document', true],
  ])('notifies after a successful %s-level shortcut submission', async (_name, global) => {
    const { onSubmitted, action } = mountRequest()
    if (global) {
      document.dispatchEvent(new KeyboardEvent('keydown', { ...shortcut(), bubbles: true, cancelable: true }))
    } else {
      await wrapper!.find('form').trigger('keydown', shortcut())
    }
    await flushPromises()
    expect(fetch).toHaveBeenCalledOnce()
    expect(onSubmitted).toHaveBeenCalledWith(action)
  })

  it.each([
    ['form', false],
    ['document', true],
  ])('submits without notifying when Shift is held at the %s level', async (_name, global) => {
    const { onSubmitted } = mountRequest()
    if (global) {
      document.dispatchEvent(new KeyboardEvent('keydown', { ...shortcut(true), bubbles: true, cancelable: true }))
    } else {
      await wrapper!.find('form').trigger('keydown', shortcut(true))
    }
    await flushPromises()
    expect(fetch).toHaveBeenCalledOnce()
    expect(onSubmitted).not.toHaveBeenCalled()
  })

  it('does not notify when the submission fails', async () => {
    const { onSubmitted } = mountRequest(false)
    await wrapper!.find('form').trigger('keydown', shortcut())
    await flushPromises()
    expect(onSubmitted).not.toHaveBeenCalled()
  })

  it('does not notify on mouse-click submission', async () => {
    const { onSubmitted } = mountRequest()
    const approve = wrapper!.findAll('button').find(button => button.text().includes('Approve'))!
    await approve.trigger('click')
    await flushPromises()
    expect(fetch).toHaveBeenCalledOnce()
    expect(onSubmitted).not.toHaveBeenCalled()
  })
})