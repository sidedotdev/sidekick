import { describe, it, expect, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { ref } from 'vue'
import SegmentedControl from '../SegmentedControl.vue'

describe('SegmentedControl', () => {
  const options = [
    { label: 'Option 1', value: 'opt1' },
    { label: 'Option 2', value: 'opt2' },
  ]

  it('renders correctly', () => {
    const wrapper = mount(SegmentedControl, {
      props: {
        modelValue: 'opt1',
        options,
      },
    })
    expect(wrapper.findAll('button').length).toBe(2)
    expect(wrapper.find('button.active').text()).toBe('Option 1')
  })

  it('emits update:modelValue event when clicked', async () => {
    const wrapper = mount(SegmentedControl, {
      props: {
        modelValue: 'opt1',
        options,
      },
    })
    await wrapper.findAll('button')[1].trigger('click')
    expect(wrapper.emitted('update:modelValue')).toBeTruthy()
    expect(wrapper.emitted('update:modelValue')![0]).toEqual(['opt2'])
  })

  it('does not submit the form when clicked', async () => {
    const formSubmit = vi.fn()
    const wrapper = mount({
      template: `
        <form @submit.prevent="onSubmit">
          <SegmentedControl v-model="selected" :options="options" />
        </form>
      `,
      components: { SegmentedControl },
      setup() {
        return { selected: ref('opt1'), options, onSubmit: formSubmit }
      },
    })

    await wrapper.find('button').trigger('click')
    expect(formSubmit).not.toHaveBeenCalled()
  })
})