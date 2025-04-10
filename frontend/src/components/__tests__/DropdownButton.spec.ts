import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import DropdownButton from '../DropdownButton.vue'

describe('DropdownButton.vue', () => {
  const defaultProps = {
    primaryText: 'Primary Action',
    options: [
      { label: 'Option 1', value: 'opt1' },
      { label: 'Option 2', value: 'opt2' }
    ]
  }

  it('renders correctly with props', () => {
    const wrapper = mount(DropdownButton, {
      props: defaultProps
    })
    
    expect(wrapper.find('.button-text').text()).toBe('Primary Action')
    expect(wrapper.findAll('.dropdown-item').length).toBe(0) // Menu starts closed
  })

  it('toggles dropdown menu on button click', async () => {
    const wrapper = mount(DropdownButton, {
      props: defaultProps
    })
    
    await wrapper.find('.main-button').trigger('click')
    expect(wrapper.findAll('.dropdown-item').length).toBe(2)
    
    await wrapper.find('.main-button').trigger('click')
    expect(wrapper.findAll('.dropdown-item').length).toBe(0)
  })

  it('emits select event with correct value when option is clicked', async () => {
    const wrapper = mount(DropdownButton, {
      props: defaultProps
    })
    
    await wrapper.find('.main-button').trigger('click')
    await wrapper.findAll('.dropdown-item')[0].trigger('click')
    
    expect(wrapper.emitted('select')).toBeTruthy()
    expect(wrapper.emitted('select')![0]).toEqual(['opt1'])
    expect(wrapper.findAll('.dropdown-item').length).toBe(0) // Menu should close
  })
})