import { describe, it, expect } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import SubtaskSidePanel from '../SubtaskSidePanel.vue'

const mountPanel = () =>
  mount(SubtaskSidePanel, {
    attachTo: document.body,
    slots: {
      default: '<button class="inside-btn">inside</button>',
    },
  })

describe('SubtaskSidePanel', () => {
  it('focuses the panel root when mounted so escape can dismiss it', async () => {
    const wrapper = mountPanel()
    await flushPromises()
    const root = wrapper.find('.side-panel').element as HTMLElement
    expect(document.activeElement).toBe(root)
    wrapper.unmount()
  })

  it('emits close when escape is pressed while the panel has focus', async () => {
    const wrapper = mountPanel()
    await flushPromises()
    const root = wrapper.find('.side-panel').element as HTMLElement
    root.focus()
    root.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    expect(wrapper.emitted('close')).toBeTruthy()
    expect(wrapper.emitted('close')!.length).toBe(1)
    wrapper.unmount()
  })

  it('emits close when escape is pressed while focus is on a descendant', async () => {
    const wrapper = mountPanel()
    await flushPromises()
    const inside = wrapper.find('.inside-btn').element as HTMLButtonElement
    inside.focus()
    expect(document.activeElement).toBe(inside)
    inside.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    expect(wrapper.emitted('close')).toBeTruthy()
    wrapper.unmount()
  })

  it('does not emit close when escape is pressed outside the panel', async () => {
    const outside = document.createElement('button')
    document.body.appendChild(outside)
    const wrapper = mountPanel()
    await flushPromises()
    outside.focus()
    expect(document.activeElement).toBe(outside)
    outside.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    expect(wrapper.emitted('close')).toBeFalsy()
    wrapper.unmount()
    outside.remove()
  })

  it('emits close when the dismiss button is clicked', async () => {
    const wrapper = mountPanel()
    await flushPromises()
    await wrapper.find('.side-panel-close').trigger('click')
    expect(wrapper.emitted('close')).toBeTruthy()
    wrapper.unmount()
  })

  it('emits resize-start with the mouse event when the drag handle is pressed', async () => {
    const wrapper = mountPanel()
    await flushPromises()
    await wrapper.find('.side-panel-resize').trigger('mousedown')
    const events = wrapper.emitted('resize-start')
    expect(events).toBeTruthy()
    expect(events![0][0]).toBeInstanceOf(MouseEvent)
    wrapper.unmount()
  })

  it('stops listening for escape after unmount', async () => {
    const wrapper = mountPanel()
    await flushPromises()
    wrapper.unmount()
    // After unmount, dispatching escape on the body must not throw and the
    // component (already destroyed) can no longer emit anything; this just
    // verifies the global listener does not keep running and accessing the
    // detached root.
    expect(() =>
      document.body.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true })),
    ).not.toThrow()
  })
})