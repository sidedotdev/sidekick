import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount, VueWrapper } from '@vue/test-utils'
import FuzzySelect from '../FuzzySelect.vue'

vi.mock('primevue/select', () => ({
  default: {
    name: 'Select',
    inheritAttrs: false,
    props: [
      'modelValue',
      'options',
      'optionLabel',
      'optionValue',
      'filter',
      'autoFilterFocus',
      'resetFilterOnHide',
      'filterFields',
      'filterPlaceholder',
    ],
    emits: ['update:modelValue', 'filter', 'hide'],
    template: '<div><slot name="value" :value="modelValue" /></div>',
  },
}))

describe('FuzzySelect', () => {
  let wrapper: VueWrapper

  const options = [
    { id: 'feature-bar', name: 'feature/bar' },
    { id: 'fix-button', name: 'fix-button' },
    { id: 'main', name: 'main' },
    { id: 'feature-baz', name: 'feature/baz' },
  ]

  beforeEach(() => {
    wrapper = mount(FuzzySelect, {
      props: {
        modelValue: 'main',
        options,
        optionLabel: 'name',
        optionValue: 'id',
      },
    })
  })

  it('ranks and filters options from filter input', async () => {
    const select = wrapper.findComponent({ name: 'Select' })

    select.vm.$emit('filter', { value: 'fb' })
    await wrapper.vm.$nextTick()

    expect(select.props('options').map((option: { name: string }) => option.name)).toEqual([
      'feature/bar',
      'feature/baz',
      'fix-button',
    ])
  })

  it('forwards selected values through v-model', async () => {
    const select = wrapper.findComponent({ name: 'Select' })

    select.vm.$emit('update:modelValue', 'feature-bar')

    expect(wrapper.emitted('update:modelValue')).toEqual([['feature-bar']])
  })

  it('restores all options after the dropdown closes', async () => {
    const select = wrapper.findComponent({ name: 'Select' })

    select.vm.$emit('filter', { value: 'main' })
    await wrapper.vm.$nextTick()
    expect(select.props('options')).toHaveLength(1)

    select.vm.$emit('hide')
    await wrapper.vm.$nextTick()
    await wrapper.vm.$nextTick()

    expect(select.props('options')).toEqual(options)
  })
})