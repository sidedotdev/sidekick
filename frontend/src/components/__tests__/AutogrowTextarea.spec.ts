import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import AutogrowTextarea from '../AutogrowTextarea.vue'

describe('AutogrowTextarea.vue', () => {
  it('renders correctly', () => {
    const wrapper = mount(AutogrowTextarea, {
      props: {
        modelValue: 'Initial value',
      },
    })
    expect(wrapper.find('textarea').element.value).toBe('Initial value')
  })

  it('emits update:modelValue event on input', async () => {
    const wrapper = mount(AutogrowTextarea, {
      props: {
        modelValue: 'Initial value',
      },
    })
    await wrapper.find('textarea').setValue('New value')
    expect(wrapper.emitted('update:modelValue')).toEqual([['New value']])
  })
})
