import { describe, it, expect, beforeEach, vi } from 'vitest'
import { DOMWrapper, mount } from '@vue/test-utils'
import TaskCreationModal from '../TaskCreationModal.vue'
import { store } from '../../lib/store'
describe('TaskCreationModal', () => {
  let wrapper: ReturnType<typeof mount>
  beforeEach(() => {
    localStorage.clear()
    wrapper = mount(TaskCreationModal)
    store.selectWorkspaceId('ws_test123')
  })
  // TODO enable after providing LLM with ability to update snapshots + logic to
  // confirm snapshot update is desirable
  // it('renders correctly', () => {
  //   expect(wrapper.element).toMatchSnapshot()
  // })
  it('emits close event when close button is clicked', async () => {
    await wrapper.find('.close').trigger('click')
    console.error(wrapper.emitted())
    expect(wrapper.emitted().close).toBeTruthy()
  })
  it('submits form and emits created event when task is created successfully', async () => {
    const mockFetch = vi.fn().mockResolvedValue({ ok: true })
    global.fetch = mockFetch
    const textarea = wrapper.find('textarea')
    await textarea.setValue('Test task')
    await wrapper.find('form').trigger('submit.prevent')
    expect(mockFetch).toHaveBeenCalledWith('/api/v1/workspaces/ws_test123/tasks', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        description: 'Test task',
        flowType: 'basic_dev',
        status: 'to_do',
        flowOptions: {
          planningPrompt: '',
          determineRequirements: true,
          envType: 'local',
        },
      }),
    })
    expect(wrapper.emitted().created).toBeTruthy()
  })
  it('submits form and emits created event when planned_dev task is created successfully', async () => {
    const mockFetch = vi.fn().mockResolvedValue({ ok: true })
    global.fetch = mockFetch
    const textarea = wrapper.find('textarea')
    await textarea.setValue('Test task')
    const statusSelect = wrapper.find('#status') as DOMWrapper<HTMLSelectElement>
    expect(statusSelect.element.value).toBe('to_do')
    const flowTypeSelect = wrapper.find('#flowType') as DOMWrapper<HTMLSelectElement>
    await flowTypeSelect.setValue('planned_dev')
    expect(flowTypeSelect.element.value).toBe('planned_dev')
    // should still be basic_dev until submit
    const wrapper2 = mount(TaskCreationModal)
    const flowTypeSelect2 = wrapper2.find('#flowType') as DOMWrapper<HTMLSelectElement>
    expect(flowTypeSelect2.element.value).toBe('basic_dev')
    await wrapper.find('form').trigger('submit.prevent')
    expect(mockFetch).toHaveBeenCalledWith('/api/v1/workspaces/ws_test123/tasks', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        description: 'Test task',
        flowType: 'planned_dev',
        status: 'to_do',
        flowOptions: {
          planningPrompt: '',
          determineRequirements: true,
          envType: 'local',
        },
      }),
    })
    expect(wrapper.emitted().created).toBeTruthy()
    // should now be planned_dev post-submit due to localstorage setup
    const wrapper3 = mount(TaskCreationModal)
    const flowTypeSelect3 = wrapper3.find('#flowType') as DOMWrapper<HTMLSelectElement>
    expect(flowTypeSelect3.element.value).toBe('planned_dev')
  })
  it('renders task status dropdown with correct options', async () => {
    const select = wrapper.find('#status')
    expect(select.exists()).toBeTruthy()
    expect(select.findAll('option').length).toBe(2)
    expect(select.findAll('option').at(0)!.text()).toBe('TODO')
    expect(select.findAll('option').at(1)!.text()).toBe('Drafting')
  })
  it('includes selected task status in request body when form is submitted', async () => {
    const mockFetch = vi.fn().mockResolvedValue({ ok: true })
    global.fetch = mockFetch
    const textarea = wrapper.find('textarea')
    await textarea.setValue('Test task')
    const select = wrapper.find('#status')
    await select.setValue('drafting')
    await wrapper.find('form').trigger('submit.prevent')
    expect(mockFetch).toHaveBeenCalledWith('/api/v1/workspaces/ws_test123/tasks', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        description: 'Test task',
        flowType: 'basic_dev',
        status: 'drafting',
        flowOptions: {
          planningPrompt: '',
          determineRequirements: true,
          envType: 'local',
        },
      }),
    })
    expect(wrapper.emitted().created).toBeTruthy()
  })
})