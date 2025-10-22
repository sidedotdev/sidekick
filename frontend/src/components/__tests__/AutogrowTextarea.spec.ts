import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import AutogrowTextarea from '../AutogrowTextarea.vue'

describe('AutogrowTextarea.vue', () => {
  it('initializes correctly with an initial modelValue', () => {
    const wrapper = mount(AutogrowTextarea, {
      props: {
        modelValue: 'Initial value',
      },
    })
    expect(wrapper.find('textarea').element.value).toBe('Initial value')
    expect(wrapper.find('.grow-wrap').attributes('data-replicated-value')).toBe('Initial value')
  })

  it('handles empty modelValue gracefully', () => {
    const wrapper = mount(AutogrowTextarea, {
      props: {
        modelValue: '',
      },
    })
    expect(wrapper.find('textarea').element.value).toBe('')
    expect(wrapper.find('.grow-wrap').attributes('data-replicated-value')).toBe('')
  })

  it('handles undefined modelValue gracefully', () => {
    const wrapper = mount(AutogrowTextarea, {
      props: {
        modelValue: undefined,
      },
    })
    expect(wrapper.find('textarea').element.value).toBe('')
    expect(wrapper.find('.grow-wrap').attributes('data-replicated-value')).toBe('')
  })

  it('emits update:modelValue event on user input', async () => {
    const wrapper = mount(AutogrowTextarea, {
      props: {
        modelValue: 'Initial value',
      },
    })
    await wrapper.find('textarea').setValue('New value')
    expect(wrapper.emitted('update:modelValue')).toEqual([['New value']])
  })

  it('responds to programmatic changes in modelValue prop', async () => {
    const wrapper = mount(AutogrowTextarea, {
      props: {
        modelValue: 'Initial value',
      },
    })

    // Change the prop programmatically
    await wrapper.setProps({ modelValue: 'Updated from parent' })
    await nextTick()

    expect(wrapper.find('textarea').element.value).toBe('Updated from parent')
  })

  it('updates replicatedValue when modelValue changes programmatically', async () => {
    const wrapper = mount(AutogrowTextarea, {
      props: {
        modelValue: 'Initial value',
      },
    })

    // Change the prop programmatically
    await wrapper.setProps({ modelValue: 'Updated from parent' })
    await nextTick()

    expect(wrapper.find('.grow-wrap').attributes('data-replicated-value')).toBe('Updated from parent')
  })

  it('maintains two-way data binding - user input updates parent', async () => {
    const wrapper = mount(AutogrowTextarea, {
      props: {
        modelValue: 'Initial value',
      },
    })

    // User types in textarea
    await wrapper.find('textarea').setValue('User typed this')
    
    // Should emit the update
    expect(wrapper.emitted('update:modelValue')).toEqual([['User typed this']])
    
    // Should update replicatedValue for auto-grow
    expect(wrapper.find('.grow-wrap').attributes('data-replicated-value')).toBe('User typed this')
  })

  it('maintains two-way data binding - parent updates child', async () => {
    const wrapper = mount(AutogrowTextarea, {
      props: {
        modelValue: 'Initial value',
      },
    })

    // Parent updates the prop
    await wrapper.setProps({ modelValue: 'Parent updated this' })
    await nextTick()

    // Should update textarea value
    expect(wrapper.find('textarea').element.value).toBe('Parent updated this')
    
    // Should update replicatedValue for auto-grow
    expect(wrapper.find('.grow-wrap').attributes('data-replicated-value')).toBe('Parent updated this')
    
    // Should not emit update:modelValue when prop changes (avoid infinite loop)
    expect(wrapper.emitted('update:modelValue')).toBeFalsy()
  })

  it('handles placeholder prop correctly', () => {
    const wrapper = mount(AutogrowTextarea, {
      props: {
        modelValue: '',
        placeholder: 'Enter text here...',
      },
    })
    expect(wrapper.find('textarea').attributes('placeholder')).toBe('Enter text here...')
  })

  it('handles disabled prop correctly', () => {
    const wrapper = mount(AutogrowTextarea, {
      props: {
        modelValue: 'Some text',
        disabled: true,
      },
    })
    expect(wrapper.find('textarea').attributes('disabled')).toBeDefined()
  })

  it('handles multiple programmatic updates correctly', async () => {
    const wrapper = mount(AutogrowTextarea, {
      props: {
        modelValue: 'Initial',
      },
    })

    // Multiple programmatic updates
    await wrapper.setProps({ modelValue: 'First update' })
    await nextTick()
    expect(wrapper.find('textarea').element.value).toBe('First update')

    await wrapper.setProps({ modelValue: 'Second update' })
    await nextTick()
    expect(wrapper.find('textarea').element.value).toBe('Second update')

    await wrapper.setProps({ modelValue: 'Final update' })
    await nextTick()
    expect(wrapper.find('textarea').element.value).toBe('Final update')
    expect(wrapper.find('.grow-wrap').attributes('data-replicated-value')).toBe('Final update')
  })
})
