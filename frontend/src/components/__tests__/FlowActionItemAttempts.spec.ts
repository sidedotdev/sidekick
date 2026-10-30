import { describe, it, expect, afterEach } from 'vitest'
import { mount, VueWrapper } from '@vue/test-utils'
import FlowActionItem from '../FlowActionItem.vue'
import type { FlowAction, FlowActionAttempt } from '../../lib/models'

function retriedAction(attempts: FlowActionAttempt[]): FlowAction {
  return {
    id: 'fa_1',
    flowId: 'flow_1',
    workspaceId: 'ws_1',
    created: new Date(),
    updated: new Date(),
    subflow: 'coding',
    actionType: 'tool_call.read_file_lines',
    actionParams: {},
    actionStatus: 'started',
    actionResult: '',
    isHumanAction: false,
    attempts,
  } as FlowAction
}

describe('FlowActionItem failed attempts', () => {
  let wrapper: VueWrapper

  afterEach(() => {
    wrapper?.unmount()
  })

  it('shows no badge without attempts', () => {
    wrapper = mount(FlowActionItem, { props: { flowAction: retriedAction([]) } })
    expect(wrapper.find('.action-attempts').exists()).toBe(false)
  })

  it('opens the chronological attempt list straight from the badge and follows new attempts', async () => {
    const first = { at: '2025-06-15T10:00:00Z', error: '429 first' }
    const second = { at: '2025-06-15T10:01:00Z', error: '429 second' }
    wrapper = mount(FlowActionItem, { props: { flowAction: retriedAction([first, second]) } })

    const badge = wrapper.find('.action-attempts')
    expect(badge.text()).toBe('2 failed attempts')
    expect(wrapper.find('details.flow-action-attempts').exists()).toBe(false)

    await badge.trigger('click')

    const details = wrapper.find('details.flow-action-attempts')
    expect(details.exists()).toBe(true)
    expect(details.attributes('open')).toBeDefined()
    expect(details.findAll('li').map((li) => li.text())).toEqual([
      expect.stringContaining('429 first'),
      expect.stringContaining('429 second'),
    ])

    const third = { at: '2025-06-15T10:03:00Z', error: '429 third' }
    await wrapper.setProps({ flowAction: retriedAction([first, second, third]) })

    expect(wrapper.find('.action-attempts').text()).toBe('3 failed attempts')
    const items = wrapper.find('details.flow-action-attempts').findAll('li')
    expect(items).toHaveLength(3)
    expect(items[2].text()).toContain('429 third')
    expect(wrapper.find('details.flow-action-attempts').attributes('open')).toBeDefined()
  })
})