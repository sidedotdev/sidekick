import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import FlowActionAttempts from '../FlowActionAttempts.vue'

describe('FlowActionAttempts', () => {
  it('renders nothing without attempts', () => {
    const wrapper = mount(FlowActionAttempts, { props: { attempts: [] } })
    expect(wrapper.find('details').exists()).toBe(false)
  })

  it('summarizes the count and lists attempts chronologically with their errors', () => {
    const wrapper = mount(FlowActionAttempts, {
      props: {
        attempts: [
          { at: '2025-06-15T10:00:00Z', error: '429 first' },
          { at: '2025-06-15T10:01:00Z', error: '429 second' },
        ],
      },
    })
    expect(wrapper.find('summary').text()).toBe('2 failed attempts')
    const items = wrapper.findAll('li')
    expect(items).toHaveLength(2)
    expect(items[0].text()).toContain('429 first')
    expect(items[1].text()).toContain('429 second')
  })
})